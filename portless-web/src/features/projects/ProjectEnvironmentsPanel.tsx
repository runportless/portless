import { useEffect, useRef, useState } from 'react'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { StatusMark } from '../../components/Status'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { CreateEnvironmentDialog } from './CreateEnvironmentDialog'
import { environmentCanStop, environmentKey, environmentRoute, runEnvironmentOperation, type EnvironmentAction } from './projectOperations'
import { formatTimestamp, statusCounts } from './projectPresentation'

export function ProjectEnvironmentsPanel({ project, environments, onNavigate, onChanged }: {
  project: Project
  environments: Environment[]
  onNavigate: (path: string) => void
  onChanged: () => Promise<void>
}) {
  const [createOpen, setCreateOpen] = useState(false)
  const [environmentActions, setEnvironmentActions] = useState<Map<string, EnvironmentAction>>(() => new Map())
  const [stoppingAll, setStoppingAll] = useState(false)
  const [actionErrorDetails, setActionErrorDetails] = useState<ActionErrorDetails | null>(null)
  const [page, setPage] = useState(0)
  const environmentActionsInFlight = useRef(new Set<string>())
  const stopAllInFlight = useRef(false)
  const counts = statusCounts(environments)
  const pagination = paginateItems(environments, page, 10)
  const stoppableEnvironments = environments.filter(environmentCanStop)

  useEffect(() => {
    setPage(0)
  }, [project.name])

  const markActions = (keys: string[], action: EnvironmentAction) => {
    setEnvironmentActions((current) => {
      const next = new Map(current)
      keys.forEach((key) => next.set(key, action))
      return next
    })
  }
  const clearActions = (keys: string[]) => {
    setEnvironmentActions((current) => {
      const next = new Map(current)
      keys.forEach((key) => next.delete(key))
      return next
    })
  }
  const runAction = async (environment: Environment, action: EnvironmentAction) => {
    const key = environmentKey(environment)
    const allowed = action === 'up' ? environment.status === 'stopped' : environmentCanStop(environment)
    if (!allowed || environmentActionsInFlight.current.has(key) || stopAllInFlight.current) return
    environmentActionsInFlight.current.add(key)
    markActions([key], action)
    setActionErrorDetails(null)
    try {
      await runEnvironmentOperation(environment, action)
      await onChanged()
    } catch (reason) {
      setActionErrorDetails(actionError(`${environment.name} wasn't ${action === 'up' ? 'started' : 'stopped'}`, reason))
      await onChanged().catch(() => undefined)
    } finally {
      environmentActionsInFlight.current.delete(key)
      clearActions([key])
    }
  }
  const stopAll = async () => {
    if (stopAllInFlight.current || environmentActionsInFlight.current.size > 0) return
    const candidates = environments.filter(environmentCanStop)
    if (candidates.length === 0) return
    const keys = candidates.map(environmentKey)
    stopAllInFlight.current = true
    keys.forEach((key) => environmentActionsInFlight.current.add(key))
    setStoppingAll(true)
    markActions(keys, 'down')
    setActionErrorDetails(null)
    try {
      const results = await Promise.allSettled(candidates.map((environment) => runEnvironmentOperation(environment, 'down')))
      const failures = results.flatMap((result, index) => result.status === 'rejected' ? [{ environment: candidates[index], reason: result.reason }] : [])
      await onChanged()
      if (failures.length > 0) {
        const first = failures[0]
        const details = actionError(`${failures.length} environment${failures.length === 1 ? " wasn't" : "s weren't"} stopped`, first.reason)
        if (failures.length > 1) details.message = `${first.environment.name}: ${details.message} ${failures.length - 1} additional environment${failures.length === 2 ? '' : 's'} also failed.`
        setActionErrorDetails(details)
      }
    } catch (reason) {
      setActionErrorDetails(actionError("The environments weren't stopped", reason))
    } finally {
      keys.forEach((key) => environmentActionsInFlight.current.delete(key))
      stopAllInFlight.current = false
      setStoppingAll(false)
      clearActions(keys)
    }
  }

  return <>
    <div className="page-heading projects-heading">
      <header className="projects-heading__title">
        <div className="eyebrow">PROJECT</div>
        <div className="projects-heading__line"><h1>{project.name}</h1></div>
      </header>
      <div className="projects-heading__controls">
        <div className="page-heading__summary"><span>{counts.failed ?? 0} failed</span><b>·</b><span>{counts.degraded ?? 0} degraded</span><b>·</b><span>{counts.recovering ?? 0} recovering</span><b>·</b><span>{counts.starting ?? 0} starting</span><b>·</b><span>{counts.healthy ?? 0} healthy</span></div>
        <button className="button project-stop-all" type="button" aria-label={`Stop all ${project.name} environments`} disabled={stoppingAll || environmentActions.size > 0 || stoppableEnvironments.length === 0} onClick={() => void stopAll()}>{stoppingAll ? 'STOPPING…' : 'STOP ALL'}</button>
      </div>
    </div>
    {actionErrorDetails && <ActionErrorNotice error={actionErrorDetails} onDismiss={() => setActionErrorDetails(null)} />}
    {environments.length > 0 ? <section className="panel environments-table">
      <div className="panel-title"><span>ENVIRONMENTS</span><button className="button button--primary button--small panel-create-button create-environment-button" type="button" aria-haspopup="dialog" onClick={() => setCreateOpen(true)}>CREATE ENVIRONMENT</button></div>
      <div className="environment-row-shell environment-row-shell--header"><div className="table-row table-row--header environment-row"><span>Status</span><span>Environment</span><span>Ready</span><span>Remote</span><span>Modified</span><span>Why</span></div><span aria-hidden="true" /></div>
      {pagination.items.map((environment) => {
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
            <span><StatusMark status={environment.status} /></span><strong title={environment.name}>{environment.name}</strong><span>{ready}/{environment.services.length}</span><span className={remote ? 'warning-text' : ''}>{remote || '—'}</span><time dateTime={environment.updatedAt} title={new Date(environment.updatedAt).toLocaleString()}>{formatTimestamp(environment.updatedAt)}</time><span className={environment.issues?.length ? 'warning-text truncate' : 'muted truncate'}>{environment.reason || environment.issues?.[0]?.message || (environment.status === 'stopped' ? 'not running' : environment.status === 'healthy' ? 'all required services are ready' : 'state is being reconciled')}</span>
          </button>
          <div className="table-row-actions environment-row-actions"><button type="button" aria-label={`${startAction ? 'Start' : 'Stop'} ${environment.name}`} disabled={stoppingAll || !!pendingAction || starting || stopping || recovering} onClick={() => void runAction(environment, startAction ? 'up' : 'down')}>{starting ? 'STARTING…' : stopping ? 'STOPPING…' : recovering ? 'RECOVERING…' : stopped ? 'START' : 'STOP'}</button></div>
        </div>
      })}
      <PanelPagination label="environments" pagination={pagination} onPage={setPage} />
    </section> : <EmptyEnvironments />}
    {createOpen && <CreateEnvironmentDialog project={project} environments={environments} onClose={() => setCreateOpen(false)} onNavigate={onNavigate} onChanged={onChanged} />}
  </>
}

function EmptyEnvironments() {
  return <section className="empty-environment panel"><div><div className="eyebrow">No environments yet</div><h2>Start one repository or assemble several.</h2><p>For one repository, run:</p><pre><span>$</span> portless up</pre><p>For several repositories, create one project and name each source:</p><pre><span>$</span> portless project create billing --source checkout=../checkout --source ledger=../ledger</pre></div></section>
}
