import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { MockProfile } from '../../api/contracts/mocks'
import { MockCreationWorkspace, MockRouteWorkspace, newMockRouteDraft, previewMockDraftRoutes, suggestMockRouteName } from './MockCreationWorkspace'

const environment: Environment = {
  project: 'store', name: 'local', revision: 1, status: 'healthy', createdAt: '', updatedAt: '',
  services: [{ name: 'inventory', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 }],
  connections: [], bindings: [{ service: 'inventory', provider: 'local', source: 'store' }],
}

const profile: MockProfile = {
  project: 'store', environment: 'local', name: 'sold-out', service: 'inventory', createdAt: '', modifiedAt: '',
  routes: [{ name: 'lookup', method: 'GET', path: '/inventory/{sku}', status: 200, body: '{"available":false}', delayMs: 0, enabled: true }],
}

describe('MockCreationWorkspace', () => {
  it('renders mock creation as an in-page route builder instead of a modal', () => {
    const html = renderToStaticMarkup(<MockCreationWorkspace
      environment={environment}
      recordings={[]}
      busy={false}
      activationBlocked={false}
      error={null}
      onDismissError={() => undefined}
      onCancel={() => undefined}
      onCreate={async () => undefined}
    />)

    expect(html).toContain('class="panel mock-create-workspace" aria-labelledby="mock-create-workspace-title"')
    expect(html).toContain('<h2 id="mock-create-workspace-title">Create mock</h2>')
    expect(html).toContain('DRAFT · INACTIVE')
    expect(html).toContain('Build routes')
    expect(html).toContain('aria-label="START WITH"')
    expect(html).toContain('aria-label="Route editor"')
    expect(html).toContain('aria-label="Maximize route editor"')
    expect(html).toContain('>MAXIMIZE</button>')
    expect(html).toContain('>PREVIEW</button>')
    expect(html).not.toContain('aria-label="Draft preview"')
    expect(html).toContain('>INACTIVE</strong>')
    expect(html).toContain('Enable for inventory after creation')
    expect(html).toContain('>CREATE MOCK</button>')
    expect(html).not.toContain('role="dialog"')
    expect(html).not.toContain('aria-modal="true"')
    expect(html).toContain('<form class="mock-create-workspace__form" autoComplete="off" data-1p-ignore="true"')
    expect(html).toMatch(/<input(?=[^>]*name="portless-mock-profile-name")(?=[^>]*autoComplete="off")(?=[^>]*data-1p-ignore="true")(?=[^>]*data-lpignore="true")(?=[^>]*data-bwignore="true")[^>]*>/)
  })

  it('uses the same full-page, maximizable editor for creating and editing profile routes', () => {
    const createHTML = renderToStaticMarkup(<MockRouteWorkspace
      profile={profile}
      busy={false}
      error={null}
      onDismissError={() => undefined}
      onCancel={() => undefined}
      onOpenRoute={() => undefined}
      onSave={async () => undefined}
    />)
    const editHTML = renderToStaticMarkup(<MockRouteWorkspace
      profile={profile}
      routeName="lookup"
      busy={false}
      error={null}
      onDismissError={() => undefined}
      onCancel={() => undefined}
      onOpenRoute={() => undefined}
      onSave={async () => undefined}
    />)

    expect(createHTML).toContain('<h2 id="mock-route-workspace-title">Create Route</h2>')
    expect(createHTML).toContain('aria-label="Route editor"')
    expect(createHTML).toContain('aria-label="Maximize route editor"')
    expect(createHTML).toContain('>SAVE ROUTE</button>')
    expect(createHTML).not.toContain('role="dialog"')
    expect(createHTML).toMatch(/<input(?=[^>]*name="portless-mock-route-name")(?=[^>]*autoComplete="off")(?=[^>]*data-1p-ignore="true")[^>]*>/)
    expect(editHTML).toContain('<h2 id="mock-route-workspace-title">Edit Route</h2>')
    expect(editHTML).toMatch(/<input(?=[^>]*name="portless-mock-route-name")(?=[^>]*disabled="")(?=[^>]*value="lookup")[^>]*>/)
  })

  it('generates readable stable route names from methods and path parameters', () => {
    expect(suggestMockRouteName('GET', '/', 1)).toBe('get-root')
    expect(suggestMockRouteName('POST', '/inventory/{sku}/availability', 2)).toBe('post-inventory-by-sku-availability')
    expect(newMockRouteDraft(2)).toMatchObject({ key: 'draft-route-2', name: 'get-route-2', path: '/route-2', enabled: true })
  })

  it('previews the most specific enabled route and exposes the 501 fallback', () => {
    const generic = { ...newMockRouteDraft(1), name: 'generic', path: '/inventory/{sku}', body: 'generic' }
    const central = { ...newMockRouteDraft(2), name: 'central', path: '/inventory/{sku}', queryText: 'warehouse=central', status: 409, body: 'sold out' }
    const disabled = { ...newMockRouteDraft(3), name: 'disabled', path: '/health', status: 503, enabled: false }

    expect(previewMockDraftRoutes([generic, central, disabled], 'GET', '/inventory/mug?warehouse=central')).toEqual({ matched: true, route: 'central', status: 409, body: 'sold out', delayMs: 0 })
    expect(previewMockDraftRoutes([generic, central, disabled], 'GET', '/inventory/mug')).toEqual({ matched: true, route: 'generic', status: 200, body: 'generic', delayMs: 0 })
    expect(previewMockDraftRoutes([generic, central, disabled], 'GET', '/health')).toEqual({ matched: false, status: 501 })
  })
})
