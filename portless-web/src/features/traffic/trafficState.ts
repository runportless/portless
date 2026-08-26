import type { TrafficExchange, TrafficTrace } from '../../types'

export type TrafficResultFilter = 'all' | 'errors' | 'slow' | 'faulted'
export type TrafficProtocolFilter = 'all' | 'http' | 'tcp'

export function mergeExchanges(current: TrafficExchange[], incoming: TrafficExchange[], limit = 2000) {
  const bySequence = new Map(current.map((exchange) => [exchange.sequence, exchange]))
  for (const exchange of incoming) bySequence.set(exchange.sequence, exchange)
  return [...bySequence.values()].sort((left, right) => right.sequence-left.sequence).slice(0, limit)
}

export function mergeTraces(current: TrafficTrace[], incoming: TrafficTrace[], limit = 1000) {
  const byNumber = new Map(current.map((trace) => [trace.number, trace]))
  for (const trace of incoming) byNumber.set(trace.number, { ...byNumber.get(trace.number), ...trace })
  return [...byNumber.values()].sort((left, right) => {
    const time = new Date(right.startedAt).getTime()-new Date(left.startedAt).getTime()
    return time || right.number-left.number
  }).slice(0, limit)
}

export function reconcileExchanges(current: TrafficExchange[], snapshot: TrafficExchange[], limit = 2000) {
  const highWater = snapshot.reduce((highest, exchange) => Math.max(highest, exchange.sequence), 0)
  const newer = current.filter((exchange) => exchange.sequence > highWater)
  return mergeExchanges([], [...snapshot, ...newer], limit)
}

export function reconcileTraces(current: TrafficTrace[], snapshot: TrafficTrace[], exchangeHighWater: number, limit = 1000) {
  const currentByNumber = new Map(current.map((trace) => [trace.number, trace]))
  const reconciled = snapshot.map((trace) => {
    const existing = currentByNumber.get(trace.number)
    return existing?.lastSequence === trace.lastSequence && existing.spans?.length ? { ...trace, spans: existing.spans } : trace
  })
  const newer = current.filter((trace) => trace.lastSequence > exchangeHighWater)
  return mergeTraces([], [...reconciled, ...newer], limit)
}

export function filterExchanges(exchanges: TrafficExchange[], search: string, result: TrafficResultFilter, protocol: TrafficProtocolFilter) {
  const query = search.trim().toLowerCase()
  return exchanges.filter((exchange) => {
    if (protocol !== 'all' && exchange.protocol !== protocol) return false
    if (!matchesResult(exchange.error, exchange.status, exchange.durationMs, exchange.fault, result)) return false
    if (!query) return true
    return `${exchange.method || ''} ${exchange.requestTarget || exchange.path || ''} ${exchange.tcp?.applicationProtocol || ''} ${exchange.tcp?.operation || ''} ${exchange.source} ${exchange.target} ${exchange.source}:${exchange.target} ${exchange.status || ''} ${exchange.fault || ''} ${exchange.recording || ''}`.toLowerCase().includes(query)
  })
}

export function filterTraces(traces: TrafficTrace[], search: string, result: TrafficResultFilter, includeBackground: boolean) {
  const query = search.trim().toLowerCase()
  return traces.filter((trace) => {
    if (trace.provisional) return false
    if (trace.background && !includeBackground) return false
    if (!matchesResult(trace.error, trace.status, trace.durationMs, trace.faulted ? 'faulted' : '', result)) return false
    if (!query) return true
    return `${trace.method || ''} ${trace.requestTarget || ''} ${trace.source} ${trace.target} ${trace.source}:${trace.target} ${trace.status || ''} ${trace.correlation}`.toLowerCase().includes(query)
  })
}

function matchesResult(error: string | boolean | undefined, status: number | undefined, durationMs: number, fault: string | undefined, result: TrafficResultFilter) {
  if (result === 'errors') return !!error || (status || 0) >= 400
  if (result === 'slow') return durationMs >= 500
  if (result === 'faulted') return !!fault
  return true
}

export function trafficWindowSummary(exchanges: TrafficExchange[], now = Date.now()) {
  const recent = exchanges.filter((exchange) => now-new Date(exchange.completedAt).getTime() <= 60_000)
  const http = recent.filter((exchange) => exchange.protocol === 'http')
  const durations = http.map((exchange) => exchange.durationMs).sort((left, right) => left-right)
  const percentile = (value: number) => durations.length ? durations[Math.max(0, Math.ceil(value*durations.length)-1)] : 0
  return {
    exchanges: recent.length,
    requestsPerSecond: http.length/60,
    errors: recent.filter((exchange) => !!exchange.error || (exchange.status || 0) >= 400).length,
    p50: percentile(.5),
    p95: percentile(.95),
  }
}
