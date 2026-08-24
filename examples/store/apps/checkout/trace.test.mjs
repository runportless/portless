import assert from 'node:assert/strict'
import test from 'node:test'
import { forwardedTraceHeaders } from './trace.mjs'

test('forwards W3C trace context to downstream services', () => {
  const headers = forwardedTraceHeaders({
    traceparent: '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01',
    tracestate: 'portless=local',
    baggage: 'tenant=store',
    authorization: 'Bearer secret',
    cookie: 'session=secret',
  })

  assert.deepEqual(headers, {
    traceparent: '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01',
    tracestate: 'portless=local',
    baggage: 'tenant=store',
  })
})

test('forwards B3 and Datadog propagation formats', () => {
  const headers = forwardedTraceHeaders({
    b3: '80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1',
    'x-b3-traceid': '80f198ee56343ba864fe8b2a57d3eff7',
    'x-b3-spanid': 'e457b5a2e4d86bd1',
    'x-b3-parentspanid': '05e3ac9a4f6e3b90',
    'x-b3-sampled': '1',
    'x-b3-flags': '0',
    'x-datadog-trace-id': '1',
    'x-datadog-parent-id': '2',
    'x-datadog-sampling-priority': '1',
    'x-datadog-origin': 'synthetics',
    'x-datadog-tags': '_dd.p.tid=463ac35c9f6413ad',
  })

  assert.equal(Object.keys(headers).length, 11)
  assert.equal(headers.b3, '80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1')
  assert.equal(headers['x-b3-spanid'], 'e457b5a2e4d86bd1')
  assert.equal(headers['x-datadog-parent-id'], '2')
  assert.equal(headers['x-datadog-tags'], '_dd.p.tid=463ac35c9f6413ad')
})

test('omits missing, malformed, and unrelated header values', () => {
  assert.deepEqual(forwardedTraceHeaders({ traceparent: ['duplicate'], tracestate: '  ' }), {})
})
