import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { SortableGridHeader, SortableTableHeader } from './SortableTableHeader'

describe('SortableTableHeader', () => {
  it('renders paired direction triangles and identifies the default sort', () => {
    const html = renderToStaticMarkup(<table><thead><tr>
      <SortableTableHeader label="State" sortKey="state" sort={{ key: 'state', direction: 'asc' }} defaultSort={{ key: 'state', direction: 'asc' }} itemCount={2} onSort={() => undefined} />
      <SortableTableHeader label="Name" sortKey="name" sort={{ key: 'state', direction: 'asc' }} defaultSort={{ key: 'state', direction: 'asc' }} itemCount={2} onSort={() => undefined} />
    </tr></thead></table>)

    expect(html).toContain('class="sortable-table-header is-active is-default-sort" aria-sort="ascending"><span>State</span>')
    expect(html).toContain('aria-label="Sort State descending"')
    expect(html).toContain('sortable-column-sort-control__triangle sortable-column-sort-control__triangle--up is-active')
    expect(html).toContain('sortable-column-sort-control__triangle sortable-column-sort-control__triangle--down')
    expect(html).toContain('class="sortable-table-header" aria-sort="none"><span>Name</span>')
    expect(html).toContain('aria-label="Sort Name ascending"')
    expect(html.match(/sortable-column-sort-control__triangle--up/g)).toHaveLength(2)
    expect(html.match(/sortable-column-sort-control__triangle--down/g)).toHaveLength(2)
    expect(html.match(/sortable-column-sort-control__triangle--up is-active/g)).toHaveLength(1)
    expect(html.match(/sortable-table-header is-active/g)).toHaveLength(1)
  })

  it('marks a non-default descending grid sort while keeping one active column', () => {
    const html = renderToStaticMarkup(<div role="row">
      <SortableGridHeader label="State" sortKey="state" sort={{ key: 'name', direction: 'desc' }} defaultSort={{ key: 'state', direction: 'asc' }} itemCount={2} onSort={() => undefined} />
      <SortableGridHeader label="Name" sortKey="name" sort={{ key: 'name', direction: 'desc' }} defaultSort={{ key: 'state', direction: 'asc' }} itemCount={2} onSort={() => undefined} />
    </div>)

    expect(html).toContain('class="sortable-grid-header" role="columnheader" aria-sort="none"><span>State</span>')
    expect(html).toContain('aria-label="Sort State ascending"')
    expect(html).toContain('class="sortable-grid-header is-active" role="columnheader" aria-sort="descending"><span>Name</span>')
    expect(html).toContain('aria-label="Sort Name ascending"')
    expect(html).toContain('sortable-column-sort-control__triangle sortable-column-sort-control__triangle--down is-active')
    expect(html).not.toContain('sortable-grid-header is-active is-default-sort')
    expect(html.match(/sortable-grid-header is-active/g)).toHaveLength(1)
  })

  it('omits the sort control when the table has fewer than two items', () => {
    const html = renderToStaticMarkup(<table><thead><tr>
      <SortableTableHeader label="Name" sortKey="name" sort={{ key: 'name', direction: 'asc' }} defaultSort={{ key: 'name', direction: 'asc' }} itemCount={1} onSort={() => undefined} />
    </tr></thead></table>)

    expect(html).toContain('<span>Name</span>')
    expect(html).not.toContain('sortable-column-sort-control')
    expect(html).not.toContain('Sort Name')
  })
})
