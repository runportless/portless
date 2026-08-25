import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange } from '../../types'
import { defaultTrafficPayloadView, formatTrafficBody, formattedTrafficHeaders, rawTrafficMessage, trafficTargetBinding, TrafficDetail } from './TrafficDetail'

const exchange = {
  project: 'billing', environment: 'local', sequence: 7, protocol: 'http',
  source: 'checkout', target: 'orders', targetProvider: 'mock', mockProfile: 'sold-out', mockRoute: 'reject-order',
  startedAt: '2026-08-13T12:00:00Z', completedAt: '2026-08-13T12:00:00.024Z',
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
  it('opens HTTP exchanges with overview context above request and response tabs', () => {
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange, onClose: () => undefined }))

    expect(markup).toContain('aria-label="Traffic request and response 7"')
    expect(markup).toContain('class="drawer-size-button"')
    expect(markup).toContain('aria-label="Full screen traffic details"')
    expect(markup).toContain('d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4"')
    expect(markup).toContain('aria-label="Exchange overview"')
    expect(markup).toContain('aria-label="Exchange payload"')
    expect(markup).toContain('aria-selected="true" class="is-active">REQUEST</button>')
    expect(markup).toContain('>RESPONSE</button>')
    expect(markup.indexOf('>REQUEST</button>')).toBeLessThan(markup.indexOf('>RESPONSE</button>'))
    expect(markup).not.toContain('traffic-overview__heading')
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
    expect(markup).not.toContain('TARGET PROVIDER')
    expect(markup).toContain('<span>COMPLETED</span><strong>24ms</strong>')
    expect(markup).not.toContain('<span>COMPLETED</span><strong>+24ms</strong>')
    expect(markup).not.toContain('traffic-trace-context')
    expect(markup).not.toContain('TRACE ID')
    expect(markup).not.toContain('4bf92f3577b34da6a3ce929d0e0e4736')
    expect(markup).toContain('sold-out / reject-order')
    expect(markup).not.toContain('aria-label="Transfer summary"')
    expect(markup).not.toContain('traffic-message-workbench--response')
    expect(markup).not.toContain('&quot;order&quot;: 42')
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
    const local = { ...exchange, targetProvider: 'local', mockProfile: undefined, mockRoute: undefined } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, {
      exchange: local,
      targetBinding: { service: 'orders', provider: 'local', source: 'checkout' },
      onClose: () => undefined,
    }))

    expect(markup).toContain('<span>TARGET BINDING</span><strong>checkout · local</strong>')
    expect(trafficTargetBinding(exchange, { service: 'orders', provider: 'mock', mock: { profile: 'sold-out' } })).toBe('sold-out · mock')
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
      exchange: { ...exchange, mockProfile: undefined, mockRoute: undefined },
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

  it('renders TCP exchanges as a session overview without HTTP payload tabs', () => {
    const tcp = {
      ...exchange,
      protocol: 'tcp', method: undefined, host: undefined, path: undefined, requestTarget: undefined, status: undefined,
      requestHeaders: undefined, responseHeaders: undefined, requestBody: undefined, responseBody: undefined,
      traceId: undefined, spanId: undefined, parentSpanId: undefined,
      source: 'orders', target: 'postgres', targetProvider: 'container', requestBytes: 8, responseBytes: 1,
    } as TrafficExchange
    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: tcp, onClose: () => undefined }))

    expect(markup).toContain('TCP session')
    expect(markup).toContain('aria-label="Exchange overview"')
    expect(markup).not.toContain('aria-label="Exchange payload"')
    expect(markup).toContain('Payload content is not captured.')
    expect(markup).toContain('8 B sent · 1 B received')
    expect(markup).not.toContain('traffic-trace-context')
    expect(markup).not.toContain('>REQUEST</button>')
    expect(markup).not.toContain('>RESPONSE</button>')
  })
})
