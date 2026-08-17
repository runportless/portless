import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { ComponentBinding, Environment, Service, TimelineEvent, TrafficExchange } from '../types'
import { buildTopology, EnvironmentPage, overviewServiceEndpoint, paginateOverview, serviceEndpoints, summarizeTopologyTraffic, TimelinePanel, topologyEdgeKey, topologyEdgeTone, topologyPanPosition, topologyParticleMotion } from './ProjectPage'
import { TrafficDetail } from './traffic'

const service = (name: string): Service => ({ name } as Service)

describe('environment topology', () => {
  it('renders an inspected HTTP event as a request and response exchange', () => {
    const event = {
      project: 'billing', environment: 'local', sequence: 7, protocol: 'http',
      source: 'checkout', target: 'orders', targetProvider: 'local',
      startedAt: '2026-08-13T12:00:00Z', completedAt: '2026-08-13T12:00:00.024Z',
      method: 'POST', host: 'orders.local.billing.localhost', path: '/orders', status: 201,
      durationMs: 24, requestBytes: 42, responseBytes: 118,
      requestHeaders: { Authorization: ['[REDACTED]'], 'Content-Type': ['application/json'] },
      responseHeaders: { 'Content-Type': ['application/json'], 'X-Request-Id': ['req-7'] },
      requestBody: '{"sku":"coffee","quantity":2}',
      responseBody: '{"order":42,"state":"created"}',
      requestCapturedBytes: 32,
      responseCapturedBytes: 30,
    } as TrafficExchange

    const markup = renderToStaticMarkup(createElement(TrafficDetail, { exchange: event, onClose: () => undefined }))

    expect(markup).toContain('aria-label="Traffic request and response 7"')
    expect(markup).toContain('>REQUEST<')
    expect(markup).toContain('POST /orders')
    expect(markup).toContain('Host: orders.local.billing.localhost')
	expect(markup).toContain('Authorization: [REDACTED]')
	expect(markup).not.toContain('Bearer local-dev-token')
		expect(markup).toContain('>HEADERS<')
    expect(markup).toContain('>RESPONSE<')
    expect(markup).toContain('HTTP 201')
    expect(markup).toContain('X-Request-Id: req-7')
    expect(markup).toContain('&quot;sku&quot;: &quot;coffee&quot;')
    expect(markup).toContain('&quot;order&quot;: 42')
    expect(markup).not.toContain('{&quot;Authorization&quot;')
  })

  it('renders the overview topology as a bounded preview that opens the dedicated view', () => {
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [], connections: [],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, tab: 'overview', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('class="panel topology-panel topology-panel--preview"')
    expect(markup).toContain('aria-label="Open topology"')
    expect(markup).toContain('class="topology-size-button"')
    expect(markup).toContain('aria-label="Topology canvas; drag to pan"')
    expect(markup).toContain('tabindex="0"')
    expect(markup).toContain('>LIVE<')
    expect(markup).toContain('topology-live__dot')
    expect(markup).toContain('title="Pause live topology"')
    expect(markup).toContain('class="topology__pan-surface"')
    expect(markup).not.toContain('>MAXIMIZE<')
    expect(markup.indexOf('>SERVICES<')).toBeLessThan(markup.indexOf('>TOPOLOGY<'))
  })

  it('renders a dedicated topology view with fullscreen support', () => {
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [], connections: [],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, tab: 'topology', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('class="panel topology-panel topology-panel--page"')
    expect(markup).toContain('aria-label="Maximize topology"')
    expect(markup).toContain('aria-pressed="false"')
    expect(markup).toContain('>topology<')
  })

  it('describes clean, remote, and protocol-specific public service endpoints', () => {
    const localService = { name: 'orders', kind: 'process', endpoints: [{ kind: 'public', protocol: 'http', host: 'orders.local.billing.localhost', port: 80, url: 'http://orders.local.billing.localhost' }], upstreamPort: 43101 } as Service
    expect(serviceEndpoints(localService)).toEqual([
      { label: 'CLEAN URL', value: 'http://orders.local.billing.localhost', detail: 'Browser and host access through Portless', href: 'http://orders.local.billing.localhost' },
    ])

    const remoteBinding = { service: 'orders', provider: 'remote', remote: { url: 'https://orders.qa.example.com', classification: 'qa', writePolicy: 'read-only' } } as ComponentBinding
    expect(serviceEndpoints({ name: 'orders', kind: 'process' } as Service, remoteBinding)).toEqual([
      { label: 'REMOTE PROVIDER', value: 'https://orders.qa.example.com', detail: 'qa · read-only', href: 'https://orders.qa.example.com' },
    ])
    expect(serviceEndpoints({ name: 'postgres', kind: 'resource', resource: { type: 'postgres', version: '17' }, endpoints: [{ kind: 'public', protocol: 'tcp', host: 'postgres.local.store.portless.test', port: 5432, url: 'tcp://postgres.local.store.portless.test:5432' }] } as Service)[0].value).toBe('tcp://postgres.local.store.portless.test:5432')
    expect(serviceEndpoints({ name: 'redis', kind: 'resource', resource: { type: 'valkey', version: '8' }, endpoints: [{ kind: 'public', protocol: 'tcp', host: 'redis.local.store.portless.test', port: 6379, url: 'tcp://redis.local.store.portless.test:6379' }] } as Service)[0].value).toBe('tcp://redis.local.store.portless.test:6379')
  })

  it('renders copyable service endpoints on the overview', () => {
    const checkout = { name: 'checkout', kind: 'process', framework: 'nestjs', status: 'ready', endpoints: [{ kind: 'public', protocol: 'http', host: 'checkout.local.billing.localhost', port: 80, url: 'http://checkout.local.billing.localhost' }], upstreamPort: 43100, restartCount: 0, recentRequests: 0, health: { kind: 'tcp' } } as Service
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [checkout], connections: [], bindings: [{ service: 'checkout', provider: 'local' }],
    } as Environment

    expect(overviewServiceEndpoint(environment, checkout)).toBe('http://checkout.local.billing.localhost')
    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { environment, tab: 'overview', onNavigate: () => undefined, onChanged: () => undefined }))
    expect(markup).toContain('aria-label="Copy checkout endpoint"')
    expect(markup).toContain('title="Copy endpoint"')
    expect(markup).toContain('http://checkout.local.billing.localhost')
    expect(markup).toContain('class="service-copy-button"')
  })

  it('translates pointer movement into scroll-based panning', () => {
    expect(topologyPanPosition({ clientX: 300, clientY: 180, scrollLeft: 160, scrollTop: 120 }, 220, 130)).toEqual({ scrollLeft: 240, scrollTop: 170 })
    expect(topologyPanPosition({ clientX: 300, clientY: 180, scrollLeft: 160, scrollTop: 120 }, 360, 230)).toEqual({ scrollLeft: 100, scrollTop: 70 })
  })

  it('paginates overview collections at nine items', () => {
    expect(paginateOverview(Array.from({ length: 8 }, (_, index) => index), 0)).toMatchObject({ page: 0, pageCount: 1, start: 0, end: 8, total: 8 })
    expect(paginateOverview(Array.from({ length: 17 }, (_, index) => index), 1)).toMatchObject({ items: [8, 9, 10, 11, 12, 13, 14, 15], page: 1, pageCount: 3, start: 8, end: 16, total: 17 })
    expect(paginateOverview(Array.from({ length: 17 }, (_, index) => index), 99)).toMatchObject({ items: [16], page: 2, start: 16, end: 17 })
  })

  it('paginates timeline records using the selected page size', () => {
    const records = Array.from({ length: 126 }, (_, index) => index)
    expect(paginateOverview(records, 0, 25)).toMatchObject({ items: records.slice(0, 25), page: 0, pageCount: 6, start: 0, end: 25, total: 126 })
    expect(paginateOverview(records, 1, 50)).toMatchObject({ items: records.slice(50, 100), page: 1, pageCount: 3, start: 50, end: 100, total: 126 })
    expect(paginateOverview(records, 1, 100)).toMatchObject({ items: records.slice(100), page: 1, pageCount: 2, start: 100, end: 126, total: 126 })
  })

  it('renders 25 timeline records by default with page-size choices', () => {
    const timeline = Array.from({ length: 26 }, (_, index) => ({
      sequence: 26-index,
      timestamp: new Date(Date.UTC(2026, 7, 13, 12, index)).toISOString(),
      severity: 'info',
      summary: `Event ${index+1}`,
      type: 'test.event',
      actor: 'test',
      project: 'billing',
      environment: 'local',
    })) as TimelineEvent[]

    const markup = renderToStaticMarkup(createElement(TimelinePanel, { timeline }))
    expect(markup).toContain('aria-label="Timeline rows per page"')
    expect(markup).toContain('<option value="25" selected="">25</option>')
    expect(markup).toContain('<option value="50">50</option>')
    expect(markup).toContain('<option value="100">100</option>')
    expect(markup.match(/class="timeline-event"/g)).toHaveLength(25)
    expect(markup).toContain('aria-label="timeline pagination"')
    expect(markup).toContain('1–25 of 26')
  })

  it('derives live edge health from the rolling traffic window', () => {
    const now = Date.parse('2026-08-12T12:00:30Z')
    const metrics = summarizeTopologyTraffic([
      { protocol: 'http', source: 'checkout', target: 'orders', startedAt: '2026-08-12T12:00:28Z', completedAt: '2026-08-12T12:00:29Z', durationMs: 1000, status: 200, sequence: 1, requestBytes: 10, responseBytes: 20 },
      { protocol: 'http', source: 'checkout', target: 'orders', startedAt: '2026-08-12T11:59:00Z', completedAt: '2026-08-12T11:59:01Z', durationMs: 10, status: 500, sequence: 2, requestBytes: 10, responseBytes: 20 },
    ] as TrafficExchange[], now)
    const metric = metrics.get(topologyEdgeKey('checkout', 'orders'))
    expect(metric?.samples).toHaveLength(1)
    expect(topologyEdgeTone(metric, false, now)).toBe('slow')
    expect(topologyEdgeTone(metric, true, now)).toBe('fault')
  })

  it('paces particles according to request rate', () => {
    const now = Date.parse('2026-08-12T12:00:30Z')
    const traffic = Array.from({ length: 9 }, (_, index) => ({
      protocol: 'http', source: 'checkout', target: 'orders',
      startedAt: new Date(now-(index+1)*3_000-10).toISOString(),
      completedAt: new Date(now-(index+1)*3_000).toISOString(),
      durationMs: 10, status: 200, sequence: index+1, requestBytes: 10, responseBytes: 20,
    })) as TrafficExchange[]
    const metric = summarizeTopologyTraffic(traffic, now).get(topologyEdgeKey('checkout', 'orders'))

    const motion = topologyParticleMotion(metric, now)
    expect(motion.count).toBe(1)
    expect(motion.durationSeconds).toBeCloseTo(10/3)
  })

  it('lays out services by their directed dependencies', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'orders', 'redis', 'postgres'].map(service),
      connections: [
        { source: 'checkout', target: 'orders', protocol: 'http', required: true },
		{ source: 'orders', target: 'redis', protocol: 'tcp', binding: 'valkey', required: true },
		{ source: 'orders', target: 'postgres', protocol: 'tcp', binding: 'postgres', required: true },
      ],
    } as Environment

    const topology = buildTopology(environment)

    expect(topology.levels.map((level) => level.map((item) => item.key))).toEqual([
      ['external'],
      ['checkout'],
      ['orders'],
      ['redis', 'postgres'],
    ])
    expect(topology.edges.map(({ source, target }) => `${source}:${target}`)).toEqual([
      'external:checkout',
      'checkout:orders',
      'orders:redis',
      'orders:postgres',
    ])
  })

  it('does not flatten disconnected services beneath the client', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'worker'].map(service),
      connections: [],
    } as unknown as Environment

    const topology = buildTopology(environment)

    expect(topology.edges).toEqual([{ source: 'external', target: 'checkout', protocol: 'http' }])
  })

  it('places a shared dependency below its deepest caller', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'orders', 'redis'].map(service),
      connections: [
        { source: 'checkout', target: 'orders', protocol: 'http', required: true },
		{ source: 'checkout', target: 'redis', protocol: 'tcp', binding: 'valkey', required: true },
		{ source: 'orders', target: 'redis', protocol: 'tcp', binding: 'valkey', required: true },
      ],
    } as Environment

    expect(buildTopology(environment).levels.map((level) => level.map((item) => item.key))).toEqual([
      ['external'],
      ['checkout'],
      ['orders'],
      ['redis'],
    ])
  })

  it('terminates when service dependencies contain a cycle', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'orders'].map(service),
      connections: [
        { source: 'checkout', target: 'orders', protocol: 'http', required: true },
        { source: 'orders', target: 'checkout', protocol: 'http', required: true },
      ],
    } as Environment

    expect(buildTopology(environment).levels.map((level) => level.map((item) => item.key))).toEqual([
      ['external'],
      ['checkout'],
      ['orders'],
    ])
  })
})
