export type TableSortDirection = 'asc' | 'desc'

export type TableSort<Key extends string> = {
  key: Key
  direction: TableSortDirection
}

type SortableHeaderProps<Key extends string> = {
  label: string
  sortKey: Key
  sort: TableSort<Key>
  itemCount: number
  onSort: (sort: TableSort<Key>) => void
}

export function SortableTableHeader<Key extends string>(props: SortableHeaderProps<Key>) {
  return <SortableColumnHeader {...props} layout="table" />
}

export function SortableGridHeader<Key extends string>(props: SortableHeaderProps<Key>) {
  return <SortableColumnHeader {...props} layout="grid" />
}

function SortableColumnHeader<Key extends string>({ label, sortKey, sort, itemCount, onSort, layout }: SortableHeaderProps<Key> & { layout: 'table' | 'grid' }) {
  const active = sort.key === sortKey
  const nextDirection: TableSortDirection = active && sort.direction === 'asc' ? 'desc' : 'asc'
  const ariaSort = active ? sort.direction === 'asc' ? 'ascending' : 'descending' : 'none'
  const content = <>
    <span>{label}</span>
    {itemCount > 1 && <button
      className="sortable-column-sort-control"
      type="button"
      aria-label={`Sort ${label} ${nextDirection === 'asc' ? 'ascending' : 'descending'}`}
      title={`Sort ${label.toLowerCase()} ${nextDirection === 'asc' ? 'ascending' : 'descending'}`}
      onClick={() => onSort({ key: sortKey, direction: nextDirection })}
    >
      <span className={`sortable-column-sort-control__triangle sortable-column-sort-control__triangle--up${active && sort.direction === 'asc' ? ' is-active' : ''}`} aria-hidden="true" />
      <span className={`sortable-column-sort-control__triangle sortable-column-sort-control__triangle--down${active && sort.direction === 'desc' ? ' is-active' : ''}`} aria-hidden="true" />
    </button>}
  </>

  const stateClasses = active ? ' is-active' : ''
  if (layout === 'table') return <th className={`sortable-table-header${stateClasses}`} aria-sort={ariaSort}>{content}</th>
  return <span className={`sortable-grid-header${stateClasses}`} role="columnheader" aria-sort={ariaSort}>{content}</span>
}
