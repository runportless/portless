import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { MockNameValueSection } from './MockNameValueSection'

describe('MockNameValueSection', () => {
  it('keeps incomplete headers in the collapsed count and prevents additions while saving', () => {
    const html = renderToStaticMarkup(<MockNameValueSection label="Response headers" rows={[
      { id: 1, name: 'Content-Type', value: 'application/json' },
      { id: 2, name: '', value: 'unfinished' },
      { id: 3, name: '', value: '' },
    ]} open={false} disabled onOpenChange={() => undefined} onChange={() => undefined} />)
    expect(html).toContain('aria-label="Response headers" aria-expanded="false"')
    expect(html).toContain('class="mock-name-value-section__count">2</span>')
    expect(html).toMatch(/<div[^>]*hidden=""[^>]*><table[^>]*aria-label="Response headers"/)
    expect(html).toContain('value="unfinished"')
    expect(html).toMatch(/<button[^>]*aria-label="Add response header"[^>]*disabled=""/)
    expect(html).not.toContain('<th scope="col">MATCH</th>')
  })
})
