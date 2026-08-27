import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange, TrafficTrace, TrafficTraceSpan } from '../../types'
import { traceNavigationItems, traceTransactionCommandSpans, traceWaterfallItems, TraceWaterfall } from './TraceWaterfall'

const waterfallProps = {
  expandedTransactions: new Set<number>(),
  onTransactionToggle: () => undefined,
  onItem: () => undefined,
}

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

    const markup = renderToStaticMarkup(<TraceWaterfall trace={trace} {...waterfallProps} />)
    expect(markup).toContain('aria-label="Trace waterfall"')
    expect(markup).toContain('aria-label="Maximize trace"')
    expect(markup).not.toContain('trace 11')
    expect(markup).toContain('aria-pressed="false"')
    expect(markup).toContain('external <i>→</i> checkout')
    expect(markup).toContain('POST /checkout')
    expect(markup).toContain('POSTGRESQL · SELECT')
    expect(markup).toContain('aria-label="Inspect orders to postgres POSTGRESQL · SELECT"')
    expect(markup).toContain('class="trace-span trace-span--dependency-summary is-tcp"')
    expect(markup).toContain('trace-span__disclosure-placeholder')
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
      project: 'store', environment: 'local', number: 1, lastSequence: 7, protocol: 'http',
      startedAt, completedAt: startedAt, durationMs: 20, source: 'external', target: 'inventory',
      error: false, faulted: false, background: false, provisional: false, spanCount: 7, correlation: 'inferred',
      spans: [
        { exchange: { ...tcpExchange(2, 'QUERY', true) }, depth: 1, startOffsetMs: 1, correlation: 'inferred' },
        ...['BEGIN', 'UPDATE', 'INSERT', 'COMMIT'].map((operation, index) => ({
          exchange: tcpExchange(index + 3, operation), depth: 1, startOffsetMs: index * 3 + 3, correlation: 'inferred' as const, transactionGroup: 1,
        })),
        { exchange: { ...tcpExchange(7, 'INSERT'), source: 'orders', target: 'orders-postgres' }, depth: 1, startOffsetMs: 16, correlation: 'inferred' },
      ],
    } as TrafficTrace

    const items = traceWaterfallItems(trace)
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ kind: 'transaction', group: 1 })
    if (items[0].kind === 'transaction') expect(items[0].spans).toHaveLength(4)
    expect(items[1]).toMatchObject({ kind: 'span', span: { exchange: { sequence: 7 } } })

    const collapsedNavigation = traceNavigationItems(trace)
    expect(collapsedNavigation.map((item) => item.key)).toEqual(['transaction:1', 'exchange:7'])
    expect(collapsedNavigation[0]).toMatchObject({ kind: 'transaction', expanded: false, exchange: { tcp: { operation: 'TRANSACTION' } } })

    const expandedNavigation = traceNavigationItems(trace, false, new Set([1]))
    expect(expandedNavigation.map((item) => item.key)).toEqual(['transaction:1', 'exchange:3', 'exchange:4', 'exchange:5', 'exchange:6', 'exchange:7'])

    const markup = renderToStaticMarkup(<TraceWaterfall trace={trace} {...waterfallProps} />)
    expect(markup).toContain('POSTGRESQL · UPDATE + INSERT')
    expect(markup).toContain('POSTGRESQL · INSERT')
    expect(markup).toContain('Inspect orders to orders-postgres POSTGRESQL · INSERT')
    expect(markup.match(/trace-span--dependency-summary/g)).toHaveLength(2)
    expect(markup).toContain('aria-expanded="false"')
    expect(markup).toContain('Inspect inventory to inventory-postgres POSTGRESQL command summary with 2 commands')
    expect(markup).toContain('Expand inventory to inventory-postgres POSTGRESQL TCP details')
    expect(markup).not.toContain('POSTGRESQL · QUERY')
    expect(markup).not.toContain('POSTGRESQL · BEGIN')

    const backgroundNavigation = traceNavigationItems(trace, true)
    expect(backgroundNavigation.map((item) => item.key)).toEqual(['exchange:2', 'transaction:1', 'exchange:7'])

    const backgroundMarkup = renderToStaticMarkup(<TraceWaterfall trace={trace} includeBackground {...waterfallProps} />)
    expect(backgroundMarkup).toContain('POSTGRESQL · QUERY')

    const expandedMarkup = renderToStaticMarkup(<TraceWaterfall trace={trace} {...waterfallProps} expandedTransactions={new Set([1])} />)
    expect(expandedMarkup).toContain('Collapse inventory to inventory-postgres POSTGRESQL TCP details')
    expect(expandedMarkup).toContain('POSTGRESQL · BEGIN')
    expect(expandedMarkup).toContain('POSTGRESQL · COMMIT')
  })

  it('does not promote transaction boundaries to commands when no application SQL ran', () => {
    const startedAt = '2026-08-25T12:00:00Z'
    const boundary = (sequence: number, operation: string): TrafficTraceSpan => ({
      exchange: {
        project: 'store', environment: 'local', sequence, protocol: 'tcp', source: 'inventory', target: 'inventory-postgres',
        startedAt, completedAt: startedAt, durationMs: 2, requestBytes: 6, responseBytes: 6, background: false,
        tcp: { kind: 'operation', applicationProtocol: 'postgresql', operation, inspection: 'decoded', outcome: 'success' },
      },
      depth: 1, startOffsetMs: sequence, correlation: 'inferred', transactionGroup: 1,
    })
    const spans = [boundary(1, 'BEGIN'), boundary(2, 'COMMIT')]
    const trace = {
      project: 'store', environment: 'local', number: 2, lastSequence: 2, protocol: 'tcp',
      startedAt, completedAt: startedAt, durationMs: 4, source: 'inventory', target: 'inventory-postgres',
      error: false, faulted: false, background: false, provisional: false, spanCount: 2, correlation: 'inferred', spans,
    } as TrafficTrace

    expect(traceTransactionCommandSpans(spans)).toEqual([])
    const markup = renderToStaticMarkup(<TraceWaterfall trace={trace} {...waterfallProps} />)
    expect(markup).toContain('POSTGRESQL · TRANSACTION')
    expect(markup).toContain('command summary with 0 commands')
    expect(markup).not.toContain('POSTGRESQL · COMMIT')
  })
})
