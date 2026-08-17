import { useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { duration } from '../../components/Status'
import type { Environment, TrafficExchange, TrafficTrace } from '../../types'
import { TrafficDetail } from './TrafficDetail'
import { TraceWaterfall } from './TraceWaterfall'
import { filterExchanges, filterTraces, mergeExchanges, mergeTraces, reconcileExchanges, reconcileTraces, trafficWindowSummary, type TrafficProtocolFilter, type TrafficResultFilter } from './trafficState'

type TrafficMode = 'traces' | 'exchanges'

function traceRequest(trace: TrafficTrace) {
  const value = `${trace.method || ''} ${trace.requestTarget || ''}`.trim()
  return value || `${trace.source} → ${trace.target}`
}

function resultTone(error: boolean | string | undefined, status: number | undefined) {
  if (error || (status || 0) >= 500) return 'danger-text'
  if ((status || 0) >= 400) return 'warning-text'
  return ''
}

function traceHasEdge(trace: TrafficTrace, edge: string) {
  if (!edge) return true
  return (trace.spans || []).some((span) => `${span.exchange.source}:${span.exchange.target}` === edge)
}

function exchangeHasEdge(exchange: TrafficExchange, edge: string) {
  return !edge || `${exchange.source}:${exchange.target}` === edge
}

export function TrafficPanel({ environment }: { environment: Environment }) {
  const requested = new URLSearchParams(location.search)
  const [mode, setMode] = useState<TrafficMode>(() => requested.get('mode') === 'exchanges' ? 'exchanges' : 'traces')
  const [traces, setTraces] = useState<TrafficTrace[]>([])
  const [exchanges, setExchanges] = useState<TrafficExchange[]>([])
  const [selectedExchange, setSelectedExchange] = useState<TrafficExchange | null>(null)
  const [expandedTrace, setExpandedTrace] = useState<number | null>(null)
  const [search, setSearch] = useState('')
  const [edgeFilter, setEdgeFilter] = useState(() => requested.get('edge') || '')
  const [resultFilter, setResultFilter] = useState<TrafficResultFilter>('all')
  const [protocol, setProtocol] = useState<TrafficProtocolFilter>(() => {
    const value = requested.get('protocol')
    return value === 'http' || value === 'tcp' ? value : 'all'
  })
  const [includeBackground, setIncludeBackground] = useState(false)
  const [paused, setPaused] = useState(false)
  const [bufferedCount, setBufferedCount] = useState(0)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const pausedRef = useRef(false)
  const knownExchanges = useRef(new Set<number>())
  const exchangeBuffer = useRef(new Map<number, TrafficExchange>())
  const traceBuffer = useRef(new Map<number, TrafficTrace>())
  const expandedRef = useRef<number | null>(null)

  useEffect(() => { knownExchanges.current = new Set(exchanges.map((exchange) => exchange.sequence)) }, [exchanges])
  useEffect(() => { expandedRef.current = expandedTrace }, [expandedTrace])

  useEffect(() => {
    let active = true
    setTraces([]); setExchanges([]); setSelectedExchange(null); setExpandedTrace(null); setError(null)
    pausedRef.current = false; setPaused(false); knownExchanges.current.clear()
    exchangeBuffer.current.clear(); traceBuffer.current.clear(); setBufferedCount(0)
    const traceQuery = `/traffic/traces?background=include&limit=1000${edgeFilter ? `&edge=${encodeURIComponent(edgeFilter)}` : ''}`
    const load = async () => {
      try {
        const [exchangeResult, traceResult] = await Promise.all([
          api<{ exchanges: TrafficExchange[] }>(environmentPath(environment, '/traffic/exchanges?protocol=all&limit=1000')),
          api<{ traces: TrafficTrace[] }>(environmentPath(environment, traceQuery)),
        ])
        if (!active) return
        if (pausedRef.current) {
          for (const exchange of exchangeResult.exchanges) if (!knownExchanges.current.has(exchange.sequence)) exchangeBuffer.current.set(exchange.sequence, exchange)
          for (const trace of traceResult.traces) traceBuffer.current.set(trace.number, trace)
          setBufferedCount(exchangeBuffer.current.size)
        } else {
          const highWater = exchangeResult.exchanges.reduce((highest, exchange) => Math.max(highest, exchange.sequence), 0)
          setExchanges((current) => reconcileExchanges(current, exchangeResult.exchanges))
          setTraces((current) => reconcileTraces(current, traceResult.traces, highWater))
        }
      } catch (value) {
        if (active) setError(actionError("Traffic couldn't be loaded", value))
      }
    }
    void load()
    const disconnect = connectEvents(environment, ['traffic.exchange', 'traffic.trace'], (type, value) => {
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

  const inspectExchange = async (exchange: TrafficExchange) => {
    try {
      setError(null)
      setSelectedExchange(await api<TrafficExchange>(environmentPath(environment, `/traffic/exchanges/${exchange.sequence}`)))
    } catch (value) {
      setError(actionError("Traffic details aren't available", value))
    }
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

  const windowSummary = useMemo(() => trafficWindowSummary(exchanges), [exchanges])
  const visibleTraces = useMemo(() => filterTraces(traces, search, resultFilter, includeBackground), [traces, search, resultFilter, includeBackground])
  const visibleExchanges = useMemo(() => filterExchanges(exchanges.filter((exchange) => exchangeHasEdge(exchange, edgeFilter)), search, resultFilter, protocol), [exchanges, edgeFilter, search, resultFilter, protocol])

  return <div className="traffic-view">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel traffic-panel">
      <div className="traffic-header">
        <div className="traffic-modes" role="tablist" aria-label="Traffic view">
          <button role="tab" aria-selected={mode === 'traces'} className={mode === 'traces' ? 'is-active' : ''} onClick={() => setMode('traces')}>TRACES</button>
          <button role="tab" aria-selected={mode === 'exchanges'} className={mode === 'exchanges' ? 'is-active' : ''} onClick={() => setMode('exchanges')}>EXCHANGES</button>
        </div>
        <div className="traffic-stream-controls"><span className={`live-count${paused ? ' is-paused' : ''}`}>{paused ? <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg> : <i />}{paused ? `PAUSED${bufferedCount ? ` · ${bufferedCount} BUFFERED` : ''}` : 'STREAMING'}</span><button className="button button--small" type="button" onClick={togglePaused}>{paused ? 'RESUME' : 'PAUSE'}</button></div>
      </div>
      <div className="traffic-summary" aria-label="Traffic in the last 60 seconds"><span>LAST 60S</span><strong>{windowSummary.exchanges} exchanges</strong><strong>{windowSummary.requestsPerSecond.toFixed(1)} rps</strong><strong className={windowSummary.errors ? 'danger-text' : ''}>{windowSummary.errors} errors</strong><strong>p50 {duration(windowSummary.p50)}</strong><strong>p95 {duration(windowSummary.p95)}</strong></div>
      <div className="traffic-filters">
        <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="filter path, service, edge, status…" aria-label="Filter traffic" />
        <select value={resultFilter} onChange={(event) => setResultFilter(event.target.value as TrafficResultFilter)} aria-label="Traffic result filter"><option value="all">All results</option><option value="errors">Errors</option><option value="slow">Slow · 500ms+</option><option value="faulted">Faulted</option></select>
        {mode === 'exchanges' && <div className="traffic-protocol" role="group" aria-label="Traffic protocol">{(['all', 'http', 'tcp'] as const).map((value) => <button key={value} className={protocol === value ? 'is-active' : ''} onClick={() => setProtocol(value)}>{value.toUpperCase()}</button>)}</div>}
        {mode === 'traces' && <button className={`traffic-background${includeBackground ? ' is-active' : ''}`} type="button" aria-pressed={includeBackground} onClick={() => setIncludeBackground((value) => !value)}>BACKGROUND</button>}
        {edgeFilter && <button className="traffic-filter-chip" type="button" onClick={() => setEdgeFilter('')}><span>EDGE</span>{edgeFilter.replace(':', ' → ')} ×</button>}
      </div>

      {mode === 'traces' ? <div className="trace-list">
        <div className="trace-row trace-row--header"><span>When</span><span>Root request</span><span>Result</span><span>Duration</span><span>Spans</span><span>Correlation</span></div>
        {visibleTraces.map((trace) => <div className={`trace-card${expandedTrace === trace.number ? ' is-expanded' : ''}`} key={trace.number}>
          <button className="trace-row" type="button" onClick={() => void toggleTrace(trace)} aria-expanded={expandedTrace === trace.number}>
            <span><code>#{trace.number}</code>{new Date(trace.startedAt).toLocaleTimeString()}</span><strong className="truncate">{traceRequest(trace)}</strong><span className={resultTone(trace.error, trace.status)}>{trace.error ? 'ERR' : trace.status || 'OK'}</span><span>{duration(trace.durationMs)}</span><span>{trace.spanCount}</span><span className={`correlation-badge correlation-badge--${trace.correlation}`}>{trace.correlation}</span>
          </button>
          {expandedTrace === trace.number && (trace.spans?.length ? <TraceWaterfall trace={trace} onExchange={(exchange) => void inspectExchange(exchange)} /> : <div className="trace-loading">Loading trace spans…</div>)}
        </div>)}
        {visibleTraces.length === 0 && <div className="empty-row">No matching traces yet. Open an application endpoint or exercise a service connection to capture one.</div>}
      </div> : <div className="exchange-list">
        <div className="table-row table-row--header traffic-row"><span>Seq</span><span>When</span><span>Protocol</span><span>Request / session</span><span>Edge</span><span>Result</span><span>Duration</span><span>Fault / recording</span></div>
        {visibleExchanges.map((exchange) => <button className="table-row traffic-row" key={exchange.sequence} onClick={() => void inspectExchange(exchange)}><code>#{exchange.sequence}</code><span>{new Date(exchange.startedAt).toLocaleTimeString()}</span><strong>{exchange.protocol.toUpperCase()}</strong><code className="truncate">{exchange.protocol === 'http' ? `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}` : 'TCP session'}</code><span>{exchange.source}<i className="edge-arrow">→</i>{exchange.target}</span><span className={resultTone(exchange.error, exchange.status)}>{exchange.error ? 'ERR' : exchange.status || 'OK'}</span><span>{duration(exchange.durationMs)}</span><span>{exchange.fault ? <b className="fault-chip">▲ {exchange.fault}</b> : exchange.recording ? <b className="record-chip">● {exchange.recording}</b> : '—'}</span></button>)}
        {visibleExchanges.length === 0 && <div className="empty-row">No matching exchanges yet.</div>}
      </div>}
    </section>
    {selectedExchange && <TrafficDetail exchange={selectedExchange} onClose={() => setSelectedExchange(null)} />}
  </div>
}
