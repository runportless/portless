import { useEffect, useRef, useState } from 'react'
import { api, environmentPath, jsonBody, projectPath } from '../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import { paginateItems, PanelPagination } from '../components/PanelPagination'
import type { Environment, EnvironmentStatus, Operation, Project, ProjectSource } from '../types'
import { StatusMark } from '../components/Status'
import { AddProjectSourceModal, DeleteProjectSourceModal } from './SourceModals'

type ProjectSourceMutation = { warnings: string[]; configurationRequired: string[] }
type EnvironmentAction = 'up' | 'down'

export function ProjectsPage({ projects, environments, selectedProject, onNavigate, onChanged }: {
  projects: Project[]
  environments: Environment[]
  selectedProject?: Project
  onNavigate: (path: string) => void
  onChanged: () => Promise<void>
}) {
  const [cloneName, setCloneName] = useState('')
  const [cloneFrom, setCloneFrom] = useState('local')
  const [cloneError, setCloneError] = useState<ActionErrorDetails | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [sourceCreateOpen, setSourceCreateOpen] = useState(false)
  const [sourceDelete, setSourceDelete] = useState<ProjectSource | null>(null)
  const [sourceBusy, setSourceBusy] = useState(false)
  const [sourceError, setSourceError] = useState<ActionErrorDetails | null>(null)
  const [sourceNotice, setSourceNotice] = useState('')
  const [environmentActions, setEnvironmentActions] = useState<Map<string, EnvironmentAction>>(() => new Map())
  const [stoppingAll, setStoppingAll] = useState(false)
  const [environmentActionError, setEnvironmentActionError] = useState<ActionErrorDetails | null>(null)
  const [environmentPage, setEnvironmentPage] = useState(0)
  const [sourcePage, setSourcePage] = useState(0)
  const createButton = useRef<HTMLButtonElement>(null)
  const sourceCreateButton = useRef<HTMLButtonElement>(null)
  const sourceActionFocus = useRef<HTMLButtonElement | null>(null)
  const environmentActionsInFlight = useRef(new Set<string>())
  const stopAllInFlight = useRef(false)
  const nameInput = useRef<HTMLInputElement>(null)
  const shown = selectedProject ? environments.filter((item) => item.project === selectedProject.name) : environments
  const projectRows = projects.map((project) => projectOverview(project, environments))
  const summarized = selectedProject ? shown : projectRows
  const counts = summarized.reduce<Record<string, number>>((result, item) => { result[item.status] = (result[item.status] ?? 0) + 1; return result }, {})
  const sourceRows = selectedProject?.sources?.map((source) => ({
    ...source,
    checkouts: sourceCheckouts(shown, source.name),
    unbound: sourceUnboundEnvironments(shown, source.name, source.services ?? []),
  })) ?? []
  const paginatedEnvironments = paginateItems(shown, environmentPage, 10)
  const paginatedSources = paginateItems(sourceRows, sourcePage, 10)
  useEffect(() => {
    setEnvironmentPage(0)
    setSourcePage(0)
  }, [selectedProject?.name])
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
    setCloneName('')
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
  const closeSourceMutation = () => {
    if (sourceBusy) return
    setSourceCreateOpen(false)
    setSourceDelete(null)
    setSourceError(null)
    requestAnimationFrame(() => (sourceActionFocus.current || sourceCreateButton.current)?.focus())
  }
  const addProjectSource = async (name: string, path: string, environment: string) => {
    if (!selectedProject) return
    setSourceBusy(true)
    setSourceError(null)
    try {
      const result = await api<ProjectSourceMutation>(projectPath(selectedProject.name, '/sources'), {
        method: 'POST', ...jsonBody({ name, path, environment }),
      })
      await onChanged()
      const notes = [...(result.warnings || [])]
      if (result.configurationRequired?.length) notes.push(`Configure ${name} in ${result.configurationRequired.join(', ')}.`)
      setSourceNotice(notes.join(' ') || `${name} was added to ${selectedProject.name} and its checkout was configured for ${environment}.`)
      setSourceCreateOpen(false)
      requestAnimationFrame(() => sourceCreateButton.current?.focus())
    } catch (reason) {
      setSourceError(actionError("Source wasn't added", reason))
    } finally {
      setSourceBusy(false)
    }
  }
  const deleteProjectSource = async () => {
    if (!selectedProject || !sourceDelete) return
    setSourceBusy(true)
    setSourceError(null)
    try {
      await api(projectPath(selectedProject.name, `/sources/${encodeURIComponent(sourceDelete.name)}`), { method: 'DELETE' })
      await onChanged()
      setSourceNotice(`${sourceDelete.name} was deleted from ${selectedProject.name}.`)
      setSourceDelete(null)
      requestAnimationFrame(() => sourceCreateButton.current?.focus())
    } catch (reason) {
      setSourceError(actionError("Source wasn't deleted", reason))
    } finally {
      setSourceBusy(false)
    }
  }
  const markEnvironmentActions = (keys: string[], action: EnvironmentAction) => {
    setEnvironmentActions((current) => {
      const next = new Map(current)
      keys.forEach((key) => next.set(key, action))
      return next
    })
  }
  const clearEnvironmentActions = (keys: string[]) => {
    setEnvironmentActions((current) => {
      const next = new Map(current)
      keys.forEach((key) => next.delete(key))
      return next
    })
  }
  const runEnvironmentAction = async (environment: Environment, action: EnvironmentAction) => {
    const key = environmentKey(environment)
    const allowed = action === 'up' ? environment.status === 'stopped' : environmentCanStop(environment)
    if (!allowed || environmentActionsInFlight.current.has(key) || stopAllInFlight.current) return
    environmentActionsInFlight.current.add(key)
    markEnvironmentActions([key], action)
    setEnvironmentActionError(null)
    try {
      await runEnvironmentOperation(environment, action)
      await onChanged()
    } catch (reason) {
      setEnvironmentActionError(actionError(`${environment.name} wasn't ${action === 'up' ? 'started' : 'stopped'}`, reason))
      await onChanged().catch(() => undefined)
    } finally {
      environmentActionsInFlight.current.delete(key)
      clearEnvironmentActions([key])
    }
  }
  const stopAllEnvironments = async () => {
    if (stopAllInFlight.current || environmentActionsInFlight.current.size > 0) return
    const candidates = shown.filter(environmentCanStop)
    if (candidates.length === 0) return
    const keys = candidates.map(environmentKey)
    stopAllInFlight.current = true
    keys.forEach((key) => environmentActionsInFlight.current.add(key))
    setStoppingAll(true)
    markEnvironmentActions(keys, 'down')
    setEnvironmentActionError(null)
    try {
      const results = await Promise.allSettled(candidates.map((environment) => runEnvironmentOperation(environment, 'down')))
      const failures = results.flatMap((result, index) => result.status === 'rejected' ? [{ environment: candidates[index], reason: result.reason }] : [])
      await onChanged()
      if (failures.length > 0) {
        const first = failures[0]
        const details = actionError(`${failures.length} environment${failures.length === 1 ? " wasn't" : "s weren't"} stopped`, first.reason)
        if (failures.length > 1) details.message = `${first.environment.name}: ${details.message} ${failures.length - 1} additional environment${failures.length === 2 ? '' : 's'} also failed.`
        setEnvironmentActionError(details)
      }
    } catch (reason) {
      setEnvironmentActionError(actionError("The environments weren't stopped", reason))
    } finally {
      keys.forEach((key) => environmentActionsInFlight.current.delete(key))
      stopAllInFlight.current = false
      setStoppingAll(false)
      clearEnvironmentActions(keys)
    }
  }
  const stoppableEnvironments = shown.filter(environmentCanStop)
  return <div className="page projects-page">
    <div className="page-heading projects-heading">
      <header className="projects-heading__title">
        {selectedProject && <div className="eyebrow">PROJECT</div>}
        <div className="projects-heading__line"><h1>{selectedProject?.name || 'Projects'}</h1></div>
      </header>
      <div className="projects-heading__controls">
        <div className="page-heading__summary"><span>{counts.failed ?? 0} failed</span><b>·</b><span>{counts.degraded ?? 0} degraded</span><b>·</b><span>{counts.recovering ?? 0} recovering</span><b>·</b><span>{counts.starting ?? 0} starting</span><b>·</b><span>{counts.healthy ?? 0} healthy</span></div>
        {selectedProject && <button className="button project-stop-all" type="button" aria-label={`Stop all ${selectedProject.name} environments`} disabled={stoppingAll || environmentActions.size > 0 || stoppableEnvironments.length === 0} onClick={() => void stopAllEnvironments()}>{stoppingAll ? 'STOPPING…' : 'STOP ALL'}</button>}
      </div>
    </div>
    {environmentActionError && selectedProject && <ActionErrorNotice error={environmentActionError} onDismiss={() => setEnvironmentActionError(null)} />}
    {selectedProject ? shown.length > 0 ? <section className="panel environments-table">
      <div className="panel-title"><span>ENVIRONMENTS</span><button ref={createButton} className="button button--primary button--small panel-create-button create-environment-button" type="button" aria-haspopup="dialog" onClick={openCreate}>CREATE ENVIRONMENT</button></div>
      <div className="environment-row-shell environment-row-shell--header"><div className="table-row table-row--header environment-row"><span>Status</span><span>Project</span><span>Environment</span><span>Ready</span><span>Remote</span><span>Modified</span><span>Why</span></div><span aria-hidden="true" /></div>
      {paginatedEnvironments.items.map((environment) => {
        const ready = environment.services.filter((service) => service.status === 'ready').length
        const remote = environment.bindings?.filter((binding) => binding.provider === 'remote').length || 0
        const pendingAction = environmentActions.get(environmentKey(environment))
        const starting = pendingAction === 'up' || environment.status === 'starting'
        const stopping = pendingAction === 'down' || environment.status === 'stopping'
        const stopped = environment.status === 'stopped' && !pendingAction
        const recovering = environment.status === 'recovering' && !pendingAction
        const startAction = stopped || starting
        return <div className="environment-row-shell" key={`${environment.project}/${environment.name}`}>
          <button className="table-row environment-row" onClick={() => onNavigate(environmentRoute(environment))}>
            <span><StatusMark status={environment.status} /></span><strong>{environment.project}</strong><code>{environment.name}</code><span>{ready}/{environment.services.length}</span><span className={remote ? 'warning-text' : ''}>{remote || '—'}</span><time dateTime={environment.updatedAt} title={new Date(environment.updatedAt).toLocaleString()}>{formatTimestamp(environment.updatedAt)}</time><span className={environment.issues?.length ? 'warning-text truncate' : 'muted truncate'}>{environment.reason || environment.issues?.[0]?.message || (environment.status === 'stopped' ? 'not running' : environment.status === 'healthy' ? 'all required services are ready' : 'state is being reconciled')}</span>
          </button>
          <div className="table-row-actions environment-row-actions"><button type="button" aria-label={`${startAction ? 'Start' : 'Stop'} ${environment.name}`} disabled={stoppingAll || !!pendingAction || starting || stopping || recovering} onClick={() => void runEnvironmentAction(environment, startAction ? 'up' : 'down')}>{starting ? 'STARTING…' : stopping ? 'STOPPING…' : recovering ? 'RECOVERING…' : stopped ? 'START' : 'STOP'}</button></div>
        </div>
      })}
      <PanelPagination label="environments" pagination={paginatedEnvironments} onPage={setEnvironmentPage} />
    </section> : <section className="empty-environment panel"><div><div className="eyebrow">No environments yet</div><h2>Start one repository or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>
      : projectRows.length > 0 ? <section className="panel projects-table">
        <div className="panel-title"><span>PROJECTS</span></div>
        <div className="table-row table-row--header project-index-row"><span>Status</span><span>Project</span><span>Environments</span><span>Sources</span><span>Services</span><span>Last updated</span></div>
        {projectRows.map((row) => <button className="table-row project-index-row" key={row.project.name} onClick={() => onNavigate(projectRoute(row.project.name))}>
          <span><StatusMark status={row.status} /></span><strong>{row.project.name}</strong><code className="truncate" title={row.environmentNames}>{row.environmentNames || '—'}</code><span>{row.sourceCount || '—'}</span><span>{row.serviceCount || '—'}</span>{row.updatedAt ? <time dateTime={row.updatedAt}>{formatTimestamp(row.updatedAt)}</time> : <span>—</span>}
        </button>)}
      </section> : <section className="empty-environment panel"><div><div className="eyebrow">No projects yet</div><h2>Start one repository or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>}
    {sourceNotice && selectedProject && <div className="mock-warning source-add-notice"><strong>SOURCE CHANGE</strong><span>{sourceNotice}</span><button type="button" onClick={() => setSourceNotice('')}>DISMISS</button></div>}
    {selectedProject && <section className="panel project-sources-panel">
      <div className="panel-title"><span>SOURCES</span><button ref={sourceCreateButton} className="button button--primary button--small panel-create-button" type="button" aria-haspopup="dialog" onClick={() => { setSourceError(null); setSourceCreateOpen(true) }}>ADD SOURCE</button></div>
      <div className="table-row table-row--header project-source-row"><span>Name</span><span>Path</span><span>Services</span><span aria-hidden="true" /></div>
      {paginatedSources.items.map((source) => <div className="table-row project-source-row" key={source.name}>
        <div className="checkout-source"><StatusMark status={projectSourceStatus(source)} label={false} /><strong>{source.name}</strong></div>
        <div className="project-source-bindings">{source.checkouts.map((checkout) => <div className="project-source-binding" key={checkout.path}>
          <code className="truncate" title={checkout.path}>{checkout.path}</code>
        </div>)}{source.unbound.map((environment) => <div className="project-source-binding" key={`unbound-${environment.name}`}>
          <span className={environment.configurationRequired ? 'warning-text truncate' : 'muted truncate'}>{environment.configurationRequired ? 'configuration required' : 'not bound locally'}</span>
        </div>)}</div>
        <span className="muted truncate" title={source.services?.join(', ')}>{source.services?.join(', ') || '—'}</span>
        <div className="table-row-actions"><button type="button" disabled={sourceBusy} onClick={(event) => { sourceActionFocus.current = event.currentTarget; setSourceError(null); setSourceDelete(source) }}>DELETE</button></div>
      </div>)}
      {sourceRows.length === 0 && <div className="empty-row">No sources are registered with this project.</div>}
      <PanelPagination label="sources" pagination={paginatedSources} onPage={setSourcePage} />
    </section>}
    {createOpen && selectedProject && <div className="modal-backdrop form-modal-backdrop" role="presentation" onMouseDown={closeCreate}>
      <section className="form-modal create-environment-modal" role="dialog" aria-modal="true" aria-labelledby="create-environment-title" aria-describedby="create-environment-description" onMouseDown={(event) => event.stopPropagation()}>
        <header><div><div className="eyebrow">NEW ENVIRONMENT</div><h2 id="create-environment-title">Create environment</h2></div><button className="icon-button" type="button" aria-label="Close create environment" disabled={creating} onClick={closeCreate}>×</button></header>
        <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); void clone() }}>
          <p id="create-environment-description">Clone providers and source bindings, then customize the result.</p>
          <div className="form-modal__fields create-environment-form__fields">
            <label><span>NAME</span><input ref={nameInput} name="portless-environment-name" value={cloneName} placeholder="qa-local" required autoComplete="off" spellCheck="false" disabled={creating} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => setCloneName(event.target.value)} /></label>
            <label><span>CLONE FROM</span><select value={cloneFrom} disabled={creating} onChange={(event) => setCloneFrom(event.target.value)}>{shown.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
          </div>
          {cloneError && <ActionErrorNotice error={cloneError} onDismiss={() => setCloneError(null)} />}
          <footer><button className="button button--quiet" type="button" disabled={creating} onClick={closeCreate}>CANCEL</button><button className="button button--primary" type="submit" disabled={creating || !cloneName.trim()}>{creating ? 'CREATING…' : 'CREATE ENVIRONMENT'}</button></footer>
        </form>
      </section>
    </div>}
    {sourceCreateOpen && selectedProject && <AddProjectSourceModal project={selectedProject} environments={shown} busy={sourceBusy} error={sourceError} onDismissError={() => setSourceError(null)} onClose={closeSourceMutation} onAdd={addProjectSource} />}
    {sourceDelete && selectedProject && <DeleteProjectSourceModal project={selectedProject} source={sourceDelete} environments={shown} busy={sourceBusy} error={sourceError} onDismissError={() => setSourceError(null)} onClose={closeSourceMutation} onDelete={deleteProjectSource} />}
  </div>
}

function environmentRoute(environment: Pick<Environment, 'project' | 'name'>) { return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}` }
function projectRoute(project: string) { return `/projects/${encodeURIComponent(project)}` }
function environmentKey(environment: Pick<Environment, 'project' | 'name'>) { return `${environment.project}/${environment.name}` }
function environmentCanStop(environment: Pick<Environment, 'status'>) { return !['stopped', 'stopping', 'recovering'].includes(environment.status) }

async function runEnvironmentOperation(environment: Environment, action: EnvironmentAction) {
  let operation = await api<Operation>(environmentPath(environment, `/${action}`), {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID(), ...(action === 'down' ? { 'Content-Type': 'application/json' } : {}) },
    ...(action === 'down' ? { body: JSON.stringify({ removeVolumes: false }) } : {}),
  })
  while (operation.state === 'running') {
    await new Promise((resolve) => window.setTimeout(resolve, 250))
    operation = await api<Operation>(environmentPath(environment, `/operations/${operation.number}`))
  }
  if (operation.state !== 'succeeded') throw new Error(operation.error || `${environment.name} ${action === 'up' ? 'startup' : 'shutdown'} ${operation.state}`)
}

function projectOverview(project: Project, environments: Environment[]) {
  const owned = environments.filter((environment) => environment.project === project.name)
  const status = aggregateProjectStatus(owned)
  const services = new Set([
    ...(project.services ?? []).map((service) => service.name),
    ...(project.sources ?? []).flatMap((source) => source.services ?? []),
    ...owned.flatMap((environment) => environment.services.map((service) => service.name)),
  ])
  return {
    project,
    status,
    environmentNames: owned.map((environment) => environment.name).sort().join(', '),
    sourceCount: project.sources?.length ?? 0,
    serviceCount: services.size,
    updatedAt: newestTimestamp([project.updatedAt, ...owned.map((environment) => environment.updatedAt)]),
  }
}

function aggregateProjectStatus(environments: Environment[]): EnvironmentStatus {
  if (environments.length === 0) return 'unknown'
  const active = environments.filter((environment) => environment.status !== 'stopped')
  if (active.length === 0) return 'stopped'
  const states = new Set(active.map((environment) => environment.status))
  const priority: EnvironmentStatus[] = ['failed', 'degraded', 'recovering', 'starting', 'stopping', 'unknown']
  const urgent = priority.find((status) => states.has(status))
  if (urgent) return urgent
  if (active.every((environment) => environment.status === 'healthy')) return 'healthy'
  return 'degraded'
}

function newestTimestamp(values: Array<string | undefined>) {
  return values.reduce<string | undefined>((latest, value) => {
    if (!value || !Number.isFinite(new Date(value).getTime())) return latest
    if (!latest || new Date(value).getTime() > new Date(latest).getTime()) return value
    return latest
  }, undefined)
}

function formatTimestamp(value: string) {
  return new Date(value).toLocaleString([], { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

function sourceCheckouts(environments: Environment[], sourceName: string) {
  const grouped = new Map<string, { path: string; statuses: string[] }>()
  for (const environment of environments) {
    for (const source of environment.sources ?? []) {
      if (source.name !== sourceName) continue
      const checkout = grouped.get(source.path) ?? { path: source.path, statuses: [] }
      checkout.statuses.push(source.status)
      grouped.set(source.path, checkout)
    }
  }
  return [...grouped.values()].map((checkout) => ({
    path: checkout.path,
    status: checkout.statuses.every((status) => status === checkout.statuses[0]) ? checkout.statuses[0] : 'unknown',
  }))
}

function sourceUnboundEnvironments(environments: Environment[], sourceName: string, services: string[]) {
  const serviceNames = new Set(services.map((service) => service.toLowerCase()))
  return environments.flatMap((environment) => {
    if ((environment.sources ?? []).some((source) => source.name.toLowerCase() === sourceName.toLowerCase())) return []
    const configurationRequired = (environment.issues ?? []).some((issue) =>
      (issue.code === 'MISSING_BINDING' || issue.code === 'MISSING_SOURCE') && Boolean(issue.subject) && serviceNames.has(issue.subject!.toLowerCase()),
    )
    return [{ name: environment.name, configurationRequired }]
  })
}

function projectSourceStatus(source: { checkouts: Array<{ status: string }>; unbound: Array<{ configurationRequired: boolean }> }) {
  if (source.checkouts.some((checkout) => ['failed', 'exited', 'unreachable'].includes(checkout.status))) return 'failed'
  if (source.unbound.some((environment) => environment.configurationRequired) || source.checkouts.some((checkout) => checkout.status !== 'ready')) return 'degraded'
  return source.checkouts.length > 0 ? 'ready' : 'unknown'
}
