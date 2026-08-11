import { useState } from 'react'
import { APIError } from '../api'
import type { Project, RuntimeStatus } from '../types'
import { relativeTime, StatusMark } from '../components/Status'

export function ProjectsPage({ projects, runtime, onNavigate, onRuntimeChange, onRuntimeStart }: {
  projects: Project[]
  runtime: RuntimeStatus | null
  onNavigate: (path: string) => void
  onRuntimeChange: (preference: RuntimeStatus['preference']) => Promise<void>
  onRuntimeStart: () => Promise<void>
}) {
  const [runtimeError, setRuntimeError] = useState('')
  const [changingRuntime, setChangingRuntime] = useState(false)
  const counts = projects.reduce<Record<string, number>>((result, project) => {
    result[project.status] = (result[project.status] ?? 0) + 1
    return result
  }, {})
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
  return (
    <div className="page projects-page">
      <div className="page-heading">
        <div><h1>Environments</h1><span>{projects.length} local {projects.length === 1 ? 'project' : 'projects'}</span></div>
        <div className="page-heading__summary">
          <span>{counts.failed ?? 0} failed</span><b>·</b><span>{counts.degraded ?? 0} degraded</span><b>·</b><span>{counts.starting ?? 0} starting</span><b>·</b><span>{counts.healthy ?? 0} healthy</span>
        </div>
      </div>
      {projects.length > 0 ? (
        <section className="panel environments-table">
          <div className="table-row table-row--header environment-row"><span>Status</span><span>Project</span><span>Services</span><span>Path</span><span>Age</span><span>Why</span><span /></div>
          {projects.map((project) => {
            const ready = project.services.filter((service) => service.status === 'ready').length
            return <button className="table-row environment-row" key={project.name} onClick={() => onNavigate(`/projects/${project.name}`)}>
              <span><StatusMark status={project.status} label={false} /></span>
              <strong>{project.name}</strong>
              <span>{ready}/{project.services.length} ready</span>
              <code title={project.path}>{project.path}</code>
              <span>{relativeTime(project.updatedAt)}</span>
              <span className="muted truncate">{project.reason || 'all required services are ready'}</span>
              <span className="row-action">OPEN</span>
            </button>
          })}
        </section>
      ) : (
        <section className="empty-environment panel">
          <div className="empty-environment__graphic" aria-hidden="true"><span>app</span><i /><span>db</span><i /><span>cache</span></div>
          <div>
            <div className="eyebrow">No environments yet</div>
            <h2>Start from the repository, not a config file.</h2>
            <p>Open a Spring Boot or NestJS checkout and run:</p>
            <pre><span>$</span> portless up</pre>
            <p>Portless will discover the services and dependencies, start the environment, and open its dashboard here.</p>
          </div>
        </section>
      )}
      {runtime && <section className="panel runtime-panel">
        <div className="panel-title"><span>CONTAINER RUNTIME</span><small>preference: {runtime.preference}</small></div>
        <div className="runtime-panel__body">
          <div className="runtime-summary">
            <StatusMark status={runtime.state} />
            <strong>{runtime.selected ?? 'none selected'}</strong>
            <span>{runtime.version ? `v${runtime.version}` : runtime.reason}</span>
          </div>
          <div className="runtime-candidates">
            {runtime.candidates.map((candidate) => <div className={candidate.name === runtime.selected ? 'runtime-candidate is-selected' : 'runtime-candidate'} key={candidate.name}>
              <div><StatusMark status={candidate.state} label={false} /><strong>{candidate.name}</strong><small>{candidate.version ? `v${candidate.version}` : candidate.state}</small></div>
              <p>{candidate.reason || (candidate.state === 'ready' ? 'Engine is available.' : 'Engine is unavailable.')}</p>
              <button className="button button--small" disabled={changingRuntime || runtime.preference === candidate.name} onClick={() => void changeRuntime(candidate.name)}>USE {candidate.name.toUpperCase()}</button>
            </div>)}
          </div>
          <div className="runtime-actions">
            <button className="button button--small" disabled={changingRuntime || runtime.preference === 'auto'} onClick={() => void changeRuntime('auto')}>USE AUTOMATIC SELECTION</button>
            {runtime.state !== 'ready' && <button className="button button--small button--primary" disabled={changingRuntime} onClick={() => void startRuntime()}>START RUNTIME</button>}
            {runtimeError && <span className="danger-text">{runtimeError}</span>}
          </div>
        </div>
      </section>}
      <section className="principles-grid">
        <div className="panel note-panel"><div className="eyebrow">Readable isolation</div><p>Each checkout receives one machine-local project name. Services route through <code>service.project.localhost</code>, while process and dependency ports stay private.</p></div>
        <div className="panel note-panel"><div className="eyebrow">Local by construction</div><p>The daemon, SQLite state, traffic, recordings, and faults remain on this machine. No account or hosted control plane is involved.</p></div>
        <div className="panel note-panel"><div className="eyebrow">Static discovery</div><p>Discovery reads known project manifests without running builds or package scripts, then starts the resulting local environment directly.</p></div>
      </section>
    </div>
  )
}
