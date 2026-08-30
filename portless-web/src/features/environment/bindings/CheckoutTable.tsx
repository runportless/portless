import { useEffect, useMemo, useState } from 'react'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import { SortableTableHeader, type TableSort } from '../../../components/SortableTableHeader'
import { StatusMark } from '../../../components/Status'
import type { Environment } from '../../../api/contracts/environments'
import type { Project } from '../../../api/contracts/projects'
import { environmentCheckoutRows, formatBindingTimestamp, type EnvironmentCheckoutRow } from './bindingPresentation'

type CheckoutSortField = 'source' | 'path' | 'created'
const defaultCheckoutSort: TableSort<CheckoutSortField> = { key: 'source', direction: 'asc' }

export function CheckoutTable({ environment, project, mutationBusy, onConfigure, onRemove, onManageSources }: {
  environment: Environment
  project?: Project
  mutationBusy: boolean
  onConfigure: (row: EnvironmentCheckoutRow) => void
  onRemove: (row: EnvironmentCheckoutRow & { checkout: NonNullable<EnvironmentCheckoutRow['checkout']> }) => void
  onManageSources: () => void
}) {
  const [page, setPage] = useState(0)
  const [sort, setSort] = useState<TableSort<CheckoutSortField>>(defaultCheckoutSort)
  const rows = useMemo(() => environmentCheckoutRows(project, environment), [project, environment])
  const orderedRows = useMemo(() => sortCheckoutRows(rows, sort), [rows, sort])
  const checkouts = useMemo(() => paginateItems(orderedRows, page, 5), [orderedRows, page])

  useEffect(() => {
    setPage(0)
    setSort(defaultCheckoutSort)
  }, [environment.project, environment.name])

  return <section className="panel source-checkouts-panel">
    <div className="panel-title"><span>CHECKOUTS</span><button type="button" onClick={onManageSources}>MANAGE SOURCES</button></div>
    {checkouts.total > 0 ? <table className="source-table" aria-label="Environment checkouts">
      <thead><tr className={`sortable-header-row${sort.key === defaultCheckoutSort.key && sort.direction === defaultCheckoutSort.direction ? ' is-default-sort' : ''}`}>
        <SortableTableHeader label="Source" sortKey="source" sort={sort} itemCount={orderedRows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <SortableTableHeader label="Path" sortKey="path" sort={sort} itemCount={orderedRows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <SortableTableHeader label="Created" sortKey="created" sort={sort} itemCount={orderedRows.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <th scope="col" aria-label="Row actions" />
      </tr></thead>
      <tbody>{checkouts.items.map((item) => <tr key={item.source.name}><td><div className="checkout-source"><StatusMark status={item.checkout ? item.checkout.status : item.required ? 'degraded' : 'stopped'} label={false} /><strong>{item.source.name}</strong></div></td><td>{item.checkout ? <code title={item.checkout.path}>{item.checkout.path}</code> : <span className={item.required ? 'warning-text' : 'muted'}>{item.required ? 'Configuration required' : 'Not configured'}</span>}</td><td>{item.checkout ? <time dateTime={item.checkout.createdAt} title={new Date(item.checkout.createdAt).toLocaleString()}>{formatBindingTimestamp(item.checkout.createdAt)}</time> : <span>—</span>}</td><td><div className="table-row-actions">{item.checkout ? <><button type="button" disabled={mutationBusy} onClick={() => onConfigure(item)}>EDIT</button><button type="button" disabled={mutationBusy} onClick={() => onRemove({ ...item, checkout: item.checkout! })}>REMOVE</button></> : <button type="button" disabled={mutationBusy} onClick={() => onConfigure(item)}>CONFIGURE</button>}</div></td></tr>)}</tbody>
    </table> : <div className="empty-row">This project has no sources to configure.</div>}
    <PanelPagination label="checkouts" pagination={checkouts} onPage={setPage} />
  </section>
}

export function sortCheckoutRows(rows: EnvironmentCheckoutRow[], sort: TableSort<CheckoutSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...rows].sort((left, right) => {
    const sourceOrder = compareCheckoutText(left.source.name, right.source.name)
    let order = 0

    switch (sort.key) {
      case 'source':
        order = sourceOrder
        break
      case 'path':
        order = compareCheckoutText(checkoutPathValue(left), checkoutPathValue(right))
        break
      case 'created':
        order = checkoutTimestampValue(left.checkout?.createdAt) - checkoutTimestampValue(right.checkout?.createdAt)
        break
    }

    return direction * order || sourceOrder
  })
}

function checkoutPathValue(row: EnvironmentCheckoutRow) {
  if (row.checkout) return row.checkout.path
  return row.required ? 'Configuration required' : 'Not configured'
}

function compareCheckoutText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
}

function checkoutTimestampValue(value?: string) {
  const timestamp = value ? Date.parse(value) : 0
  return Number.isNaN(timestamp) ? 0 : timestamp
}
