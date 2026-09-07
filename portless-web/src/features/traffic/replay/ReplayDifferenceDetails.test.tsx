import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { ReplayDifferenceDetails } from './ReplayDifferenceDetails'

it('associates the expanded differences toggle with the captured change table', () => {
  const html = renderToStaticMarkup(<ReplayDifferenceDetails section={{ state: 'different', changes: [{ path: '/quantity', kind: 'changed', before: '1', after: '2' }] }} representation="body" expanded onExpandedChange={() => undefined} />)
  expect(html).toContain('aria-expanded="true"')
  const id = html.match(/aria-controls="([^"]+)"/)?.[1]
  expect(id).toBeTruthy()
  expect(html).toContain(`<div id="${id}">`)
  expect(html).toContain('aria-label="body changes"')
  expect(html).toContain('<code>/quantity</code>')
  expect(html).toContain('<pre>1</pre>')
  expect(html).toContain('<pre>2</pre>')
})

it('keeps a partial comparison status visible when its details are collapsed', () => {
  const html = renderToStaticMarkup(<ReplayDifferenceDetails section={{ state: 'partial', reason: 'Only a captured prefix is available.' }} representation="body" expanded={false} onExpandedChange={() => undefined} />)
  expect(html).toContain('aria-expanded="false"')
  expect(html).toContain('<strong>Partial comparison</strong>')
  const hidden = html.indexOf('hidden=""')
  expect(hidden).toBeGreaterThan(html.indexOf('</button>'))
  expect(html.indexOf('Only a captured prefix is available.')).toBeGreaterThan(hidden)
})

it.each([['equal', 'No differences'], ['unavailable', 'Comparison unavailable']] as const)('avoids an empty disclosure for %s comparisons', (state, label) => {
  const html = renderToStaticMarkup(<ReplayDifferenceDetails section={{ state }} representation="headers" expanded onExpandedChange={() => undefined} />)
  expect(html).toContain(`<strong>${label}</strong>`)
  expect(html).not.toContain('<button')
  expect(html).not.toContain('<table')
})
