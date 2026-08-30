import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { MockProfile } from '../../api/contracts/mocks'
import { createAndEnableMockProfile, CreateProfileModal, MockProfileDrawer, MockProfilesList, MocksPanel, mockHTTPStatusGroups, mockProfileIsActive, mockRequestSupportsBody, parseMockHeaderPairs, parseMockPairs, RouteModal } from './MocksPanel'

const environment: Environment = {
  project: 'store', name: 'local', revision: 1, status: 'healthy', createdAt: '', updatedAt: '',
  services: [{ name: 'inventory', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 }],
  connections: [], bindings: [{ service: 'inventory', provider: 'local', source: 'store' }],
}

const profile: MockProfile = {
  project: 'store', environment: 'local', name: 'sold-out', service: 'inventory', description: 'Inventory has no available stock', createdAt: '2026-08-18T12:00:00Z', modifiedAt: '2026-08-18T12:00:00Z',
  routes: [{ name: 'lookup', method: 'GET', path: '/inventory/{sku}', status: 200, body: '{"available":false}', delayMs: 0, enabled: true }],
}

describe('MocksPanel', () => {
  it('starts with a clear environment-scoped profile workspace', () => {
    const html = renderToStaticMarkup(<MocksPanel environment={environment} onSelectProfile={() => undefined} onChanged={() => undefined} />)
    expect(html).toContain('MOCK PROFILES')
    expect(html).toContain('CREATE PROFILE')
    expect(html).toContain('<span>Name</span><span>Service</span>')
    expect(html).toContain('Loading mock profiles')
  })

  it('renders profile routes in the standard maximizable drawer', () => {
    const html = renderToStaticMarkup(<MockProfileDrawer environment={environment} profile={profile} active busy="" deleteName="" error={null} onDismissError={() => undefined} onClose={() => undefined} onPreview={() => undefined} onAddRoute={() => undefined} onEditRoute={() => undefined} onDeleteRoute={() => undefined} />)
    expect(html).toContain('class="drawer mock-profile-drawer"')
    expect(html).toContain('role="dialog" aria-modal="true" aria-label="sold-out mock profile"')
    expect(html).toContain('aria-label="Full screen sold-out mock profile"')
    expect(html).toContain('aria-label="Close mock profile"')
    expect(html).toContain('PREVIEW REQUEST')
    expect(html).toContain('ADD ROUTE')
    expect(html).toContain('class="mock-row-actions table-row-actions"')
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
      onDisableAll={() => undefined}
    />)

    expect(html).toContain('class="mock-profiles-bulk-actions"')
    expect(html).toMatch(/class="mock-profiles-disable-all-link"[^>]*disabled=""[^>]*>DISABLE ALL<\/button>/)
    expect(html.indexOf('CREATE PROFILE')).toBeLessThan(html.indexOf('DISABLE ALL'))
    expect(html).toContain('aria-label="Enable sold-out"')
    expect(html).toContain('>ENABLE</button>')
  })

  it('offers disable actions for the active profile and enables the bulk control', () => {
    const activeEnvironment: Environment = {
      ...environment,
      bindings: [{ service: 'inventory', provider: 'mock', mock: { profile: 'sold-out' } }],
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
      onDisableAll={() => undefined}
    />)

    expect(mockProfileIsActive(activeEnvironment, profile)).toBe(true)
    expect(html).toMatch(/class="mock-profiles-disable-all-link"(?![^>]*disabled)[^>]*>DISABLE ALL<\/button>/)
    expect(html).toContain('aria-label="Disable sold-out"')
    expect(html).toContain('>DISABLE</button>')
    expect(html).toContain('>bound</span>')
  })

  it('enables a newly created profile by default and preserves it when activation fails', async () => {
    const created = { mock: profile, warnings: [] }
    let enabledProfile = ''

    await expect(createAndEnableMockProfile(
      async () => created,
      async (item) => { enabledProfile = item.name },
    )).resolves.toEqual({ created, activated: true })
    expect(enabledProfile).toBe('sold-out')

    const activationFailure = new Error('provider handoff failed')
    await expect(createAndEnableMockProfile(
      async () => created,
      async () => { throw activationFailure },
    )).resolves.toEqual({ created, activated: false, activationFailure })
  })

  it('parses multiline query and header fields without changing values', () => {
    expect(parseMockPairs('warehouse=central\ninclude=stock+price', '=')).toEqual({ warehouse: 'central', include: 'stock+price' })
    expect(parseMockPairs('Content-Type: application/json\nX-Mode: sold-out', ':')).toEqual({ 'Content-Type': 'application/json', 'X-Mode': 'sold-out' })
    expect(parseMockHeaderPairs('Content-Type: application/json\nX-Trace: one\nx-trace: two')).toEqual({ 'Content-Type': ['application/json'], 'X-Trace': ['one', 'two'] })
    expect(() => parseMockPairs('invalid', '=')).toThrow(/name=value/)
    expect(() => parseMockHeaderPairs('Bad Header: value')).toThrow(/valid HTTP header name/)
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

  it('keeps password managers away from the mock route name field', () => {
    const html = renderToStaticMarkup(<RouteModal
      draft={{ name: '', method: 'GET', path: '/', status: 200, body: '', delayMs: 0, enabled: true, queryText: '', headersText: '' }}
      editing={false}
      busy={false}
      error={null}
      onDismissError={() => undefined}
      onChange={() => undefined}
      onClose={() => undefined}
      onSave={async () => undefined}
    />)

    expect(html).toContain('<form autoComplete="off" data-1p-ignore="true"')
    expect(html).toMatch(/<input(?=[^>]*name="portless-mock-route-name")(?=[^>]*autoComplete="off")(?=[^>]*data-1p-ignore="true")[^>]*>/)
  })

  it('keeps password managers away from the mock profile name field', () => {
    const html = renderToStaticMarkup(<CreateProfileModal
      environment={environment}
      recordings={[]}
      busy={false}
      error={null}
      onDismissError={() => undefined}
      onClose={() => undefined}
      onCreate={async () => undefined}
    />)

    expect(html).toContain('<form autoComplete="off" data-1p-ignore="true"')
    expect(html).toMatch(/<input(?=[^>]*name="portless-mock-profile-name")(?=[^>]*autoComplete="off")(?=[^>]*data-1p-ignore="true")(?=[^>]*data-lpignore="true")(?=[^>]*data-bwignore="true")[^>]*>/)
  })
})
