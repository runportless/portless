import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange, TrafficTrace } from '../../api/contracts/traffic'
import { ExchangeTraceDrawer as TrafficDetail } from './ExchangeTraceDrawer'
import { databaseResultCSV, databaseResultRows, DatabaseResultTable } from './detail/DatabaseResultTable'
import { defaultTrafficDetailView } from './detail/TrafficDrawerShell'
import { TrafficOverview, trafficStartedTime, trafficTargetBinding } from './detail/TrafficOverview'
import { genericTcpTrafficPresentation } from './protocols/GenericTcpTrafficDetail'
import { defaultTrafficPayloadView, formatTrafficBody, formattedTrafficHeaders, rawTrafficMessage } from './protocols/HttpTrafficDetail'
import { postgresTrafficPresentation } from './protocols/PostgreSQLTrafficDetail'
import { redisTrafficPresentation } from './protocols/RedisPresentation'
import { traceNavigationItems } from './TraceWaterfall'
import { orderedTraceExchanges, WaterfallTraceDrawer } from './WaterfallTraceDrawer'

const exchange = {
  project: 'billing', environment: 'local', sequence: 7, protocol: 'http',
  source: 'checkout', target: 'orders', background: false, targetProvider: 'mock', mockScenario: 'sold-out', mockRoute: 'reject-order',
  startedAt: '2026-08-13T12:00:00.123Z', completedAt: '2026-08-13T12:00:00.147Z',
  method: 'POST', host: 'orders.local.billing.localhost', path: '/orders', status: 201,
  durationMs: 24, requestBytes: 42, responseBytes: 118,
  requestHeaders: { Authorization: ['[REDACTED]'], 'Content-Type': ['application/json'], Traceparent: ['00-4bf92f3577b34da6a3ce929d0e0e4736-b7ad6b7169203331-01'] },
  responseHeaders: { 'Content-Type': ['application/json'], 'X-Request-Id': ['req-7'] },
  requestBody: '{"sku":"coffee","quantity":2}',
  responseBody: '{"order":42,"state":"created"}',
  requestCapturedBytes: 42,
  responseCapturedBytes: 118,
  traceId: '4bf92f3577b34da6a3ce929d0e0e4736',
  spanId: '00f067aa0ba902b7',
  parentSpanId: 'b7ad6b7169203331',
  traceContextSource: 'w3c',
} as TrafficExchange

describe('TrafficDetail', () => {
  it.each(['http', 'tcp'] as const)('uses the shared red notice for a captured %s error', (protocol) => {
    const markup = renderToStaticMarkup(createElement(TrafficOverview, { exchange: { ...exchange, protocol, error: 'The upstream connection failed.' } }))
    expect(markup).toContain('class="action-error action-error--persistent" role="alert"')
    expect(markup).toContain(protocol === 'http' ? 'Request error' : 'Operation error')
    expect(markup).toContain('The upstream connection failed.')
    expect(markup).not.toContain('traffic-detail__error')
  })

  it('opens HTTP exchanges with overview context above request and response tabs', () => {
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange, onClose: () => undefined }))

    expect(markup).toContain('aria-label="Traffic request and response 7"')
    expect(markup).toContain('<span class="traffic-detail__protocol-badge">HTTP</span>')
    expect(markup).toContain('<h3><span>POST</span><code>/orders</code></h3>')
    expect(markup).not.toContain('HTTP EXCHANGE')
    expect(markup).toContain('class="drawer-size-button"')
    expect(markup).toContain('aria-label="Full screen traffic details"')
    expect(markup).toContain('d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4"')
    expect(markup).toContain('aria-label="Exchange overview"')
    expect(markup).toContain('aria-label="Exchange payload"')
    expect(markup).toContain('aria-selected="true" class="is-active">REQUEST</button>')
    expect(markup).toContain('>RESPONSE</button>')
    expect(markup.indexOf('>REQUEST</button>')).toBeLessThan(markup.indexOf('>RESPONSE</button>'))
    expect(markup).toContain('<div class="traffic-overview__heading">OVERVIEW</div>')
    expect(markup).toContain('traffic-overview__panel')
    expect(markup).not.toContain('traffic-overview__summary')
    expect(markup).not.toContain('traffic-interventions')
    expect(markup).toContain('aria-label="Exchange interventions"')
    expect(markup).toContain('traffic-intervention-badge--mock')
    expect(markup.indexOf('traffic-intervention-badges')).toBeLessThan(markup.indexOf('</header>'))
    expect(markup.indexOf('</header>')).toBeLessThan(markup.indexOf('traffic-overview__context'))
    expect(markup.indexOf('traffic-overview__context')).toBeLessThan(markup.indexOf('aria-label="Exchange payload"'))
    expect(markup).not.toContain('>COMPARE<')
    expect(markup).toContain('traffic-message-workbench--request')
    expect(markup).toContain('<span>application/json</span><span>42 B</span>')
    expect(markup).not.toContain('<span>42 B captured</span>')
    expect(markup).toContain('>HEADERS</button>')
    expect(markup).not.toMatch(/>HEADERS \d+<\/button>/)
    expect(markup).toContain('class="traffic-json"')
    expect(markup).toContain('class="traffic-json__key"')
    expect(markup).toContain('class="traffic-json__string"')
    expect(markup).toContain('class="traffic-json__number"')
    expect(markup).toContain('&quot;sku&quot;')
    expect(markup).toContain('&quot;coffee&quot;')
    expect(markup).not.toContain('CAPTURE COMPLETE')
    expect(markup).toContain('<span>ENVIRONMENT</span><strong>local</strong>')
    expect(markup).toContain('<span>TARGET BINDING</span><strong>sold-out · mock</strong>')
    expect(trafficStartedTime(exchange.startedAt)).toMatch(/\.123(?:\s|$)/)
    expect(markup).toContain(`<span>STARTED</span><strong>${trafficStartedTime(exchange.startedAt)}</strong>`)
    expect(markup).not.toContain('TARGET PROVIDER')
    expect(markup).toContain('<span>COMPLETED</span><strong>24ms</strong>')
    expect(markup).not.toContain('<span>COMPLETED</span><strong>+24ms</strong>')
    expect(markup).not.toContain('traffic-trace-context')
    expect(markup).not.toContain('TRACE ID')
    expect(markup).not.toContain('4bf92f3577b34da6a3ce929d0e0e4736')
    expect(markup).not.toContain('Trace span navigation')
    expect(markup).toContain('sold-out / reject-order')
    expect(markup).not.toContain('aria-label="Transfer summary"')
    expect(markup).not.toContain('traffic-message-workbench--response')
    expect(markup).not.toContain('&quot;order&quot;: 42')
  })

  it('navigates a multi-exchange trace in chronological order', () => {
    const first = { ...exchange, sequence: 6, source: 'external', target: 'checkout' } as TrafficExchange
    const last = { ...exchange, sequence: 9, source: 'orders', target: 'inventory' } as TrafficExchange
    const trace = {
      project: 'billing', environment: 'local', number: 6, lastSequence: 9, rootSequence: 6,
      protocol: 'http', startedAt: first.startedAt, completedAt: last.completedAt, durationMs: 24,
      source: 'external', target: 'checkout', status: 201, error: false, faulted: false, background: false, provisional: false,
      spanCount: 3, correlation: 'exact',
      spans: [
        { exchange: last, depth: 2, startOffsetMs: 12, correlation: 'exact' },
        { exchange, parentSequence: 6, depth: 1, startOffsetMs: 5, correlation: 'exact' },
        { exchange: first, depth: 0, startOffsetMs: 0, correlation: 'exact' },
      ],
    } as TrafficTrace
    const markup = renderToStaticMarkup(createElement(WaterfallTraceDrawer, {
      exchange,
      trace,
      onNavigate: () => undefined,
      onClose: () => undefined,
    }))

    expect(orderedTraceExchanges(trace).map((candidate) => candidate.sequence)).toEqual([6, 7, 9])
    expect(markup).toContain('aria-label="Trace span navigation"')
    expect(markup).toContain('aria-label="Trace navigation scope"')
    expect(markup).toContain('aria-pressed="true">HTTP</button>')
    expect(markup).toContain('aria-pressed="false">ALL</button>')
    expect(markup).toContain('aria-label="First visible span in trace"')
    expect(markup).toContain('aria-label="Previous visible span in trace"')
    expect(markup).toContain('aria-keyshortcuts="ArrowLeft"')
    expect(markup).toContain('aria-label="Span 2 of 3"')
    expect(markup).toContain('aria-label="Next visible span in trace"')
    expect(markup).toContain('aria-keyshortcuts="ArrowRight"')
    expect(markup).toContain('aria-label="Last visible span in trace"')
    expect(markup).toContain('<output aria-live="polite" aria-label="Span 2 of 3"><strong>2</strong><span>OF</span><strong>3</strong></output>')
    expect(markup).not.toContain('traffic-trace-navigator__scope-label')
    expect(markup).not.toContain('>TRACE<')
  })

  it('uses simple previous and next pagination for the filtered exchange list', () => {
    const newer = { ...exchange, sequence: 8, path: '/newer' } as TrafficExchange
    const older = { ...exchange, sequence: 6, path: '/older' } as TrafficExchange
    const items = [newer, exchange, older]
    const markup = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange,
      exchanges: items,
      onNavigate: () => undefined,
      onClose: () => undefined,
    }))

    expect(markup).toContain('aria-label="Exchange navigation"')
    expect(markup).toContain('aria-label="Navigate filtered exchanges"')
    expect(markup).toContain('aria-label="Previous exchange"')
    expect(markup).toContain('aria-keyshortcuts="ArrowLeft"')
    expect(markup).toContain('aria-label="Exchange 2 of 3"')
    expect(markup).toContain('aria-label="Next exchange"')
    expect(markup).toContain('aria-keyshortcuts="ArrowRight"')
    expect(markup).not.toContain('Trace span navigation')
    expect(markup).not.toContain('First visible span in trace')
    expect(markup).not.toContain('Last visible span in trace')
    expect(markup).not.toContain('>HTTP</button>')
    expect(markup).not.toContain('>ALL</button>')

    const boundary = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: newer,
      exchanges: items,
      onNavigate: () => undefined,
      onClose: () => undefined,
    }))
    expect(boundary).toMatch(/aria-label="Previous exchange"[^>]*disabled=""/)
    expect(boundary).not.toMatch(/aria-label="Next exchange"[^>]*disabled=""/)
  })

  it('presents a database transaction as one navigable summary', () => {
    const root = { ...exchange, sequence: 6, source: 'external', target: 'checkout' } as TrafficExchange
    const operation = (sequence: number, name: string, content = name, result = name) => ({
      ...exchange,
      sequence,
      protocol: 'tcp',
      source: 'inventory',
      target: 'inventory-postgres',
      status: undefined,
      method: undefined,
      requestTarget: undefined,
      path: undefined,
      requestBody: undefined,
      responseBody: undefined,
      tcp: {
        kind: 'operation', applicationProtocol: 'postgresql', operation: name, inspection: 'decoded', outcome: 'success',
        requestMessages: [
          { type: 'query', offsetMs: 0, summary: name, wireBytes: 6, content, contentType: 'text/x-sql', encoding: 'utf8' },
          ...(name === 'UPDATE' ? [{ type: 'bind', offsetMs: 0, summary: 'Bind parameters', wireBytes: 20, content: '[1,"coffee"]', contentType: 'application/json', encoding: 'utf8' }] : []),
        ],
        responseMessages: [{ type: 'command-complete', offsetMs: 1, summary: result, wireBytes: 6, fields: [{ name: 'command', value: result }] }],
      },
    }) as TrafficExchange
    const trace = {
      project: 'billing', environment: 'local', number: 6, lastSequence: 9, rootSequence: 6,
      protocol: 'http', startedAt: root.startedAt, completedAt: root.completedAt, durationMs: 24,
      source: 'external', target: 'checkout', status: 201, error: false, faulted: false, background: false, provisional: false,
      spanCount: 4, correlation: 'exact',
      spans: [
        { exchange: root, depth: 0, startOffsetMs: 0, correlation: 'exact' },
        { exchange: operation(7, 'BEGIN'), parentSequence: 6, depth: 1, startOffsetMs: 2, correlation: 'inferred', transactionGroup: 1 },
        { exchange: operation(8, 'UPDATE', 'UPDATE inventory SET on_hand = on_hand - $1 WHERE sku = $2', 'UPDATE 1'), parentSequence: 6, depth: 1, startOffsetMs: 4, correlation: 'inferred', transactionGroup: 1 },
        { exchange: operation(9, 'COMMIT'), parentSequence: 6, depth: 1, startOffsetMs: 6, correlation: 'inferred', transactionGroup: 1 },
      ],
    } as TrafficTrace
    const navigationItems = traceNavigationItems(trace)
    const transaction = navigationItems[1]
    expect(transaction).toMatchObject({ kind: 'transaction' })

    const markup = renderToStaticMarkup(createElement(WaterfallTraceDrawer, {
      exchange: transaction.exchange,
      trace,
      traceNavigationItems: navigationItems,
      traceNavigationItem: transaction,
      onNavigate: () => undefined,
      onClose: () => undefined,
    }))

    expect(markup).toContain('<span class="traffic-detail__protocol-badge">TCP</span>')
    expect(markup).toContain('<h3><span>POSTGRESQL</span><code>TRANSACTION</code></h3>')
    expect(markup).toContain('<span class="traffic-detail__transaction-count">1 command</span>')
    expect(markup).not.toContain('class="eyebrow"')
    expect(markup).toContain('aria-label="Command"')
    expect(markup).toContain('aria-selected="true" class="is-active">COMMAND</button>')
    expect(markup).toContain('aria-selected="false" class="">RESULT</button>')
    expect(markup).not.toContain('TCP DETAILS')
    expect(markup).toContain('aria-label="Exchange overview"')
    expect(markup).toContain('traffic-overview__context')
    expect(markup).toContain('<span>ENVIRONMENT</span><strong>local</strong>')
    expect(markup).toContain('<span>TARGET BINDING</span>')
    expect(markup).toContain(`<span>STARTED</span><strong>${trafficStartedTime(exchange.startedAt)}</strong>`)
    expect(markup).toContain('<span>COMPLETED</span><strong>28ms</strong>')
    expect(markup).not.toContain('<strong>BEGIN</strong>')
    expect(markup).not.toContain('<strong>COMMIT</strong>')
    expect(postgresTrafficPresentation(trace.spans![2].exchange, 'request')).toMatchObject({ content: "UPDATE inventory SET on_hand = on_hand - 1 WHERE sku = 'coffee'" })
    expect(markup).not.toContain('aria-label="Bound parameters"')
    expect(markup).not.toContain('$1')
    expect(markup).not.toContain('$2')
    expect(markup).not.toContain('UPDATE 1')
    expect(postgresTrafficPresentation(trace.spans![2].exchange, 'response')).toMatchObject({ label: 'RESULT', title: 'UPDATE 1' })
    expect(markup).toContain('aria-label="Current span is outside HTTP navigation; 1 HTTP span available"')
    expect(markup).toContain('<strong>—</strong><span>OF</span><strong>1</strong>')
    expect(markup).not.toContain('PROTOCOL MESSAGES')
  })

  it('formats body, header, and raw payload representations without exposing secrets', () => {
    expect(formatTrafficBody(exchange.requestBody || '')).toBe('{\n  "sku": "coffee",\n  "quantity": 2\n}')
    expect(formattedTrafficHeaders(exchange.requestHeaders, exchange.host)).toBe('Authorization: [REDACTED]\nContent-Type: application/json\nHost: orders.local.billing.localhost\nTraceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-b7ad6b7169203331-01')
    expect(rawTrafficMessage(exchange, 'request')).toContain('POST /orders\nAuthorization: [REDACTED]')
    expect(rawTrafficMessage(exchange, 'request')).not.toContain('Bearer local-dev-token')
    expect(rawTrafficMessage(exchange, 'response')).toContain('HTTP 201\nContent-Type: application/json')
    expect(defaultTrafficPayloadView(exchange, 'request')).toBe('body')
    expect(defaultTrafficPayloadView({ ...exchange, requestBody: undefined }, 'request')).toBe('headers')
  })

  it('highlights header names separately from their values', () => {
    const markup = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: { ...exchange, requestBody: undefined },
      onClose: () => undefined,
    }))

    expect(markup).toContain('<pre class="traffic-headers">')
    expect(markup).toContain('<span class="traffic-headers__key">Authorization</span>')
    expect(markup).toContain('<span class="traffic-headers__separator">:</span>')
    expect(markup).toContain('<span class="traffic-headers__value"> [REDACTED]</span>')
    expect(markup).toContain('<span class="traffic-headers__key">Content-Type</span>')
  })

  it('keeps trace metadata out of the exchange overview', () => {
    const generated = {
      ...exchange,
      traceContextSource: 'generated',
      parentSpanId: undefined,
      requestHeaders: { Accept: ['application/json'] },
    } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: generated, onClose: () => undefined }))

    expect(markup).not.toContain('traffic-trace-context')
    expect(markup).not.toContain('TRACE ID')
    expect(markup).not.toContain('GENERATED')
    expect(markup).not.toContain('PROPAGATED')
    expect(markup).not.toContain('content type not reported')
    expect(formattedTrafficHeaders(generated.requestHeaders, generated.host)).not.toContain('Traceparent')
  })

  it('shows the target binding configuration with its provider type', () => {
    const local = { ...exchange, targetProvider: 'local', mockScenario: undefined, mockRoute: undefined } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: local,
      targetBinding: { service: 'orders', provider: 'local', source: 'checkout' },
      onClose: () => undefined,
    }))

    expect(markup).toContain('<span>TARGET BINDING</span><strong>checkout · local</strong>')
    expect(trafficTargetBinding(exchange, { service: 'orders', provider: 'mock', mock: { scenario: 'sold-out' } })).toBe('sold-out · mock')
    expect(trafficTargetBinding({ ...exchange, targetProvider: 'container' }, { service: 'orders', provider: 'container' })).toBe('Portless managed · container')
    expect(trafficTargetBinding({ ...exchange, targetProvider: 'remote', remoteClassification: 'qa' }, { service: 'orders', provider: 'remote', remote: { url: 'https://orders.qa.test', classification: 'qa', writePolicy: 'read-only' } })).toBe('https://orders.qa.test · remote')
  })

  it('renders only active interventions as typed badges', () => {
    const active = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: { ...exchange, fault: 'orders-delay', recording: 'checkout-flow' },
      onClose: () => undefined,
    }))

    expect(active).toContain('traffic-intervention-badge--fault')
    expect(active).toContain('<b>FAULT</b><span>orders-delay</span>')
    expect(active).toContain('traffic-intervention-badge--recording')
    expect(active).toContain('<b>RECORDING</b><span>checkout-flow</span>')
    expect(active).toContain('traffic-intervention-badge--mock')
    expect(active).toContain('<b>MOCK</b><span>sold-out / reject-order</span>')

    const inactive = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: { ...exchange, mockScenario: undefined, mockRoute: undefined },
      onClose: () => undefined,
    }))
    expect(inactive).not.toContain('aria-label="Exchange interventions"')
    expect(inactive).not.toContain('>none<')
  })

  it('colors valid JSON media types and leaves malformed JSON unstyled', () => {
    const json = {
      ...exchange,
      requestHeaders: { 'Content-Type': ['application/problem+json; charset=utf-8'] },
      requestBody: '{"ok":true,"count":2,"missing":null}',
    } as TrafficExchange
    const jsonMarkup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: json, onClose: () => undefined }))

    expect(jsonMarkup).toContain('class="traffic-json__boolean"')
    expect(jsonMarkup).toContain('class="traffic-json__number"')
    expect(jsonMarkup).toContain('class="traffic-json__null"')

    const malformedMarkup = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: { ...json, requestBody: '{"ok":' },
      onClose: () => undefined,
    }))
    expect(malformedMarkup).not.toContain('class="traffic-json"')
    expect(malformedMarkup).toContain('{&quot;ok&quot;:')
  })

  it('reserves the capture footer for truncated payloads', () => {
    const markup = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: { ...exchange, requestBodyTruncated: true, requestCapturedBytes: 20 },
      onClose: () => undefined,
    }))

    expect(markup).toContain('CAPTURE TRUNCATED')
    expect(markup).not.toContain('CAPTURE COMPLETE')
  })

  it('renders opaque TCP sessions as a bounded fallback without payload tabs', () => {
    const tcp = {
      ...exchange,
      protocol: 'tcp', method: undefined, host: undefined, path: undefined, requestTarget: undefined, status: undefined,
      requestHeaders: undefined, responseHeaders: undefined, requestBody: undefined, responseBody: undefined,
      traceId: undefined, spanId: undefined, parentSpanId: undefined,
      source: 'orders', target: 'postgres', targetProvider: 'container', requestBytes: 8, responseBytes: 1,
      tcp: { kind: 'session', applicationProtocol: 'postgresql', inspection: 'encrypted', inspectionReason: 'TLS-encrypted PostgreSQL traffic' },
    } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: tcp, onClose: () => undefined }))

    expect(markup).toContain('<span class="traffic-detail__protocol-badge">TCP</span>')
    expect(markup).toContain('<h3><span>POSTGRESQL</span><code>SESSION</code></h3>')
    expect(markup).toContain('aria-label="Exchange overview"')
    expect(markup).toContain('traffic-overview__context')
    expect(markup).toContain('<span>ENVIRONMENT</span><strong>local</strong>')
    expect(markup).toContain('<span>TARGET BINDING</span><strong>Portless managed · container</strong>')
    expect(markup).toContain(`<span>STARTED</span><strong>${trafficStartedTime(exchange.startedAt)}</strong>`)
    expect(markup).toContain('<span>COMPLETED</span><strong>24ms</strong>')
    expect(markup).not.toContain('aria-label="Exchange payload"')
    expect(markup).toContain('TLS-encrypted PostgreSQL traffic')
    expect(markup).toContain('8 B sent · 1 B received')
    expect(markup).not.toContain('traffic-trace-context')
    expect(markup).not.toContain('>REQUEST</button>')
    expect(markup).not.toContain('>RESPONSE</button>')
  })

  it('opens decoded TCP operations with command and result as peers', () => {
    const tcp = {
      ...exchange,
      protocol: 'tcp', method: undefined, host: undefined, path: undefined, requestTarget: undefined, status: undefined,
      requestHeaders: undefined, responseHeaders: undefined, requestBody: undefined, responseBody: undefined,
      traceId: undefined, spanId: undefined, parentSpanId: undefined,
      source: 'api', target: 'nats', targetProvider: 'container', requestBytes: 58, responseBytes: 0,
      tcp: {
        kind: 'operation', applicationProtocol: 'nats', operation: 'PUB', inspection: 'decoded', outcome: 'one-way',
        requestMessageCount: 1, responseMessageCount: 0,
        requestMessages: [{
          type: 'pub', offsetMs: 0, summary: 'PUB orders.created', wireBytes: 58,
          contentBytes: 34, capturedBytes: 34, contentType: 'application/json', encoding: 'utf8',
          fields: [{ name: 'subject', value: 'orders.created' }],
          content: '{"orderId":42,"state":"created"}',
        }],
      },
    } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: tcp, onClose: () => undefined }))

    expect(markup).toContain('<span class="traffic-detail__protocol-badge">TCP</span>')
    expect(markup).toContain('<h3><span>NATS</span><code>PUB</code></h3>')
    expect(defaultTrafficDetailView(tcp)).toBe('request')
    expect(markup).toContain('aria-label="Command"')
    expect(markup).toContain('aria-selected="true" class="is-active">COMMAND</button>')
    expect(markup).toContain('aria-selected="false" class="">RESULT</button>')
    expect(markup).not.toContain('TCP DETAILS')
    expect(markup).toContain('aria-label="Exchange overview"')
    expect(markup).toContain('traffic-overview__context')
    expect(markup).toContain('<span>ENVIRONMENT</span><strong>local</strong>')
    expect(markup).toContain('<span>TARGET BINDING</span><strong>Portless managed · container</strong>')
    expect(markup).toContain(`<span>STARTED</span><strong>${trafficStartedTime(exchange.startedAt)}</strong>`)
    expect(markup).toContain('<span>COMPLETED</span><strong>24ms</strong>')
    expect(markup).not.toContain('>REQUEST</button>')
    expect(markup).not.toContain('>RESPONSE</button>')
    expect(markup).not.toContain('aria-label="command protocol messages"')
    expect(markup).toContain('aria-label="command summary"')
    expect(markup).toContain('PUB orders.created')
    expect(markup).toContain('class="traffic-json"')
    expect(markup).toContain('class="traffic-json__key"')
    expect(markup).toContain('&quot;orderId&quot;')
    expect(markup).not.toContain('This command does not have a result.')
    expect(genericTcpTrafficPresentation(tcp, 'response')).toMatchObject({ label: 'RESULT', title: 'No response' })
    expect(markup).toContain('>SENT</b>')
  })

  it('summarizes PostgreSQL queries and results without protocol plumbing', () => {
    const sql = 'UPDATE store_inventory SET on_hand = on_hand - $1 WHERE sku = $2'
    const tcp = {
      ...exchange,
      protocol: 'tcp', method: undefined, host: undefined, path: undefined, requestTarget: undefined, status: undefined,
      requestHeaders: undefined, responseHeaders: undefined, requestBody: undefined, responseBody: undefined,
      source: 'inventory', target: 'inventory-postgres', targetProvider: 'container', requestBytes: 96, responseBytes: 0,
      tcp: {
        kind: 'operation', applicationProtocol: 'postgresql', operation: 'UPDATE', inspection: 'decoded', outcome: 'success',
        requestMessageCount: 5, responseMessageCount: 3,
        requestMessages: [{
          type: 'parse', offsetMs: 0, summary: `Parse ${sql}`, wireBytes: 96,
          contentType: 'text/x-sql', encoding: 'utf8', fields: [{ name: 'statement', value: '' }], content: sql,
        }, {
          type: 'bind', offsetMs: 0, summary: 'Bind parameters', wireBytes: 20,
          contentBytes: 25, capturedBytes: 25, content: '[\n  2,\n  "coffee-mug"\n]', contentType: 'application/json', encoding: 'utf8',
          fields: [{ name: 'parameters', value: '2' }],
        }, { type: 'describe', offsetMs: 0, summary: 'Describe', wireBytes: 5 }, { type: 'execute', offsetMs: 0, summary: 'Execute', wireBytes: 5 }, { type: 'sync', offsetMs: 0, summary: 'Sync', wireBytes: 5 }],
        responseMessages: [{ type: 'parse-complete', offsetMs: 0, summary: 'Parse complete', wireBytes: 5 }, { type: 'command-complete', offsetMs: 1, summary: 'UPDATE 1', wireBytes: 14, fields: [{ name: 'command', value: 'UPDATE 1' }] }, { type: 'ready', offsetMs: 1, summary: 'Ready for query', wireBytes: 6 }],
      },
    } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: tcp, onClose: () => undefined }))
    const command = postgresTrafficPresentation(tcp, 'request')

    expect(command.content.match(/UPDATE store_inventory/g)).toHaveLength(1)
    expect(markup).not.toContain(`Parse ${sql}`)
    expect(markup).not.toContain('<dt>statement</dt>')
    expect(markup).toContain('<small>text/x-sql</small>')
    expect(markup.match(/<strong>UPDATE<\/strong>/g)).toHaveLength(1)
    expect(markup).toContain('<div class="traffic-semantic-card__body"><pre class="traffic-sql">')
    expect(markup).toContain('class="traffic-sql"')
    expect(markup).toContain('class="traffic-sql__keyword"')
    expect(markup).toContain('class="traffic-sql__number"')
    expect(markup).toContain('class="traffic-sql__string"')
    expect(markup).not.toContain('aria-label="Bound parameters"')
    expect(markup).not.toContain('$1')
    expect(markup).not.toContain('$2')
    expect(command).toMatchObject({ content: "UPDATE store_inventory SET on_hand = on_hand - 2 WHERE sku = 'coffee-mug'", showTitle: false })
    expect(markup).not.toContain('<strong>UPDATE 1</strong>')
    expect(postgresTrafficPresentation(tcp, 'response')).toMatchObject({ label: 'RESULT', title: 'UPDATE 1' })
    expect(markup).not.toContain('Parse complete')
    expect(markup).not.toContain('Ready for query')
    expect(markup).not.toContain('<span>+0ms</span>')
  })

  it('renders PostgreSQL result rows as a table instead of JSON', () => {
    const tcp = {
      ...exchange,
      protocol: 'tcp', status: undefined, source: 'orders', target: 'orders-postgres', requestBytes: 0, responseBytes: 72,
      tcp: {
        kind: 'operation', applicationProtocol: 'postgresql', operation: 'SELECT', inspection: 'decoded', outcome: 'success',
        responseMessages: [{
          type: 'row-description', offsetMs: 0, summary: '3 columns', wireBytes: 24,
          fields: [{ name: 'column', value: 'id' }, { name: 'column', value: 'state' }, { name: 'column', value: 'note' }],
        }, {
          type: 'data-row', offsetMs: 1, summary: 'Data row', wireBytes: 24,
          content: '{"id":"42","state":"created","note":null}', contentType: 'application/json', encoding: 'utf8',
        }, {
          type: 'data-row', offsetMs: 2, summary: 'Data row', wireBytes: 24,
          content: '{"id":"43","state":"paid","note":"priority"}', contentType: 'application/json', encoding: 'utf8',
        }, { type: 'command-complete', offsetMs: 2, summary: 'SELECT 2', wireBytes: 6, fields: [{ name: 'command', value: 'SELECT 2' }] }],
      },
    } as TrafficExchange
    const result = databaseResultRows(tcp)

    expect(postgresTrafficPresentation(tcp, 'response')).toMatchObject({ title: 'Data row', content: '{"id":"42","state":"created","note":null}', showTitle: false })
    expect(result).toMatchObject({
      columns: ['id', 'state', 'note'],
      rows: [['42', 'created', null], ['43', 'paid', 'priority']],
      truncated: false,
    })
    expect(databaseResultCSV(result!)).toBe('id,state,note\n42,created,\n43,paid,priority')
    expect(databaseResultCSV({ columns: ['id', 'note'], rows: [[1, 'priority, "rush"'], [2, 'line\nbreak'], [3, '']] })).toBe('id,note\n1,"priority, ""rush"""\n2,"line\nbreak"\n3,""')
  })

  it('renders a MySQL result set with its captured column names', () => {
    const tcp = {
      ...exchange,
      protocol: 'tcp', status: undefined, source: 'api', target: 'api-mysql', requestBytes: 42, responseBytes: 36,
      tcp: {
        kind: 'operation', applicationProtocol: 'mysql', operation: 'SELECT', inspection: 'decoded', outcome: 'success',
        requestMessages: [
          { type: 'query', offsetMs: 0, summary: 'SELECT delivery_id, state FROM deliveries', wireBytes: 42, content: 'SELECT delivery_id, state FROM deliveries', contentType: 'text/x-sql', encoding: 'utf8' },
        ],
        responseMessages: [
          { type: 'result-set', offsetMs: 0, summary: '2 columns', wireBytes: 4, fields: [{ name: 'columns', value: '2' }] },
          { type: 'column', offsetMs: 0, summary: 'delivery_id', wireBytes: 8, fields: [{ name: 'name', value: 'delivery_id' }] },
          { type: 'column', offsetMs: 0, summary: 'state', wireBytes: 8, fields: [{ name: 'name', value: 'state' }] },
          { type: 'row', offsetMs: 1, summary: 'Data row', wireBytes: 16, content: '{"delivery_id":"d-42","state":"created"}', contentType: 'application/json', encoding: 'utf8' },
          { type: 'result-end', offsetMs: 1, summary: 'Result set complete', wireBytes: 4 },
        ],
      },
    } as TrafficExchange
    const result = databaseResultRows(tcp)
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: tcp, onClose: () => undefined }))

    expect(result).toMatchObject({ columns: ['delivery_id', 'state'], rows: [['d-42', 'created']], truncated: false })
    expect(markup).toContain('<span class="traffic-detail__protocol-badge">TCP</span>')
    expect(markup).toContain('<h3><span>MYSQL</span><code>SELECT</code></h3>')
    expect(markup).toContain('<pre class="traffic-sql">')
    expect(markup).toContain('class="traffic-sql__keyword">SELECT</span>')
    expect(markup).toContain('delivery_id')
  })

  it('shows at most ten database rows per result page', () => {
    const rows = Array.from({ length: 12 }, (_, index) => [index + 1, `row-${index + 1}`])
    const markup = renderToStaticMarkup(createElement(DatabaseResultTable, {
      result: { columns: ['id', 'value'], rows, truncated: false, contentBytes: 0, capturedBytes: 0 },
    }))

    expect(markup.match(/<tr/g)).toHaveLength(11)
    expect(markup).toContain('row-10')
    expect(markup).not.toContain('row-11')
    expect(markup).not.toContain('row-12')
    expect(markup).toContain('aria-label="database result rows pagination"')
    expect(markup).toContain('1–10 of 12')
    expect(markup).toContain('aria-label="Next database result rows page"')
  })

  it('selects Redis commands and decoded results for the semantic summary', () => {
    const tcp = {
      ...exchange,
      protocol: 'tcp', status: undefined, source: 'orders', target: 'orders-redis', requestBytes: 34, responseBytes: 8,
      tcp: {
        kind: 'operation', applicationProtocol: 'redis', operation: 'GET', inspection: 'decoded', outcome: 'success',
        requestMessages: [{ type: 'command', offsetMs: 0, summary: 'GET store:order:24', wireBytes: 34, contentType: 'application/json', encoding: 'utf8', content: '[\n  "GET",\n  "store:order:24"\n]' }],
        responseMessages: [{ type: 'response', offsetMs: 1, summary: '6 byte value', wireBytes: 8, contentType: 'application/json', encoding: 'utf8', content: '"cached"' }],
      },
    } as TrafficExchange

    expect(redisTrafficPresentation(tcp, 'request')).toMatchObject({ content: 'GET store:order:24', meta: '1 argument', command: { name: 'GET' } })
    expect(redisTrafficPresentation(tcp, 'response')).toMatchObject({ content: 'cached', meta: 'string', json: false })
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: tcp, onClose: () => undefined }))
    expect(markup).toContain('class="traffic-redis-command"')
    expect(markup).toContain('traffic-redis-command__name">GET</span>')
    expect(markup).toContain('traffic-redis-command__key">store:order:24</span>')
    expect(markup).not.toContain('[\n  &quot;GET&quot;')
    expect(markup).not.toContain('<dt>COMMAND</dt>')
    expect(markup).not.toContain('<dt>KEY</dt>')
  })
})
