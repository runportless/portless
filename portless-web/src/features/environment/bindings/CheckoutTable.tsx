import { useEffect, useMemo, useState } from 'react'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import { StatusMark } from '../../../components/Status'
import type { Environment, Project } from '../../../types'
import { environmentCheckoutRows, formatBindingTimestamp, type EnvironmentCheckoutRow } from './bindingPresentation'

export function CheckoutTable({ environment, project, mutationBusy, onConfigure, onRemove, onManageSources }: {
  environment: Environment
  project?: Project
  mutationBusy: boolean
  onConfigure: (row: EnvironmentCheckoutRow) => void
  onRemove: (row: EnvironmentCheckoutRow & { checkout: NonNullable<EnvironmentCheckoutRow['checkout']> }) => void
  onManageSources: () => void
}) {
  const [page, setPage] = useState(0)
  const rows = useMemo(() => environmentCheckoutRows(project, environment), [project, environment])
  const checkouts = useMemo(() => paginateItems(rows, page, 5), [rows, page])

  useEffect(() => setPage(0), [environment.project, environment.name])

  return <section className="panel source-checkouts-panel">
    <div className="panel-title"><span>CHECKOUTS</span><button type="button" onClick={onManageSources}>MANAGE SOURCES</button></div>
    {checkouts.total > 0 ? <table className="source-table" aria-label="Environment checkouts">
      <thead><tr><th scope="col">Source</th><th scope="col">Path</th><th scope="col">Created</th><th scope="col" aria-label="Row actions" /></tr></thead>
      <tbody>{checkouts.items.map((item) => <tr key={item.source.name}><td><div className="checkout-source"><StatusMark status={item.checkout ? item.checkout.status : item.required ? 'degraded' : 'stopped'} label={false} /><strong>{item.source.name}</strong></div></td><td>{item.checkout ? <code title={item.checkout.path}>{item.checkout.path}</code> : <span className={item.required ? 'warning-text' : 'muted'}>{item.required ? 'Configuration required' : 'Not configured'}</span>}</td><td>{item.checkout ? <time dateTime={item.checkout.createdAt} title={new Date(item.checkout.createdAt).toLocaleString()}>{formatBindingTimestamp(item.checkout.createdAt)}</time> : <span>—</span>}</td><td><div className="table-row-actions">{item.checkout ? <><button type="button" disabled={mutationBusy} onClick={() => onConfigure(item)}>EDIT</button><button type="button" disabled={mutationBusy} onClick={() => onRemove({ ...item, checkout: item.checkout! })}>REMOVE</button></> : <button type="button" disabled={mutationBusy} onClick={() => onConfigure(item)}>CONFIGURE</button>}</div></td></tr>)}</tbody>
    </table> : <div className="empty-row">This project has no sources to configure.</div>}
    <PanelPagination label="checkouts" pagination={checkouts} onPage={setPage} />
  </section>
}
