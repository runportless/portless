import { useEffect, useMemo, useState } from 'react'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import { StatusMark } from '../../../components/Status'
import type { Environment } from '../../../types'
import { formatBindingTimestamp, providerDisplayName } from './bindingPresentation'

export function ProviderBindingsTable({ environment, onConfigure }: { environment: Environment; onConfigure: (service?: string) => void }) {
  const [page, setPage] = useState(0)
  const providers = useMemo(() => paginateItems(environment.bindings || [], page, 5), [environment.bindings, page])

  useEffect(() => setPage(0), [environment.project, environment.name])

  return <section className="panel experiment-list configured-providers-panel">
    <div className="panel-title"><span>PROVIDERS</span><button className="button button--primary button--small panel-create-button configure-provider-button" type="button" aria-haspopup="dialog" disabled={!environment.services.length} onClick={() => onConfigure()}>CONFIGURE PROVIDER</button></div>
    <div className="provider-table" role="table" aria-label="Configured providers">
      <div className="provider-row provider-row--header" role="row"><span role="columnheader">Service</span><span role="columnheader">Provider</span><span role="columnheader">Configuration</span><span role="columnheader">Modified</span><span role="columnheader" aria-label="Row actions" /></div>
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
