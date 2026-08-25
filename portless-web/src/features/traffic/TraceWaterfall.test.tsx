import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange, TrafficTrace } from '../../types'
import { TraceWaterfall } from './TraceWaterfall'

describe('trace waterfall', () => {
  it('renders nested, protocol-aware spans with correlation confidence', () => {
    const base = {
      project: 'store', environment: 'local', protocol: 'http',
      startedAt: '2026-08-17T12:00:00Z', completedAt: '2026-08-17T12:00:00.100Z',
      durationMs: 100, requestBytes: 0, responseBytes: 12, status: 200,
    } as TrafficExchange
    const trace = {
      project: 'store', environment: 'local', number: 11, lastSequence: 12,
      protocol: 'http', provisional: false,
      startedAt: '2026-08-17T12:00:00Z', completedAt: '2026-08-17T12:00:00.100Z', durationMs: 100,
      source: 'external', target: 'checkout', error: false, faulted: false, background: false, spanCount: 2, correlation: 'inferred',
      spans: [
        { exchange: { ...base, sequence: 11, source: 'external', target: 'checkout', method: 'POST', requestTarget: '/checkout' }, depth: 0, startOffsetMs: 0, correlation: 'exact' },
        { exchange: { ...base, sequence: 12, protocol: 'tcp', source: 'orders', target: 'postgres', status: undefined, durationMs: 35 }, parentSequence: 11, depth: 1, startOffsetMs: 20, correlation: 'inferred' },
      ],
    } as TrafficTrace

    const markup = renderToStaticMarkup(<TraceWaterfall trace={trace} onExchange={() => undefined} />)
    expect(markup).toContain('aria-label="Trace waterfall"')
    expect(markup).toContain('aria-label="Maximize trace"')
    expect(markup).not.toContain('trace 11')
    expect(markup).toContain('aria-pressed="false"')
    expect(markup).toContain('external <i>→</i> checkout')
    expect(markup).toContain('POST /checkout')
    expect(markup).toContain('class="trace-span is-tcp"')
    expect(markup).toContain('--span-depth:1')
    expect(markup).toContain('correlation-badge--inferred')
  })
})
