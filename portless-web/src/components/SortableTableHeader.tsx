export type TableSortDirection = 'asc' | 'desc'

export type TableSort<Key extends string> = {
  key: Key
  direction: TableSortDirection
}

type SortableHeaderProps<Key extends string> = {
  label: string
  sortKey: Key
  sort: TableSort<Key>
  onSort: (sort: TableSort<Key>) => void
}

export function SortableTableHeader<Key extends string>(props: SortableHeaderProps<Key>) {
  return <SortableColumnHeader {...props} layout="table" />
}

export function SortableGridHeader<Key extends string>(props: SortableHeaderProps<Key>) {
  return <SortableColumnHeader {...props} layout="grid" />
}

function SortableColumnHeader<Key extends string>({ label, sortKey, sort, onSort, layout }: SortableHeaderProps<Key> & { layout: 'table' | 'grid' }) {
  const active = sort.key === sortKey
  const nextDirection: TableSortDirection = active && sort.direction === 'asc' ? 'desc' : 'asc'
  const ariaSort = active ? sort.direction === 'asc' ? 'ascending' : 'descending' : 'none'
  const content = <>
    <span>{label}</span>
    <button
      type="button"
      aria-label={`Sort ${label} ${nextDirection === 'asc' ? 'ascending' : 'descending'}`}
      title={`Sort ${label.toLowerCase()} ${nextDirection === 'asc' ? 'ascending' : 'descending'}`}
      onClick={() => onSort({ key: sortKey, direction: nextDirection })}
    ><span aria-hidden="true">{active && sort.direction === 'desc' ? '▼' : '▲'}</span></button>
  </>

  if (layout === 'table') return <th className={`sortable-table-header${active ? ' is-active' : ''}`} aria-sort={ariaSort}>{content}</th>
  return <span className={`sortable-grid-header${active ? ' is-active' : ''}`} role="columnheader" aria-sort={ariaSort}>{content}</span>
}
