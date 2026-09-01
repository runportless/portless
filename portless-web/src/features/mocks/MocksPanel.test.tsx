import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { MockProfile } from '../../api/contracts/mocks'
import { createConfiguredMockProfile, MockProfileDrawer, MockProfilesList, MocksPanel, mockCreationRoutes, mockHTTPStatusGroups, mockProfileIsActive, mockRequestSupportsBody, parseMockHeaderPairs, parseMockPairs, parseMockResponseHeaderPairs, sortMockProfiles } from './MocksPanel'
import { newMockRouteDraft } from './MockCreationWorkspace'

const environment: Environment = {
  project: 'store', name: 'local', revision: 1, status: 'healthy', createdAt: '', updatedAt: '',
  services: [{ name: 'inventory', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 }],
  connections: [], bindings: [{ service: 'inventory', provider: 'local', source: 'store' }],
}

const profile: MockProfile = {
  project: 'store', environment: 'local', name: 'sold-out', service: 'inventory', description: 'Inventory has no available stock', createdAt: '2026-08-18T12:00:00Z', modifiedAt: '2026-08-18T13:15:00Z',
  routes: [{ name: 'lookup', method: 'GET', path: '/inventory/{sku}', status: 200, body: '{"available":false}', delayMs: 0, enabled: true }],
}

describe('MocksPanel', () => {
  it('starts with a clear environment-scoped profile workspace', () => {
    const html = renderToStaticMarkup(<MocksPanel environment={environment} onSelectProfile={() => undefined} onChanged={() => undefined} />)
    expect(html).toContain('MOCK PROFILES')
    expect(html).toContain('CREATE MOCK')
    expect(html).toContain('class="mock-profile-row mock-profile-row--header sortable-header-row is-default-sort" role="row"')
    expect(html).toContain('class="sortable-grid-header is-active" role="columnheader" aria-sort="ascending"><span>State</span>')
    for (const label of ['Name', 'Service', 'Routes', 'Created at', 'Enabled at', 'Modified at']) {
      expect(html).toContain(`class="sortable-grid-header" role="columnheader" aria-sort="none"><span>${label}</span>`)
    }
    expect(html.match(/class="sortable-grid-header/g)).toHaveLength(7)
    expect(html).not.toContain('sortable-column-sort-control')
    expect(html).not.toContain('Sort Actions')
    expect(html).toContain('Loading mock profiles')
  })

  it('renders profile routes in the standard maximizable drawer', () => {
    const html = renderToStaticMarkup(<MockProfileDrawer environment={environment} profile={profile} active busy="" deleteName="" error={null} onDismissError={() => undefined} onClose={() => undefined} onPreview={() => undefined} onAddRoute={() => undefined} onEditRoute={() => undefined} onDeleteRoute={() => undefined} onDismissDelete={() => undefined} />)
    expect(html).toContain('class="drawer mock-profile-drawer"')
    expect(html).toContain('role="dialog" aria-modal="true" aria-label="sold-out mock profile"')
    expect(html).toContain('aria-label="Full screen sold-out mock profile"')
    expect(html).toContain('aria-label="Close mock profile"')
    expect(html).toContain('PREVIEW REQUEST')
    expect(html).toContain('ADD ROUTE')
    expect(html).toContain('class="mock-row-actions table-row-actions"')
    expect(html).toContain('>EDIT</button>')
    expect(html).toContain('aria-label="Route actions for lookup"')
    expect(html).not.toContain('>DELETE</button>')
    expect(html).not.toContain('<span>Actions</span>')
    expect(html).toContain('GET /inventory/{sku}')
    expect(html).toContain('Inventory has no available stock')
  })

  it('adds a disable-all subheader and enable action for available profiles', () => {
    const html = renderToStaticMarkup(<MockProfilesList
      environment={environment}
      profiles={[profile]}
      loading={false}
      busy=""
      deleteName=""
      transitionBlocked={false}
      onCreate={() => undefined}
      onOpen={() => undefined}
      onToggle={() => undefined}
      onDelete={() => undefined}
      onDismissDelete={() => undefined}
      onDisableAll={() => undefined}
    />)

    expect(html).toContain('class="mock-profiles-bulk-actions"')
    expect(html).toMatch(/class="mock-profiles-disable-all-link"[^>]*disabled=""[^>]*>DISABLE ALL<\/button>/)
    expect(html.indexOf('CREATE MOCK')).toBeLessThan(html.indexOf('DISABLE ALL'))
    expect(html).toContain('aria-label="Enable sold-out"')
    expect(html).toContain('>ENABLE</button>')
    expect(html).toContain('aria-label="Open sold-out mock profile"')
    expect(html).toContain('aria-label="Mock profile actions for sold-out"')
    expect(html).not.toContain('>OPEN</button>')
    expect(html).not.toContain('>DELETE</button>')
    expect(html).toContain('class="mock-enabled-state"')
    expect(html).toContain('class="status status--muted" title="disabled"')
    expect(html).toContain('<span>Disabled</span>')
    expect(html).toContain('class="mock-profile-row__timestamp mock-profile-row__created" dateTime="2026-08-18T12:00:00Z"')
    expect(html).toContain('class="mock-profile-row__timestamp mock-profile-row__enabled">—</span>')
    expect(html).toContain('class="mock-profile-row__timestamp mock-profile-row__modified" dateTime="2026-08-18T13:15:00Z"')
    expect(html).not.toContain('>available</span>')
  })

  it('offers disable actions for the active profile and enables the bulk control', () => {
    const activeEnvironment: Environment = {
      ...environment,
      bindings: [{ service: 'inventory', provider: 'mock', mock: { profile: 'sold-out' }, modifiedAt: '2026-08-18T14:30:00Z' }],
    }
    const html = renderToStaticMarkup(<MockProfilesList
      environment={activeEnvironment}
      profiles={[profile]}
      loading={false}
      busy=""
      deleteName=""
      transitionBlocked={false}
      onCreate={() => undefined}
      onOpen={() => undefined}
      onToggle={() => undefined}
      onDelete={() => undefined}
      onDismissDelete={() => undefined}
      onDisableAll={() => undefined}
    />)

    expect(mockProfileIsActive(activeEnvironment, profile)).toBe(true)
    expect(html).toMatch(/class="mock-profiles-disable-all-link"(?![^>]*disabled)[^>]*>DISABLE ALL<\/button>/)
    expect(html).toContain('aria-label="Disable sold-out"')
    expect(html).toContain('>DISABLE</button>')
    expect(html).toContain('class="mock-enabled-state is-enabled"')
    expect(html).toContain('class="status status--success" title="enabled"')
    expect(html).toContain('<span>Enabled</span>')
    expect(html).toContain('class="mock-profile-row__timestamp mock-profile-row__enabled" dateTime="2026-08-18T14:30:00Z"')
    expect(html).not.toContain('>bound</span>')
  })

  it('saves every drafted route before optional activation', async () => {
    const created = { mock: { ...profile, routes: [] }, warnings: [] }
    const routes = [
      { ...profile.routes[0], name: 'lookup' },
      { ...profile.routes[0], name: 'health', path: '/health' },
    ]
    const events: string[] = []
    let saved: MockProfile = created.mock

    const result = await createConfiguredMockProfile(
      async () => { events.push('create'); return created },
      routes,
      async (route) => { events.push(`save:${route.name}`); saved = { ...saved, routes: [...saved.routes, route] }; return saved },
      async (item) => { events.push(`activate:${item.routes.length}`) },
    )

    expect(result.state).toBe('activated')
    expect(events).toEqual(['create', 'save:lookup', 'save:health', 'activate:2'])

    events.length = 0
    const inactive = await createConfiguredMockProfile(async () => created, routes.slice(0, 1), async (route) => ({ ...created.mock, routes: [route] }))
    expect(inactive.state).toBe('inactive')
    expect(events).toEqual([])

    const configurationFailure = new Error('route rejected')
    let activationAttempted = false
    const partial = await createConfiguredMockProfile(
      async () => created,
      routes,
      async (route) => {
        if (route.name === 'health') throw configurationFailure
        return { ...created.mock, routes: [route] }
      },
      async () => { activationAttempted = true },
    )
    expect(partial).toMatchObject({ state: 'configuration-failed', failedRoute: { name: 'health' }, configurationFailure })
    expect(activationAttempted).toBe(false)
  })

  it('sorts profiles by every data column in either direction', () => {
    const profiles: MockProfile[] = [
      { ...profile, name: 'bravo', service: 'zeta', routes: [profile.routes[0], profile.routes[0], profile.routes[0]], createdAt: '2026-08-18T14:00:00Z', modifiedAt: '2026-08-18T11:00:00Z' },
      { ...profile, name: 'alpha', service: 'orders', routes: [profile.routes[0]], createdAt: '2026-08-18T12:00:00Z', modifiedAt: '2026-08-18T14:00:00Z' },
      { ...profile, name: 'charlie', service: 'billing', routes: [profile.routes[0], profile.routes[0]], createdAt: '2026-08-18T13:00:00Z', modifiedAt: '2026-08-18T13:00:00Z' },
    ]
    const sortEnvironment: Environment = {
      ...environment,
      bindings: [
        { service: 'orders', provider: 'mock', mock: { profile: 'alpha' }, modifiedAt: '2026-08-18T12:00:00Z' },
        { service: 'billing', provider: 'mock', mock: { profile: 'charlie' }, modifiedAt: '2026-08-18T13:00:00Z' },
      ],
    }
    const names = (key: Parameters<typeof sortMockProfiles>[2]['key'], direction: 'asc' | 'desc') => sortMockProfiles(profiles, sortEnvironment, { key, direction }).map((item) => item.name)

    expect(names('state', 'asc')).toEqual(['alpha', 'charlie', 'bravo'])
    expect(names('state', 'desc')).toEqual(['bravo', 'alpha', 'charlie'])
    expect(names('name', 'asc')).toEqual(['alpha', 'bravo', 'charlie'])
    expect(names('name', 'desc')).toEqual(['charlie', 'bravo', 'alpha'])
    expect(names('service', 'asc')).toEqual(['charlie', 'alpha', 'bravo'])
    expect(names('service', 'desc')).toEqual(['bravo', 'alpha', 'charlie'])
    expect(names('routes', 'asc')).toEqual(['alpha', 'charlie', 'bravo'])
    expect(names('routes', 'desc')).toEqual(['bravo', 'charlie', 'alpha'])
    expect(names('createdAt', 'asc')).toEqual(['alpha', 'charlie', 'bravo'])
    expect(names('createdAt', 'desc')).toEqual(['bravo', 'charlie', 'alpha'])
    expect(names('enabledAt', 'asc')).toEqual(['bravo', 'alpha', 'charlie'])
    expect(names('enabledAt', 'desc')).toEqual(['charlie', 'alpha', 'bravo'])
    expect(names('modifiedAt', 'asc')).toEqual(['bravo', 'charlie', 'alpha'])
    expect(names('modifiedAt', 'desc')).toEqual(['alpha', 'charlie', 'bravo'])
  })

  it('parses multiline query and header fields without changing values', () => {
    expect(parseMockPairs('warehouse=central\ninclude=stock+price', '=')).toEqual({ warehouse: 'central', include: 'stock+price' })
    expect(parseMockPairs('Content-Type: application/json\nX-Mode: sold-out', ':')).toEqual({ 'Content-Type': 'application/json', 'X-Mode': 'sold-out' })
    expect(parseMockHeaderPairs('Content-Type: application/json\nX-Trace: one\nx-trace: two')).toEqual({ 'Content-Type': ['application/json'], 'X-Trace': ['one', 'two'] })
    expect(parseMockResponseHeaderPairs('Content-Type: application/json\nX-Mode: sold-out')).toEqual({ 'Content-Type': 'application/json', 'X-Mode': 'sold-out' })
    expect(() => parseMockPairs('invalid', '=')).toThrow(/name=value/)
    expect(() => parseMockHeaderPairs('Bad Header: value')).toThrow(/valid HTTP header name/)
    expect(() => parseMockResponseHeaderPairs('X-Mode: one\nx-mode: two')).toThrow(/duplicated/)
  })

  it('validates the complete route draft before creating a profile', () => {
    const first = { ...newMockRouteDraft(1), path: '/inventory/{sku}', queryText: 'warehouse=central' }
    const second = { ...newMockRouteDraft(2), name: 'get-inventory-by-id', path: '/inventory/{id}', queryText: 'warehouse=central' }
    expect(mockCreationRoutes([first])).toEqual([expect.objectContaining({ name: 'get-root', path: '/inventory/{sku}', query: { warehouse: 'central' }, headers: { 'Content-Type': 'application/json' } })])
    expect(() => mockCreationRoutes([{ ...first, name: 'Bad Route' }])).toThrow(/lowercase URL-safe/)
    expect(() => mockCreationRoutes([first, second])).toThrow(/ambiguous/)
  })

  it('offers request bodies only for methods that normally carry one', () => {
    for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) expect(mockRequestSupportsBody(method)).toBe(true)
    for (const method of ['GET', 'HEAD', 'OPTIONS']) expect(mockRequestSupportsBody(method)).toBe(false)
  })

  it('offers only registered final HTTP response statuses', () => {
    const statuses = mockHTTPStatusGroups.flatMap((group) => group.statuses.map(([code, text]) => ({ code, text })))
    const codes: readonly number[] = statuses.map(({ code }) => code)
    expect(statuses).toContainEqual({ code: 200, text: 'OK' })
    expect(statuses).toContainEqual({ code: 404, text: 'Not Found' })
    expect(statuses).toContainEqual({ code: 503, text: 'Service Unavailable' })
    expect(codes).not.toContain(103)
    expect(codes).not.toContain(299)
    expect(codes).not.toContain(599)
  })

})
