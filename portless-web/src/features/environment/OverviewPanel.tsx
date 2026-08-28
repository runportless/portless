import { useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { StatePanel, StatusMark } from '../../components/Status'
import type { Environment, FaultRule, Recording, Service, TimelineEvent } from '../../types'
import type { EnvironmentNavigationOptions, EnvironmentTab } from './navigation'
import { displayLaunchMode, overviewServiceEndpoint } from './service/servicePresentation'
import { TopologyPreview } from './topology/TopologyPanel'

const overviewPageSize = 8

export function OverviewPanel({ environment, timeline, ready, faults, activeRecording, trafficCount, onService, onNavigate }: {
  environment: Environment
  timeline: TimelineEvent[]
  ready: number
  faults: FaultRule[]
  activeRecording?: Recording
  trafficCount: number
  onService: (service: Service) => void
  onNavigate: (tab: EnvironmentTab, options?: EnvironmentNavigationOptions) => void
}) {
  const [servicePage, setServicePage] = useState(0)
  const [activityPage, setActivityPage] = useState(0)
  const [copiedEndpoint, setCopiedEndpoint] = useState('')
  const copyReset = useRef<number | undefined>(undefined)
  const services = paginateItems(environment.services, servicePage, overviewPageSize)
  const activities = paginateItems(timeline, activityPage, overviewPageSize)
  const bindingSummary = summarizeEnvironmentBindings(environment)

  useEffect(() => {
    setServicePage(0)
    setActivityPage(0)
  }, [environment.project, environment.name])
  useEffect(() => () => window.clearTimeout(copyReset.current), [])

  const copyServiceEndpoint = async (event: ReactMouseEvent<HTMLButtonElement>, serviceName: string, endpoint: string) => {
    event.stopPropagation()
    try {
      await navigator.clipboard.writeText(endpoint)
      setCopiedEndpoint(serviceName)
      window.clearTimeout(copyReset.current)
      copyReset.current = window.setTimeout(() => setCopiedEndpoint((current) => current === serviceName ? '' : current), 1400)
    } catch {
      setCopiedEndpoint('')
    }
  }

  return <>
    <div className="state-grid">
      <StatePanel title="READY" value={`${ready}/${environment.services.length}`} detail="required services" />
      <StatePanel title="TRAFFIC" value={trafficCount} detail="recent requests" />
      <StatePanel title="RECORDING" value={activeRecording ? 'ON' : 'OFF'} tone={activeRecording ? 'danger' : undefined} detail={activeRecording?.name || 'capture disabled'} />
      <StatePanel title="FAULTS" value={faults.length} tone={faults.length ? 'warning' : undefined} detail={faults.length ? 'affecting local traffic' : 'none active'} />
      <StatePanel title="BINDINGS" value={bindingSummary.value} tone={bindingSummary.tone} detail={bindingSummary.detail} />
    </div>
    <section className="panel services-panel">
      <div className="panel-title"><span>SERVICES</span><small>{environment.services.length} workloads</small></div>
      <div className="table-row table-row--header service-row"><span /><span>Name</span><span>Mode</span><span>State</span><span>Restarts</span><span>Requests</span><span>P95</span><span>Endpoint / reason</span><span /></div>
      {services.items.map((service) => {
        const endpoint = overviewServiceEndpoint(environment, service)
        const copied = copiedEndpoint === service.name
        return <div className="table-row service-row service-row--interactive" key={service.name} onClick={() => onService(service)}>
          <StatusMark status={service.status} label={false} /><strong>{service.name}</strong><span>{displayLaunchMode(environment, service)}</span><StatusMark status={service.status} /><span className={service.restartCount ? 'warning-text' : ''}>{service.restartCount}</span><span>{service.recentRequests || '—'}</span><span>{service.p95Millis ? `${service.p95Millis}ms` : '—'}</span><span className="service-list-endpoint"><span className="truncate muted" title={service.reason || endpoint || 'not running'}>{service.reason || endpoint || 'not running'}</span>{!service.reason && endpoint && <button className={`service-copy-button${copied ? ' is-copied' : ''}`} type="button" aria-label={`Copy ${service.name} endpoint`} title={copied ? 'Copied' : 'Copy endpoint'} onClick={(event) => void copyServiceEndpoint(event, service.name, endpoint)}><CopyIcon copied={copied} /></button>}</span><button className="row-action" type="button" onClick={(event) => { event.stopPropagation(); onService(service) }}>INSPECT</button>
        </div>
      })}
      <PanelPagination label="services" pagination={services} onPage={setServicePage} />
    </section>
    <div className="overview-grid">
      <TopologyPreview
        environment={environment}
        faults={faults}
        onService={onService}
        onOpen={() => onNavigate('topology')}
        onEdge={(edge) => onNavigate('traffic', { edge: `${edge.source}:${edge.target}`, protocol: edge.protocol === 'http' ? 'http' : 'tcp' })}
      />
      <section className="panel activity-panel">
        <div className="panel-title"><span>RECENT ACTIVITY</span><button onClick={() => onNavigate('timeline')}>FULL TIMELINE</button></div>
        <div className="activity-list">
          {activities.items.map((event) => <div className="activity" key={event.sequence}><time>{new Date(event.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</time><span className={`activity__line activity__line--${event.severity}`} /><div><strong>{event.summary}</strong><small>{event.subject || event.type} · {event.actor}</small></div></div>)}
          {timeline.length === 0 && <div className="empty-row">No lifecycle events have been recorded yet.</div>}
        </div>
        <PanelPagination label="activities" pagination={activities} onPage={setActivityPage} />
      </section>
    </div>
  </>
}

export function summarizeEnvironmentBindings(environment: Environment): { value: 'LOCAL' | 'HYBRID' | 'REMOTE'; detail: string; tone?: 'warning' } {
  const bindingServices = new Set((environment.bindings || []).map((binding) => binding.service))
  const remoteBindings = (environment.bindings || []).filter((binding) => binding.provider === 'remote')
  const remoteServices = new Set(remoteBindings.map((binding) => binding.service))
  const mockServices = new Set((environment.bindings || []).filter((binding) => binding.provider === 'mock').map((binding) => binding.service))
  const total = Math.max(environment.services.length, bindingServices.size)
  const remote = remoteServices.size
  const mocked = mockServices.size
  const local = Math.max(0, total - remote - mocked)

  if (remote === 0) {
    if (mocked > 0) return { value: 'LOCAL', detail: `${local} local · ${mocked} mocked` }
    return { value: 'LOCAL', detail: `${local} ${local === 1 ? 'service' : 'services'} local` }
  }
  if (local === 0 && mocked === 0) {
    return { value: 'REMOTE', detail: `${remote} remote ${remote === 1 ? 'service' : 'services'}`, tone: 'warning' }
  }

  const classifications = new Set(remoteBindings.map((binding) => binding.remote?.classification).filter((classification) => classification && classification !== 'unknown'))
  const remoteDetail = classifications.size === 1 ? `${remote} ${[...classifications][0]!.toUpperCase()}` : `${remote} remote`
  return { value: 'HYBRID', detail: `${local} local${mocked ? ` · ${mocked} mocked` : ''} · ${remoteDetail}`, tone: 'warning' }
}

function CopyIcon({ copied }: { copied: boolean }) {
  return copied
    ? <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m3 8 3 3 7-7" /></svg>
    : <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="3" width="8" height="9" /><path d="M10 12v2H2V5h3" /></svg>
}
