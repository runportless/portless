import type { MouseEvent } from 'react'
import type { Environment } from '../../api/contracts/environments'
import type { Recording } from '../../api/contracts/experiments'
import { FaultIcon, MockIcon, RecordIcon } from '../../components/ExperimentIcons'
import { StatusMark } from '../../components/Status'
import { environmentUIPath } from './navigation'
import { publicEndpoint } from './service/servicePresentation'
import { environmentLifecycleLabel, type EnvironmentActions } from './useEnvironmentActions'
import { boundMockScenarios, type EnvironmentActivity } from './useEnvironmentActivity'

function followLink(event: MouseEvent<HTMLAnchorElement>, path: string, onNavigate: (path: string) => void) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  onNavigate(path)
}

export function EnvironmentHeaderContext({ environment, live, onNavigate }: {
  environment: Environment
  live: boolean
  onNavigate: (path: string) => void
}) {
  const ready = environment.services.filter((service) => service.status === 'ready').length
  const readiness = environment.services.length ? `${ready}/${environment.services.length} ready` : 'No services'
  const overview = environmentUIPath(environment)
  return <div className="environment-header-context">
    <a className={`environment-health${live ? '' : ' environment-health--stale'}`} href={overview}
      aria-label={`${environment.project}/${environment.name} health: ${live ? environment.status : `reconnecting, last known ${environment.status}`}; ${readiness}. Open service overview`}
      onClick={(event) => followLink(event, overview, onNavigate)}>
      <StatusMark status={environment.status} />
      <span className="environment-health__readiness">{readiness}</span>
      {!live && <span className="environment-health__stale">RECONNECTING</span>}
    </a>
  </div>
}

export function EnvironmentOverviewHeading({ environment }: {
  environment: Environment
}) {
  return <section className="environment-overview-heading" aria-label={`${environment.project}/${environment.name} overview summary`}>
    <div className="environment-overview-heading__identity">
      <div className="eyebrow">ENVIRONMENT</div>
      <div className="environment-overview-heading__name"><h2>{environment.name}</h2><StatusMark status={environment.status} /><EnvironmentCloneOrigin environment={environment} /></div>
    </div>
  </section>
}

function EnvironmentCloneOrigin({ environment }: { environment: Pick<Environment, 'project' | 'clonedFrom'> }) {
  return environment.clonedFrom ? <span className="environment-clone-origin" title={`Created by cloning ${environment.project}/${environment.clonedFrom}; changes are independent.`}>FROM <strong>{environment.clonedFrom}</strong></span> : null
}

export function EnvironmentHeaderActions({ environment, activity, actions, onNavigate }: {
  environment: Environment
  activity: Pick<EnvironmentActivity, 'recordings' | 'faults'>
  actions: EnvironmentActions
  onNavigate: (path: string) => void
}) {
  const primary = environment.services.find((service) => service.name === environment.primaryService)
  const endpoint = primary && publicEndpoint(primary, 'http')
  const label = environmentLifecycleLabel(environment, actions.busy)
  const lifecyclePending = actions.busy === 'up' || actions.busy === 'down'
  const showStart = (['stopped', 'starting'].includes(environment.status) && actions.busy !== 'down') || actions.busy === 'up'

  return <>
    <EnvironmentActivityIndicators environment={environment} activeRecording={activity.recordings.find((recording) => recording.status === 'active')} activeFaultCount={activity.faults.filter((fault) => fault.enabled).length} mockScenarios={boundMockScenarios(environment)} onNavigate={onNavigate} />
    {(endpoint || showStart) && <div className="environment-header-actions">
      {showStart
        ? <button className="button environment-lifecycle button--primary" type="button" aria-label={label} disabled={actions.disabled} title={`Start ${environment.project}/${environment.name}`} onClick={() => void actions.run('up')}>{label}</button>
        : endpoint && <a className="button environment-open-app" aria-label="OPEN APP" href={endpoint.url} target="_blank" rel="noreferrer" title={`Open ${primary.name} in a new tab`}>Open <span aria-hidden="true">↗</span></a>}
    </div>}
    <span className="sr-only" role="status">{lifecyclePending ? label : ''}</span>
  </>
}

export function EnvironmentActivityIndicators({ environment, activeRecording, activeFaultCount, mockScenarios = [], onNavigate }: {
  environment: Pick<Environment, 'project' | 'name'>
  activeRecording?: Recording
  activeFaultCount: number
  mockScenarios?: string[]
  onNavigate: (path: string) => void
}) {
  if (!activeRecording && !activeFaultCount && !mockScenarios.length) return null
  const recordingPath = environmentUIPath(environment, 'recordings')
  const faultsPath = environmentUIPath(environment, 'faults')
  const faultSummary = `${activeFaultCount} active ${activeFaultCount === 1 ? 'fault' : 'faults'}`
  const mockPath = environmentUIPath(environment, 'mocks')
  const mockLabel = mockScenarios.length === 1 ? `Active mock scenario ${mockScenarios[0]}. Open mocks` : `${mockScenarios.length} active mock scenarios. Open mocks`
  return <div className="environment-activity-indicators">
    {activeRecording && <a className="recording-indicator" href={recordingPath} aria-label={`Recording ${activeRecording.name}. Open recordings`} title="Recording" onClick={(event) => followLink(event, recordingPath, onNavigate)}><RecordIcon /></a>}
    {activeFaultCount > 0 && <a className="fault-indicator" href={faultsPath} aria-label={`${faultSummary}. Open faults`} title={`${activeFaultCount} Active ${activeFaultCount === 1 ? 'Fault' : 'Faults'}`} onClick={(event) => followLink(event, faultsPath, onNavigate)}><FaultIcon /></a>}
    {mockScenarios.length > 0 && <a className="mock-indicator" href={mockPath} aria-label={mockLabel} title={`${mockScenarios.length} Active ${mockScenarios.length === 1 ? 'Mock' : 'Mocks'}`} onClick={(event) => followLink(event, mockPath, onNavigate)}><MockIcon /></a>}
  </div>
}
