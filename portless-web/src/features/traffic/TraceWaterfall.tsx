import { useEffect, useState, type CSSProperties } from 'react'
import { duration } from '../../components/Status'
import type { TrafficCorrelation, TrafficExchange, TrafficTrace, TrafficTraceSpan } from '../../types'

function spanOperation(exchange: TrafficExchange) {
  if (exchange.protocol === 'http') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  const application = exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
  if (exchange.tcp?.kind === 'operation') return `${application} · ${exchange.tcp.operation || 'UNKNOWN'}`
  return `${application} SESSION`
}

export type TraceWaterfallItem =
  | { kind: 'span'; span: TrafficTraceSpan }
  | { kind: 'transaction'; group: number; spans: TrafficTraceSpan[] }

export type TraceNavigationItem =
  | { kind: 'exchange'; key: string; exchange: TrafficExchange }
  | { kind: 'transaction'; key: string; group: number; exchange: TrafficExchange; spans: TrafficTraceSpan[] }

const transactionBoundaryOperations = new Set(['BEGIN', 'COMMIT', 'ROLLBACK', 'SAVEPOINT', 'RELEASE'])

export function traceTransactionCommandSpans(spans: TrafficTraceSpan[]) {
  return spans.filter((span) => !transactionBoundaryOperations.has((span.exchange.tcp?.operation || '').toUpperCase()))
}

function visibleSpan(span: TrafficTraceSpan) {
  const exchange = span.exchange
  if (!exchange.background) return true
  return Boolean(exchange.error || exchange.fault || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete')
}

export function traceWaterfallItems(trace: TrafficTrace): TraceWaterfallItem[] {
  const spans = (trace.spans || [])
    .filter(visibleSpan)
    .sort((left, right) => left.startOffsetMs - right.startOffsetMs || left.exchange.sequence - right.exchange.sequence)
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

function transactionRepresentative(spans: TrafficTraceSpan[]) {
  return spans.find((span) => span.exchange.error || span.exchange.tcp?.outcome === 'error' || span.exchange.tcp?.outcome === 'incomplete')
    || spans.find((span) => !transactionBoundaryOperations.has((span.exchange.tcp?.operation || '').toUpperCase()))
    || spans[0]
}

function transactionExchange(spans: TrafficTraceSpan[]) {
  const ordered = [...spans].sort((left, right) => left.startOffsetMs - right.startOffsetMs || left.exchange.sequence - right.exchange.sequence)
  const representative = transactionRepresentative(ordered).exchange
  const first = ordered[0].exchange
  const start = Math.min(...ordered.map((span) => span.startOffsetMs))
  const end = Math.max(...ordered.map((span) => span.startOffsetMs + span.exchange.durationMs))
  const error = ordered.find((span) => span.exchange.error)?.exchange.error
  const fault = ordered.find((span) => span.exchange.fault)?.exchange.fault
  const recording = ordered.find((span) => span.exchange.recording)?.exchange.recording
  const outcome: NonNullable<TrafficExchange['tcp']>['outcome'] = ordered.some((span) => span.exchange.tcp?.outcome === 'error')
    ? 'error'
    : ordered.some((span) => span.exchange.tcp?.outcome === 'incomplete') ? 'incomplete' : 'success'
  return {
    ...representative,
    sequence: first.sequence,
    startedAt: first.startedAt,
    completedAt: ordered[ordered.length - 1].exchange.completedAt,
    durationMs: Math.max(0, end - start),
    requestBytes: ordered.reduce((total, span) => total + Math.max(0, span.exchange.requestBytes), 0),
    responseBytes: ordered.reduce((total, span) => total + Math.max(0, span.exchange.responseBytes), 0),
    fault,
    recording,
    error,
    tcp: representative.tcp ? { ...representative.tcp, operation: 'TRANSACTION', outcome } : representative.tcp,
  } as TrafficExchange
}

function transactionNavigationItem(item: Extract<TraceWaterfallItem, { kind: 'transaction' }>): TraceNavigationItem {
  return {
    kind: 'transaction',
    key: `transaction:${item.group}`,
    group: item.group,
    exchange: transactionExchange(item.spans),
    spans: item.spans,
  }
}

export function exchangeNavigationItem(exchange: TrafficExchange): TraceNavigationItem {
  return { kind: 'exchange', key: `exchange:${exchange.sequence}`, exchange }
}

export function traceNavigationItems(trace: TrafficTrace): TraceNavigationItem[] {
  return traceWaterfallItems(trace).map((item) => {
    if (item.kind === 'span') return exchangeNavigationItem(item.span.exchange)
    return transactionNavigationItem(item)
  })
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

function TraceSpanRow({ span, total, depth = span.depth, className = '', dependencySummary = span.exchange.protocol === 'tcp', onInspect }: {
  span: TrafficTraceSpan
  total: number
  depth?: number
  className?: string
  dependencySummary?: boolean
  onInspect: (exchange: TrafficExchange) => void
}) {
  const exchange = span.exchange
  return <button className={`trace-span${dependencySummary ? ' trace-span--dependency-summary' : ''}${spanTone(exchange)}${className}`} style={spanStyle(span.startOffsetMs, exchange.durationMs, depth, total)} type="button" onClick={() => onInspect(exchange)} aria-label={`Inspect ${exchange.source} to ${exchange.target} ${spanOperation(exchange)}`}>
    <span className="trace-span__label"><strong>{exchange.source} <i>→</i> {exchange.target}</strong><small>{spanOperation(exchange)}</small></span>
    <span className="trace-span__track"><i /><small>{duration(exchange.durationMs)}</small></span>
    <span className={`correlation-badge correlation-badge--${span.correlation}`}>{span.correlation}</span>
  </button>
}

export function TraceWaterfall({ trace, onItem }: {
  trace: TrafficTrace
  onItem: (item: TraceNavigationItem) => void
}) {
  const [maximized, setMaximized] = useState(false)
  useEffect(() => {
    if (!maximized) return
    const restore = (event: KeyboardEvent) => { if (event.key === 'Escape') setMaximized(false) }
    window.addEventListener('keydown', restore)
    return () => window.removeEventListener('keydown', restore)
  }, [maximized])
  const total = Math.max(1, trace.durationMs)
  const inspect = (item: TraceNavigationItem) => { setMaximized(false); onItem(item) }
  return <div className={`trace-waterfall${maximized ? ' panel trace-waterfall--maximized' : ''}`} role="region" aria-label="Trace waterfall">
    {maximized && <div className="panel-title trace-waterfall__toolbar"><span>TRACE WATERFALL</span><div><button className="icon-button" type="button" title="Restore trace" aria-label="Restore trace" aria-pressed="true" onClick={() => setMaximized(false)}>×</button></div></div>}
    <div className="trace-waterfall__content">
    <div className="trace-waterfall__axis"><span>SERVICE / OPERATION</span><div><i>0</i><i>{duration(Math.round(total / 2))}</i><i>{duration(total)}</i></div>{!maximized && <button className="trace-waterfall__size" type="button" title="Maximize trace" aria-label="Maximize trace" aria-pressed="false" onClick={() => setMaximized(true)}><TraceSizeIcon /></button>}</div>
    {traceWaterfallItems(trace).map((item) => {
      if (item.kind === 'span') return <TraceSpanRow key={item.span.exchange.sequence} span={item.span} total={total} onInspect={(exchange) => inspect(exchangeNavigationItem(exchange))} />

      const navigationItem = transactionNavigationItem(item)
      const exchange = navigationItem.exchange
      const start = Math.min(...item.spans.map((span) => span.startOffsetMs))
      const depth = Math.min(...item.spans.map((span) => span.depth))
      const application = exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
      const tone = item.spans.some((span) => spanTone(span.exchange) === ' is-error') ? ' is-error' : item.spans.some((span) => span.exchange.fault) ? ' is-faulted' : ' is-tcp'
      const correlation = weakestCorrelation(item.spans)
      return <button key={`transaction-${item.group}`} className={`trace-span trace-span--dependency-summary trace-span--transaction${tone}`} style={spanStyle(start, exchange.durationMs, depth, total)} type="button" aria-label={`Inspect ${exchange.source} to ${exchange.target} ${application} transaction`} onClick={() => inspect(navigationItem)}>
        <span className="trace-span__label"><strong>{exchange.source} <i>→</i> {exchange.target}</strong><small>{application} · TRANSACTION</small></span>
        <span className="trace-span__track"><i /><small>{duration(exchange.durationMs)}</small></span>
        <span className={`correlation-badge correlation-badge--${correlation}`}>{correlation}</span>
      </button>
    })}
    </div>
  </div>
}

function TraceSizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}
