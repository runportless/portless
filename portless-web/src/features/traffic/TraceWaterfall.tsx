import { Fragment, useEffect, useState, type CSSProperties } from 'react'
import { duration } from '../../components/Status'
import type { TrafficCorrelation, TrafficExchange, TrafficTrace, TrafficTraceSpan } from '../../types'

function spanOperation(exchange: TrafficExchange) {
  if (exchange.protocol === 'http') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  return `${exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'} ${exchange.tcp?.operation || 'SESSION'}`
}

type TraceWaterfallItem =
  | { kind: 'span'; span: TrafficTraceSpan }
  | { kind: 'transaction'; group: number; spans: TrafficTraceSpan[] }

function visibleSpan(span: TrafficTraceSpan, includeBackground: boolean) {
  const exchange = span.exchange
  if (!exchange.background || includeBackground) return true
  return Boolean(exchange.error || exchange.fault || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete')
}

export function traceWaterfallItems(trace: TrafficTrace, includeBackground = false): TraceWaterfallItem[] {
  const spans = (trace.spans || []).filter((span) => visibleSpan(span, includeBackground))
  const transactions = new Map<number, TrafficTraceSpan[]>()
  for (const span of spans) {
    if (!span.transactionGroup) continue
    const members = transactions.get(span.transactionGroup) || []
    members.push(span)
    transactions.set(span.transactionGroup, members)
  }

  const emitted = new Set<number>()
  const items: TraceWaterfallItem[] = []
  for (const span of spans) {
    const group = span.transactionGroup
    if (!group) {
      items.push({ kind: 'span', span })
      continue
    }
    if (emitted.has(group)) continue
    emitted.add(group)
    items.push({ kind: 'transaction', group, spans: transactions.get(group) || [span] })
  }
  return items
}

function spanStyle(startOffsetMs: number, durationMs: number, depth: number, total: number) {
  const left = Math.max(0, Math.min(100, startOffsetMs / total * 100))
  const width = Math.max(1.25, Math.min(100 - left, durationMs / total * 100))
  return { '--span-left': `${left}%`, '--span-width': `${width}%`, '--span-depth': depth } as CSSProperties
}

function spanTone(exchange: TrafficExchange) {
  if (exchange.error || (exchange.status || 0) >= 500 || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete') return ' is-error'
  if (exchange.fault) return ' is-faulted'
  if (exchange.protocol === 'tcp') return ' is-tcp'
  return ''
}

function weakestCorrelation(spans: TrafficTraceSpan[]): TrafficCorrelation {
  const strength: Record<TrafficCorrelation, number> = { exact: 0, inferred: 1, partial: 2, ambiguous: 3 }
  return spans.reduce<TrafficCorrelation>((current, span) => strength[span.correlation] > strength[current] ? span.correlation : current, 'exact')
}

function TraceSpanRow({ span, total, depth = span.depth, className = '', onInspect }: {
  span: TrafficTraceSpan
  total: number
  depth?: number
  className?: string
  onInspect: (exchange: TrafficExchange) => void
}) {
  const exchange = span.exchange
  return <button className={`trace-span${spanTone(exchange)}${className}`} style={spanStyle(span.startOffsetMs, exchange.durationMs, depth, total)} type="button" onClick={() => onInspect(exchange)} aria-label={`Inspect ${exchange.source} to ${exchange.target} ${spanOperation(exchange)}`}>
    <span className="trace-span__label"><strong>{exchange.source} <i>→</i> {exchange.target}</strong><small>{spanOperation(exchange)}</small></span>
    <span className="trace-span__track"><i /><small>{duration(exchange.durationMs)}</small></span>
    <span className={`correlation-badge correlation-badge--${span.correlation}`}>{span.correlation}</span>
  </button>
}

export function TraceWaterfall({ trace, includeBackground = false, onExchange }: { trace: TrafficTrace; includeBackground?: boolean; onExchange: (exchange: TrafficExchange) => void }) {
  const [maximized, setMaximized] = useState(false)
  const [expandedTransactions, setExpandedTransactions] = useState<Set<number>>(() => new Set())
  useEffect(() => {
    if (!maximized) return
    const restore = (event: KeyboardEvent) => { if (event.key === 'Escape') setMaximized(false) }
    window.addEventListener('keydown', restore)
    return () => window.removeEventListener('keydown', restore)
  }, [maximized])
  useEffect(() => setExpandedTransactions(new Set()), [trace.number])
  const total = Math.max(1, trace.durationMs)
  const inspect = (exchange: TrafficExchange) => { setMaximized(false); onExchange(exchange) }
  const toggleTransaction = (group: number) => setExpandedTransactions((current) => {
    const next = new Set(current)
    if (next.has(group)) next.delete(group)
    else next.add(group)
    return next
  })
  return <div className={`trace-waterfall${maximized ? ' panel trace-waterfall--maximized' : ''}`} role="region" aria-label="Trace waterfall">
    {maximized && <div className="panel-title trace-waterfall__toolbar"><span>TRACE WATERFALL</span><div><button className="icon-button" type="button" title="Restore trace" aria-label="Restore trace" aria-pressed="true" onClick={() => setMaximized(false)}>×</button></div></div>}
    <div className="trace-waterfall__content">
    <div className="trace-waterfall__axis"><span>SERVICE / OPERATION</span><div><i>0</i><i>{duration(Math.round(total / 2))}</i><i>{duration(total)}</i></div>{!maximized && <button className="trace-waterfall__size" type="button" title="Maximize trace" aria-label="Maximize trace" aria-pressed="false" onClick={() => setMaximized(true)}><TraceSizeIcon /></button>}</div>
    {traceWaterfallItems(trace, includeBackground).map((item) => {
      if (item.kind === 'span') return <TraceSpanRow key={item.span.exchange.sequence} span={item.span} total={total} onInspect={inspect} />

      const first = item.spans[0]
      const exchange = first.exchange
      const expanded = expandedTransactions.has(item.group)
      const start = Math.min(...item.spans.map((span) => span.startOffsetMs))
      const end = Math.max(...item.spans.map((span) => span.startOffsetMs + span.exchange.durationMs))
      const depth = Math.min(...item.spans.map((span) => span.depth))
      const application = exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
      const operationLabel = `${item.spans.length} ${item.spans.length === 1 ? 'OPERATION' : 'OPERATIONS'}`
      const tone = item.spans.some((span) => spanTone(span.exchange) === ' is-error') ? ' is-error' : item.spans.some((span) => span.exchange.fault) ? ' is-faulted' : ' is-tcp'
      const correlation = weakestCorrelation(item.spans)
      return <Fragment key={`transaction-${item.group}`}>
        <button className={`trace-span trace-span--transaction${tone}`} style={spanStyle(start, Math.max(0, end - start), depth, total)} type="button" aria-expanded={expanded} aria-label={`${expanded ? 'Collapse' : 'Expand'} ${exchange.source} to ${exchange.target} ${application} transaction with ${operationLabel.toLowerCase()}`} onClick={() => toggleTransaction(item.group)}>
          <span className="trace-span__label"><strong><b className="trace-span__disclosure">{expanded ? '−' : '+'}</b>{exchange.source} <i>→</i> {exchange.target}</strong><small>{application} TRANSACTION · {operationLabel}</small></span>
          <span className="trace-span__track"><i /><small>{duration(Math.max(0, end - start))}</small></span>
          <span className={`correlation-badge correlation-badge--${correlation}`}>{correlation}</span>
        </button>
        {expanded && item.spans.map((span) => <TraceSpanRow key={span.exchange.sequence} span={span} total={total} depth={span.depth + 1} className=" trace-span--transaction-child" onInspect={inspect} />)}
      </Fragment>
    })}
    </div>
  </div>
}

function TraceSizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}
