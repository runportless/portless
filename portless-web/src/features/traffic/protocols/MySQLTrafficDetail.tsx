import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficDetailView, TrafficDirection } from '../detail/trafficDetailTypes'
import { DatabaseTrafficDetail } from './DatabaseTrafficDetail'
import { decodedMessagePresentation } from './DecodedMessagePresentation'

export function mysqlTrafficPresentation(exchange: TrafficExchange, direction: TrafficDirection) {
  return decodedMessagePresentation(exchange, direction)
}

export function MySQLTrafficDetail({ exchanges, view }: { exchanges: TrafficExchange[]; view: TrafficDetailView }) {
  return <DatabaseTrafficDetail exchanges={exchanges} view={view} present={mysqlTrafficPresentation} />
}
