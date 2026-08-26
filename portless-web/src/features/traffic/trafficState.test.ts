import { describe, expect, it } from 'vitest'
import type { TrafficExchange, TrafficTrace } from '../../types'
import { filterExchanges, filterTraces, mergeExchanges, mergeTraces, reconcileExchanges, reconcileTraces, trafficWindowSummary } from './trafficState'

function exchange(sequence: number, overrides: Partial<TrafficExchange> = {}): TrafficExchange {
  return {
    project: 'store', environment: 'local', sequence, protocol: 'http',
    source: 'checkout', target: 'orders', startedAt: '2026-08-17T12:00:00Z',
    completedAt: '2026-08-17T12:00:00.100Z', method: 'GET', requestTarget: '/orders',
    background: false, durationMs: 100, requestBytes: 0, responseBytes: 10, status: 200,
    ...overrides,
  }
}

function trace(number: number, overrides: Partial<TrafficTrace> = {}): TrafficTrace {
  return {
    project: 'store', environment: 'local', number, lastSequence: number,
    protocol: 'http', provisional: false,
    startedAt: '2026-08-17T12:00:00Z', completedAt: '2026-08-17T12:00:00.100Z',
    durationMs: 100, method: 'GET', requestTarget: '/orders', source: 'external', target: 'checkout',
    status: 200, error: false, faulted: false, background: false, spanCount: 2, correlation: 'inferred',
    ...overrides,
  }
}

describe('traffic state', () => {
  it('merges snapshot and stream updates without duplicates', () => {
    expect(mergeExchanges([exchange(2), exchange(1)], [exchange(2, { status: 503 }), exchange(3)]))
      .toEqual([exchange(3), exchange(2, { status: 503 }), exchange(1)])
    expect(mergeTraces([trace(1)], [trace(1, { spanCount: 3 }), trace(2)]).map((item) => [item.number, item.spanCount]))
      .toEqual([[2, 2], [1, 3]])
  })

  it('removes stale trace fragments without dropping events newer than a snapshot', () => {
    const snapshotExchanges = [exchange(5), exchange(4), exchange(3)]
    expect(reconcileExchanges([exchange(6), exchange(2)], snapshotExchanges).map((item) => item.sequence)).toEqual([6, 5, 4, 3])

    const finalTrace = trace(1, { lastSequence: 5, spanCount: 5 })
    const current = [trace(1), trace(2), trace(3), trace(6, { lastSequence: 6 })]
    expect(reconcileTraces(current, [finalTrace], 5).map((item) => item.number)).toEqual([6, 1])

	const detailed = trace(1, { lastSequence: 5, spans: [] })
	detailed.spans = [{ exchange: exchange(1), depth: 0, startOffsetMs: 0, correlation: 'exact' }]
	expect(reconcileTraces([detailed], [finalTrace], 5)[0].spans).toHaveLength(1)
  })

  it('filters exchanges and traces by operator-visible fields', () => {
    const exchanges = [
      exchange(1),
      exchange(2, { protocol: 'tcp', source: 'orders', target: 'redis', method: undefined, requestTarget: undefined, status: undefined, tcp: { kind: 'operation', applicationProtocol: 'redis', operation: 'GET', inspection: 'decoded', outcome: 'success' } }),
      exchange(3, { status: 503, fault: 'orders-down' }),
    ]
    expect(filterExchanges(exchanges, 'orders:redis', 'all', 'tcp').map((item) => item.sequence)).toEqual([2])
    expect(filterExchanges(exchanges, 'redis get', 'all', 'tcp').map((item) => item.sequence)).toEqual([2])
    expect(filterExchanges(exchanges, '', 'errors', 'all').map((item) => item.sequence)).toEqual([3])
    expect(filterExchanges(exchanges, 'orders-down', 'faulted', 'all').map((item) => item.sequence)).toEqual([3])

    const traces = [trace(1), trace(2, { background: true, requestTarget: '/favicon.ico' }), trace(3, { durationMs: 700 })]
    expect(filterTraces(traces, '', 'all', false).map((item) => item.number)).toEqual([1, 3])
    expect(filterTraces(traces, 'favicon', 'all', true).map((item) => item.number)).toEqual([2])
    expect(filterTraces(traces, '', 'slow', true).map((item) => item.number)).toEqual([3])
  })

  it('keeps provisional TCP roots out of traces while preserving settled TCP and correlated request traces', () => {
    const provisionalTCP = trace(4, { protocol: 'tcp', provisional: true, method: undefined, requestTarget: undefined, source: 'orders', target: 'redis', spanCount: 1 })
    const correlated = trace(5, { method: 'GET', requestTarget: '/checkout', source: 'external', target: 'checkout', spanCount: 2 })
    const settledTCP = trace(6, { protocol: 'tcp', method: undefined, requestTarget: undefined, source: 'worker', target: 'redis', spanCount: 1 })

    expect(filterTraces([provisionalTCP, correlated, settledTCP], '', 'all', true).map((item) => item.number)).toEqual([5])
    expect(filterTraces([provisionalTCP, correlated, settledTCP], '', 'all', true, true).map((item) => item.number)).toEqual([5, 6])
  })

  it('summarizes only the rolling 60 second window', () => {
    const now = Date.parse('2026-08-17T12:01:00Z')
    const values = [
      exchange(1, { completedAt: '2026-08-17T12:00:59Z', durationMs: 10 }),
      exchange(2, { completedAt: '2026-08-17T12:00:50Z', durationMs: 100, status: 500 }),
      exchange(3, { completedAt: '2026-08-17T12:00:40Z', durationMs: 900 }),
      exchange(4, { completedAt: '2026-08-17T11:59:00Z', durationMs: 9999 }),
    ]
    expect(trafficWindowSummary(values, now)).toEqual({ exchanges: 3, requestsPerSecond: .05, errors: 1, p50: 100, p95: 900 })
  })
})
