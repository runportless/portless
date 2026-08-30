export type TableSortDirection = 'asc' | 'desc'

export type TableSort<Key extends string> = {
  key: Key
  direction: TableSortDirection
}

export function SortableTableHeader<Key extends string>({ label, sortKey, sort, onSort }: {
  label: string
  sortKey: Key
  sort: TableSort<Key>
  onSort: (sort: TableSort<Key>) => void
}) {
  const active = sort.key === sortKey
  const nextDirection: TableSortDirection = active && sort.direction === 'asc' ? 'desc' : 'asc'

  return <th className={`sortable-table-header${active ? ' is-active' : ''}`} aria-sort={active ? sort.direction === 'asc' ? 'ascending' : 'descending' : 'none'}>
    <span>{label}</span>
    <button
      type="button"
      aria-label={`Sort ${label} ${nextDirection === 'asc' ? 'ascending' : 'descending'}`}
      title={`Sort ${label.toLowerCase()} ${nextDirection === 'asc' ? 'ascending' : 'descending'}`}
      onClick={() => onSort({ key: sortKey, direction: nextDirection })}
    ><span aria-hidden="true">{active && sort.direction === 'desc' ? '▼' : '▲'}</span></button>
  </th>
}
