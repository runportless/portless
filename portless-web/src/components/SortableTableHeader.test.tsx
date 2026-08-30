import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { SortableTableHeader } from './SortableTableHeader'

describe('SortableTableHeader', () => {
  it('places an accessible direction arrow next to the column label', () => {
    const html = renderToStaticMarkup(<table><thead><tr>
      <SortableTableHeader label="State" sortKey="state" sort={{ key: 'state', direction: 'asc' }} onSort={() => undefined} />
      <SortableTableHeader label="Name" sortKey="name" sort={{ key: 'state', direction: 'asc' }} onSort={() => undefined} />
    </tr></thead></table>)

    expect(html).toContain('class="sortable-table-header is-active" aria-sort="ascending"><span>State</span>')
    expect(html).toContain('aria-label="Sort State descending"')
    expect(html).toContain('<span aria-hidden="true">▲</span>')
    expect(html).toContain('class="sortable-table-header" aria-sort="none"><span>Name</span>')
    expect(html).toContain('aria-label="Sort Name ascending"')
    expect(html.match(/<span aria-hidden="true">▲<\/span>/g)).toHaveLength(2)
    expect(html.match(/sortable-table-header is-active/g)).toHaveLength(1)
  })
})
