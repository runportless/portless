import { describe, expect, it } from 'vitest'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import { mergeTopologySignal, summarizeTopologyTraffic, topologyEdgeKey, topologyEdgeLabel, topologyEdgeTone, topologyParticleMotion, topologyServiceProtocols, type TopologyEdge } from './topologyModel'

const now = Date.parse('2026-09-05T18:30:00Z')
const edge: TopologyEdge = { source: 'chat', target: 'rooms', protocol: 'http' }
const ingress: TopologyEdge = { source: 'external', target: 'chat', protocol: 'http' }
const key = topologyEdgeKey(edge.source, edge.target)
const handshake = (changes: Partial<TrafficExchange> = {}): TrafficExchange => ({
  project: 'chat', environment: 'local', sequence: 1, protocol: 'http', source: 'chat', target: 'rooms', background: false,
  startedAt: new Date(now - 12).toISOString(), completedAt: new Date(now).toISOString(),
  method: 'GET', path: '/ws', status: 101, durationMs: 12, requestBytes: 0, responseBytes: 0, ...changes,
})

describe('observed topology protocols', () => {
  it('shows WebSocket edges from retained handshakes after their activity has expired', () => {
    const old = handshake({ startedAt: new Date(now - 120_012).toISOString(), completedAt: new Date(now - 120_000).toISOString() })
    const metrics = summarizeTopologyTraffic([old], now)
    const metric = metrics.get(key)

    expect(topologyEdgeLabel(edge, metric, now)).toBe('WEBSOCKET')
    expect(metric?.samples).toHaveLength(0)
    expect(metric?.activeConnections).toBe(0)
    expect(metric?.bytes).toBe(0)
    expect(topologyEdgeTone(metric, false, now)).toBe('idle')
    expect(topologyParticleMotion(metric, now).count).toBe(0)
  })

  it('recognizes a live upgrade and preserves its protocol after the activity window ends', () => {
    const metrics = mergeTopologySignal(new Map(), handshake(), now)
    const metric = metrics.get(key)

    expect(topologyEdgeLabel(edge, metric, now)).toBe('WEBSOCKET')
    expect(topologyEdgeTone(metric, false, now)).toBe('active')
    expect(topologyEdgeLabel(edge, metric, now + 31_000)).toBe('WEBSOCKET')
    expect(topologyEdgeTone(metric, false, now + 31_000)).toBe('idle')
    expect(topologyParticleMotion(metric, now + 31_000).count).toBe(0)
    expect(topologyEdgeLabel(edge, metric, now, 'rooms-delay')).toBe('▲ rooms-delay')
  })

  it('labels mixed HTTP and WebSocket traffic without losing either protocol', () => {
    const http = handshake({ sequence: 2, status: 200, path: '/messages', responseBytes: 80 })
    const snapshot = summarizeTopologyTraffic([http, handshake()], now)
    const live = mergeTopologySignal(mergeTopologySignal(new Map(), handshake(), now), http, now)

    for (const metrics of [snapshot, live]) {
      expect(topologyEdgeLabel(edge, metrics.get(key), now)).toBe('HTTP + WS')
      expect(topologyServiceProtocols('chat', [ingress, edge], metrics)).toBe('HTTP + WebSocket')
      expect(topologyServiceProtocols('rooms', [ingress, edge], metrics)).toBe('HTTP + WebSocket')
    }
  })

  it('ignores background health checks and marks only services on an observed WebSocket edge', () => {
    const metrics = summarizeTopologyTraffic([handshake(), handshake({ sequence: 2, status: 200, path: '/healthz', background: true })], now)

    expect(topologyEdgeLabel(edge, metrics.get(key), now)).toBe('WEBSOCKET')
    expect(topologyEdgeLabel(ingress, metrics.get(topologyEdgeKey('external', 'chat')), now)).toBe('HTTP')
    expect(topologyServiceProtocols('chat', [ingress, edge], metrics)).toBe('WebSocket')
    expect(topologyServiceProtocols('rooms', [ingress, edge], metrics)).toBe('WebSocket')
    expect(topologyServiceProtocols('unrelated', [ingress, edge], metrics)).toBe('')
    expect(topologyServiceProtocols('rooms', [ingress, edge], new Map())).toBe('')
  })

  it('preserves HTTP metrics and never promotes an unsuccessful upgrade to WebSocket', () => {
    const metrics = summarizeTopologyTraffic([handshake({ status: 403 })], now)

    expect(topologyEdgeLabel(edge, metrics.get(key), now)).toBe('0.03 RPS · 12MS')
    expect(topologyEdgeLabel(edge, metrics.get(key), now + 31_000)).toBe('HTTP')
    expect(topologyServiceProtocols('rooms', [edge], metrics)).toBe('')
  })
})
