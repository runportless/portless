import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { MockNameValueEditor } from './MockNameValueEditor'

describe('MockNameValueEditor', () => {
  it('renders labeled name/value cells, row removal, and an inline blank row', () => {
    const html = renderToStaticMarkup(<MockNameValueEditor rows={[{ id: 1, name: 'Location', value: 'https://example.test:8443/items' }]} label="Response headers" rowLabel="Header" disabled={false} onChange={() => undefined} />)
    expect(html).toContain('<table class="mock-name-value" aria-label="Response headers">')
    expect(html).toContain('<th scope="col">NAME</th>')
    expect(html).toContain('<th scope="col">VALUE</th>')
    expect(html).toContain('aria-label="Header name 1"')
    expect(html).toContain('aria-label="Header value 1"')
    expect(html).toContain('value="https://example.test:8443/items"')
    expect(html).toContain('aria-label="Remove header 1"')
    expect(html).toContain('aria-label="New header name"')
    expect(html).toContain('aria-label="New header value"')
  })

  it('disables all edits while a route operation is pending', () => {
    const html = renderToStaticMarkup(<MockNameValueEditor rows={[{ id: 2, name: 'X-Example', value: '' }]} label="Response headers" rowLabel="Header" disabled={true} onChange={() => undefined} />)
    expect(html.match(/disabled=""/g)).toHaveLength(5)
    expect(html).not.toContain('aria-label="Remove header 2"')
  })

  it('provides query match operators and disables the value for presence matching', () => {
    const html = renderToStaticMarkup(<MockNameValueEditor rows={[{ id: 1, name: 'include', value: '' }]} label="Required query parameters" rowLabel="Query parameter" matching disabled={false} onChange={() => undefined} />)
    expect(html).toContain('aria-label="Required query parameters"')
    expect(html).toContain('<th scope="col">MATCH</th>')
    expect(html).toContain('aria-label="Query parameter match 1"')
    expect(html).toContain('<option value="exists" selected="">Exists</option>')
    expect(html).toMatch(/<input(?=[^>]*aria-label="Query parameter value 1")(?=[^>]*disabled="")(?=[^>]*placeholder="Any value")[^>]*>/)
    expect(html).toContain('aria-label="Query parameter name 1"')
    expect(html).toContain('aria-label="Query parameter value 1"')
    expect(html).toContain('aria-label="Remove query parameter 1"')
    expect(html).toContain('aria-label="New query parameter name"')
    expect(html).toContain('aria-label="New query parameter value"')
  })

  it('shows a selected Regex operator with an editable pattern cell', () => {
    const html = renderToStaticMarkup(<MockNameValueEditor rows={[{ id: 1, name: 'sku', match: 'regex', value: 'coffee-.*' }]} label="Required query parameters" rowLabel="Query parameter" matching disabled={false} onChange={() => undefined} />)
    expect(html).toContain('<option value="regex" selected="">Regex</option>')
    expect(html).toMatch(/<input(?=[^>]*aria-label="Query parameter value 1")(?=[^>]*value="coffee-\.\*")(?![^>]*disabled=)[^>]*>/)
    expect(html).toContain('title="Go regex (RE2). Matches the entire query value."')
  })
})
