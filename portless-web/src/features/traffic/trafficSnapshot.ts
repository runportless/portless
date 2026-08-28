import { api, environmentPath } from '../../api'
import type { Environment } from '../../api/contracts/environments'
import type { TrafficExchange, TrafficExchangeList, TrafficTrace, TrafficTraceList } from '../../api/contracts/traffic'

export interface TrafficSnapshot {
  exchanges: TrafficExchange[]
  traces: TrafficTrace[]
  throughSequence: number
}

export async function loadTrafficSnapshot(environment: Pick<Environment, 'project' | 'name'>, edgeFilter: string): Promise<TrafficSnapshot> {
  // The trace projection must be captured after this cutoff. Running these
  // requests concurrently can let an older trace response prune a live event.
  const exchangeResult = await api<TrafficExchangeList>(environmentPath(environment, '/traffic/exchanges?protocol=all&limit=1000'))
  const throughSequence = exchangeResult.exchanges.reduce((highest, exchange) => Math.max(highest, exchange.sequence), 0)
  const edge = edgeFilter ? `&edge=${encodeURIComponent(edgeFilter)}` : ''
  const traceResult = await api<TrafficTraceList>(environmentPath(environment, `/traffic/traces?background=include&limit=1000${edge}`))

  return { exchanges: exchangeResult.exchanges, traces: traceResult.traces, throughSequence }
}
