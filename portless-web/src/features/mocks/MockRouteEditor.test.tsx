import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { MockScenario } from '../../api/contracts/mocks'
import { MockRouteEditor, mockRouteDraft, mockRouteDraftHasChanges, newMockRouteDraft, suggestMockRouteName } from './MockRouteEditor'

const scenario: MockScenario = {
  project: 'store', environment: 'local', name: 'checkout-failure', createdAt: '2026-08-18T12:00:00Z', modifiedAt: '2026-08-18T13:15:00Z',
  activation: { state: 'disabled', targetServices: ['inventory'], activeServices: [] },
  routes: [{ name: 'lookup', service: 'inventory', method: 'GET', path: '/inventory/{sku}', status: 200, body: '{"available":false}', delayMs: 0, enabled: true }],
}

const editorProps = {
  scenario,
  services: ['inventory', 'payments'],
  draft: newMockRouteDraft(2, 'inventory'),
  dirty: false,
  busy: false,
  error: null,
  onDismissError: () => undefined,
  onChange: () => undefined,
  onCancel: () => undefined,
  onSave: async () => undefined,
}

describe('MockRouteEditor', () => {
  it('creates one service-owned route in a compact, maximizable pane', () => {
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} />)
    expect(html).toContain('class="mock-route-workspace" role="region" aria-label="Create Route"')
    expect(html).toContain('<h3 id="mock-route-workspace-title">Create Route</h3>')
    expect(html).toContain('aria-label="SERVICE"')
    expect(html).toContain('<option selected="">inventory</option><option>payments</option>')
    expect(html).toContain('aria-label="Maximize route editor"')
    expect(html).toContain('autofocus="" aria-label="PATH"')
    expect(html).toContain('>SAVE ROUTE</button>')
    expect(html).not.toContain('role="dialog"')
    expect(html).not.toContain('BACK TO SCENARIO')
  })

  it('loads an existing route and its service while keeping identity fixed', () => {
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} routeName="lookup" draft={mockRouteDraft(scenario.routes[0])} />)
    expect(html).toContain('aria-label="Edit Route"')
    expect(html).toContain('<option selected="">inventory</option>')
    expect(html).toContain('value="/inventory/{sku}"')
    expect(html).toMatch(/<input(?=[^>]*aria-label="ROUTE NAME")(?=[^>]*disabled="")(?=[^>]*value="lookup")[^>]*>/)
    expect(html).toContain('All changes saved')
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>SAVE ROUTE<\/button>/)
  })

  it('shows a stable missing state for a removed route', () => {
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} routeName="removed" />)
    expect(html).toContain('ROUTE NOT FOUND')
    expect(html).toContain('removed is no longer part of this mock scenario.')
    expect(html).not.toContain('SAVE ROUTE')
  })

  it('generates readable route names and preserves service drafts', () => {
    expect(suggestMockRouteName('GET', '/', 1)).toBe('get-root')
    expect(suggestMockRouteName('POST', '/inventory/{sku}/availability', 2)).toBe('post-inventory-by-sku-availability')
    expect(newMockRouteDraft(2, 'payments')).toMatchObject({ name: 'get-route-2', service: 'payments', path: '/route-2', enabled: true })
    expect(mockRouteDraft(scenario.routes[0])).toMatchObject({ name: 'lookup', service: 'inventory', path: '/inventory/{sku}', headersText: '' })
  })

  it('detects editable changes without treating name suggestion state as a change', () => {
    const route = scenario.routes[0]
    const draft = mockRouteDraft(route)
    expect(mockRouteDraftHasChanges(draft, route)).toBe(false)
    expect(mockRouteDraftHasChanges({ ...draft, nameCustomized: false }, route)).toBe(false)
    expect(mockRouteDraftHasChanges({ ...draft, body: 'changed' }, route)).toBe(true)
    expect(mockRouteDraftHasChanges({ ...draft, enabled: false }, route)).toBe(true)
    expect(mockRouteDraftHasChanges({ ...draft, service: 'payments' }, route)).toBe(true)
  })
})
