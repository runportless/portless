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
  previewing: false,
  error: null,
  onDismissError: () => undefined,
  onChange: () => undefined,
  onCancel: () => undefined,
  onSave: async () => undefined,
  onPreview: async () => ({ service: 'inventory', matched: false, status: 501 }),
}

describe('MockRouteEditor', () => {
  it('opens request configuration with response fields outside the active panel', () => {
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} />)
    expect(html).toContain('class="mock-route-workspace" role="region" aria-label="Create Route"')
    expect(html).not.toContain('<header')
    expect(html).toContain('aria-label="SERVICE"')
    expect(html).toContain('<option selected="">inventory</option><option>payments</option>')
    expect(html).toContain('autofocus="" aria-label="PATH"')
    expect(html).not.toContain('SAVE ROUTE')
    expect(html).not.toContain('Cancel new route')
    expect(html).toContain('aria-label="Mock request preview" hidden=""')
    expect(html).toContain('aria-label="Query parameters" aria-expanded="false"')
    expect(html).toContain('aria-label="Add query parameter"')
    expect(html).toContain('aria-label="Mock request preview"')
    expect(html.match(/<form /g)).toHaveLength(2)
    expect(html).not.toContain('<footer ')
    expect(html).not.toContain('>EDIT</button>')
    expect(html).toContain('role="tablist" aria-label="Mock route configuration"')
    expect(html).toMatch(/<button(?=[^>]*role="tab")(?=[^>]*aria-selected="true")(?=[^>]*tabindex="0")[^>]*>Request<\/button>/)
    expect(html).toMatch(/<button(?=[^>]*role="tab")(?=[^>]*aria-selected="false")(?=[^>]*tabindex="-1")[^>]*>Response<\/button>/)
    expect(html.match(/role="tabpanel"/g)).toHaveLength(2)
    expect(html.match(/<div(?=[^>]*role="tabpanel")(?=[^>]*hidden="")[^>]*><\/div>/g)).toHaveLength(1)
    expect(html).toContain('aria-label="ROUTE NAME"')
    expect(html).toContain('aria-label="Path match"')
    expect(html).toContain('<option value="exact" selected="">Exact</option>')
    expect(html).toContain('Template {…}')
    expect(html).toContain('<th scope="col">MATCH</th>')
    expect(html).not.toContain('An empty value matches any value.')
    expect(html).not.toContain('ROUTE ENABLED')
    expect(html).toContain('aria-label="Query parameters"')
    expect(html).toContain('aria-label="New query parameter name"')
    for (const label of ['RESPONSE STATUS', 'DELAY (MS)', 'RESPONSE BODY', 'Response headers']) {
      expect(html).not.toContain(`aria-label="${label}"`)
    }
    expect(html).not.toContain('ADVANCED OPTIONS')
    expect(html).not.toContain('role="dialog"')
    expect(html).not.toContain('BACK TO SCENARIO')
  })

  it('shows the same editor alongside preview with a route identity and shared save footer', () => {
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} previewing dirty routeName="lookup" draft={{ ...mockRouteDraft(scenario.routes[0]), body: 'changed', enabled: false }} />)
    expect(html).toContain('class="mock-route-layout is-preview"')
    expect(html).toContain('class="mock-route-configuration-name" title="lookup">lookup</span>')
    expect(html).toContain('class="mock-route-disabled-state">DISABLED</span>')
    expect(html).toContain('aria-label="Mock request preview"')
    expect(html).not.toContain('aria-label="Mock request preview" hidden=""')
    expect(html).toContain('role="tablist" aria-label="Mock route configuration"')
    expect(html.match(/>SAVE ROUTE<\/button>/g)).toHaveLength(1)
    expect(html.match(/<footer /g)).toHaveLength(1)
    expect(html).toContain('aria-label="Discard route changes"')
    expect(html).toContain('Unsaved changes')
    expect(html).toMatch(/<button[^>]*type="submit"[^>]*form="[^"]+"[^>]*>SAVE ROUTE<\/button>/)
  })

  it('loads an existing route with an editable name and preserves its service', () => {
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} routeName="lookup" draft={mockRouteDraft(scenario.routes[0])} />)
    expect(html).toContain('aria-label="Edit Route"')
    expect(html).not.toContain('<header')
    expect(html).not.toContain('Maximize route editor')
    expect(html).toContain('<option selected="">inventory</option>')
    expect(html).toContain('value="/inventory/{sku}"')
    expect(html).toContain('<option value="template" selected="">Template {…}</option>')
    expect(html).toMatch(/<input(?=[^>]*aria-label="ROUTE NAME")(?=[^>]*value="lookup")(?![^>]*disabled=)[^>]*>/)
    expect(html).not.toContain('All changes saved')
    expect(html).not.toContain('mock-route-disabled-state')
    expect(html).not.toContain('SAVE ROUTE')
    expect(html).not.toContain('Discard route changes')
    expect(html).not.toContain('<footer ')
    expect(html).toContain('role="tablist" aria-label="Mock route configuration"')
    expect(html).not.toContain('aria-label="RESPONSE BODY"')
  })

  it('tracks a renamed draft while retaining its saved route identity', () => {
    const draft = { ...mockRouteDraft(scenario.routes[0]), name: 'renamed' }
    expect(mockRouteDraftHasChanges(draft, scenario.routes[0])).toBe(true)
    const html = renderToStaticMarkup(<MockRouteEditor {...editorProps} routeName="lookup" draft={draft} dirty />)
    expect(html).toContain('aria-label="Edit Route"')
    expect(html).toContain('value="renamed"')
    expect(html).not.toContain('ROUTE NOT FOUND')
    expect(html).toContain('>SAVE ROUTE</button>')
    expect(html).not.toMatch(/<button[^>]*disabled=""[^>]*>SAVE ROUTE<\/button>/)
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
    expect(mockRouteDraft(scenario.routes[0])).toMatchObject({ name: 'lookup', service: 'inventory', path: '/inventory/{sku}', headers: [] })
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

  it('compares header values without treating row identities or empty rows as saved changes', () => {
    const route = { ...scenario.routes[0], headers: { 'Content-Type': 'application/json' } }
    const draft = mockRouteDraft(route)
    expect(mockRouteDraftHasChanges({ ...draft, headers: [{ ...draft.headers[0], id: 9 }, { id: 10, name: '', value: '' }] }, route)).toBe(false)
    expect(mockRouteDraftHasChanges({ ...draft, headers: [{ ...draft.headers[0], value: 'text/plain' }] }, route)).toBe(true)
    expect(mockRouteDraftHasChanges({ ...draft, headers: [] }, route)).toBe(true)
    expect(mockRouteDraftHasChanges({ ...draft, headers: [...draft.headers, { id: 2, name: '', value: 'unfinished' }] }, route)).toBe(true)
  })

  it('loads query rows and tracks edited or incomplete values independently of row IDs', () => {
    const route: MockScenario['routes'][number] = { ...scenario.routes[0], query: { include: { match: 'exists' }, expression: { match: 'equals', value: 'a=b&c' } } }
    const draft = mockRouteDraft(route)
    expect(draft.query.map(({ name, value }) => [name, value])).toEqual([['include', ''], ['expression', 'a=b&c']])
    expect(mockRouteDraftHasChanges({ ...draft, query: draft.query.map((row) => ({ ...row, id: row.id + 10 })) }, route)).toBe(false)
    expect(mockRouteDraftHasChanges({ ...draft, query: [...draft.query, { id: 3, name: '', value: '' }] }, route)).toBe(false)
    expect(mockRouteDraftHasChanges({ ...draft, query: [{ ...draft.query[0], match: 'equals', value: 'stock' }, draft.query[1]] }, route)).toBe(true)
    expect(mockRouteDraftHasChanges({ ...draft, query: [draft.query[0], { ...draft.query[1], match: 'regex' }] }, route)).toBe(true)
    expect(mockRouteDraftHasChanges({ ...draft, query: [{ id: 1, name: '', value: 'unfinished' }] }, route)).toBe(true)
  })
})
