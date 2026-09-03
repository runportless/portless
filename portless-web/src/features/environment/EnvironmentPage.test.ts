import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment, TimelineEvent } from '../../api/contracts/environments'
import type { FaultRule, Recording } from '../../api/contracts/experiments'
import type { Project } from '../../api/contracts/projects'
import type { ComponentBinding, Service } from '../../api/contracts/topology'
import type { TrafficActivity, TrafficExchange } from '../../api/contracts/traffic'
import { paginateItems } from '../../components/PanelPagination'
import { sortCheckoutRows } from './bindings/CheckoutTable'
import { defaultProviderBinding, providerBindingMatches, providerDisplayName } from './bindings/bindingPresentation'
import { sortProviderBindings } from './bindings/ProviderBindingsTable'
import { EnvironmentPage } from './EnvironmentPage'
import type { EnvironmentActivity } from './useEnvironmentActivity'
import type { EnvironmentActions } from './useEnvironmentActions'
import { sortOverviewServices, summarizeEnvironmentBindings } from './OverviewPanel'
import { displayLaunchMode, overviewServiceEndpoint, serviceEndpoints } from './service/servicePresentation'
import { TimelinePanel } from './timeline/TimelinePanel'
import { topologyServicePreviewDetails, topologyServicePreviewPlacement } from './topology/TopologyCanvas'
import { buildTopology, mergeTopologySignal, summarizeTopologyTraffic, topologyCenterPosition, topologyEdgeKey, topologyEdgeTone, topologyEdgeVisualState, topologyPanPosition, topologyParticleMotion } from './topology/topologyModel'

const service = (name: string): Service => ({ name } as Service)

const activity: EnvironmentActivity = { timeline: [], recordings: [], faults: [], error: null, loading: false, refresh: async () => undefined, dismissError: () => undefined }
const actions: EnvironmentActions = { identity: 'billing/local', busy: null, error: null, forgetError: null, trackingInterrupted: false, disabled: false, run: async () => undefined, forget: async () => undefined, resumeTracking: () => undefined, dismissError: () => undefined, dismissForgetError: () => undefined }

describe('environment content notices', () => {
  const environment: Environment = { project: 'billing', name: 'local', status: 'healthy', revision: 1, createdAt: '', updatedAt: '', services: [], connections: [] }
  const render = (value: Environment, view: 'overview' | 'topology' | 'mocks' = 'overview') => renderToStaticMarkup(createElement(EnvironmentPage, { environment: value, activity, actions, view, onNavigate: () => undefined, onChanged: () => undefined }))

  it('keeps navigation and routine status in the header while showing Overview identity', () => {
    for (const value of [environment, { ...environment, status: 'stopped' as const, reason: 'not running' }]) {
      const markup = render(value)
      expect(markup).not.toContain('environment-notices')
      expect(markup).not.toContain('aria-label="Environment views"')
      expect(markup).not.toContain('<h1')
      expect(markup).not.toContain('environment-heading__message')
    }
    expect(render(environment)).toContain('all services')
    expect(render(environment)).toContain('<h2>local</h2>')
    expect(render(environment).indexOf('environment-overview-heading')).toBeLessThan(render(environment).indexOf('state-grid'))
    for (const view of ['topology', 'mocks'] as const) {
      expect(render(environment, view)).not.toContain('environment-overview-heading')
    }
  })

  it('keeps activity in the status cards without duplicate Overview controls', () => {
    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { environment: { ...environment, bindings: [{ service: 'checkout', provider: 'mock', mock: { scenario: 'sold-out' } }] }, actions, view: 'overview', onNavigate: () => undefined, onChanged: () => undefined,
      activity: { ...activity,
        recordings: [{ name: 'completed-capture', status: 'completed' }, { name: 'live-capture', status: 'active' }] as Recording[],
        faults: [{ name: 'disabled-fault', enabled: false }, { name: 'enabled-fault', enabled: true }] as FaultRule[],
      },
    }))
    expect(markup).not.toContain('environment-activity-indicators')
    expect(markup).not.toContain('recording-indicator')
    expect(markup).not.toContain('fault-indicator')
    expect(markup).not.toContain('mock-indicator')
    expect(markup).toContain('live-capture')
    expect(markup).toContain('affecting local traffic')
    expect(markup).not.toContain('completed-capture')
  })

  it.each([
    ['starting', 'services are starting'],
    ['recovering', 'services are being recovered'],
    ['stopping', 'services are stopping'],
  ] as const)('keeps %s progress out of the content layout', (status, reason) => {
    for (const view of ['overview', 'topology', 'mocks'] as const) {
      const markup = render({ ...environment, status, reason }, view)
      expect(markup).not.toContain('environment-notices')
      expect(markup).not.toContain('environment-status-reason')
    }
  })

  it('keeps failure reasons and configuration remediation readable', () => {
    const markup = render({ ...environment, status: 'degraded', reason: 'Waiting for orders to become ready', issues: [{ code: 'MISSING_SOURCE', message: 'Orders checkout is missing.', remediation: 'Attach the orders checkout in Bindings.' }] })
    expect(markup).toContain('environment-notices')
    expect(markup).toContain('Waiting for orders to become ready')
    expect(markup).toContain('Orders checkout is missing.')
    expect(markup).toContain('Attach the orders checkout in Bindings.')
    expect(markup.match(/class="action-error" role="alert"/g)).toHaveLength(2)
    expect(markup).not.toContain('class="alert')
  })

  it.each(['failed', 'unknown'] as const)('keeps %s explanations visible', (status) => {
    const markup = render({ ...environment, status, reason: 'Orders runtime could not be verified.' })
    expect(markup).toContain('class="action-error" role="alert"')
    expect(markup).not.toContain('environment-status-reason')
    expect(markup).toContain('Orders runtime could not be verified.')
  })

  it('still shows actionable configuration issues during a transition', () => {
    const markup = render({ ...environment, status: 'recovering', reason: 'services are being recovered', issues: [{ code: 'MISSING_SOURCE', message: 'Orders checkout is missing.', remediation: 'Attach the orders checkout in Bindings.' }] })
    expect(markup).not.toContain('environment-status-reason')
    expect(markup).toContain('Configuration needs attention')
    expect(markup).toContain('Attach the orders checkout in Bindings.')
    expect(markup).toContain('class="action-error" role="alert"')
  })
})

describe('overview services lifecycle controls', () => {
  const render = (status: Environment['status'], statuses: readonly Service['status'][], actionOverrides: Partial<EnvironmentActions> = {}) => {
    const environment: Environment = {
      project: 'billing', name: 'local', status, revision: 1, createdAt: '', updatedAt: '', connections: [],
      services: statuses.map((state, index): Service => ({ name: `service-${index}`, kind: 'process', status: state, launchMode: 'managed', generation: 1, required: true, health: { kind: 'tcp', timeout: 0, interval: 0 }, restartCount: 0, recentRequests: 0, endpoints: [] })),
    }
    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { environment, activity, actions: { ...actions, ...actionOverrides }, view: 'overview', onNavigate: () => undefined, onChanged: () => undefined }))
    return { markup, button: markup.match(/<button class="button button--small services-lifecycle[^>]*>[^<]*<\/button>/)?.[0] }
  }

  it.each([
    ['stopped', ['stopped', 'stopped'], 'Start All'],
    ['stopped', ['planned', 'planned'], 'Start All'],
    ['healthy', ['ready', 'ready'], 'Stop All'],
  ] as const)('offers the bulk action for a %s environment without a workload count', (status, statuses, label) => {
    const { markup, button } = render(status, statuses)
    expect(button).toContain(`>${label}</button>`)
    expect(button).not.toContain('disabled=""')
    expect(markup).not.toContain('workloads')
  })

  it.each([
    ['stopped', []],
    ['healthy', []],
    ['degraded', ['ready', 'stopped']],
    ['degraded', ['ready', 'unhealthy']],
    ['failed', ['failed', 'failed']],
    ['unknown', ['ready', 'unknown']],
  ] as const)('omits the bulk action for mixed, empty, or unverified services in %s state', (status, statuses) => {
    expect(render(status, statuses).button).toBeUndefined()
  })

  it.each([
    ['healthy', ['ready', 'ready'], 'up', 'Starting…'],
    ['stopped', ['stopped', 'stopped'], 'down', 'Stopping…'],
    ['starting', ['ready', 'stopped'], null, 'Starting…'],
    ['stopping', ['ready', 'stopped'], null, 'Stopping…'],
    ['degraded', ['ready', 'stopped'], 'up', 'Starting…'],
    ['degraded', ['ready', 'stopped'], 'down', 'Stopping…'],
  ] as const)('keeps confirmed progress disabled during %s, including mixed service snapshots', (status, statuses, busy, label) => {
    const { markup, button } = render(status, statuses, { busy, disabled: true })
    expect(button).toContain(`>${label}</button>`)
    expect(button).toContain('disabled=""')
    const serviceMenus = markup.match(/<button class="service-row__menu-trigger"[^>]*>/g)
    expect(serviceMenus).toHaveLength(2)
    for (const menu of serviceMenus!) expect(menu).toContain('disabled=""')
  })

  it('disables the bulk action while the daemon is disconnected', () => {
    const { button } = render('healthy', ['ready', 'ready'], { disabled: true })
    expect(button).toContain('>Stop All</button>')
    expect(button).toContain('disabled=""')
  })
})

describe('environment topology', () => {
  it('renders the overview topology as a bounded preview that opens the dedicated view', () => {
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [], connections: [],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions,
      environment, view: 'overview', onNavigate: () => undefined, onChanged: () => undefined,
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
    expect(markup).toContain('id="topology-arrow-warning"')
    expect(markup).toContain('id="topology-arrow-error"')
    expect(markup.match(/markerUnits="userSpaceOnUse"/g)).toHaveLength(4)
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

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions,
      environment, view: 'topology', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('class="panel topology-panel topology-panel--page"')
    expect(markup).toContain('aria-label="Center topology"')
    expect(markup).toContain('aria-label="Maximize topology"')
    expect(markup).toContain('aria-pressed="false"')
    expect(markup).toContain('>TOPOLOGY<')
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
    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions, environment, view: 'overview', onNavigate: () => undefined, onChanged: () => undefined }))
    expect(markup).toContain('aria-label="Copy checkout endpoint"')
    expect(markup).toContain('title="Copy endpoint"')
    expect(markup).toContain('http://checkout.local.billing.localhost')
    expect(markup).toContain('class="service-list-endpoint service-list-endpoint--copyable"')
    expect(markup).toContain('<span class="service-copy-indicator" aria-hidden="true">')
    expect(markup).toContain('aria-label="View checkout details"')
    expect(markup).toContain('aria-label="Service actions for checkout"')
    expect(markup).not.toContain('>DETAILS</button>')
    expect(markup).not.toContain('>INSPECT</button>')
    expect(markup).not.toContain('sortable-column-sort-control')
  })

  it('uses the same endpoint cell for mock explanations without a copy action', () => {
    const checkout = { name: 'checkout', kind: 'process', status: 'ready', reason: 'mock scenario sold-out', endpoints: [{ kind: 'public', protocol: 'http', host: 'checkout.local.billing.localhost', port: 80, url: 'http://checkout.local.billing.localhost' }], restartCount: 0, recentRequests: 0 } as Service
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [checkout], connections: [], bindings: [{ service: 'checkout', provider: 'mock', mock: { scenario: 'sold-out' } }],
    } as Environment
    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions, environment, view: 'overview', onNavigate: () => undefined, onChanged: () => undefined }))

    expect(markup).toContain('<span class="service-list-endpoint"><span class="truncate muted" title="mock scenario sold-out">mock scenario sold-out</span></span>')
    expect(markup).not.toContain('aria-label="Copy checkout endpoint"')
    expect(markup).not.toContain('service-list-endpoint--copyable')
    expect(markup).toContain('aria-label="View checkout details"')
  })

  it('sorts overview services and renders controls when multiple rows can move', () => {
    const checkout: Service = { name: 'checkout', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, restartCount: 2, recentRequests: 10, p95Millis: 80, endpoints: [] }
    const orders: Service = { name: 'orders', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'stopped', reason: 'not running', generation: 1, restartCount: 0, recentRequests: 2, endpoints: [] }
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [orders, checkout], connections: [], bindings: [{ service: 'checkout', provider: 'local' }, { service: 'orders', provider: 'remote' }],
    } as Environment
    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions, environment, view: 'overview', onNavigate: () => undefined, onChanged: () => undefined }))

    expect(markup.match(/class="sortable-column-sort-control"/g)).toHaveLength(7)
    expect(markup).toContain('class="table-row table-row--header service-row sortable-header-row is-default-sort" role="row"')
    expect(markup).toContain('class="sortable-grid-header is-active" role="columnheader" aria-sort="ascending"><span>Name</span>')
    expect(sortOverviewServices(environment.services, environment, { key: 'name', direction: 'asc' }).map((item) => item.name)).toEqual(['checkout', 'orders'])
    expect(sortOverviewServices(environment.services, environment, { key: 'requests', direction: 'desc' }).map((item) => item.name)).toEqual(['checkout', 'orders'])
    expect(sortOverviewServices(environment.services, environment, { key: 'state', direction: 'asc' }).map((item) => item.name)).toEqual(['checkout', 'orders'])
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
      { service: 'orders', provider: 'mock', mock: { scenario: 'sold-out' } },
      { service: 'postgres', provider: 'container' },
    ] })).toEqual({ value: 'LOCAL', detail: '2 local · 1 mocked' })
  })

  it('shows mock as the mode for a service using a mock provider', () => {
    const inventory = { name: 'inventory', kind: 'process', launchMode: 'managed' } as Service
    const environment = {
      services: [inventory],
      bindings: [{ service: 'inventory', provider: 'mock', mock: { scenario: 'sold-out' } }],
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

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions,
      environment, project, view: 'bindings', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('class="experiment-layout bindings-layout"')
    expect(markup).toContain('<span>PROVIDERS</span><button')
    expect(markup).toContain('role="table" aria-label="Configured providers"')
    expect(markup).toContain('class="provider-row provider-row--header sortable-header-row is-default-sort" role="row"')
    expect(markup).toContain('class="sortable-grid-header is-active" role="columnheader" aria-sort="ascending"><span>Service</span>')
    for (const label of ['Provider', 'Configuration', 'Modified']) expect(markup).toContain(`role="columnheader" aria-sort="none"><span>${label}</span>`)
    expect(markup).toContain('<span role="columnheader" aria-label="Row actions"></span>')
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
    expect(markup).toContain('<tr class="sortable-header-row is-default-sort">')
    expect(markup).toContain('class="sortable-table-header is-active" aria-sort="ascending"><span>Source</span>')
    expect(markup).toContain('class="sortable-table-header" aria-sort="none"><span>Path</span>')
    expect(markup).toContain('class="sortable-table-header" aria-sort="none"><span>Created</span>')
    expect(markup).toContain('<th scope="col" aria-label="Row actions"></th>')
    expect(markup).not.toContain('sortable-column-sort-control')
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

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions,
      environment, view: 'bindings', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('aria-label="providers pagination"')
    expect(markup).toContain('aria-label="checkouts pagination"')
    expect(markup).toContain('1–5 of 6')
    expect(markup).toContain('service-5')
    expect(markup).not.toContain('service-6')
    expect(markup).toContain('/workspace/source-5')
    expect(markup).not.toContain('/workspace/source-6')
    expect(markup.match(/class="sortable-column-sort-control"/g)).toHaveLength(7)
  })

  it('sorts provider and checkout binding rows by their displayed values', () => {
    const providers: ComponentBinding[] = [
      { service: 'orders', provider: 'remote', remote: { url: 'https://orders.example.test', classification: 'qa', writePolicy: 'read-only' }, modifiedAt: '2026-08-18T13:00:00Z' },
      { service: 'checkout', provider: 'local', source: 'workspace', modifiedAt: '2026-08-18T12:00:00Z' },
    ]
    const checkoutRows: Parameters<typeof sortCheckoutRows>[0] = [
      { source: { name: 'beta' }, checkout: { name: 'beta', path: '/workspace/beta', status: 'ready', createdAt: '2026-08-18T13:00:00Z', scannedAt: '' }, usedBy: [], required: false },
      { source: { name: 'alpha' }, checkout: { name: 'alpha', path: '/workspace/zeta', status: 'ready', createdAt: '2026-08-18T12:00:00Z', scannedAt: '' }, usedBy: [], required: false },
    ]

    expect(sortProviderBindings(providers, { key: 'service', direction: 'asc' }).map((item) => item.service)).toEqual(['checkout', 'orders'])
    expect(sortProviderBindings(providers, { key: 'provider', direction: 'desc' }).map((item) => item.service)).toEqual(['orders', 'checkout'])
    expect(sortProviderBindings(providers, { key: 'modified', direction: 'desc' }).map((item) => item.service)).toEqual(['orders', 'checkout'])
    expect(sortCheckoutRows(checkoutRows, { key: 'source', direction: 'asc' }).map((item) => item.source.name)).toEqual(['alpha', 'beta'])
    expect(sortCheckoutRows(checkoutRows, { key: 'path', direction: 'asc' }).map((item) => item.source.name)).toEqual(['beta', 'alpha'])
    expect(sortCheckoutRows(checkoutRows, { key: 'created', direction: 'desc' }).map((item) => item.source.name)).toEqual(['beta', 'alpha'])
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

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, { activity, actions,
      environment, project, view: 'bindings', onNavigate: () => undefined, onChanged: () => undefined,
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
    expect(providerBindingMatches({ service: 'checkout', provider: 'mock', mock: { scenario: 'sold-out' } }, { service: 'checkout', provider: 'mock', mock: { scenario: 'SOLD-OUT' } })).toBe(true)
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

  it('summarizes topology service details, relationships, and active faults', () => {
    const checkout = {
      name: 'checkout', kind: 'process', framework: 'nestjs', launchMode: 'managed', status: 'ready', endpoints: [
        { kind: 'public', protocol: 'http', host: 'checkout.local.store.localhost', port: 80, url: 'http://checkout.local.store.localhost' },
      ],
    } as Service
    const environment = {
      project: 'store', name: 'local', primaryService: 'checkout', services: [checkout, service('inventory'), service('orders')],
      connections: [{ source: 'checkout', target: 'inventory', protocol: 'http' }, { source: 'checkout', target: 'orders', protocol: 'http' }],
      bindings: [{ service: 'checkout', provider: 'local' }],
    } as Environment
    const faults = [{ name: 'checkout-delay', source: 'checkout', target: 'orders', enabled: true }] as FaultRule[]

    expect(topologyServicePreviewDetails(environment, checkout, buildTopology(environment).edges, faults)).toEqual({
      type: 'nestjs',
      mode: 'managed',
      endpoint: 'http://checkout.local.store.localhost',
      mockScenario: undefined,
      reason: undefined,
      inbound: 1,
      outbound: 2,
      fault: 'checkout-delay on checkout → orders',
    })
  })

  it('keeps topology service previews within the visible canvas viewport', () => {
    expect(topologyServicePreviewPlacement(
      { left: 800, right: 964, top: 500 },
      { left: 238, right: 908, top: 306, bottom: 950 },
      { left: 300, top: 400 },
    )).toEqual({ left: 200, top: 72, onLeft: true })
    expect(topologyServicePreviewPlacement(
      { left: 350, right: 514, top: 400 },
      { left: 238, right: 908, top: 306, bottom: 950 },
      { left: 300, top: 400 },
    )).toEqual({ left: 228, top: -28, onLeft: false })
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
    expect(markup).toContain('<span>RECENT ACTIVITY</span>')
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
    const metricFor = (count: number, durationMs = 10, status = 200) => summarizeTopologyTraffic(Array.from({ length: count }, (_, index) => ({
      protocol: 'http', source: 'checkout', target: 'orders',
      startedAt: new Date(now-index*100-10).toISOString(),
      completedAt: new Date(now-index*100).toISOString(),
      durationMs, status, sequence: index+1, requestBytes: 10, responseBytes: 20,
    })) as TrafficExchange[], now).get(topologyEdgeKey('checkout', 'orders'))
    const firstRequest = metricFor(1)
    const heavyTraffic = metricFor(180)

    const inactive = topologyEdgeVisualState(undefined, now, false)
    const active = topologyEdgeVisualState(firstRequest, now, false)
    expect(inactive).toEqual({ strokeWidth: 1, markerID: 'topology-arrow-inactive' })
    expect(active.strokeWidth).toBeCloseTo(1.77)
    expect(active.markerID).toBe('topology-arrow-active')
    expect(topologyEdgeVisualState(heavyTraffic, now, false)).toBe(active)
    expect(topologyEdgeVisualState(undefined, now, true)).toEqual({ strokeWidth: 1.77, markerID: 'topology-arrow-warning' })
    expect(topologyEdgeVisualState(metricFor(1, 800), now, false).markerID).toBe('topology-arrow-warning')
    expect(topologyEdgeVisualState(metricFor(1, 10, 503), now, false).markerID).toBe('topology-arrow-error')
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

  it('renders a topology when a stopped environment omits its empty connections', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout'].map(service),
    } as Environment

    expect(buildTopology(environment).edges).toEqual([{ source: 'external', target: 'checkout', protocol: 'http' }])
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
