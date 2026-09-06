import { api, environmentPath } from '../../api'
import type { Environment } from '../../api/contracts/environments'
import type { TrafficExchange, TrafficExchangeList, TrafficTrace, TrafficTraceList } from '../../api/contracts/traffic'

export interface TrafficSnapshot {
  exchanges: TrafficExchange[]
  traces: TrafficTrace[]
  throughSequence: number
  revision: number
}

export function loadTrafficTraces(environment: Pick<Environment, 'project' | 'name'>, edgeFilter: string, signal?: AbortSignal) {
  const edge = edgeFilter ? `&edge=${encodeURIComponent(edgeFilter)}` : ''
  return api<TrafficTraceList>(environmentPath(environment, `/traffic/traces?background=include&limit=1000${edge}`), { signal })
}

export async function loadTrafficSnapshot(environment: Pick<Environment, 'project' | 'name'>, edgeFilter: string, signal?: AbortSignal): Promise<TrafficSnapshot> {
  const exchangeResult = await api<TrafficExchangeList>(environmentPath(environment, '/traffic/exchanges?protocol=all&limit=1000'), { signal })
  const traceResult = await loadTrafficTraces(environment, edgeFilter, signal)
  // A filtered/limited list cannot establish a complete projection watermark.
  return { exchanges: exchangeResult.exchanges, ...traceResult }
}
