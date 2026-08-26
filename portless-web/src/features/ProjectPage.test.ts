import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { ComponentBinding, Environment, Project, Service, TimelineEvent, TrafficActivity, TrafficExchange } from '../types'
import { paginateItems } from '../components/PanelPagination'
import { buildTopology, defaultProviderBinding, displayLaunchMode, EnvironmentPage, mergeTopologySignal, overviewServiceEndpoint, providerBindingMatches, providerDisplayName, serviceEndpoints, summarizeEnvironmentBindings, summarizeTopologyTraffic, TimelinePanel, topologyCenterPosition, topologyEdgeKey, topologyEdgeTone, topologyEdgeVisualState, topologyPanPosition, topologyParticleMotion } from './ProjectPage'

const service = (name: string): Service => ({ name } as Service)

describe('environment topology', () => {
  it('places the infrequently used bindings tab immediately before timeline', () => {
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [], connections: [],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, tab: 'overview', onNavigate: () => undefined, onChanged: () => undefined,
    }))
    const tabs = markup.match(/<nav class="tabs" aria-label="Environment views">(.*?)<\/nav>/)?.[1] ?? ''

    expect(tabs).toContain('>faults<')
    expect(tabs.indexOf('>faults<')).toBeLessThan(tabs.indexOf('>bindings<'))
    expect(tabs.indexOf('>bindings<')).toBeLessThan(tabs.indexOf('>timeline<'))
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
    expect(markup).toContain('aria-label="Center topology"')
    expect(markup).toContain('class="topology-center-button"')
    expect(markup).toContain('aria-label="Open topology"')
    expect(markup).toContain('class="topology-size-button"')
    expect(markup).toContain('aria-label="Topology canvas; drag to pan"')
    expect(markup).toContain('tabindex="0"')
    expect(markup).toContain('>LIVE<')
    expect(markup).toContain('topology-live__dot')
    expect(markup).toContain('title="Pause live topology"')
    expect(markup).toContain('class="topology__pan-surface"')
    expect(markup).toContain('id="topology-arrow-inactive"')
    expect(markup).toContain('id="topology-arrow-active"')
    expect(markup.match(/markerUnits="userSpaceOnUse"/g)).toHaveLength(2)
    expect(markup).toContain('markerWidth="6" markerHeight="6"')
    expect(markup).toContain('markerWidth="10.62" markerHeight="10.62"')
    expect(markup).not.toContain('>MAXIMIZE<')
    expect(markup).toContain('>BINDINGS<')
    expect(markup).toContain('>LOCAL<')
    expect(markup).not.toContain('>REVISION<')
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
    expect(markup).toContain('aria-label="Center topology"')
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

  it('summarizes local, hybrid, and remote environment bindings', () => {
    const services = ['checkout', 'orders', 'postgres'].map((name) => ({ name } as Service))
    const environment = { services, bindings: [
      { service: 'checkout', provider: 'local' },
      { service: 'orders', provider: 'local' },
      { service: 'postgres', provider: 'container' },
    ] } as Environment

    expect(summarizeEnvironmentBindings(environment)).toEqual({ value: 'LOCAL', detail: '3 services local' })
    expect(summarizeEnvironmentBindings({ ...environment, bindings: [
      { service: 'checkout', provider: 'local' },
      { service: 'orders', provider: 'remote', remote: { url: 'https://orders.qa.example.test', classification: 'qa', writePolicy: 'read-only' } },
      { service: 'postgres', provider: 'container' },
    ] })).toEqual({ value: 'HYBRID', detail: '2 local · 1 QA', tone: 'warning' })
    expect(summarizeEnvironmentBindings({ ...environment, services: services.slice(0, 2), bindings: [
      { service: 'checkout', provider: 'remote' },
      { service: 'orders', provider: 'remote' },
    ] })).toEqual({ value: 'REMOTE', detail: '2 remote services', tone: 'warning' })
    expect(summarizeEnvironmentBindings({ ...environment, bindings: [
      { service: 'checkout', provider: 'local' },
      { service: 'orders', provider: 'mock', mock: { profile: 'sold-out' } },
      { service: 'postgres', provider: 'container' },
    ] })).toEqual({ value: 'LOCAL', detail: '2 local · 1 mocked' })
  })

  it('shows mock as the mode for a service using a mock provider', () => {
    const inventory = { name: 'inventory', kind: 'process', launchMode: 'managed' } as Service
    const environment = {
      services: [inventory],
      bindings: [{ service: 'inventory', provider: 'mock', mock: { profile: 'sold-out' } }],
    } as Environment

    expect(displayLaunchMode(environment, inventory)).toBe('mock')
  })

  it('shows container as the mode for resource services', () => {
    const resources = ['postgres', 'redis'].map((name) => ({ name, kind: 'resource', resource: { type: name, version: 'latest' } } as Service))
    const environment = {
      services: resources,
      bindings: resources.map((resource) => ({ service: resource.name, provider: 'container' })),
    } as Environment

    expect(resources.map((resource) => displayLaunchMode(environment, resource))).toEqual(['container', 'container'])
  })

  it('keeps the provider table full width and configures each service from a modal action', () => {
    const checkout = { name: 'checkout', kind: 'process', status: 'stopped' } as Service
    const project = { name: 'billing', sources: [{ name: 'checkout', services: ['checkout'] }] } as Project
    const environment = {
      project: 'billing', name: 'local', status: 'stopped', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [checkout], connections: [],
      sources: [{ name: 'checkout', path: '/workspace/checkout', status: 'ready', createdAt: '2026-08-17T16:20:00Z', scannedAt: new Date(0).toISOString() }],
      bindings: [{ service: 'checkout', provider: 'local', source: 'checkout', modifiedAt: '2026-08-18T18:30:00Z' }],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, project, tab: 'bindings', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('class="experiment-layout bindings-layout"')
    expect(markup).toContain('<span>PROVIDERS</span><button')
    expect(markup).toContain('role="table" aria-label="Configured providers"')
    expect(markup).toContain('<span role="columnheader">Service</span><span role="columnheader">Provider</span><span role="columnheader">Configuration</span><span role="columnheader">Modified</span><span role="columnheader" aria-label="Row actions"></span>')
    expect(markup).toContain('class="provider-service"')
    expect(markup).toContain('title="stopped"')
    expect(markup).not.toContain('<span>healthy</span>')
    expect(markup).toContain('>Checkout</div>')
    expect(markup).toContain('dateTime="2026-08-18T18:30:00Z"')
    expect(markup).not.toContain(' ago</time>')
    expect(markup).toContain('aria-haspopup="dialog"')
    expect(markup).toContain('>CONFIGURE PROVIDER</button>')
    expect(markup).not.toContain('>CHANGE</button>')
    expect(markup).toContain('>EDIT</button>')
    expect(markup).toContain('<span>CHECKOUTS</span><button')
    expect(markup).toContain('>MANAGE SOURCES</button>')
    expect(markup).not.toContain('>ADD SOURCE</button>')
    expect(markup).toContain('<table class="source-table" aria-label="Environment checkouts">')
    expect(markup).toContain('<th scope="col">Source</th><th scope="col">Path</th><th scope="col">Created</th><th scope="col" aria-label="Row actions"></th>')
    expect(markup).not.toContain('>Actions</')
    expect(markup).not.toContain('class="source-name-button"')
    expect(markup).not.toContain('class="source-row--interactive"')
    expect(markup).not.toContain('aria-label="Configure checkout source"')
    expect(markup).toContain('<strong>checkout</strong>')
    expect(markup).toContain('<code title="/workspace/checkout">/workspace/checkout</code>')
    expect(markup).toContain('<time dateTime="2026-08-17T16:20:00Z"')
    expect(markup).toContain('>EDIT</button>')
    expect(markup).toContain('>REMOVE</button>')
    expect(markup).not.toContain('>DELETE</button>')
    expect(markup).not.toContain('change a path to a Git worktree')
    expect(markup).not.toContain('class="form-modal add-source-modal"')
    expect(markup).not.toContain('class="form-modal configure-provider-modal"')
    expect(markup).not.toContain('>REMOTE URL<')
    expect(markup).not.toContain('source checkout')
    expect(markup).not.toContain('deterministic HTTP mock')
    expect(markup).not.toContain('container runtime')
  })

  it('uses concise provider names on the bindings page', () => {
    expect(providerDisplayName('local')).toBe('Checkout')
    expect(providerDisplayName('remote')).toBe('Remote')
    expect(providerDisplayName('mock')).toBe('Mock')
    expect(providerDisplayName('container')).toBe('Container')
  })

  it('paginates providers and checkouts independently after five rows', () => {
    const services = Array.from({ length: 6 }, (_, index) => ({ name: `service-${index + 1}`, kind: 'process', status: 'stopped' } as Service))
    const environment = {
      project: 'billing', name: 'local', status: 'stopped', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services, connections: [],
      sources: services.map((item, index) => ({ name: `source-${index + 1}`, path: `/workspace/source-${index + 1}`, status: 'ready', createdAt: new Date(0).toISOString(), scannedAt: new Date(0).toISOString() })),
      bindings: services.map((item, index) => ({ service: item.name, provider: 'local', source: `source-${index + 1}` })),
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, tab: 'bindings', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('aria-label="providers pagination"')
    expect(markup).toContain('aria-label="checkouts pagination"')
    expect(markup).toContain('1–5 of 6')
    expect(markup).toContain('service-5')
    expect(markup).not.toContain('service-6')
    expect(markup).toContain('/workspace/source-5')
    expect(markup).not.toContain('/workspace/source-6')
  })

  it('shows project sources without checkouts as environment configuration choices', () => {
    const project = { name: 'billing', sources: [{ name: 'checkout', services: ['checkout'] }, { name: 'inventory', services: ['inventory'] }] } as Project
    const environment = {
      project: 'billing', name: 'qa', status: 'stopped', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [{ name: 'checkout', kind: 'process', status: 'stopped' } as Service, { name: 'inventory', kind: 'process', status: 'stopped' } as Service],
      connections: [],
      sources: [{ name: 'checkout', path: '/workspace/checkout', status: 'ready', createdAt: new Date(0).toISOString(), scannedAt: new Date(0).toISOString() }],
      bindings: [{ service: 'checkout', provider: 'local', source: 'checkout' }],
      issues: [{ code: 'MISSING_BINDING', subject: 'inventory', message: 'component has no provider binding' }],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, project, tab: 'bindings', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('<strong>inventory</strong>')
    expect(markup).toContain('Configuration required')
    expect(markup).toContain('>CONFIGURE</button>')
    expect(markup).not.toContain('>ADD SOURCE</button>')
  })

  it('derives and compares canonical provider defaults from the project topology', () => {
    const project = { name: 'billing', sources: [{ name: 'checkout', services: ['checkout'] }] } as Project
    const environment = { sources: [{ name: 'checkout' }] } as Environment
    const checkout = { name: 'checkout', kind: 'process' } as Service
    const postgres = { name: 'postgres', kind: 'resource' } as Service

    expect(defaultProviderBinding(project, environment, checkout)).toEqual({ service: 'checkout', provider: 'local', source: 'checkout' })
    expect(defaultProviderBinding(project, environment, postgres)).toEqual({ service: 'postgres', provider: 'container' })
    expect(providerBindingMatches({ service: 'checkout', provider: 'local', source: 'checkout' }, { service: 'checkout', provider: 'local', source: 'CHECKOUT' })).toBe(true)
    expect(providerBindingMatches({ service: 'checkout', provider: 'mock', mock: { profile: 'sold-out' } }, { service: 'checkout', provider: 'mock', mock: { profile: 'SOLD-OUT' } })).toBe(true)
    expect(providerBindingMatches({ service: 'checkout', provider: 'remote' }, { service: 'checkout', provider: 'local', source: 'checkout' })).toBe(false)
  })

  it('translates pointer movement into scroll-based panning', () => {
    expect(topologyPanPosition({ clientX: 300, clientY: 180, scrollLeft: 160, scrollTop: 120 }, 220, 130)).toEqual({ scrollLeft: 240, scrollTop: 170 })
    expect(topologyPanPosition({ clientX: 300, clientY: 180, scrollLeft: 160, scrollTop: 120 }, 360, 230)).toEqual({ scrollLeft: 100, scrollTop: 70 })
  })

  it('calculates the initial and requested topology center consistently', () => {
    expect(topologyCenterPosition({ scrollWidth: 1200, clientWidth: 800, scrollHeight: 900, clientHeight: 500 })).toEqual({ scrollLeft: 200, scrollTop: 120 })
    expect(topologyCenterPosition({ scrollWidth: 600, clientWidth: 800, scrollHeight: 400, clientHeight: 500 })).toEqual({ scrollLeft: 0, scrollTop: 0 })
  })

  it('paginates overview collections at eight items', () => {
    expect(paginateItems(Array.from({ length: 8 }, (_, index) => index), 0, 8)).toMatchObject({ page: 0, pageCount: 1, start: 0, end: 8, total: 8 })
    expect(paginateItems(Array.from({ length: 17 }, (_, index) => index), 1, 8)).toMatchObject({ items: [8, 9, 10, 11, 12, 13, 14, 15], page: 1, pageCount: 3, start: 8, end: 16, total: 17 })
    expect(paginateItems(Array.from({ length: 17 }, (_, index) => index), 99, 8)).toMatchObject({ items: [16], page: 2, start: 16, end: 17 })
  })

  it('paginates timeline records using the selected page size', () => {
    const records = Array.from({ length: 126 }, (_, index) => index)
    expect(paginateItems(records, 0, 25)).toMatchObject({ items: records.slice(0, 25), page: 0, pageCount: 6, start: 0, end: 25, total: 126 })
    expect(paginateItems(records, 1, 50)).toMatchObject({ items: records.slice(50, 100), page: 1, pageCount: 3, start: 50, end: 100, total: 126 })
    expect(paginateItems(records, 1, 100)).toMatchObject({ items: records.slice(100), page: 1, pageCount: 2, start: 100, end: 126, total: 126 })
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

  it('ignores background operations and protocol heartbeats while retaining real TCP activity', () => {
    const now = Date.parse('2026-08-25T12:00:30Z')
    const heartbeat = {
      project: 'store', environment: 'local', protocol: 'tcp', applicationProtocol: 'redis',
      source: 'orders', target: 'redis', observedAt: new Date(now).toISOString(),
      phase: 'heartbeat', activeConnections: 2,
    } as TrafficActivity
    const heartbeatMetrics = mergeTopologySignal(new Map(), heartbeat, now)
    const heartbeatMetric = heartbeatMetrics.get(topologyEdgeKey('orders', 'redis'))
    expect(heartbeatMetric?.activeConnections).toBe(2)
    expect(heartbeatMetric?.bytes).toBe(0)
    expect(topologyEdgeTone(heartbeatMetric, false, now)).toBe('idle')
    expect(topologyParticleMotion(heartbeatMetric, now).count).toBe(0)

    const housekeeping = {
      project: 'store', environment: 'local', sequence: 1, protocol: 'tcp', background: true,
      source: 'orders', target: 'redis', startedAt: new Date(now-2).toISOString(), completedAt: new Date(now-1).toISOString(),
      durationMs: 1, requestBytes: 20, responseBytes: 10,
      tcp: { kind: 'operation', applicationProtocol: 'redis', operation: 'PING', inspection: 'decoded', outcome: 'success' },
    } as TrafficExchange
    expect(mergeTopologySignal(heartbeatMetrics, housekeeping, now)).toBe(heartbeatMetrics)
    expect(summarizeTopologyTraffic([housekeeping], now).size).toBe(0)

    const applicationOperation = {
      ...housekeeping, sequence: 2, background: false,
      tcp: { ...housekeeping.tcp!, operation: 'GET' },
    } as TrafficExchange
    const activeMetrics = mergeTopologySignal(heartbeatMetrics, applicationOperation, now)
    const activeMetric = activeMetrics.get(topologyEdgeKey('orders', 'redis'))
    expect(activeMetric?.bytes).toBe(30)
    expect(topologyEdgeTone(activeMetric, false, now)).toBe('active')
    expect(topologyParticleMotion(activeMetric, now).count).toBe(1)
  })

  it('uses byte activity for undeclared TCP without letting zero-byte heartbeats animate it', () => {
    const now = Date.parse('2026-08-25T12:00:30Z')
    const activity = (phase: TrafficActivity['phase'], requestBytes: number) => ({
      project: 'store', environment: 'local', protocol: 'tcp', source: 'worker', target: 'queue',
      observedAt: new Date(now).toISOString(), phase, activeConnections: 1, requestBytes,
    }) as TrafficActivity

    const idle = mergeTopologySignal(new Map(), activity('heartbeat', 0), now)
    expect(topologyEdgeTone(idle.get(topologyEdgeKey('worker', 'queue')), false, now)).toBe('idle')
    const active = mergeTopologySignal(idle, activity('data', 12), now)
    expect(topologyEdgeTone(active.get(topologyEdgeKey('worker', 'queue')), false, now)).toBe('active')
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

  it('bolds an active edge once while particles communicate higher traffic', () => {
    const now = Date.parse('2026-08-12T12:00:30Z')
    const metricFor = (count: number) => summarizeTopologyTraffic(Array.from({ length: count }, (_, index) => ({
      protocol: 'http', source: 'checkout', target: 'orders',
      startedAt: new Date(now-index*100-10).toISOString(),
      completedAt: new Date(now-index*100).toISOString(),
      durationMs: 10, status: 200, sequence: index+1, requestBytes: 10, responseBytes: 20,
    })) as TrafficExchange[], now).get(topologyEdgeKey('checkout', 'orders'))
    const firstRequest = metricFor(1)
    const heavyTraffic = metricFor(180)

    const inactive = topologyEdgeVisualState(undefined, now, false)
    const active = topologyEdgeVisualState(firstRequest, now, false)
    expect(inactive).toEqual({ strokeWidth: 1, markerID: 'topology-arrow-inactive' })
    expect(active.strokeWidth).toBeCloseTo(1.77)
    expect(active.markerID).toBe('topology-arrow-active')
    expect(topologyEdgeVisualState(heavyTraffic, now, false)).toBe(active)
    expect(topologyEdgeVisualState(undefined, now, true)).toBe(active)
    expect(topologyParticleMotion(firstRequest, now).count).toBe(1)
    expect(topologyParticleMotion(heavyTraffic, now).count).toBe(4)
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
