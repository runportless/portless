import { useEffect, useMemo, useState } from 'react'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import { SortableGridHeader, type TableSort } from '../../../components/SortableTableHeader'
import { StatusMark } from '../../../components/Status'
import type { Environment } from '../../../api/contracts/environments'
import type { ComponentBinding } from '../../../api/contracts/topology'
import { formatBindingTimestamp, providerDisplayName } from './bindingPresentation'

type ProviderBindingSortField = 'service' | 'provider' | 'configuration' | 'modified'
const defaultProviderBindingSort: TableSort<ProviderBindingSortField> = { key: 'service', direction: 'asc' }

export function ProviderBindingsTable({ environment, onConfigure }: { environment: Environment; onConfigure: (service?: string) => void }) {
  const [page, setPage] = useState(0)
  const [sort, setSort] = useState<TableSort<ProviderBindingSortField>>(defaultProviderBindingSort)
  const orderedBindings = useMemo(() => sortProviderBindings(environment.bindings || [], sort), [environment.bindings, sort])
  const providers = useMemo(() => paginateItems(orderedBindings, page, 5), [orderedBindings, page])

  useEffect(() => {
    setPage(0)
    setSort(defaultProviderBindingSort)
  }, [environment.project, environment.name])

  return <section className="panel experiment-list configured-providers-panel">
    <div className="panel-title"><span>PROVIDERS</span><button className="button button--primary button--small panel-create-button configure-provider-button" type="button" aria-haspopup="dialog" disabled={!environment.services.length} onClick={() => onConfigure()}>CONFIGURE PROVIDER</button></div>
    <div className="provider-table" role="table" aria-label="Configured providers">
      <div className="provider-row provider-row--header sortable-header-row" role="row">
        <SortableGridHeader label="Service" sortKey="service" sort={sort} defaultSort={defaultProviderBindingSort} itemCount={orderedBindings.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <SortableGridHeader label="Provider" sortKey="provider" sort={sort} defaultSort={defaultProviderBindingSort} itemCount={orderedBindings.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <SortableGridHeader label="Configuration" sortKey="configuration" sort={sort} defaultSort={defaultProviderBindingSort} itemCount={orderedBindings.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <SortableGridHeader label="Modified" sortKey="modified" sort={sort} defaultSort={defaultProviderBindingSort} itemCount={orderedBindings.length} onSort={(nextSort) => { setSort(nextSort); setPage(0) }} />
        <span role="columnheader" aria-label="Row actions" />
      </div>
      {providers.items.map((binding) => <div className={`experiment-row provider-row ${binding.provider === 'remote' ? 'is-warning' : ''}`} role="row" key={binding.service}>
        <div className="provider-service" role="cell"><StatusMark status={environment.services.find((item) => item.name === binding.service)?.status || 'planned'} label={false} /><strong>{binding.service}</strong></div>
        <div className="provider-kind" role="cell">{providerDisplayName(binding.provider)}</div>
        <div className="provider-configuration" role="cell">{binding.provider === 'remote' ? <code>{binding.remote?.url}</code> : binding.provider === 'local' ? <code>{binding.source}</code> : binding.provider === 'mock' ? <code>{binding.mock?.profile}</code> : <span>Portless managed</span>}</div>
        {binding.modifiedAt ? <time role="cell" dateTime={binding.modifiedAt} title={new Date(binding.modifiedAt).toLocaleString()}>{formatBindingTimestamp(binding.modifiedAt)}</time> : <time role="cell">—</time>}
        <div className="provider-actions table-row-actions" role="cell"><button type="button" onClick={() => onConfigure(binding.service)}>EDIT</button></div>
      </div>)}
      {!environment.bindings?.length && <div className="empty-row">No providers have been compiled for this environment.</div>}
    </div>
    <PanelPagination label="providers" pagination={providers} onPage={setPage} />
  </section>
}

export function sortProviderBindings(bindings: ComponentBinding[], sort: TableSort<ProviderBindingSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...bindings].sort((left, right) => {
    const serviceOrder = compareProviderText(left.service, right.service)
    let order = 0

    switch (sort.key) {
      case 'service':
        order = serviceOrder
        break
      case 'provider':
        order = compareProviderText(providerDisplayName(left.provider), providerDisplayName(right.provider))
        break
      case 'configuration':
        order = compareProviderText(providerConfigurationValue(left), providerConfigurationValue(right))
        break
      case 'modified':
        order = providerTimestampValue(left.modifiedAt) - providerTimestampValue(right.modifiedAt)
        break
    }

    return direction * order || serviceOrder
  })
}

function providerConfigurationValue(binding: ComponentBinding) {
  if (binding.provider === 'remote') return binding.remote?.url || ''
  if (binding.provider === 'local') return binding.source || ''
  if (binding.provider === 'mock') return binding.mock?.profile || ''
  return 'Portless managed'
}

function compareProviderText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
}

function providerTimestampValue(value?: string) {
  const timestamp = value ? Date.parse(value) : 0
  return Number.isNaN(timestamp) ? 0 : timestamp
}
