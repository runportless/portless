import { useEffect, useRef, useState } from 'react'
import { api, jsonBody } from '../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import type { Environment, Project } from '../types'
import { relativeTime, StatusMark } from '../components/Status'

export function ProjectsPage({ projects, environments, selectedProject, onNavigate, onChanged }: {
  projects: Project[]
  environments: Environment[]
  selectedProject?: Project
  onNavigate: (path: string) => void
  onChanged: () => Promise<void>
}) {
  const [cloneName, setCloneName] = useState('qa-local')
  const [cloneFrom, setCloneFrom] = useState('local')
  const [cloneError, setCloneError] = useState<ActionErrorDetails | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const createButton = useRef<HTMLButtonElement>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const shown = selectedProject ? environments.filter((item) => item.project === selectedProject.name) : environments
  const counts = shown.reduce<Record<string, number>>((result, environment) => { result[environment.status] = (result[environment.status] ?? 0) + 1; return result }, {})
  const sourceRows = selectedProject?.sources?.map((source) => ({ ...source, checkouts: sourceCheckouts(shown, source.name) })) ?? []
  useEffect(() => {
    if (createOpen) nameInput.current?.focus()
  }, [createOpen])
  useEffect(() => {
    if (!createOpen) return
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !creating) {
        setCreateOpen(false)
        setCloneError(null)
        requestAnimationFrame(() => createButton.current?.focus())
      }
    }
    document.addEventListener('keydown', closeOnEscape)
    return () => document.removeEventListener('keydown', closeOnEscape)
  }, [createOpen, creating])
  const openCreate = () => {
    if (!shown.some((environment) => environment.name === cloneFrom)) setCloneFrom(shown[0]?.name ?? '')
    setCloneError(null)
    setCreateOpen(true)
  }
  const closeCreate = () => {
    if (creating) return
    setCreateOpen(false)
    setCloneError(null)
    requestAnimationFrame(() => createButton.current?.focus())
  }
  const clone = async () => {
    if (!selectedProject) return
    const name = cloneName.trim()
    if (!name) {
      setCloneError(actionError("Environment wasn't created", new Error('Enter an environment name.')))
      nameInput.current?.focus()
      return
    }
    setCloneError(null)
    setCreating(true)
    try {
      await api('/environments', { method: 'POST', ...jsonBody({ project: selectedProject.name, name, from: cloneFrom }) })
      await onChanged()
      setCreateOpen(false)
      onNavigate(environmentRoute({ project: selectedProject.name, name }))
    } catch (error) {
      setCloneError(actionError("Environment wasn't created", error))
    } finally {
      setCreating(false)
    }
  }
  return <div className="page projects-page">
    <div className="page-heading projects-heading">
      <header className="projects-heading__title">
        {selectedProject && <div className="eyebrow">PROJECT</div>}
        <div className="projects-heading__line"><h1>{selectedProject?.name || 'Projects'}</h1>{!selectedProject && <span>{projects.length} projects · {environments.length} environments</span>}</div>
      </header>
      <div className="page-heading__summary"><span>{counts.failed ?? 0} failed</span><b>·</b><span>{counts.degraded ?? 0} degraded</span><b>·</b><span>{counts.recovering ?? 0} recovering</span><b>·</b><span>{counts.starting ?? 0} starting</span><b>·</b><span>{counts.healthy ?? 0} healthy</span></div>
    </div>
    {shown.length > 0 ? <section className="panel environments-table">
      <div className="panel-title"><span>ENVIRONMENTS</span><div className="panel-title__actions"><small>{shown.length} environment{shown.length === 1 ? '' : 's'}</small>{selectedProject && <button ref={createButton} className="button button--primary button--small create-environment-button" type="button" aria-haspopup="dialog" onClick={openCreate}>CREATE ENVIRONMENT</button>}</div></div>
      <div className="table-row table-row--header environment-row"><span>Status</span><span>Project</span><span>Environment</span><span>Ready</span><span>Remote</span><span>Age</span><span>Why</span></div>
      {shown.map((environment) => {
        const ready = environment.services.filter((service) => service.status === 'ready').length
        const remote = environment.bindings?.filter((binding) => binding.provider === 'remote').length || 0
        return <button className="table-row environment-row" key={`${environment.project}/${environment.name}`} onClick={() => onNavigate(environmentRoute(environment))}>
          <span><StatusMark status={environment.status} label={false} /></span><strong>{environment.project}</strong><code>{environment.name}</code><span>{ready}/{environment.services.length}</span><span className={remote ? 'warning-text' : ''}>{remote || '—'}</span><span>{relativeTime(environment.updatedAt)}</span><span className="muted truncate">{environment.reason || (environment.status === 'stopped' ? 'not running' : environment.status === 'healthy' ? 'all required services are ready' : 'state is being reconciled')}</span>
        </button>
      })}
    </section> : <section className="empty-environment panel"><div><div className="eyebrow">No environments yet</div><h2>Start one repository or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>}
    {selectedProject && sourceRows.length > 0 && <section className="panel project-sources-panel">
      <div className="panel-title"><span>PROJECT SOURCES</span><small>{sourceRows.length} source{sourceRows.length === 1 ? '' : 's'}</small></div>
      <div className="table-row table-row--header project-source-row"><span>Source</span><span>Filesystem bindings</span><span>Services</span></div>
      {sourceRows.map((source) => <div className="table-row project-source-row" key={source.name}>
        <strong>{source.name}</strong>
        <div className="project-source-bindings">{source.checkouts.length > 0 ? source.checkouts.map((checkout) => <div className="project-source-binding" key={checkout.path}>
          <StatusMark status={checkout.status} label={false} /><code className="truncate" title={checkout.path}>{checkout.path}</code><small>{checkout.environments.join(', ')}</small>
        </div>) : <span className="muted">not bound in an environment</span>}</div>
        <span className="muted truncate" title={source.services?.join(', ')}>{source.services?.join(', ') || '—'}</span>
      </div>)}
    </section>}
    {createOpen && selectedProject && <div className="modal-backdrop create-environment-backdrop" role="presentation" onMouseDown={closeCreate}>
      <section className="create-environment-modal" role="dialog" aria-modal="true" aria-labelledby="create-environment-title" aria-describedby="create-environment-description" onMouseDown={(event) => event.stopPropagation()}>
        <header><div><div className="eyebrow">NEW ENVIRONMENT</div><h2 id="create-environment-title">Create environment</h2></div><button className="icon-button" type="button" aria-label="Close create environment" disabled={creating} onClick={closeCreate}>×</button></header>
        <form onSubmit={(event) => { event.preventDefault(); void clone() }}>
          <p id="create-environment-description">Clone providers and source bindings, then customize the result.</p>
          <div className="create-environment-form__fields">
            <label><span>NAME</span><input ref={nameInput} value={cloneName} required autoComplete="off" spellCheck="false" disabled={creating} onChange={(event) => setCloneName(event.target.value)} /></label>
            <label><span>CLONE FROM</span><select value={cloneFrom} disabled={creating} onChange={(event) => setCloneFrom(event.target.value)}>{shown.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
          </div>
          {cloneError && <ActionErrorNotice error={cloneError} onDismiss={() => setCloneError(null)} />}
          <footer><button className="button button--quiet" type="button" disabled={creating} onClick={closeCreate}>CANCEL</button><button className="button button--primary" type="submit" disabled={creating || !cloneName.trim()}>{creating ? 'CREATING…' : 'CREATE ENVIRONMENT'}</button></footer>
        </form>
      </section>
    </div>}
  </div>
}

function environmentRoute(environment: Pick<Environment, 'project' | 'name'>) { return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}` }

function sourceCheckouts(environments: Environment[], sourceName: string) {
  const grouped = new Map<string, { path: string; environments: string[]; statuses: string[] }>()
  for (const environment of environments) {
    for (const source of environment.sources ?? []) {
      if (source.name !== sourceName) continue
      const checkout = grouped.get(source.path) ?? { path: source.path, environments: [], statuses: [] }
      checkout.environments.push(environment.name)
      checkout.statuses.push(source.status)
      grouped.set(source.path, checkout)
    }
  }
  return [...grouped.values()].map((checkout) => ({
    path: checkout.path,
    environments: checkout.environments,
    status: checkout.statuses.every((status) => status === checkout.statuses[0]) ? checkout.statuses[0] : 'unknown',
  }))
}
