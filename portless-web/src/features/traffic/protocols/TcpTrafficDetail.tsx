import type { TrafficExchange } from '../../../api/contracts/traffic'
import { CompareIcon } from '../detail/TrafficFormatting'
import type { TrafficDetailView } from '../detail/trafficDetailTypes'
import { GenericTcpTrafficDetail } from './GenericTcpTrafficDetail'
import { MySQLTrafficDetail } from './MySQLTrafficDetail'
import { PostgreSQLTrafficDetail } from './PostgreSQLTrafficDetail'
import { RedisTrafficDetail } from './RedisTrafficDetail'

function TcpProtocolDetail({ exchange, exchanges, view }: { exchange: TrafficExchange; exchanges: TrafficExchange[]; view: TrafficDetailView }) {
  const protocol = exchange.tcp?.applicationProtocol?.toLowerCase()
  if (protocol === 'postgresql') return <PostgreSQLTrafficDetail exchanges={exchanges} view={view} />
  if (protocol === 'redis') return <RedisTrafficDetail exchanges={exchanges} view={view} />
  if (protocol === 'mysql') return <MySQLTrafficDetail exchanges={exchanges} view={view} />
  return <GenericTcpTrafficDetail exchanges={exchanges} view={view} />
}

export function TcpTrafficDetail({ exchange, exchanges, maximized, view, onView }: {
  exchange: TrafficExchange
  exchanges: TrafficExchange[]
  maximized: boolean
  view: TrafficDetailView
  onView: (view: TrafficDetailView) => void
}) {
  return <>
    <nav className="traffic-detail__tabs" role="tablist" aria-label="Exchange payload">
      <button type="button" role="tab" aria-selected={view === 'request'} className={view === 'request' ? 'is-active' : ''} onClick={() => onView('request')}>COMMAND</button>
      <button type="button" role="tab" aria-selected={view === 'response'} className={view === 'response' ? 'is-active' : ''} onClick={() => onView('response')}>RESULT</button>
      {maximized && <button className="traffic-detail__compare" type="button" role="tab" aria-selected={view === 'compare'} onClick={() => onView('compare')}><CompareIcon />COMPARE</button>}
    </nav>
    <TcpProtocolDetail exchange={exchange} exchanges={exchanges} view={view} />
  </>
}
