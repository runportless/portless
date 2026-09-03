import { useEffect, useRef, useState, type MouseEvent } from 'react'
import { createPortal } from 'react-dom'
import type { Environment } from '../../api/contracts/environments'
import type { Recording } from '../../api/contracts/experiments'
import { FaultIcon, MockIcon, RecordIcon } from '../../components/ExperimentIcons'
import { MoreActionsIcon } from '../../components/MoreActionsIcon'
import { StatusMark } from '../../components/Status'
import { ForgetEnvironmentDialog } from './ForgetEnvironmentDialog'
import { environmentUIPath } from './navigation'
import { publicEndpoint } from './service/servicePresentation'
import { environmentLifecycleLabel, type EnvironmentActions } from './useEnvironmentActions'
import type { EnvironmentActivity } from './useEnvironmentActivity'

function followLink(event: MouseEvent<HTMLAnchorElement>, path: string, onNavigate: (path: string) => void) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  onNavigate(path)
}

function boundMockScenarios(environment: Pick<Environment, 'bindings'>) {
  return [...new Set((environment.bindings || []).flatMap((binding) =>
    binding.provider === 'mock' && binding.mock?.scenario ? [binding.mock.scenario] : [],
  ))].sort((left, right) => left.localeCompare(right))
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
    <EnvironmentCloneOrigin environment={environment} />
  </div>
}

export function EnvironmentOverviewHeading({ environment, activeRecording, activeFaultCount, onNavigate }: {
  environment: Environment
  activeRecording?: Recording
  activeFaultCount: number
  onNavigate: (path: string) => void
}) {
  return <section className="environment-overview-heading" aria-label={`${environment.project}/${environment.name} overview summary`}>
    <div className="environment-overview-heading__identity">
      <div className="eyebrow">{environment.project} / ENVIRONMENT</div>
      <div className="environment-overview-heading__name"><h2>{environment.name}</h2><EnvironmentCloneOrigin environment={environment} /></div>
    </div>
    <EnvironmentActivityIndicators environment={environment} activeRecording={activeRecording} activeFaultCount={activeFaultCount} mockScenarios={boundMockScenarios(environment)} onNavigate={onNavigate} />
  </section>
}

function EnvironmentCloneOrigin({ environment }: { environment: Pick<Environment, 'project' | 'clonedFrom'> }) {
  return environment.clonedFrom ? <span className="environment-clone-origin" title={`Created by cloning ${environment.project}/${environment.clonedFrom}; changes are independent.`}>FROM <strong>{environment.clonedFrom}</strong></span> : null
}

export function EnvironmentHeaderActions({ environment, activity, actions, live, onNavigate }: {
  environment: Environment
  activity: Pick<EnvironmentActivity, 'recordings' | 'faults'>
  actions: EnvironmentActions
  live: boolean
  onNavigate: (path: string) => void
}) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [forgetOpen, setForgetOpen] = useState(false)
  const menu = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const primary = environment.services.find((service) => service.name === environment.primaryService)
  const endpoint = primary && publicEndpoint(primary, 'http')
  const label = environmentLifecycleLabel(environment, actions.busy)

  useEffect(() => {
    if (!menuOpen) return
    const frame = window.requestAnimationFrame(() => menu.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus())
    const outside = (event: globalThis.MouseEvent) => { if (!menu.current?.contains(event.target as Node)) setMenuOpen(false) }
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      event.stopPropagation()
      setMenuOpen(false)
      window.requestAnimationFrame(() => trigger.current?.focus())
    }
    document.addEventListener('mousedown', outside)
    window.addEventListener('keydown', keydown)
    return () => { window.cancelAnimationFrame(frame); document.removeEventListener('mousedown', outside); window.removeEventListener('keydown', keydown) }
  }, [menuOpen])

  return <>
    <EnvironmentActivityIndicators environment={environment} activeRecording={activity.recordings.find((recording) => recording.status === 'active')} activeFaultCount={activity.faults.filter((fault) => fault.enabled).length} mockScenarios={boundMockScenarios(environment)} appearance="icons" onNavigate={onNavigate} />
    <div className="environment-header-actions">
      {endpoint && <a className="button environment-open-app" aria-label="OPEN APP" href={endpoint.url} target="_blank" rel="noreferrer"><span className="environment-action__full">OPEN APP ↗</span><span className="environment-action__short" aria-hidden="true">OPEN ↗</span></a>}
      <button className={`button environment-lifecycle${environment.status === 'stopped' ? ' button--primary' : ''}`} aria-label={label} disabled={actions.disabled} onClick={() => void actions.run(environment.status === 'stopped' ? 'up' : 'down')}>
        <span className="environment-action__full">{label}</span><span className="environment-action__short" aria-hidden="true">{label.replace(' ALL', '')}</span>
      </button>
      <div ref={menu} className="environment-heading-actions">
        <button ref={trigger} className="environment-heading-actions__trigger" type="button" aria-label={`Environment actions for ${environment.project}/${environment.name}`} aria-haspopup="menu" aria-expanded={menuOpen} disabled={!!actions.busy} onClick={() => setMenuOpen((open) => !open)}><MoreActionsIcon /></button>
        {menuOpen && <div className="environment-heading-actions__menu" role="menu" aria-label={`${environment.project}/${environment.name} actions`}>
          <div className="environment-menu-context" role="presentation"><strong>{environment.project}/{environment.name}</strong>{environment.clonedFrom && <span>Created by cloning {environment.project}/{environment.clonedFrom}; changes are independent.</span>}</div>
          <button className="is-danger" type="button" role="menuitem" onClick={() => { setMenuOpen(false); actions.dismissForgetError(); setForgetOpen(true) }}>FORGET ENVIRONMENT</button>
        </div>}
      </div>
    </div>
    {forgetOpen && createPortal(<ForgetEnvironmentDialog environment={environment} busy={actions.busy === 'forget'} unavailable={!live} error={actions.forgetError} restoreFocusRef={trigger} onDismissError={actions.dismissForgetError} onClose={() => { if (actions.busy !== 'forget') { setForgetOpen(false); actions.dismissForgetError() } }} onForget={actions.forget} />, document.body)}
  </>
}

export function EnvironmentActivityIndicators({ environment, activeRecording, activeFaultCount, mockScenarios = [], appearance = 'badges', onNavigate }: {
  environment: Pick<Environment, 'project' | 'name'>
  activeRecording?: Recording
  activeFaultCount: number
  mockScenarios?: string[]
  appearance?: 'badges' | 'icons'
  onNavigate: (path: string) => void
}) {
  if (!activeRecording && !activeFaultCount && !mockScenarios.length) return null
  const recordingPath = environmentUIPath(environment, 'recordings')
  const faultsPath = environmentUIPath(environment, 'faults')
  const icons = appearance === 'icons'
  const faultSummary = `${activeFaultCount} active ${activeFaultCount === 1 ? 'fault' : 'faults'}`
  const mockPath = environmentUIPath(environment, 'mocks', mockScenarios.length === 1 ? { scenario: mockScenarios[0] } : undefined)
  const mockLabel = mockScenarios.length === 1 ? `Active mock scenario ${mockScenarios[0]}. Open scenario` : `${mockScenarios.length} active mock scenarios. Open mocks`
  const mockTitle = mockScenarios.length === 1 ? `Active mock scenario: ${mockScenarios[0]}` : `${mockScenarios.length} active mock scenarios: ${mockScenarios.join(', ')}`
  return <div className={`environment-activity-indicators${icons ? ' environment-activity-indicators--icons' : ''}`}>
    {activeRecording && <a className="recording-indicator" href={recordingPath} aria-label={`Recording ${activeRecording.name}. Open recordings`} title={`Recording ${activeRecording.name}`} onClick={(event) => followLink(event, recordingPath, onNavigate)}>{icons ? <RecordIcon /> : <><i aria-hidden="true" />REC <span className="recording-indicator__name">{activeRecording.name}</span></>}</a>}
    {activeFaultCount > 0 && <a className="fault-indicator" href={faultsPath} aria-label={`${faultSummary}. Open faults`} title={faultSummary} onClick={(event) => followLink(event, faultsPath, onNavigate)}>{icons ? <FaultIcon /> : <>▲ {activeFaultCount} <span className="fault-indicator__active">ACTIVE </span>{activeFaultCount === 1 ? 'FAULT' : 'FAULTS'}</>}</a>}
    {icons ? mockScenarios.length > 0 && <a className="mock-indicator" href={mockPath} aria-label={mockLabel} title={mockTitle} onClick={(event) => followLink(event, mockPath, onNavigate)}><MockIcon /></a> : mockScenarios.map((scenario) => {
      const path = environmentUIPath(environment, 'mocks', { scenario })
      return <a key={scenario} className="mock-indicator" href={path} aria-label={`Active mock scenario ${scenario}. Open scenario`} title={`Active mock scenario: ${scenario}`} onClick={(event) => followLink(event, path, onNavigate)}><MockIcon />MOCK <span className="mock-indicator__name">{scenario}</span></a>
    })}
  </div>
}
