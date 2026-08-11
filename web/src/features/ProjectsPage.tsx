import { useState } from 'react'
import { api, APIError, jsonBody } from '../api'
import type { Environment, Project, RuntimeStatus } from '../types'
import { relativeTime, StatusMark } from '../components/Status'

export function ProjectsPage({ projects, environments, selectedProject, runtime, onNavigate, onRuntimeChange, onRuntimeStart, onChanged }: {
  projects: Project[]
  environments: Environment[]
  selectedProject?: Project
  runtime: RuntimeStatus | null
  onNavigate: (path: string) => void
  onRuntimeChange: (preference: RuntimeStatus['preference']) => Promise<void>
  onRuntimeStart: () => Promise<void>
  onChanged: () => Promise<void>
}) {
  const [runtimeError, setRuntimeError] = useState('')
  const [changingRuntime, setChangingRuntime] = useState(false)
  const [cloneName, setCloneName] = useState('qa-local')
  const [cloneFrom, setCloneFrom] = useState('local')
  const [cloneError, setCloneError] = useState('')
  const shown = selectedProject ? environments.filter((item) => item.project === selectedProject.name) : environments
  const counts = shown.reduce<Record<string, number>>((result, environment) => { result[environment.status] = (result[environment.status] ?? 0) + 1; return result }, {})
  const changeRuntime = async (preference: RuntimeStatus['preference']) => {
    setChangingRuntime(true); setRuntimeError('')
    try { await onRuntimeChange(preference) }
    catch (error) { setRuntimeError(error instanceof APIError ? error.message : String(error)) }
    finally { setChangingRuntime(false) }
  }
  const startRuntime = async () => {
    setChangingRuntime(true); setRuntimeError('')
    try { await onRuntimeStart() }
    catch (error) { setRuntimeError(error instanceof APIError ? error.message : String(error)) }
    finally { setChangingRuntime(false) }
  }
  const clone = async () => {
    if (!selectedProject) return
    setCloneError('')
    try {
      await api('/environments', { method: 'POST', ...jsonBody({ project: selectedProject.name, name: cloneName, from: cloneFrom }) })
      await onChanged()
      onNavigate(environmentRoute({ project: selectedProject.name, name: cloneName }))
    } catch (error) { setCloneError(error instanceof Error ? error.message : String(error)) }
  }
  return <div className="page projects-page">
    <div className="page-heading">
      <div><div className="eyebrow">{selectedProject ? 'PROJECT' : 'LOCAL CONTROL PLANE'}</div><h1>{selectedProject?.name || 'Projects & environments'}</h1><span>{selectedProject ? `${selectedProject.sources?.length || 0} sources · ${shown.length} environments` : `${projects.length} projects · ${environments.length} environments`}</span></div>
      <div className="page-heading__summary"><span>{counts.failed ?? 0} failed</span><b>·</b><span>{counts.degraded ?? 0} degraded</span><b>·</b><span>{counts.starting ?? 0} starting</span><b>·</b><span>{counts.healthy ?? 0} healthy</span></div>
    </div>
    {selectedProject && <section className="panel source-panel">
      <div className="panel-title"><span>PROJECT SOURCES</span><small>one logical application, many repositories</small></div>
      <div className="source-list">{selectedProject.sources?.map((source) => <div key={source.name}><strong>{source.name}</strong><span>{source.services?.join(', ') || 'no process services'}</span></div>)}</div>
      <p className="muted">Each environment clones this topology, then chooses a provider for every component independently.</p>
    </section>}
    {shown.length > 0 ? <section className="panel environments-table">
      <div className="table-row table-row--header environment-row"><span>Status</span><span>Project</span><span>Environment</span><span>Ready</span><span>Remote</span><span>Age</span><span>Why</span></div>
      {shown.map((environment) => {
        const ready = environment.services.filter((service) => service.status === 'ready').length
        const remote = environment.bindings?.filter((binding) => binding.provider === 'remote').length || 0
        return <button className="table-row environment-row" key={`${environment.project}/${environment.name}`} onClick={() => onNavigate(environmentRoute(environment))}>
			<span><StatusMark status={environment.status} label={false} /></span><strong>{environment.project}</strong><code>{environment.name}</code><span>{ready}/{environment.services.length}</span><span className={remote ? 'warning-text' : ''}>{remote || '—'}</span><span>{relativeTime(environment.updatedAt)}</span><span className="muted truncate">{environment.reason || (environment.status === 'stopped' ? 'not running' : environment.status === 'healthy' ? 'all required services are ready' : 'state is being reconciled')}</span>
        </button>
      })}
    </section> : <section className="empty-environment panel"><div className="empty-environment__graphic" aria-hidden="true"><span>app</span><i /><span>db</span><i /><span>cache</span></div><div><div className="eyebrow">No environments yet</div><h2>Start one repository—or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>}
    {selectedProject && shown.length > 0 && <section className="panel clone-panel"><div><div className="eyebrow">NEW ENVIRONMENT</div><p>Clone providers and source bindings, then customize the result.</p></div><label><span>NAME</span><input value={cloneName} onChange={(event) => setCloneName(event.target.value)} /></label><label><span>CLONE FROM</span><select value={cloneFrom} onChange={(event) => setCloneFrom(event.target.value)}>{shown.map((item) => <option key={item.name}>{item.name}</option>)}</select></label><button className="button button--primary" onClick={clone}>CREATE</button>{cloneError && <span className="danger-text">{cloneError}</span>}</section>}
    {runtime && <section className="panel runtime-panel"><div className="panel-title"><span>CONTAINER RUNTIME</span><small>preference: {runtime.preference}</small></div><div className="runtime-panel__body"><div className="runtime-summary"><StatusMark status={runtime.state} /><strong>{runtime.selected ?? 'none selected'}</strong><span>{runtime.version ? `v${runtime.version}` : runtime.reason}</span></div><div className="runtime-candidates">{runtime.candidates.map((candidate) => <div className={candidate.name === runtime.selected ? 'runtime-candidate is-selected' : 'runtime-candidate'} key={candidate.name}><div><StatusMark status={candidate.state} label={false} /><strong>{candidate.name}</strong><small>{candidate.version ? `v${candidate.version}` : candidate.state}</small></div><p>{candidate.reason || (candidate.state === 'ready' ? 'Engine is available.' : 'Engine is unavailable.')}</p><button className="button button--small" disabled={changingRuntime || runtime.preference === candidate.name} onClick={() => void changeRuntime(candidate.name)}>USE {candidate.name.toUpperCase()}</button></div>)}</div><div className="runtime-actions"><button className="button button--small" disabled={changingRuntime || runtime.preference === 'auto'} onClick={() => void changeRuntime('auto')}>USE AUTOMATIC SELECTION</button>{runtime.state !== 'ready' && <button className="button button--small button--primary" disabled={changingRuntime} onClick={() => void startRuntime()}>START RUNTIME</button>}{runtimeError && <span className="danger-text">{runtimeError}</span>}</div></div></section>}
    <section className="principles-grid"><div className="panel note-panel"><div className="eyebrow">Reusable topology</div><p>A project describes the application. Environments reuse it without copying repositories or inventing ports.</p></div><div className="panel note-panel"><div className="eyebrow">Provider per component</div><p>An environment can run a service locally, manage a container, or route HTTP(S) to an explicitly classified remote target.</p></div><div className="panel note-panel"><div className="eyebrow">Readable isolation</div><p>Applications use <code>service.environment.project.localhost</code>; process and dependency ports remain private.</p></div></section>
  </div>
}

function environmentRoute(environment: Pick<Environment, 'project' | 'name'>) { return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}` }
