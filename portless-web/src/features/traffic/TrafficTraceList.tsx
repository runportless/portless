import type { Pagination } from '../../components/PanelPagination'
import { PanelPagination } from '../../components/PanelPagination'
import { duration } from '../../components/Status'
import type { TrafficTrace } from '../../types'
import { trafficStartedTime } from './detail/TrafficOverview'
import { traceRequest, trafficResultTone } from './TrafficListPresentation'
import { TrafficTableHeader } from './TrafficTableHeader'
import { TraceWaterfall, type TraceNavigationItem } from './TraceWaterfall'

export function TraceSummaryRow({ trace, expanded, onToggle }: { trace: TrafficTrace; expanded: boolean; onToggle: () => void }) {
  return <button className="trace-row" type="button" onClick={onToggle} aria-expanded={expanded}>
    <span>{trafficStartedTime(trace.startedAt)}</span><strong className="truncate">{traceRequest(trace)}</strong><span className={trafficResultTone(trace.error, trace.status)}>{trace.error ? 'ERR' : trace.status || 'OK'}</span><span>{duration(trace.durationMs)}</span><span>{trace.spanCount}</span><span className={`correlation-badge correlation-badge--${trace.correlation}`}>{trace.correlation}</span>
  </button>
}

export function TrafficTraceList({ pagination, expandedTrace, includeBackground, onToggleTrace, onInspect, onPage }: {
  pagination: Pagination<TrafficTrace>
  expandedTrace: number | null
  includeBackground: boolean
  onToggleTrace: (trace: TrafficTrace) => void
  onInspect: (item: TraceNavigationItem, trace: TrafficTrace) => void
  onPage: (page: number) => void
}) {
  return <div className="trace-list">
    <TrafficTableHeader mode="traces" />
    {pagination.items.map((trace) => <div className={`trace-card${expandedTrace === trace.number ? ' is-expanded' : ''}`} key={trace.number}>
      <TraceSummaryRow trace={trace} expanded={expandedTrace === trace.number} onToggle={() => onToggleTrace(trace)} />
      {expandedTrace === trace.number && (trace.spans?.length ? <TraceWaterfall trace={trace} includeBackground={includeBackground} onItem={(item) => onInspect(item, trace)} /> : <div className="trace-loading">Loading trace spans…</div>)}
    </div>)}
    {pagination.total === 0 && <div className="empty-row">No matching traces yet. Open an application endpoint or exercise a service connection to capture one.</div>}
    <PanelPagination label="traces" pagination={pagination} onPage={onPage} />
  </div>
}
