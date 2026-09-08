import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { MockPreviewResponse, MockRoutePreview } from './MockRoutePreview'

describe('MockRoutePreview', () => {
  it('renders accessible request inputs, stale status, disabled route guidance, and structured errors', () => {
    const markup = renderToStaticMarkup(<MockRoutePreview services={['inventory']} enabled={false} busy={false} hidden={false} preview={{
      request: { service: 'inventory', method: 'GET', path: '/items/sku-123', query: [{ id: 1, name: 'include', value: 'stock' }] },
      result: { service: 'inventory', outcome: 'mocked', route: 'lookup', response: { status: 200, body: '{"stock":0}', delayMs: 25 } },
      outdated: true, error: { title: "Preview couldn't run", message: 'Request was invalid.' }, running: false,
      changeRequest: () => undefined, resetRequest: () => undefined, dismissError: () => undefined, run: async () => undefined, cancel: () => undefined,
    }} />)
    expect(markup).toContain('aria-label="Mock request preview"')
    for (const label of ['PREVIEW SERVICE', 'PREVIEW METHOD', 'PREVIEW PATH', 'Preview query parameters', 'Query parameter name 1', 'Query parameter value 1']) expect(markup).toContain(`aria-label="${label}"`)
    expect(markup).toContain('<h3>PREVIEW</h3>')
    expect(markup).toContain('>RESET</button>')
    expect(markup).toContain('>PREVIEW</button>')
    expect(markup).toContain('aria-label="Preview query parameters" aria-expanded="true"')
    expect(markup).toContain('This route is disabled and will not match.')
    expect(markup).toContain('OUTDATED')
    expect(markup).toContain('role="alert"')
    expect(markup).not.toContain('>EDIT</button>')
    expect(markup).not.toContain('>SAVE ROUTE</button>')
    expect(markup).not.toContain('autofocus')
  })

  it('presents no match as an expected 501 response and escapes response content', () => {
    const markup = renderToStaticMarkup(<MockPreviewResponse result={{ service: 'inventory', outcome: 'rejected', response: { status: 501, body: '<script>example()</script>' } }} outdated={false} />)
    expect(markup).toContain('No route matched')
    expect(markup).toContain('<strong>501</strong>')
    expect(markup).toContain('&lt;script&gt;example()&lt;/script&gt;')
    expect(markup).not.toContain('role="alert"')
    expect(markup).toContain('role="tabpanel"')
    expect(markup).toContain('aria-selected="true"')
  })

  it('distinguishes empty response bodies from an unrun preview', () => {
    expect(renderToStaticMarkup(<MockPreviewResponse result={{ service: 'inventory', outcome: 'mocked', route: 'empty', response: { status: 204 } }} outdated={false} />)).toContain('This response has no body.')
    expect(renderToStaticMarkup(<MockPreviewResponse result={null} outdated={false} />)).toContain('The expected status, headers, and body will appear here.')
  })
})

it('shows forwarding without inventing a response and explains a policy block', () => {
  const forward = renderToStaticMarkup(<MockPreviewResponse outdated={false} result={{ service: 'inventory', outcome: 'forward', destination: { provider: 'remote', url: 'http://inventory.local.store.localhost', classification: 'qa', writePolicy: 'read-only' } }} />)
  expect(forward).toContain('Would forward to service')
  expect(forward).toContain('No request was sent.')
  expect(forward).not.toContain('<strong>501</strong>')
  expect(forward).not.toContain('This response has no body.')
  const blocked = renderToStaticMarkup(<MockPreviewResponse outdated={false} result={{ service: 'inventory', outcome: 'blocked', reason: { code: 'REMOTE_READ_ONLY', message: 'Remote target is read-only' } }} />)
  expect(blocked).toContain('Request would be blocked')
  expect(blocked).toContain('Remote target is read-only')
})
