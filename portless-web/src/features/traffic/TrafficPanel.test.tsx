import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange, TrafficTrace } from '../../types'
import { traceCandidatesForExchange, TraceSummaryRow, TrafficTableHeader } from './TrafficPanel'

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
    const trace = {
      project: 'store', environment: 'local', number: 42, lastSequence: 45,
      protocol: 'http', provisional: false,
      startedAt: '2026-08-17T12:00:00.123Z', completedAt: '2026-08-17T12:00:00.223Z', durationMs: 100,
      source: 'external', target: 'checkout', method: 'GET', requestTarget: '/orders', status: 200,
      error: false, faulted: false, background: false, spanCount: 4, correlation: 'exact',
    } as TrafficTrace

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
