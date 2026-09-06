import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { MockRoute, MockScenario } from '../../api/contracts/mocks'
import { MockScenarioCreateDialog, MockScenariosList, MockScenarioWorkspace, MocksPanel, mockHTTPStatusGroups, mockRoutesFromDrafts, mockScenarioIsActive, sortMockRoutes, sortMockScenarios } from './MocksPanel'
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
  services: ['inventory', 'payments'],
  busy: '',
  deleteName: '',
  transitionBlocked: false,
  error: null,
  onDismissError: () => undefined,
  onBack: () => undefined,
  onToggle: () => undefined,
  onAddRoute: () => undefined,
  onSelectRoute: () => undefined,
  onSaveRoute: async () => true,
  onPreviewRoute: async () => ({ service: 'inventory', matched: false, status: 501 }),
  onToggleRoute: async () => true,
  onDeleteRoute: async () => true,
  onDismissDelete: () => undefined,
}

describe('MocksPanel', () => {
  it('starts with a compact environment-scoped scenario table', () => {
    const html = renderToStaticMarkup(<MocksPanel environment={environment} onSelectScenario={() => undefined} onSelectRoute={() => undefined} onCreateRoute={() => undefined} onChanged={() => undefined} />)
    expect(html).toContain('<span>SCENARIOS</span>')
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
    expect(html).toContain('<p id="mock-scenario-create-description">Create the scenario, then add one or more service routes.</p>')
    expect(html).not.toContain('It remains disabled until you enable it.')
    expect(html).toMatch(/<input[^>]*aria-label="NAME"[^>]*value=""/)
    expect(html).toMatch(/<input[^>]*aria-label="DESCRIPTION"[^>]*value=""/)
    expect(html).not.toContain('placeholder=')
    expect(html).not.toContain('aria-label="SERVICE"')
    expect(html).toContain('>CREATE SCENARIO</button>')
  })

  it('renders compact help and no service eyebrow for an empty route table', () => {
    const empty = { ...scenario, activation: { state: 'disabled' as const, targetServices: [], activeServices: [] }, routes: [] }
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={empty} />)
    expect(html).toContain('aria-label="checkout-failure mock scenario"')
    expect(html).toContain('aria-label="Back to mock scenarios from checkout-failure"')
    expect(html).toContain('<h2 title="Routes">Routes</h2>')
    expect(html).not.toContain('NO SERVICES YET')
    expect(html).not.toContain('mock-scenario-service-eyebrow')
    expect(html).toContain('<div class="empty-row">No routes. Add one to define a request and response for a service.</div>')
    expect(html).not.toContain('mock-scenario-empty')
    expect(html).not.toContain('ADD FIRST ROUTE')
    expect(html.match(/>ADD ROUTE<\/button>/g)).toHaveLength(1)
    expect(html).toMatch(/<button(?=[^>]*role="tab")(?=[^>]*disabled="")[^>]*>Preview<\/button>/)
    expect(html).toMatch(/<input(?=[^>]*role="switch")(?=[^>]*disabled="")(?=[^>]*aria-label="checkout-failure enabled")[^>]*>/)
  })

  it('renders a compact route list beside the first route configuration', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, routes: [route, { ...scenario.routes[1], method: 'POST', enabled: false, delayMs: 150 }] }} />)
    expect(html).toContain('class="mock-scenario-header__context"')
    expect(html).toContain('<span title="checkout-failure">checkout-failure</span>')
    expect(html).toContain('<h2 title="lookup">lookup</h2>')
    expect(html).toContain('class="mock-route-heading__endpoint" title="GET /inventory/{sku}"')
    expect(html).not.toContain('SERVICES /')
    expect(html).not.toContain(scenario.description)
    expect(html).not.toContain('class="empty-row"')
    expect(html).toContain('class="mock-scenario-split"')
    expect(html).toContain('role="tablist" aria-label="Mock workspace view"')
    expect(html).toMatch(/<button(?=[^>]*role="tab")(?=[^>]*aria-selected="true")(?=[^>]*tabindex="0")[^>]*>Routes<\/button>/)
    expect(html).toMatch(/<button(?=[^>]*role="tab")(?=[^>]*aria-selected="false")(?=[^>]*tabindex="-1")[^>]*>Preview<\/button>/)
    expect(html).toContain('aria-label="Mock request preview" hidden=""')
    expect(html).toContain('class="mock-route-browser"')
    expect(html).toContain('class="mock-route-item is-selected"')
    expect(html).toContain('aria-label="Edit lookup route"')
    expect(html).toContain('aria-label="Edit lookup route" aria-current="true"')
    expect(html).toContain('aria-label="Edit decline route"')
    expect(html).toContain('aria-label="Sort routes by"')
    for (const label of ['Service', 'Route', 'Match', 'Response', 'State']) expect(html).toContain(`>${label}</option>`)
    expect(html).not.toContain('<option value="delay">')
    expect(html).toContain('aria-label="Route actions for lookup"')
    expect(html).toContain('title="Service: inventory"')
    expect(html).toContain('class="mock-route-method">GET</span>')
    expect(html).toContain('class="mock-route-method">POST</span>')
    expect(html).toContain('title="Response: 503 Service Unavailable"')
    expect(html).toContain('title="Response delay: 150 ms"')
    expect(html).toContain('aria-label="lookup route enabled"')
    expect(html).toContain('aria-label="decline route enabled"')
    expect(html).toContain('class="mock-route-item is-off"')
    expect(html.match(/class="mock-route-disabled-state"/g)).toHaveLength(1)
    expect(html).toMatch(/aria-describedby="[^"]+-route-disabled-decline"/)
    expect(html).toMatch(/<span id="[^"]+-route-disabled-decline" class="mock-route-disabled-state">DISABLED<\/span>/)
    expect(html).toContain('<span class="sr-only">On</span>')
    expect(html).toContain('<span class="sr-only">Off</span>')
    expect(html).toContain('aria-label="Edit Route"')
    expect(html).toContain('value="/inventory/{sku}"')
    expect(html).toContain('role="tablist" aria-label="Mock route configuration"')
    expect(html).toContain('aria-label="Query parameters"')
    expect(html).not.toContain('aria-label="RESPONSE BODY"')
  })

  it('paginates routes after ten entries', () => {
    const routes = Array.from({ length: 11 }, (_, index) => ({ ...route, name: `route-${String(index + 1).padStart(2, '0')}`, path: `/route-${index + 1}` }))
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, activation: { ...scenario.activation, targetServices: ['inventory'] }, routes }} />)
    const tenRouteHTML = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, activation: { ...scenario.activation, targetServices: ['inventory'] }, routes: routes.slice(0, 10) }} />)
    expect(html.match(/class="mock-route-item(?: is-selected)?"/g)).toHaveLength(10)
    expect(html).toContain('route-10')
    expect(html).not.toContain('route-11')
    expect(html).toContain('aria-label="routes pagination"')
    expect(html).toContain('<span>1–10 of 11</span>')
    expect(tenRouteHTML).not.toContain('aria-label="routes pagination"')
  })

  it('selects a linked route while retaining the route list', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, routes: [route, { ...scenario.routes[1], enabled: false }] }} selectedRoute="decline" />)
    expect(html).toContain('aria-label="Edit decline route" aria-current="true"')
    expect(html).toContain('value="/payments"')
    expect(html).toContain('class="mock-route-browser"')
    expect(html).toContain('<h2 title="decline">decline</h2><span class="mock-route-disabled-state">DISABLED</span>')
  })

  it('creates routes in the right pane without replacing the saved route list', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={scenario} creatingRoute />)
    expect(html).toContain('aria-label="Create Route"')
    expect(html).toContain('<h2 title="get-route-3">get-route-3</h2>')
    expect(html).toContain('class="mock-route-new is-selected"')
    expect(html).toContain('aria-label="Edit lookup route"')
    expect(html).toContain('aria-label="Edit decline route"')
    expect(html).not.toContain('class="mock-route-item is-selected"')
    expect(html).toMatch(/<button(?=[^>]*role="tab")(?![^>]*disabled="")[^>]*>Preview<\/button>/)
  })

  it('keeps the route list available for a missing route selection', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={scenario} selectedRoute="missing" />)
    expect(html).toContain('ROUTE NOT FOUND')
    expect(html).toContain('<h2 title="missing">missing</h2>')
    expect(html).not.toContain('mock-route-heading__endpoint')
    expect(html).toContain('aria-label="Edit lookup route"')
  })

  it('keeps operation errors inside the editor above its scrolling form', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={scenario} error={{ title: 'Route was not saved', message: 'Check the request path.' }} />)
    expect(html.match(/role="alert"/g)).toHaveLength(1)
    expect(html.indexOf('class="mock-route-workspace"')).toBeLessThan(html.indexOf('role="alert"'))
    expect(html.indexOf('role="alert"')).toBeLessThan(html.indexOf('class="mock-route-form mock-route-configuration"'))
  })

  it('uses the shared red notice when a scenario is only partially active', () => {
    const html = renderToStaticMarkup(<MockScenarioWorkspace {...workspaceProps} scenario={{ ...scenario, activation: { state: 'degraded', targetServices: ['inventory', 'payments'], activeServices: ['inventory'] } }} />)
    expect(html).toContain('class="action-error action-error--persistent" role="alert"')
    expect(html).toContain('Scenario is partially active')
    expect(html).toContain('1 of 2 services currently use this scenario.')
    expect(html).not.toContain('class="alert')
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
    expect(() => mockRoutesFromDrafts([{ ...first, path: '/inventory/{id}/{id}' }])).toThrow(/duplicated/)
    expect(() => mockRoutesFromDrafts([{ ...first, path: '/inventory/item-{id}' }])).toThrow(/whole segment/)
  })

  it('exposes only registered final statuses', () => {
    const codes = mockHTTPStatusGroups.flatMap((group) => group.statuses.map(([code]) => code))
    expect(codes).toContain(200)
    expect(codes).toContain(503)
    expect(codes).not.toContain(103)
    expect(codes).not.toContain(599)
  })

  it('converts response header rows through the same validation for saved and previewed routes', () => {
    const draft = { ...newMockRouteDraft(1, 'inventory'), headers: [{ id: 1, name: 'Location', value: 'https://example.test:8443/items:a' }, { id: 2, name: 'X-Empty', value: '' }, { id: 3, name: '', value: '' }] }
    expect(mockRoutesFromDrafts([draft])[0].headers).toEqual({ Location: 'https://example.test:8443/items:a', 'X-Empty': '' })
    expect(() => mockRoutesFromDrafts([{ ...draft, headers: [{ id: 1, name: 'X-Mode', value: 'one' }, { id: 2, name: 'x-mode', value: 'two' }] }])).toThrow(/duplicated/)
    expect(() => mockRoutesFromDrafts([{ ...draft, headers: [{ id: 1, name: 'Content-Length', value: '100' }] }])).toThrow(/managed by the HTTP transport/)
  })

  it('converts required query rows without losing literal or case-sensitive values', () => {
    const draft = { ...newMockRouteDraft(1, 'inventory'), query: [{ id: 1, name: 'warehouse', value: 'central' }, { id: 2, name: 'Warehouse', value: 'east' }, { id: 3, name: 'filter', value: 'a=b&c:d?' }, { id: 4, name: 'optional', value: '' }] }
    expect(mockRoutesFromDrafts([draft])[0].query).toEqual({ warehouse: { match: 'equals', value: 'central' }, Warehouse: { match: 'equals', value: 'east' }, filter: { match: 'equals', value: 'a=b&c:d?' }, optional: { match: 'exists' } })
    expect(() => mockRoutesFromDrafts([{ ...draft, query: [...draft.query, { id: 5, name: 'warehouse', value: 'west' }] }])).toThrow(/duplicated/)
    expect(() => mockRoutesFromDrafts([{ ...draft, query: [{ id: 1, name: '', value: 'central' }] }])).toThrow(/name is required/)
  })

  it('serializes the complete request and response draft for saving or previewing from either tab', () => {
    const draft = {
      ...newMockRouteDraft(1, 'payments'),
      name: 'create-order',
      nameCustomized: true,
      method: 'POST',
      path: '/orders/{id}',
      pathMatch: 'template' as const,
      query: [{ id: 1, name: 'region', value: 'east' }],
      status: 202,
      delayMs: 150,
      body: '{"reserved":true}',
      headers: [{ id: 1, name: 'Content-Type', value: 'application/json' }, { id: 2, name: 'X-Mode', value: 'pending' }],
      enabled: false,
    }
    expect(mockRoutesFromDrafts([draft])).toEqual([{
      name: 'create-order', service: 'payments', method: 'POST', path: '/orders/{id}', query: { region: { match: 'equals', value: 'east' } },
      status: 202, delayMs: 150, body: '{"reserved":true}', headers: { 'Content-Type': 'application/json', 'X-Mode': 'pending' }, enabled: false,
    }])
  })

  it('orders query operators by specificity and rejects equal-rank regex routes', () => {
    const base = newMockRouteDraft(1, 'inventory')
    const exact = { ...base, name: 'exact', query: [{ id: 1, name: 'sku', match: 'equals' as const, value: 'coffee-mug' }] }
    const regex = { ...base, name: 'pattern', query: [{ id: 1, name: 'sku', match: 'regex' as const, value: 'coffee-.*' }] }
    const exists = { ...base, name: 'present', query: [{ id: 1, name: 'sku', match: 'exists' as const, value: '' }] }
    expect(mockRoutesFromDrafts([exact, regex, exists])).toHaveLength(3)
    expect(() => mockRoutesFromDrafts([regex, { ...regex, name: 'other', query: [{ id: 1, name: 'sku', match: 'regex', value: 'tea-.*' }] }])).toThrow(/ambiguous/)
  })

  it('validates fields belonging to both tabs before saving or previewing', () => {
    const draft = newMockRouteDraft(1, 'inventory')
    expect(() => mockRoutesFromDrafts([{ ...draft, name: 'invalid name' }])).toThrow(/lowercase URL-safe name/)
    expect(() => mockRoutesFromDrafts([{ ...draft, delayMs: -1 }])).toThrow(/delay must be between/)
    expect(() => mockRoutesFromDrafts([{ ...draft, status: 103 }])).toThrow(/registered final HTTP response status/)
    expect(() => mockRoutesFromDrafts([{ ...draft, headers: [{ id: 1, name: '', value: 'unfinished' }] }])).toThrow(/header name is required/)
    expect(() => mockRoutesFromDrafts([{ ...draft, query: [{ id: 1, name: '', value: 'unfinished' }] }])).toThrow(/parameter name is required/)
    expect(() => mockRoutesFromDrafts([{ ...draft, path: '/inventory/{sku}' }])).toThrow(/Choose Template/)
    expect(() => mockRoutesFromDrafts([{ ...draft, pathMatch: 'template' }])).toThrow(/choose Exact/)
  })
})
