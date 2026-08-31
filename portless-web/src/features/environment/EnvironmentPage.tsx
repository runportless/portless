import { useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react'
import { api, environmentPath, jsonBody } from '../../api'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'
import { MoreActionsIcon } from '../../components/MoreActionsIcon'
import { StatusMark } from '../../components/Status'
import type { Environment, Operation } from '../../api/contracts/environments'
import type { Recording } from '../../api/contracts/experiments'
import type { Project } from '../../api/contracts/projects'
import type { Service } from '../../api/contracts/topology'
import { MocksPanel } from '../mocks'
import { TrafficPanel } from '../traffic'
import { BindingsPanel } from './bindings/BindingsPanel'
import { FaultsPanel } from './faults/FaultsPanel'
import { ForgetEnvironmentDialog } from './ForgetEnvironmentDialog'
import { environmentUIPath, type EnvironmentNavigationOptions, type EnvironmentTab } from './navigation'
import { OverviewPanel } from './OverviewPanel'
import { RecordingsPanel } from './recordings/RecordingsPanel'
import { ServiceDrawer } from './service/ServiceDrawer'
import { publicEndpoint } from './service/servicePresentation'
import { TimelinePanel } from './timeline/TimelinePanel'
import { TopologyPanel } from './topology/TopologyPanel'
import { useEnvironmentActivity } from './useEnvironmentActivity'

const environmentTabs: EnvironmentTab[] = ['overview', 'topology', 'traffic', 'mocks', 'recordings', 'faults', 'bindings', 'timeline']

export function EnvironmentPage({ environment, project, tab, mockProfile, onNavigate, onChanged }: {
  environment: Environment
  project?: Project
  tab: EnvironmentTab
  mockProfile?: string
  onNavigate: (path: string) => void
  onChanged: () => void
}) {
  const [selectedService, setSelectedService] = useState<Service | null>(null)
  const [busy, setBusy] = useState('')
  const [actionFailure, setActionFailure] = useState('')
  const [environmentMenuOpen, setEnvironmentMenuOpen] = useState(false)
  const [forgetOpen, setForgetOpen] = useState(false)
  const [forgetBusy, setForgetBusy] = useState(false)
  const [forgetError, setForgetError] = useState<ActionErrorDetails | null>(null)
  const environmentMenu = useRef<HTMLDivElement>(null)
  const environmentMenuTrigger = useRef<HTMLButtonElement>(null)
  const activity = useEnvironmentActivity(environment, onChanged)

  useEffect(() => {
    if (!selectedService) return
    const updated = environment.services.find((service) => service.name === selectedService.name)
    if (updated) setSelectedService(updated)
  }, [environment.services]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!environmentMenuOpen) return
    const dismissOutside = (event: MouseEvent) => {
      if (!environmentMenu.current?.contains(event.target as Node)) setEnvironmentMenuOpen(false)
    }
    const dismissWithEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setEnvironmentMenuOpen(false)
      window.requestAnimationFrame(() => environmentMenuTrigger.current?.focus())
    }
    document.addEventListener('mousedown', dismissOutside)
    window.addEventListener('keydown', dismissWithEscape)
    return () => {
      document.removeEventListener('mousedown', dismissOutside)
      window.removeEventListener('keydown', dismissWithEscape)
    }
  }, [environmentMenuOpen])

  const run = async (action: 'up' | 'down') => {
    setBusy(action)
    setActionFailure('')
    try {
      await api<Operation>(environmentPath(environment, `/${action}`), { method: 'POST', ...(action === 'down' ? jsonBody({ removeVolumes: false }) : {}) })
      onChanged()
    } catch (value) {
      setActionFailure(value instanceof Error ? value.message : String(value))
    } finally {
      setBusy('')
    }
  }

  const forget = async () => {
    if (environment.status !== 'stopped' || forgetBusy) return
    setForgetBusy(true)
    setForgetError(null)
    try {
      await api(environmentPath(environment), { method: 'DELETE' })
      await onChanged()
      onNavigate('/projects')
    } catch (reason) {
      setForgetError(actionError("Environment couldn't be forgotten", reason))
    } finally {
      setForgetBusy(false)
    }
  }

  const navigateTab = (next: EnvironmentTab, options?: EnvironmentNavigationOptions) => onNavigate(environmentUIPath(environment, next, options))
  const activeRecording = activity.recordings.find((recording) => recording.status === 'active')
  const activeFaults = activity.faults.filter((fault) => fault.enabled)
  const ready = environment.services.filter((service) => service.status === 'ready').length
  const trafficCount = environment.services.reduce((sum, service) => sum + (service.recentRequests || 0), 0)
  const primaryService = environment.services.find((service) => service.name === environment.primaryService)
  const primaryHTTP = primaryService && publicEndpoint(primaryService, 'http')
  const error = actionFailure || activity.error
  const statusMessage = environment.reason || (environment.status === 'stopped' ? 'not running' : '')

  return <div className="page project-page environment-page">
    <div className="project-heading">
      <div><div className="eyebrow">{environment.project} / ENVIRONMENT</div><div className="title-with-status"><h1>{environment.name}</h1><StatusMark status={environment.status} />{environment.clonedFrom && <span className="environment-clone-origin" title={`Created by cloning ${environment.project}/${environment.clonedFrom}; changes are independent.`}>FROM <strong>{environment.clonedFrom}</strong></span>}</div><p className="environment-heading__message">{statusMessage}</p></div>
      <div className="project-actions">
        <EnvironmentActivityIndicators environment={environment} activeRecording={activeRecording} activeFaultCount={activeFaults.length} onNavigate={onNavigate} />
        {environment.status !== 'stopped' ? <button className="button" disabled={!!busy || environment.status === 'recovering'} onClick={() => run('down')}>{busy === 'down' ? 'STOPPING…' : environment.status === 'recovering' ? 'RECOVERING…' : 'STOP ALL'}</button> : <button className="button button--primary" disabled={!!busy} onClick={() => run('up')}>{busy === 'up' ? 'STARTING…' : 'START ALL'}</button>}
        {primaryHTTP && <a className="button" href={primaryHTTP.url} target="_blank" rel="noreferrer">OPEN APP ↗</a>}
        <div ref={environmentMenu} className="environment-heading-actions">
          <button ref={environmentMenuTrigger} className="environment-heading-actions__trigger" type="button" aria-label={`Environment actions for ${environment.project}/${environment.name}`} aria-haspopup="menu" aria-expanded={environmentMenuOpen} disabled={!!busy || forgetBusy} onClick={() => setEnvironmentMenuOpen((open) => !open)}><MoreActionsIcon /></button>
          {environmentMenuOpen && <div className="environment-heading-actions__menu" role="menu" aria-label={`${environment.project}/${environment.name} actions`}>
            <button className="is-danger" type="button" role="menuitem" onClick={() => { setEnvironmentMenuOpen(false); setForgetError(null); setForgetOpen(true) }}>FORGET ENVIRONMENT</button>
          </div>}
        </div>
      </div>
    </div>
    {!!environment.issues?.length && <div className="alert alert--danger"><strong>Configuration needs attention</strong><span>{environment.issues.map((issue) => issue.message).join(' · ')}</span></div>}
    {error && <div className="alert alert--danger"><strong>Action failed</strong><span>{error}</span><button onClick={() => { setActionFailure(''); activity.dismissError() }}>DISMISS</button></div>}
    <nav className="tabs" aria-label="Environment views">
      {environmentTabs.map((name) => <button key={name} className={tab === name ? 'is-active' : ''} onClick={() => navigateTab(name)}>{name}<small>{name === 'recordings' ? activity.recordings.length : name === 'faults' ? activeFaults.length : ''}</small></button>)}
    </nav>
    {tab === 'overview' && <OverviewPanel environment={environment} timeline={activity.timeline} ready={ready} faults={activeFaults} activeRecording={activeRecording} trafficCount={trafficCount} onService={setSelectedService} onNavigate={navigateTab} onChanged={onChanged} />}
    {tab === 'topology' && <TopologyPanel environment={environment} faults={activeFaults} onService={setSelectedService} onEdge={(edge) => navigateTab('traffic', { edge: `${edge.source}:${edge.target}`, protocol: edge.protocol === 'http' ? 'http' : 'tcp' })} />}
    {tab === 'bindings' && <BindingsPanel environment={environment} project={project} onNavigate={onNavigate} onChanged={onChanged} />}
    {tab === 'traffic' && <TrafficPanel environment={environment} />}
    {tab === 'mocks' && <MocksPanel environment={environment} project={project} selectedProfile={mockProfile} onSelectProfile={(profile) => navigateTab('mocks', { profile })} onChanged={onChanged} />}
    {tab === 'recordings' && <RecordingsPanel environment={environment} recordings={activity.recordings} refresh={activity.refresh} />}
    {tab === 'faults' && <FaultsPanel environment={environment} faults={activity.faults} refresh={activity.refresh} />}
    {tab === 'timeline' && <TimelinePanel key={`${environment.project}/${environment.name}`} timeline={activity.timeline} />}
    {selectedService && <ServiceDrawer environment={environment} service={selectedService} onClose={() => setSelectedService(null)} onChanged={onChanged} />}
    {forgetOpen && <ForgetEnvironmentDialog environment={environment} busy={forgetBusy} error={forgetError} restoreFocusRef={environmentMenuTrigger} onDismissError={() => setForgetError(null)} onClose={() => { if (!forgetBusy) { setForgetOpen(false); setForgetError(null) } }} onForget={forget} />}
  </div>
}

export function EnvironmentActivityIndicators({ environment, activeRecording, activeFaultCount, onNavigate }: {
  environment: Pick<Environment, 'project' | 'name'>
  activeRecording?: Recording
  activeFaultCount: number
  onNavigate: (path: string) => void
}) {
  const recordingPath = environmentUIPath(environment, 'recordings')
  const faultsPath = environmentUIPath(environment, 'faults')
  const navigate = (event: ReactMouseEvent<HTMLAnchorElement>, path: string) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    onNavigate(path)
  }

  return <>
    {activeRecording && <a className="recording-indicator" href={recordingPath} onClick={(event) => navigate(event, recordingPath)}><i />REC {activeRecording.name}</a>}
    {activeFaultCount > 0 && <a className="fault-indicator" href={faultsPath} onClick={(event) => navigate(event, faultsPath)}>▲ {activeFaultCount} ACTIVE {activeFaultCount === 1 ? 'FAULT' : 'FAULTS'}</a>}
  </>
}
