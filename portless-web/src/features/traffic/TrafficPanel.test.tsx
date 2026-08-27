import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { paginateItems } from '../../components/PanelPagination'
import type { TrafficExchange, TrafficTrace } from '../../types'
import { TrafficControls } from './TrafficControls'
import { TrafficExchangeList } from './TrafficExchangeList'
import { traceCandidatesForExchange } from './trafficSelection'
import { TrafficTableHeader } from './TrafficTableHeader'
import { TraceSummaryRow, TrafficTraceList } from './TrafficTraceList'

const trace = {
  project: 'store', environment: 'local', number: 42, lastSequence: 45,
  protocol: 'http', provisional: false,
  startedAt: '2026-08-17T12:00:00.123Z', completedAt: '2026-08-17T12:00:00.223Z', durationMs: 100,
  source: 'external', target: 'checkout', method: 'GET', requestTarget: '/orders', status: 200,
  error: false, faulted: false, background: false, spanCount: 4, correlation: 'exact',
} as TrafficTrace

describe('traffic table headers', () => {
  it('labels the time column as Timestamp in traces and exchanges', () => {
    for (const mode of ['traces', 'exchanges'] as const) {
      const markup = renderToStaticMarkup(<TrafficTableHeader mode={mode} />)
      expect(markup).toContain('Timestamp')
      expect(markup).not.toContain('When')
    }
  })
})

describe('trace summary row', () => {
  it('does not present the internal root-exchange sequence as a trace number', () => {
    const markup = renderToStaticMarkup(<TraceSummaryRow trace={trace} expanded={false} onToggle={() => undefined} />)

    expect(markup).toContain('GET /orders')
    expect(markup).toMatch(/\d{1,2}:\d{2}:\d{2}\.123/)
    expect(markup).not.toContain('#42')
    expect(markup).not.toContain('<code>')
  })

  it('resolves an exchange to a loaded trace before correlation and sequence-range fallbacks', () => {
    const exchange = { sequence: 12, traceId: 'trace-a' } as TrafficExchange
    const range = { number: 10, lastSequence: 15, spanCount: 3 } as TrafficTrace
    const exact = { number: 11, lastSequence: 14, traceId: 'trace-a', spanCount: 2 } as TrafficTrace
    const loaded = {
      number: 8, lastSequence: 12, spanCount: 2,
      spans: [{ exchange: { sequence: 8 } }, { exchange }],
    } as TrafficTrace
    const staleSummary = { ...loaded, spans: undefined } as TrafficTrace

    const candidates = traceCandidatesForExchange([range, exact, staleSummary], exchange, loaded)

    expect(candidates.map((trace) => trace.number)).toEqual([8, 11, 10])
    expect(candidates[0].spans).toHaveLength(2)
  })
})

describe('traffic page components', () => {
  const stream = { paused: false, bufferedCount: 0, clearing: false, empty: false, onClear: () => undefined, onTogglePaused: () => undefined }
  const summary = { exchanges: 3, requestsPerSecond: 0.1, errors: 1, p50: 12, p95: 24 }
  const filters = {
    search: '', result: 'all' as const, protocol: 'all' as const, includeTCPRoots: false, includeBackground: false, edge: '',
    onSearch: () => undefined, onResult: () => undefined, onProtocol: () => undefined,
    onToggleTCPRoots: () => undefined, onToggleBackground: () => undefined, onClearEdge: () => undefined,
  }

  it('keeps trace and exchange controls scoped to their respective views', () => {
    const traces = renderToStaticMarkup(<TrafficControls mode="traces" onMode={() => undefined} stream={stream} summary={summary} filters={filters} />)
    const exchanges = renderToStaticMarkup(<TrafficControls mode="exchanges" onMode={() => undefined} stream={stream} summary={summary} filters={filters} />)

    expect(traces).toContain('SHOW TCP ROOTS')
    expect(traces).toContain('SHOW BACKGROUND')
    expect(traces).not.toContain('aria-label="Traffic protocol"')
    expect(exchanges).toContain('aria-label="Traffic protocol"')
    expect(exchanges).not.toContain('SHOW TCP ROOTS')
    expect(exchanges).not.toContain('SHOW BACKGROUND')
  })

  it('renders trace and exchange lists through their dedicated components', () => {
    const traceMarkup = renderToStaticMarkup(<TrafficTraceList
      pagination={paginateItems([trace], 0, 25)}
      expandedTrace={null}
      includeBackground={false}
      onToggleTrace={() => undefined}
      onInspect={() => undefined}
      onPage={() => undefined}
    />)
    const exchange = {
      project: 'store', environment: 'local', sequence: 45, protocol: 'tcp', source: 'orders', target: 'orders-redis', background: false,
      startedAt: trace.startedAt, completedAt: trace.completedAt, durationMs: 2, requestBytes: 12, responseBytes: 4,
      tcp: { kind: 'operation', applicationProtocol: 'redis', operation: 'GET', inspection: 'decoded', outcome: 'success' },
    } as TrafficExchange
    const exchangeMarkup = renderToStaticMarkup(<TrafficExchangeList pagination={paginateItems([exchange], 0, 25)} onInspect={() => undefined} onPage={() => undefined} />)

    expect(traceMarkup).toContain('GET /orders')
    expect(traceMarkup).toContain('Root request')
    expect(exchangeMarkup).toContain('REDIS GET')
    expect(exchangeMarkup).toContain('orders<i class="edge-arrow">→</i>orders-redis')
    expect(exchangeMarkup).toContain('Request / operation')
  })
})
