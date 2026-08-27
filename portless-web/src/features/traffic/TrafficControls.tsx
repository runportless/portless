import { duration } from '../../components/Status'
import type { TrafficProtocolFilter, TrafficResultFilter } from './trafficState'
import type { TrafficMode } from './useTrafficView'

type TrafficSummary = {
  exchanges: number
  requestsPerSecond: number
  errors: number
  p50: number
  p95: number
}

export function TrafficControls({ mode, onMode, stream, summary, filters }: {
  mode: TrafficMode
  onMode: (mode: TrafficMode) => void
  stream: {
    paused: boolean
    bufferedCount: number
    clearing: boolean
    empty: boolean
    onClear: () => void
    onTogglePaused: () => void
  }
  summary: TrafficSummary
  filters: {
    search: string
    result: TrafficResultFilter
    protocol: TrafficProtocolFilter
    includeTCPRoots: boolean
    includeBackground: boolean
    edge: string
    onSearch: (value: string) => void
    onResult: (value: TrafficResultFilter) => void
    onProtocol: (value: TrafficProtocolFilter) => void
    onToggleTCPRoots: () => void
    onToggleBackground: () => void
    onClearEdge: () => void
  }
}) {
  return <>
    <div className="traffic-header">
      <div className="traffic-modes" role="tablist" aria-label="Traffic view">
        <button role="tab" aria-selected={mode === 'traces'} className={mode === 'traces' ? 'is-active' : ''} onClick={() => onMode('traces')}>TRACES</button>
        <button role="tab" aria-selected={mode === 'exchanges'} className={mode === 'exchanges' ? 'is-active' : ''} onClick={() => onMode('exchanges')}>EXCHANGES</button>
      </div>
      <div className="traffic-stream-controls"><span className={`live-count${stream.paused ? ' is-paused' : ''}`}>{stream.paused ? <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg> : <i />}{stream.paused ? `PAUSED${stream.bufferedCount ? ` · ${stream.bufferedCount} BUFFERED` : ''}` : 'STREAMING'}</span><button className="button button--small button--quiet" type="button" title="Clear retained traces and exchanges" disabled={stream.clearing || stream.empty} onClick={stream.onClear}>{stream.clearing ? 'CLEARING…' : 'CLEAR'}</button><button className="button button--small" type="button" disabled={stream.clearing} onClick={stream.onTogglePaused}>{stream.paused ? 'RESUME' : 'PAUSE'}</button></div>
    </div>
    <div className="traffic-summary" aria-label="Traffic in the last 60 seconds"><span>LAST 60S</span><strong>{summary.exchanges} exchanges</strong><strong>{summary.requestsPerSecond.toFixed(1)} rps</strong><strong className={summary.errors ? 'danger-text' : ''}>{summary.errors} errors</strong><strong>p50 {duration(summary.p50)}</strong><strong>p95 {duration(summary.p95)}</strong></div>
    <div className="traffic-filters">
      <input value={filters.search} onChange={(event) => filters.onSearch(event.target.value)} placeholder="filter path, service, edge, status…" aria-label="Filter traffic" />
      <select value={filters.result} onChange={(event) => filters.onResult(event.target.value as TrafficResultFilter)} aria-label="Traffic result filter"><option value="all">All results</option><option value="errors">Errors</option><option value="slow">Slow · 500ms+</option><option value="faulted">Faulted</option></select>
      {mode === 'exchanges' && <div className="traffic-protocol" role="group" aria-label="Traffic protocol">{(['all', 'http', 'tcp'] as const).map((value) => <button key={value} className={filters.protocol === value ? 'is-active' : ''} onClick={() => filters.onProtocol(value)}>{value.toUpperCase()}</button>)}</div>}
      {mode === 'traces' && <>
        <button className={`traffic-trace-option${filters.includeTCPRoots ? ' is-active' : ''}`} type="button" aria-pressed={filters.includeTCPRoots} onClick={filters.onToggleTCPRoots}>SHOW TCP ROOTS</button>
        <button className={`traffic-trace-option${filters.includeBackground ? ' is-active' : ''}`} type="button" aria-pressed={filters.includeBackground} onClick={filters.onToggleBackground}>SHOW BACKGROUND</button>
      </>}
      {filters.edge && <button className="traffic-filter-chip" type="button" onClick={filters.onClearEdge}><span>EDGE</span>{filters.edge.replace(':', ' → ')} ×</button>}
    </div>
  </>
}
