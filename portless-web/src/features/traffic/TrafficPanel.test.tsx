import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficTrace } from '../../types'
import { TraceSummaryRow } from './TrafficPanel'

describe('trace summary row', () => {
  it('does not present the internal root-exchange sequence as a trace number', () => {
    const trace = {
      project: 'store', environment: 'local', number: 42, lastSequence: 45,
      protocol: 'http', provisional: false,
      startedAt: '2026-08-17T12:00:00Z', completedAt: '2026-08-17T12:00:00.100Z', durationMs: 100,
      source: 'external', target: 'checkout', method: 'GET', requestTarget: '/orders', status: 200,
      error: false, faulted: false, background: false, spanCount: 4, correlation: 'exact',
    } as TrafficTrace

    const markup = renderToStaticMarkup(<TraceSummaryRow trace={trace} expanded={false} onToggle={() => undefined} />)

    expect(markup).toContain('GET /orders')
    expect(markup).not.toContain('#42')
    expect(markup).not.toContain('<code>')
  })
})
