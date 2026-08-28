import type { Environment, Service, TrafficActivity, TrafficExchange } from '../../../types'

export type TopologyItem = { kind: 'client'; key: 'external' } | { kind: 'service'; key: string; service: Service }
export type TopologySignal = TrafficExchange | TrafficActivity
export type TopologyEdgeMetric = {
  samples: Array<{ observedAt: number; duration: number; error: boolean }>
  bytes: number
  activeConnections: number
  lastSeen: number
  latestSequence: number
  fault?: string
  faultSeen?: number
}
export type TopologyEdge = ReturnType<typeof buildTopology>['edges'][number]

export const topologyWindowMilliseconds = 30_000
export const topologyInactiveArrowSize = 6
export const topologyActiveArrowSize = 10.62
export const topologyInactiveEdgeVisual = { strokeWidth: 1, markerID: 'topology-arrow-inactive' } as const
export const topologyActiveEdgeVisual = { strokeWidth: 1.77, markerID: 'topology-arrow-active' } as const

export function topologyEdgeKey(source: string, target: string) {
  return `${source}\u0000${target}`
}

export function summarizeTopologyTraffic(events: TrafficExchange[], now = Date.now()) {
  const metrics = new Map<string, TopologyEdgeMetric>()
  for (const event of events) {
    if (event.background) continue
    const observedAt = new Date(event.completedAt || event.startedAt).getTime()
    if (!Number.isFinite(observedAt) || now - observedAt > topologyWindowMilliseconds) continue
    const key = topologyEdgeKey(event.source, event.target)
    const current = metrics.get(key) || emptyTopologyMetric()
    current.samples.push({ observedAt, duration: event.durationMs || 0, error: topologyExchangeError(event) })
    current.bytes += Math.max(0, event.requestBytes || 0) + Math.max(0, event.responseBytes || 0)
    current.lastSeen = Math.max(current.lastSeen, observedAt)
    current.latestSequence = Math.max(current.latestSequence, event.sequence || 0)
    if (event.fault) {
      current.fault = event.fault
      current.faultSeen = observedAt
    }
    metrics.set(key, current)
  }
  return metrics
}

export function mergeTopologySignal(metrics: Map<string, TopologyEdgeMetric>, signal: TopologySignal, now = Date.now()) {
  if (!('phase' in signal) && signal.background) return metrics
  const key = topologyEdgeKey(signal.source, signal.target)
  const next = new Map(metrics)
  const current = { ...(next.get(key) || emptyTopologyMetric()) }
  current.samples = current.samples.filter((sample) => now - sample.observedAt <= topologyWindowMilliseconds)
  if ('phase' in signal) {
    current.activeConnections = Math.max(0, signal.activeConnections || 0)
    const observedAt = new Date(signal.observedAt).getTime() || now
    const bytes = Math.max(0, signal.requestBytes || 0) + Math.max(0, signal.responseBytes || 0)
    if (!signal.applicationProtocol && bytes > 0) {
      current.bytes += bytes
      current.lastSeen = observedAt
    }
    if (signal.fault) {
      current.fault = signal.fault
      current.faultSeen = observedAt
      current.lastSeen = observedAt
    }
  } else {
    const observedAt = new Date(signal.completedAt || signal.startedAt).getTime() || now
    current.samples.push({ observedAt, duration: signal.durationMs || 0, error: topologyExchangeError(signal) })
    current.bytes += Math.max(0, signal.requestBytes || 0) + Math.max(0, signal.responseBytes || 0)
    current.lastSeen = observedAt
    current.latestSequence = Math.max(current.latestSequence, signal.sequence || 0)
    if (signal.fault) {
      current.fault = signal.fault
      current.faultSeen = observedAt
    }
  }
  next.set(key, current)
  return next
}

export function topologyEdgeTone(metric: TopologyEdgeMetric | undefined, hasFault: boolean, now = Date.now()) {
  if (hasFault) return 'fault'
  if (!metric || now - metric.lastSeen > topologyWindowMilliseconds) return 'idle'
  if (metric.fault && metric.faultSeen && now - metric.faultSeen <= topologyWindowMilliseconds) return 'fault'
  const samples = metric.samples.filter((sample) => now - sample.observedAt <= topologyWindowMilliseconds)
  if (samples.some((sample) => sample.error)) return 'error'
  if (samples.length > 0 && samples.reduce((sum, sample) => sum + sample.duration, 0) / samples.length >= 500) return 'slow'
  return 'active'
}

export function topologyEdgeLabel(edge: TopologyEdge, metric: TopologyEdgeMetric | undefined, now: number, activeFault?: string) {
  if (activeFault) return `▲ ${activeFault}`
  if (!metric) return edge.protocol.toUpperCase()
  if (now - metric.lastSeen > topologyWindowMilliseconds) return edge.protocol !== 'http' && metric.activeConnections > 0 ? `${metric.activeConnections} OPEN` : edge.protocol.toUpperCase()
  if (edge.protocol !== 'http') return metric.activeConnections > 0 ? `${metric.activeConnections} OPEN · ${formatBytes(metric.bytes)}` : formatBytes(metric.bytes)
  const samples = metric.samples.filter((sample) => now - sample.observedAt <= topologyWindowMilliseconds)
  const requestsPerSecond = samples.length / (topologyWindowMilliseconds / 1000)
  const average = samples.length ? Math.round(samples.reduce((sum, sample) => sum + sample.duration, 0) / samples.length) : 0
  const errors = samples.filter((sample) => sample.error).length
  return `${requestsPerSecond < 0.1 ? requestsPerSecond.toFixed(2) : requestsPerSecond.toFixed(1)} RPS · ${average}MS${errors ? ` · ${errors} ERR` : ''}`
}

export function topologyEdgeVisualState(metric: TopologyEdgeMetric | undefined, now: number, hasFault: boolean) {
  return hasFault || (metric && now - metric.lastSeen <= topologyWindowMilliseconds) ? topologyActiveEdgeVisual : topologyInactiveEdgeVisual
}

export function topologyParticleMotion(metric: TopologyEdgeMetric | undefined, now: number) {
  if (!metric || now - metric.lastSeen > topologyWindowMilliseconds) return { count: 0, durationSeconds: 0 }
  const recentRequests = metric.samples.filter((sample) => now - sample.observedAt <= topologyWindowMilliseconds).length
  if (recentRequests > 0) {
    const requestsPerSecond = recentRequests / (topologyWindowMilliseconds / 1000)
    const count = requestsPerSecond >= 5 ? 4 : requestsPerSecond >= 2 ? 3 : requestsPerSecond >= 0.75 ? 2 : 1
    return { count, durationSeconds: Math.min(12, Math.max(0.9, count / requestsPerSecond)) }
  }
  if (metric.activeConnections > 0) return { count: Math.min(3, metric.activeConnections), durationSeconds: 3.5 }
  if (metric.bytes > 0) return { count: 1, durationSeconds: 4.5 }
  return { count: 0, durationSeconds: 0 }
}

export function topologyPanPosition(origin: { clientX: number; clientY: number; scrollLeft: number; scrollTop: number }, clientX: number, clientY: number) {
  return { scrollLeft: origin.scrollLeft - (clientX - origin.clientX), scrollTop: origin.scrollTop - (clientY - origin.clientY) }
}

export function topologyCenterPosition(viewport: { scrollWidth: number; clientWidth: number; scrollHeight: number; clientHeight: number }) {
  return {
    scrollLeft: Math.max(0, (viewport.scrollWidth - viewport.clientWidth) / 2),
    scrollTop: Math.min(120, Math.max(0, (viewport.scrollHeight - viewport.clientHeight) / 2)),
  }
}

export function buildTopology(environment: Environment) {
  const services = new Map(environment.services.map((service) => [service.name, service]))
  const primary = environment.primaryService && services.has(environment.primaryService) ? environment.primaryService : environment.services[0]?.name
  const graphEdges = environment.connections.filter((connection) => services.has(connection.source) && services.has(connection.target))
  const edges = [...(primary ? [{ source: 'external', target: primary, protocol: 'http' as const }] : []), ...graphEdges]
  const depths = new Map<string, number>(primary ? [[primary, 1]] : [])
  const incoming = new Map<string, string[]>()
  for (const edge of graphEdges) incoming.set(edge.target, [...(incoming.get(edge.target) || []), edge.source])
  const depthFor = (name: string, visiting = new Set<string>()): number => {
    const known = depths.get(name)
    if (known) return known
    if (visiting.has(name)) return 1
    visiting.add(name)
    let depth = 1
    for (const source of incoming.get(name) || []) {
      if (source !== name) depth = Math.max(depth, depthFor(source, visiting) + 1)
    }
    visiting.delete(name)
    depths.set(name, depth)
    return depth
  }
  for (const service of environment.services) depthFor(service.name)
  const maxDepth = Math.max(1, ...depths.values())
  const levels: TopologyItem[][] = [[{ kind: 'client', key: 'external' }]]
  for (let depth = 1; depth <= maxDepth; depth++) {
    const level = environment.services
      .filter((service) => depths.get(service.name) === depth)
      .map((service): TopologyItem => ({ kind: 'service', key: service.name, service }))
    if (level.length) levels.push(level)
  }
  return { levels, edges }
}

function emptyTopologyMetric(): TopologyEdgeMetric {
  return { samples: [], bytes: 0, activeConnections: 0, lastSeen: 0, latestSequence: 0 }
}

function topologyExchangeError(exchange: TrafficExchange) {
  return !!exchange.error || (exchange.status || 0) >= 500 || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete'
}

function formatBytes(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)} MB`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)} KB`
  return `${value} B`
}
