import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange, TrafficTrace } from '../../types'
import { traceWaterfallItems, TraceWaterfall } from './TraceWaterfall'

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
        { exchange: { ...base, sequence: 12, protocol: 'tcp', source: 'orders', target: 'postgres', status: undefined, durationMs: 35, tcp: { kind: 'operation', applicationProtocol: 'postgresql', operation: 'SELECT', inspection: 'decoded', outcome: 'success' } }, parentSequence: 11, depth: 1, startOffsetMs: 20, correlation: 'inferred' },
      ],
    } as TrafficTrace

    const markup = renderToStaticMarkup(<TraceWaterfall trace={trace} onExchange={() => undefined} />)
    expect(markup).toContain('aria-label="Trace waterfall"')
    expect(markup).toContain('aria-label="Maximize trace"')
    expect(markup).not.toContain('trace 11')
    expect(markup).toContain('aria-pressed="false"')
    expect(markup).toContain('external <i>→</i> checkout')
    expect(markup).toContain('POST /checkout')
    expect(markup).toContain('POSTGRESQL SELECT')
    expect(markup).toContain('aria-label="Inspect orders to postgres POSTGRESQL SELECT"')
    expect(markup).toContain('class="trace-span is-tcp"')
    expect(markup).toContain('--span-depth:1')
    expect(markup).toContain('correlation-badge--inferred')
  })

  it('hides background spans and collapses one database transaction', () => {
    const startedAt = '2026-08-25T12:00:00Z'
    const tcpExchange = (sequence: number, operation: string, background = false): TrafficExchange => ({
      project: 'store', environment: 'local', sequence, protocol: 'tcp', source: 'inventory', target: 'inventory-postgres', background,
      startedAt, completedAt: startedAt, durationMs: 2, requestBytes: 6, responseBytes: 6,
      tcp: { kind: 'operation', applicationProtocol: 'postgresql', operation, inspection: 'decoded', outcome: 'success' },
    })
    const trace = {
      project: 'store', environment: 'local', number: 1, lastSequence: 6, protocol: 'http',
      startedAt, completedAt: startedAt, durationMs: 20, source: 'external', target: 'inventory',
      error: false, faulted: false, background: false, provisional: false, spanCount: 6, correlation: 'inferred',
      spans: [
        { exchange: { ...tcpExchange(2, 'QUERY', true) }, depth: 1, startOffsetMs: 1, correlation: 'inferred' },
        ...['BEGIN', 'UPDATE', 'INSERT', 'COMMIT'].map((operation, index) => ({
          exchange: tcpExchange(index + 3, operation), depth: 1, startOffsetMs: index * 3 + 3, correlation: 'inferred' as const, transactionGroup: 1,
        })),
      ],
    } as TrafficTrace

    const items = traceWaterfallItems(trace)
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ kind: 'transaction', group: 1 })
    if (items[0].kind === 'transaction') expect(items[0].spans).toHaveLength(4)

    const markup = renderToStaticMarkup(<TraceWaterfall trace={trace} onExchange={() => undefined} />)
    expect(markup).toContain('POSTGRESQL TRANSACTION · 4 OPERATIONS')
    expect(markup).toContain('aria-expanded="false"')
    expect(markup).not.toContain('POSTGRESQL QUERY')
    expect(markup).not.toContain('POSTGRESQL BEGIN')

    const backgroundMarkup = renderToStaticMarkup(<TraceWaterfall trace={trace} includeBackground onExchange={() => undefined} />)
    expect(backgroundMarkup).toContain('POSTGRESQL QUERY')
  })
})
