import type { Pagination } from '../../components/PanelPagination'
import { PanelPagination } from '../../components/PanelPagination'
import { duration } from '../../components/Status'
import type { TrafficExchange } from '../../api/contracts/traffic'
import { trafficStartedTime } from './detail/TrafficOverview'
import { exchangeOperation, exchangeResult, trafficResultTone } from './TrafficListPresentation'
import { TrafficTableHeader } from './TrafficTableHeader'

export function TrafficExchangeList({ pagination, onInspect, onPage }: {
  pagination: Pagination<TrafficExchange>
  onInspect: (exchange: TrafficExchange) => void
  onPage: (page: number) => void
}) {
  return <div className="exchange-list">
    <TrafficTableHeader mode="exchanges" />
    {pagination.items.map((exchange) => <button className="table-row traffic-row" key={exchange.sequence} onClick={() => onInspect(exchange)}><code>#{exchange.sequence}</code><span>{trafficStartedTime(exchange.startedAt)}</span><strong>{exchange.protocol === 'tcp' ? exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP' : 'HTTP'}</strong><code className="truncate">{exchangeOperation(exchange)}</code><span>{exchange.source}<i className="edge-arrow">→</i>{exchange.target}</span><span className={trafficResultTone(exchange.error || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete', exchange.status)}>{exchangeResult(exchange)}</span><span>{duration(exchange.durationMs)}</span><span>{exchange.fault ? <b className="fault-chip">▲ {exchange.fault}</b> : exchange.recording ? <b className="record-chip">● {exchange.recording}</b> : '—'}</span></button>)}
    {pagination.total === 0 && <div className="empty-row">No matching exchanges yet.</div>}
    <PanelPagination label="exchanges" pagination={pagination} onPage={onPage} />
  </div>
}
