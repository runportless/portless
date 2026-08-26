import { useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { duration } from '../../components/Status'
import type { Environment, TrafficExchange, TrafficTrace } from '../../types'
import { trafficStartedTime, TrafficDetail } from './TrafficDetail'
import { TraceWaterfall } from './TraceWaterfall'
import { loadTrafficSnapshot } from './trafficSnapshot'
import { filterExchanges, filterTraces, mergeExchanges, mergeTraces, reconcileExchanges, reconcileTraces, trafficWindowSummary, type TrafficProtocolFilter, type TrafficResultFilter } from './trafficState'

type TrafficMode = 'traces' | 'exchanges'
type TrafficClearResult = { cleared: number; throughSequence: number }
const trafficPageSize = 25

function traceRequest(trace: TrafficTrace) {
  const value = `${trace.method || ''} ${trace.requestTarget || ''}`.trim()
  return value || `${trace.source} → ${trace.target}`
}

function resultTone(error: boolean | string | undefined, status: number | undefined) {
  if (error || (status || 0) >= 500) return 'danger-text'
  if ((status || 0) >= 400) return 'warning-text'
  return ''
}

function exchangeOperation(exchange: TrafficExchange) {
  if (exchange.protocol === 'http') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  const application = exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
  return `${application} ${exchange.tcp?.operation || 'SESSION'}`
}

function exchangeResult(exchange: TrafficExchange) {
  if (exchange.error || exchange.tcp?.outcome === 'error') return 'ERR'
  if (exchange.status) return exchange.status
  if (exchange.tcp?.outcome === 'one-way') return 'SENT'
  if (exchange.tcp?.outcome === 'incomplete') return 'INCOMPLETE'
  return 'OK'
}

function traceHasEdge(trace: TrafficTrace, edge: string) {
  if (!edge) return true
  return (trace.spans || []).some((span) => `${span.exchange.source}:${span.exchange.target}` === edge)
}

function exchangeHasEdge(exchange: TrafficExchange, edge: string) {
  return !edge || `${exchange.source}:${exchange.target}` === edge
}

function traceContainsExchange(trace: TrafficTrace, sequence: number) {
  return Boolean(trace.spans?.some((span) => span.exchange.sequence === sequence))
}

export function traceCandidatesForExchange(traces: TrafficTrace[], exchange: TrafficExchange, hint?: TrafficTrace) {
  const candidates = new Map<number, TrafficTrace>()
  for (const trace of traces) candidates.set(trace.number, trace)
  if (hint) candidates.set(hint.number, hint)
  const priority = (trace: TrafficTrace) => {
    if (traceContainsExchange(trace, exchange.sequence)) return 0
    if (exchange.traceId && trace.traceId === exchange.traceId) return 1
    if (trace.rootSequence === exchange.sequence) return 2
    if (trace.number <= exchange.sequence && trace.lastSequence >= exchange.sequence) return 3
    return 4
  }
  return [...candidates.values()]
    .filter((trace) => priority(trace) < 4)
    .sort((left, right) => priority(left) - priority(right) || (left.lastSequence - left.number) - (right.lastSequence - right.number) || right.number - left.number)
}

export function TrafficTableHeader({ mode }: { mode: 'traces' | 'exchanges' }) {
  if (mode === 'traces') return <div className="trace-row trace-row--header"><span>Timestamp</span><span>Root request</span><span>Result</span><span>Duration</span><span>Spans</span><span>Correlation</span></div>
  return <div className="table-row table-row--header traffic-row"><span>Seq</span><span>Timestamp</span><span>Protocol</span><span>Request / operation</span><span>Edge</span><span>Result</span><span>Duration</span><span>Fault / recording</span></div>
}

export function TraceSummaryRow({ trace, expanded, onToggle }: { trace: TrafficTrace; expanded: boolean; onToggle: () => void }) {
  return <button className="trace-row" type="button" onClick={onToggle} aria-expanded={expanded}>
    <span>{trafficStartedTime(trace.startedAt)}</span><strong className="truncate">{traceRequest(trace)}</strong><span className={resultTone(trace.error, trace.status)}>{trace.error ? 'ERR' : trace.status || 'OK'}</span><span>{duration(trace.durationMs)}</span><span>{trace.spanCount}</span><span className={`correlation-badge correlation-badge--${trace.correlation}`}>{trace.correlation}</span>
  </button>
}

export function TrafficPanel({ environment }: { environment: Environment }) {
  const requested = new URLSearchParams(location.search)
  const [mode, setMode] = useState<TrafficMode>(() => requested.get('mode') === 'exchanges' ? 'exchanges' : 'traces')
  const [traces, setTraces] = useState<TrafficTrace[]>([])
  const [exchanges, setExchanges] = useState<TrafficExchange[]>([])
  const [selectedExchange, setSelectedExchange] = useState<TrafficExchange | null>(null)
  const [selectedTrace, setSelectedTrace] = useState<TrafficTrace | null>(null)
  const [traceNavigationPending, setTraceNavigationPending] = useState(false)
  const [expandedTrace, setExpandedTrace] = useState<number | null>(null)
  const [search, setSearch] = useState('')
  const [edgeFilter, setEdgeFilter] = useState(() => requested.get('edge') || '')
  const [resultFilter, setResultFilter] = useState<TrafficResultFilter>('all')
  const [protocol, setProtocol] = useState<TrafficProtocolFilter>(() => {
    const value = requested.get('protocol')
    return value === 'http' || value === 'tcp' ? value : 'all'
  })
  const [includeBackground, setIncludeBackground] = useState(false)
  const [tracePage, setTracePage] = useState(0)
  const [exchangePage, setExchangePage] = useState(0)
  const [clearing, setClearing] = useState(false)
  const [paused, setPaused] = useState(false)
  const [bufferedCount, setBufferedCount] = useState(0)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const pausedRef = useRef(false)
  const knownExchanges = useRef(new Set<number>())
  const exchangeBuffer = useRef(new Map<number, TrafficExchange>())
  const traceBuffer = useRef(new Map<number, TrafficTrace>())
  const expandedRef = useRef<number | null>(null)
  const selectionRequest = useRef(0)

  const applyTrafficClear = (throughSequence: number) => {
    setExchanges((current) => current.filter((exchange) => exchange.sequence > throughSequence))
    setTraces((current) => current.filter((trace) => trace.lastSequence > throughSequence))
    setSelectedExchange((current) => current && current.sequence <= throughSequence ? null : current)
    setSelectedTrace((current) => current && current.lastSequence <= throughSequence ? null : current)
    setTraceNavigationPending(false)
    selectionRequest.current += 1
    setExpandedTrace((current) => current !== null && current <= throughSequence ? null : current)
    for (const sequence of knownExchanges.current) if (sequence <= throughSequence) knownExchanges.current.delete(sequence)
    for (const sequence of exchangeBuffer.current.keys()) if (sequence <= throughSequence) exchangeBuffer.current.delete(sequence)
    for (const [number, trace] of traceBuffer.current) if (trace.lastSequence <= throughSequence) traceBuffer.current.delete(number)
    setBufferedCount(exchangeBuffer.current.size)
    setTracePage(0); setExchangePage(0)
  }

  useEffect(() => { knownExchanges.current = new Set(exchanges.map((exchange) => exchange.sequence)) }, [exchanges])
  useEffect(() => { expandedRef.current = expandedTrace }, [expandedTrace])

  useEffect(() => {
    let active = true
    let loading = false
    setTraces([]); setExchanges([]); setSelectedExchange(null); setSelectedTrace(null); setTraceNavigationPending(false); setExpandedTrace(null); setError(null)
    selectionRequest.current += 1
    setTracePage(0); setExchangePage(0)
    pausedRef.current = false; setPaused(false); knownExchanges.current.clear()
    exchangeBuffer.current.clear(); traceBuffer.current.clear(); setBufferedCount(0)
    const load = async () => {
      if (loading) return
      loading = true
      try {
        const snapshot = await loadTrafficSnapshot(environment, edgeFilter)
        if (!active) return
        setError((current) => current?.code === 'DAEMON_UNAVAILABLE' ? null : current)
        if (pausedRef.current) {
          for (const exchange of snapshot.exchanges) if (!knownExchanges.current.has(exchange.sequence)) exchangeBuffer.current.set(exchange.sequence, exchange)
          for (const trace of snapshot.traces) traceBuffer.current.set(trace.number, trace)
          setBufferedCount(exchangeBuffer.current.size)
        } else {
          setExchanges((current) => reconcileExchanges(current, snapshot.exchanges))
          setTraces((current) => reconcileTraces(current, snapshot.traces, snapshot.throughSequence))
        }
      } catch (value) {
        if (active) setError(actionError("Traffic couldn't be loaded", value))
      } finally {
        loading = false
      }
    }
    void load()
    const disconnect = connectEvents(environment, ['traffic.exchange', 'traffic.trace', 'traffic.cleared'], (type, value) => {
      if (type === 'traffic.cleared') {
        applyTrafficClear((value as TrafficClearResult).throughSequence)
        return
      }
      if (type === 'traffic.exchange') {
        const exchange = value as TrafficExchange
        if (pausedRef.current) {
          if (!knownExchanges.current.has(exchange.sequence)) exchangeBuffer.current.set(exchange.sequence, exchange)
          setBufferedCount(exchangeBuffer.current.size)
        } else setExchanges((current) => mergeExchanges(current, [exchange]))
        return
      }
      const trace = value as TrafficTrace
      const acceptTrace = async () => {
        let candidate = trace
        if (edgeFilter || expandedRef.current === trace.number) {
          try { candidate = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${trace.number}`)) } catch { /* Snapshot polling will reconcile an evicted trace. */ }
        }
        if (!active || (edgeFilter && !traceHasEdge(candidate, edgeFilter))) return
        if (pausedRef.current) traceBuffer.current.set(candidate.number, candidate)
        else setTraces((current) => mergeTraces(current, [candidate]))
      }
      void acceptTrace()
    })
    const timer = window.setInterval(() => void load(), 5000)
    return () => { active = false; disconnect(); window.clearInterval(timer) }
  }, [environment.project, environment.name, edgeFilter])

  const togglePaused = () => {
    if (!pausedRef.current) {
      pausedRef.current = true
      setPaused(true)
      return
    }
    pausedRef.current = false
    setExchanges((current) => mergeExchanges(current, [...exchangeBuffer.current.values()]))
    setTraces((current) => mergeTraces(current, [...traceBuffer.current.values()]))
    exchangeBuffer.current.clear(); traceBuffer.current.clear()
    setBufferedCount(0); setPaused(false)
  }

  const clearTraffic = async () => {
    setClearing(true); setError(null)
    try {
      const result = await api<TrafficClearResult>(environmentPath(environment, '/traffic'), { method: 'DELETE' })
      applyTrafficClear(result.throughSequence)
    } catch (value) {
      setError(actionError("Traffic couldn't be cleared", value))
    } finally {
      setClearing(false)
    }
  }

  const resolveTrace = async (exchange: TrafficExchange, hint?: TrafficTrace) => {
    for (const candidate of traceCandidatesForExchange(traces, exchange, hint)) {
      let detail = candidate
      if ((candidate.spans?.length || 0) !== candidate.spanCount) {
        try { detail = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${candidate.number}`)) } catch { continue }
      }
      if (traceContainsExchange(detail, exchange.sequence)) return detail
    }
    return null
  }

  const inspectExchange = async (exchange: TrafficExchange, traceHint?: TrafficTrace) => {
    const request = ++selectionRequest.current
    setTraceNavigationPending(true)
    if (!traceHint) setSelectedTrace(null)
    try {
      setError(null)
      const [detail, trace] = await Promise.all([
        api<TrafficExchange>(environmentPath(environment, `/traffic/exchanges/${exchange.sequence}`)),
        resolveTrace(exchange, traceHint),
      ])
      if (selectionRequest.current !== request) return
      setSelectedExchange(detail)
      setSelectedTrace(trace)
      if (trace) setTraces((current) => mergeTraces(current, [trace]))
    } catch (value) {
      if (selectionRequest.current === request) setError(actionError("Traffic details aren't available", value))
    } finally {
      if (selectionRequest.current === request) setTraceNavigationPending(false)
    }
  }

  const closeExchange = () => {
    selectionRequest.current += 1
    setSelectedExchange(null)
    setSelectedTrace(null)
    setTraceNavigationPending(false)
  }

  const toggleTrace = async (trace: TrafficTrace) => {
    if (expandedTrace === trace.number) { setExpandedTrace(null); return }
    setExpandedTrace(trace.number)
    if (trace.spans?.length) return
    try {
      const detail = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${trace.number}`))
      setTraces((current) => mergeTraces(current, [detail]))
    } catch (value) {
      setError(actionError("Trace details aren't available", value))
    }
  }

  const windowSummary = useMemo(() => trafficWindowSummary(mode === 'traces' && !includeBackground ? exchanges.filter((exchange) => !exchange.background) : exchanges), [exchanges, includeBackground, mode])
  const visibleTraces = useMemo(() => filterTraces(traces, search, resultFilter, includeBackground), [traces, search, resultFilter, includeBackground])
  const visibleExchanges = useMemo(() => filterExchanges(exchanges.filter((exchange) => exchangeHasEdge(exchange, edgeFilter)), search, resultFilter, protocol), [exchanges, edgeFilter, search, resultFilter, protocol])
  const tracePagination = useMemo(() => paginateItems(visibleTraces, tracePage, trafficPageSize), [visibleTraces, tracePage])
  const exchangePagination = useMemo(() => paginateItems(visibleExchanges, exchangePage, trafficPageSize), [visibleExchanges, exchangePage])

  useEffect(() => { setTracePage(0); setExchangePage(0) }, [search, resultFilter, edgeFilter])
  useEffect(() => { setTracePage(0) }, [includeBackground])
  useEffect(() => { setExchangePage(0) }, [protocol])
  useEffect(() => { if (tracePage !== tracePagination.page) setTracePage(tracePagination.page) }, [tracePage, tracePagination.page])
  useEffect(() => { if (exchangePage !== exchangePagination.page) setExchangePage(exchangePagination.page) }, [exchangePage, exchangePagination.page])

  return <div className="traffic-view">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel traffic-panel">
      <div className="traffic-header">
        <div className="traffic-modes" role="tablist" aria-label="Traffic view">
          <button role="tab" aria-selected={mode === 'traces'} className={mode === 'traces' ? 'is-active' : ''} onClick={() => setMode('traces')}>TRACES</button>
          <button role="tab" aria-selected={mode === 'exchanges'} className={mode === 'exchanges' ? 'is-active' : ''} onClick={() => setMode('exchanges')}>EXCHANGES</button>
        </div>
        <div className="traffic-stream-controls"><span className={`live-count${paused ? ' is-paused' : ''}`}>{paused ? <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg> : <i />}{paused ? `PAUSED${bufferedCount ? ` · ${bufferedCount} BUFFERED` : ''}` : 'STREAMING'}</span><button className="button button--small button--quiet" type="button" title="Clear retained traces and exchanges" disabled={clearing || (exchanges.length === 0 && traces.length === 0 && bufferedCount === 0)} onClick={() => void clearTraffic()}>{clearing ? 'CLEARING…' : 'CLEAR'}</button><button className="button button--small" type="button" disabled={clearing} onClick={togglePaused}>{paused ? 'RESUME' : 'PAUSE'}</button></div>
      </div>
      <div className="traffic-summary" aria-label="Traffic in the last 60 seconds"><span>LAST 60S</span><strong>{windowSummary.exchanges} exchanges</strong><strong>{windowSummary.requestsPerSecond.toFixed(1)} rps</strong><strong className={windowSummary.errors ? 'danger-text' : ''}>{windowSummary.errors} errors</strong><strong>p50 {duration(windowSummary.p50)}</strong><strong>p95 {duration(windowSummary.p95)}</strong></div>
      <div className="traffic-filters">
        <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="filter path, service, edge, status…" aria-label="Filter traffic" />
        <select value={resultFilter} onChange={(event) => setResultFilter(event.target.value as TrafficResultFilter)} aria-label="Traffic result filter"><option value="all">All results</option><option value="errors">Errors</option><option value="slow">Slow · 500ms+</option><option value="faulted">Faulted</option></select>
        {mode === 'exchanges' && <div className="traffic-protocol" role="group" aria-label="Traffic protocol">{(['all', 'http', 'tcp'] as const).map((value) => <button key={value} className={protocol === value ? 'is-active' : ''} onClick={() => setProtocol(value)}>{value.toUpperCase()}</button>)}</div>}
        {mode === 'traces' && <button className={`traffic-background${includeBackground ? ' is-active' : ''}`} type="button" aria-pressed={includeBackground} onClick={() => setIncludeBackground((value) => !value)}>SHOW BACKGROUND</button>}
        {edgeFilter && <button className="traffic-filter-chip" type="button" onClick={() => setEdgeFilter('')}><span>EDGE</span>{edgeFilter.replace(':', ' → ')} ×</button>}
      </div>

      {mode === 'traces' ? <div className="trace-list">
        <TrafficTableHeader mode="traces" />
        {tracePagination.items.map((trace) => <div className={`trace-card${expandedTrace === trace.number ? ' is-expanded' : ''}`} key={trace.number}>
          <TraceSummaryRow trace={trace} expanded={expandedTrace === trace.number} onToggle={() => void toggleTrace(trace)} />
          {expandedTrace === trace.number && (trace.spans?.length ? <TraceWaterfall trace={trace} onExchange={(exchange) => void inspectExchange(exchange, trace)} /> : <div className="trace-loading">Loading trace spans…</div>)}
        </div>)}
        {visibleTraces.length === 0 && <div className="empty-row">No matching traces yet. Open an application endpoint or exercise a service connection to capture one.</div>}
        <PanelPagination label="traces" pagination={tracePagination} onPage={setTracePage} />
      </div> : <div className="exchange-list">
        <TrafficTableHeader mode="exchanges" />
        {exchangePagination.items.map((exchange) => <button className="table-row traffic-row" key={exchange.sequence} onClick={() => void inspectExchange(exchange)}><code>#{exchange.sequence}</code><span>{trafficStartedTime(exchange.startedAt)}</span><strong>{exchange.protocol === 'tcp' ? exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP' : 'HTTP'}</strong><code className="truncate">{exchangeOperation(exchange)}</code><span>{exchange.source}<i className="edge-arrow">→</i>{exchange.target}</span><span className={resultTone(exchange.error || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete', exchange.status)}>{exchangeResult(exchange)}</span><span>{duration(exchange.durationMs)}</span><span>{exchange.fault ? <b className="fault-chip">▲ {exchange.fault}</b> : exchange.recording ? <b className="record-chip">● {exchange.recording}</b> : '—'}</span></button>)}
        {visibleExchanges.length === 0 && <div className="empty-row">No matching exchanges yet.</div>}
        <PanelPagination label="exchanges" pagination={exchangePagination} onPage={setExchangePage} />
      </div>}
    </section>
    {selectedExchange && <TrafficDetail
      exchange={selectedExchange}
      trace={selectedTrace}
      traceNavigationPending={traceNavigationPending}
      targetBinding={environment.bindings?.find((binding) => binding.service.toLowerCase() === selectedExchange.target.toLowerCase())}
      onTraceNavigate={(exchange) => void inspectExchange(exchange, selectedTrace || undefined)}
      onClose={closeExchange}
    />}
  </div>
}
