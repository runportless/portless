import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { MockRoute, MockScenario } from '../../api/contracts/mocks'
import { MockScenarioCreateDialog, MockScenariosList, MockScenarioWorkspace, MocksPanel, mockHTTPStatusGroups, mockRoutesFromDrafts, mockScenarioIsActive, parseMockPairs, parseMockResponseHeaderPairs, sortMockRoutes, sortMockScenarios } from './MocksPanel'
import { newMockRouteDraft } from './MockRouteEditor'

const environment: Environment = {
  project: 'store', name: 'local', revision: 1, status: 'healthy', createdAt: '', updatedAt: '',
  services: [
    { name: 'inventory', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 },
    { name: 'payments', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 },
  ],
  connections: [], bindings: [{ service: 'inventory', provider: 'local', source: 'store' }, { service: 'payments', provider: 'local', source: 'store' }],
}

const route: MockRoute = { name: 'lookup', service: 'inventory', method: 'GET', path: '/inventory/{sku}', status: 200, body: '{"available":false}', delayMs: 0, enabled: true }
const scenario: MockScenario = {
  project: 'store', environment: 'local', name: 'checkout-failure', description: 'Inventory empty and payments down', createdAt: '2026-08-18T12:00:00Z', modifiedAt: '2026-08-18T13:15:00Z',
  activation: { state: 'disabled', targetServices: ['inventory', 'payments'], activeServices: [] },
  routes: [route, { ...route, name: 'decline', service: 'payments', path: '/payments', status: 503 }],
}

const listProps = {
  loading: false,
  busy: '',
  deleteName: '',
  transitionBlocked: false,
  onCreate: () => undefined,
  onOpen: () => undefined,
  onToggle: () => undefined,
  onDelete: () => undefined,
  onDismissDelete: () => undefined,
}

const workspaceProps = {
  environment,
  busy: '',
  deleteName: '',
  transitionBlocked: false,
  error: null,
  onDismissError: () => undefined,
  onBack: () => undefined,
  onToggle: () => undefined,
  onAddRoute: () => undefined,
  onEditRoute: () => undefined,
  onToggleRoute: () => undefined,
  onDeleteRoute: () => undefined,
  onDismissDelete: () => undefined,
}

describe('MocksPanel', () => {
  it('starts with a compact environment-scoped scenario table', () => {
    const html = renderToStaticMarkup(<MocksPanel environment={environment} onSelectScenario={() => undefined} onChanged={() => undefined} />)
    expect(html).toContain('MOCK SCENARIOS')
    expect(html).toContain('CREATE SCENARIO')
    expect(html).toContain('class="sortable-grid-header is-active" role="columnheader" aria-sort="ascending"><span>State</span>')
    for (const label of ['Scenario', 'Services', 'Routes', 'Modified']) expect(html).toContain(`<span>${label}</span>`)
    expect(html.match(/class="sortable-grid-header/g)).toHaveLength(5)
    expect(html).toContain('Loading mock scenarios')
    expect(html).not.toContain('mock-scenario-workspace')
  })

  it('creates only scenario identity in the modal', () => {
    const html = renderToStaticMarkup(<MockScenarioCreateDialog busy={false} error={null} onDismissError={() => undefined} onClose={() => undefined} onCreate={async () => undefined} />)
    expect(html).toContain('<h2 id="mock-scenario-create-title">Create Mock Scenario</h2>')
    expect(html).toContain('aria-label="NAME"')
    expect(html).toContain('aria-label="DESCRIPTION"')
    expect(html).not.toContain('aria-label="SERVICE"')
    expect(html).toContain('>CREATE SCENARIO</button>')
  })

  it('summarizes multi-service coverage above an empty route workspace', () => {
    const empty = { ...scenario, activation: { state: 'disabled' as const, targetServices: [], activeServices: [] }, routes: [] }
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={empty} />)
    expect(html).toContain('aria-label="checkout-failure mock scenario"')
    expect(html).toContain('aria-label="Back to mock scenarios"')
    expect(html).toContain('NO SERVICES YET')
    expect(html).toContain('NO ROUTES YET')
    expect(html).toContain('ADD FIRST ROUTE')
    expect(html).toMatch(/<input(?=[^>]*role="switch")(?=[^>]*disabled="")(?=[^>]*aria-label="checkout-failure enabled")[^>]*>/)
  })

  it('renders service-owned routes with sortable columns and row navigation', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={scenario} />)
    expect(html).toContain('SERVICES / inventory · payments')
    expect(html).toContain('class="mock-route-row mock-route-row--interactive"')
    expect(html).toContain('inventory</strong><strong>lookup</strong><code>GET /inventory/{sku}</code>')
    expect(html).toContain('payments</strong><strong>decline</strong>')
    expect(html).toContain('class="sortable-grid-header is-active" role="columnheader" aria-sort="ascending"><span>Service</span>')
    for (const label of ['Route', 'Match', 'Response', 'Delay', 'State']) expect(html).toContain(`<span>${label}</span>`)
    expect(html.match(/class="sortable-grid-header/g)).toHaveLength(6)
    expect(html).toContain('aria-label="Route actions for lookup"')
  })

  it('paginates routes after ten entries', () => {
    const routes = Array.from({ length: 11 }, (_, index) => ({ ...route, name: `route-${String(index + 1).padStart(2, '0')}`, path: `/route-${index + 1}` }))
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, activation: { ...scenario.activation, targetServices: ['inventory'] }, routes }} />)
    const tenRouteHTML = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, activation: { ...scenario.activation, targetServices: ['inventory'] }, routes: routes.slice(0, 10) }} />)
    expect(html.match(/class="mock-route-row mock-route-row--interactive"/g)).toHaveLength(10)
    expect(html).toContain('route-10')
    expect(html).not.toContain('route-11')
    expect(html).toContain('aria-label="routes pagination"')
    expect(html).toContain('<span>1–10 of 11</span>')
    expect(tenRouteHTML).not.toContain('aria-label="routes pagination"')
  })

  it('derives activation controls from scenario state rather than one service binding', () => {
    const enabled = { ...scenario, activation: { state: 'enabled' as const, targetServices: ['inventory', 'payments'], activeServices: ['inventory', 'payments'], enabledAt: '2026-08-18T14:30:00Z' } }
    const degraded = { ...scenario, name: 'partial', activation: { state: 'degraded' as const, targetServices: ['inventory', 'payments'], activeServices: ['inventory'] } }
    const html = renderToStaticMarkup(<MockScenariosList {...listProps} scenarios={[scenario, enabled, degraded]} />)
    expect(mockScenarioIsActive(scenario)).toBe(false)
    expect(mockScenarioIsActive(enabled)).toBe(true)
    expect(mockScenarioIsActive(degraded)).toBe(true)
    expect(html).toContain('aria-label="Disable checkout-failure"')
    expect(html).toContain('aria-label="Disable partial"')
    expect(html).toContain('Degraded')
  })

  it('sorts scenarios by every displayed data column', () => {
    const scenarios: MockScenario[] = [
      { ...scenario, name: 'bravo', activation: { state: 'disabled', targetServices: ['zeta'], activeServices: [] }, routes: [route, route, route], modifiedAt: '2026-08-18T11:00:00Z' },
      { ...scenario, name: 'alpha', activation: { state: 'enabled', targetServices: ['orders'], activeServices: ['orders'] }, routes: [route], modifiedAt: '2026-08-18T14:00:00Z' },
      { ...scenario, name: 'charlie', activation: { state: 'degraded', targetServices: ['billing'], activeServices: [] }, routes: [route, route], modifiedAt: '2026-08-18T13:00:00Z' },
    ]
    const names = (key: Parameters<typeof sortMockScenarios>[1]['key'], direction: 'asc' | 'desc') => sortMockScenarios(scenarios, { key, direction }).map((item) => item.name)
    expect(names('state', 'asc')).toEqual(['alpha', 'charlie', 'bravo'])
    expect(names('name', 'desc')).toEqual(['charlie', 'bravo', 'alpha'])
    expect(names('services', 'asc')).toEqual(['charlie', 'alpha', 'bravo'])
    expect(names('routes', 'asc')).toEqual(['alpha', 'charlie', 'bravo'])
    expect(names('modifiedAt', 'desc')).toEqual(['alpha', 'charlie', 'bravo'])
  })

  it('sorts routes by service and every remaining data column', () => {
    const routes: MockRoute[] = [
      { ...route, name: 'bravo', service: 'zeta', method: 'POST', path: '/alpha', status: 404, body: 'unavailable', delayMs: 10, enabled: false },
      { ...route, name: 'alpha', service: 'orders', method: 'GET', path: '/zeta', status: 200, body: '', delayMs: 0, enabled: true },
      { ...route, name: 'charlie', service: 'billing', method: 'DELETE', path: '/middle', status: 201, body: 'ok', delayMs: 50, enabled: false },
    ]
    const names = (key: Parameters<typeof sortMockRoutes>[1]['key'], direction: 'asc' | 'desc') => sortMockRoutes(routes, { key, direction }).map((item) => item.name)
    expect(names('service', 'asc')).toEqual(['charlie', 'alpha', 'bravo'])
    expect(names('route', 'desc')).toEqual(['charlie', 'bravo', 'alpha'])
    expect(names('match', 'asc')).toEqual(['charlie', 'alpha', 'bravo'])
    expect(names('response', 'asc')).toEqual(['alpha', 'charlie', 'bravo'])
    expect(names('delay', 'desc')).toEqual(['charlie', 'bravo', 'alpha'])
    expect(names('state', 'asc')).toEqual(['alpha', 'bravo', 'charlie'])
  })

  it('validates route service ownership and ambiguity within each service', () => {
    const first = { ...newMockRouteDraft(1, 'inventory'), path: '/health' }
    const sameService = { ...newMockRouteDraft(2, 'inventory'), name: 'inventory-health-two', path: '/health' }
    const otherService = { ...sameService, name: 'payments-health', service: 'payments' }
    expect(mockRoutesFromDrafts([first])).toEqual([expect.objectContaining({ name: 'get-root', service: 'inventory', path: '/health' })])
    expect(() => mockRoutesFromDrafts([{ ...first, service: '' }])).toThrow(/valid service/)
    expect(() => mockRoutesFromDrafts([first, sameService])).toThrow(/ambiguous for inventory/)
    expect(mockRoutesFromDrafts([first, otherService])).toHaveLength(2)
  })

  it('parses route fields and exposes only registered final statuses', () => {
    expect(parseMockPairs('warehouse=central\ninclude=stock+price', '=')).toEqual({ warehouse: 'central', include: 'stock+price' })
    expect(parseMockResponseHeaderPairs('Content-Type: application/json\nX-Mode: sold-out')).toEqual({ 'Content-Type': 'application/json', 'X-Mode': 'sold-out' })
    expect(() => parseMockResponseHeaderPairs('X-Mode: one\nx-mode: two')).toThrow(/duplicated/)
    const codes = mockHTTPStatusGroups.flatMap((group) => group.statuses.map(([code]) => code))
    expect(codes).toContain(200)
    expect(codes).toContain(503)
    expect(codes).not.toContain(103)
    expect(codes).not.toContain(599)
  })
})
