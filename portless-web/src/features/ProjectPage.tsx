import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { api, connectEvents, jsonBody, environmentPath } from '../api'
import type { ComponentBinding, FaultRule, MockProfile, Operation, Environment, Project, ProjectSource, Protocol, ProviderKind, Recording, RemoteClassification, Service, SourceBinding, TimelineEvent, TrafficActivity, TrafficExchange, WritePolicy } from '../types'
import { duration, relativeTime, StatePanel, StatusMark } from '../components/Status'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import { DrawerSizeButton } from '../components/DrawerSizeButton'
import { paginateItems, PanelPagination } from '../components/PanelPagination'
import { experimentScopes, preferredFaultScope, recordingScopeLabel } from './experimentScopes'
import { TrafficPanel } from './traffic'
import { MocksPanel } from './mocks'
import { ConfigureCheckoutModal, RemoveCheckoutModal } from './SourceModals'
import { ServiceLogs } from './ServiceLogs'

type Tab = 'overview' | 'topology' | 'bindings' | 'traffic' | 'mocks' | 'recordings' | 'faults' | 'timeline'
type SourcePathMutation = { environment: Environment; warnings: string[] }
type EnvironmentCheckoutRow = { source: ProjectSource; checkout?: SourceBinding; usedBy: string[]; required: boolean }

export function EnvironmentPage({ environment, project, tab, mockProfile, onNavigate, onChanged }: { environment: Environment; project?: Project; tab: Tab; mockProfile?: string; onNavigate: (path: string) => void; onChanged: () => void }) {
  const [selectedService, setSelectedService] = useState<Service | null>(null)
  const [timeline, setTimeline] = useState<TimelineEvent[]>([])
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [faults, setFaults] = useState<FaultRule[]>([])
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  const refreshSecondary = async () => {
    const base = environmentPath(environment)
    const [timelineResult, recordingResult, faultResult] = await Promise.all([
      api<{ timeline: TimelineEvent[] }>(`${base}/timeline?limit=1000`),
      api<{ recordings: Recording[] }>(`${base}/recordings`),
      api<{ faults: FaultRule[] }>(`${base}/faults`),
    ])
    setTimeline(timelineResult.timeline)
    setRecordings(recordingResult.recordings)
    setFaults(faultResult.faults)
  }

  useEffect(() => {
    refreshSecondary().catch((value) => setError(value.message))
    return connectEvents(environment, ['environment.state', 'service.state', 'recording.state', 'fault.state', 'operation.state'], () => {
      onChanged(); refreshSecondary().catch(() => undefined)
    })
  }, [environment.project, environment.name]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!selectedService) return
    const updated = environment.services.find((service) => service.name === selectedService.name)
    if (updated) setSelectedService(updated)
  }, [environment.services]) // eslint-disable-line react-hooks/exhaustive-deps

  const run = async (action: 'up' | 'down') => {
    setBusy(action); setError('')
    try {
      await api<Operation>(environmentPath(environment, `/${action}`), { method: 'POST', ...(action === 'down' ? jsonBody({ removeVolumes: false }) : {}) })
      onChanged()
    } catch (value) { setError(value instanceof Error ? value.message : String(value)) }
    finally { setBusy('') }
  }

  const activeRecording = recordings.find((recording) => recording.status === 'active')
  const activeFaults = faults.filter((fault) => fault.enabled)
  const ready = environment.services.filter((service) => service.status === 'ready').length
  const trafficCount = environment.services.reduce((sum, service) => sum + (service.recentRequests || 0), 0)
	const primaryService = environment.services.find((service) => service.name === environment.primaryService)
	const primaryHTTP = primaryService && publicEndpoint(primaryService, 'http')

  return (
    <div className="page project-page">
      <div className="project-heading">
		<div><div className="eyebrow">{environment.project} / ENVIRONMENT</div><div className="title-with-status"><h1>{environment.name}</h1><StatusMark status={environment.status} /></div>{(environment.reason || environment.status === 'stopped') && <p>{environment.reason || 'not running'}</p>}</div>
        <div className="project-actions">
          {activeRecording && <span className="recording-indicator"><i />REC {activeRecording.name}</span>}
          {activeFaults.length > 0 && <span className="fault-indicator">▲ {activeFaults.length} ACTIVE {activeFaults.length === 1 ? 'FAULT' : 'FAULTS'}</span>}
          {environment.status !== 'stopped' ? <button className="button" disabled={!!busy || environment.status === 'recovering'} onClick={() => run('down')}>{busy === 'down' ? 'STOPPING…' : environment.status === 'recovering' ? 'RECOVERING…' : 'STOP ALL'}</button> : <button className="button button--primary" disabled={!!busy} onClick={() => run('up')}>{busy === 'up' ? 'STARTING…' : 'START ALL'}</button>}
          {primaryHTTP && <a className="button" href={primaryHTTP.url} target="_blank" rel="noreferrer">OPEN APP ↗</a>}
        </div>
      </div>
      {!!environment.issues?.length && <div className="alert alert--danger"><strong>Configuration needs attention</strong><span>{environment.issues.map((issue) => issue.message).join(' · ')}</span></div>}
      {error && <div className="alert alert--danger"><strong>Action failed</strong><span>{error}</span><button onClick={() => setError('')}>DISMISS</button></div>}
      <nav className="tabs" aria-label="Environment views">
        {(['overview', 'topology', 'bindings', 'traffic', 'mocks', 'recordings', 'faults', 'timeline'] as Tab[]).map((name) => <button key={name} className={tab === name ? 'is-active' : ''} onClick={() => onNavigate(environmentUIPath(environment, name))}>{name}<small>{name === 'recordings' ? recordings.length : name === 'faults' ? activeFaults.length : ''}</small></button>)}
      </nav>
      {tab === 'overview' && <Overview environment={environment} timeline={timeline} ready={ready} faults={activeFaults} activeRecording={activeRecording} trafficCount={trafficCount} onService={setSelectedService} onTab={(next, edge, protocol) => onNavigate(environmentUIPath(environment, next, { edge, protocol }))} />}
      {tab === 'topology' && <TopologyView environment={environment} faults={activeFaults} onService={setSelectedService} onTab={(next, edge, protocol) => onNavigate(environmentUIPath(environment, next, { edge, protocol }))} />}
      {tab === 'bindings' && <BindingsPanel environment={environment} project={project} onNavigate={onNavigate} onChanged={onChanged} />}
      {tab === 'traffic' && <TrafficPanel environment={environment} />}
      {tab === 'mocks' && <MocksPanel environment={environment} selectedProfile={mockProfile} onSelectProfile={(profile) => onNavigate(environmentUIPath(environment, 'mocks', { profile }))} />}
      {tab === 'recordings' && <RecordingsPanel environment={environment} recordings={recordings} refresh={refreshSecondary} />}
      {tab === 'faults' && <FaultsPanel environment={environment} faults={faults} refresh={refreshSecondary} />}
      {tab === 'timeline' && <TimelinePanel key={`${environment.project}/${environment.name}`} timeline={timeline} />}
      {selectedService && <ServiceDrawer environment={environment} service={selectedService} onClose={() => setSelectedService(null)} onChanged={onChanged} />}
    </div>
  )
}

function Overview({ environment, timeline, ready, faults, activeRecording, trafficCount, onService, onTab }: {
  environment: Environment; timeline: TimelineEvent[]; ready: number; faults: FaultRule[]; activeRecording?: Recording; trafficCount: number; onService: (service: Service) => void; onTab: (tab: Tab, edge?: string, protocol?: 'http' | 'tcp') => void
}) {
  const [topologyPaused, setTopologyPaused] = useState(false)
  const [topologyCenterRequest, setTopologyCenterRequest] = useState(0)
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
    } catch { setCopiedEndpoint('') }
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
      <section className="panel topology-panel topology-panel--preview" aria-label="Service topology">
        <div className="panel-title topology-toolbar"><span>TOPOLOGY</span><div><TopologyLiveButton paused={topologyPaused} onToggle={() => setTopologyPaused((value) => !value)} /><TopologyCenterButton onCenter={() => setTopologyCenterRequest((value) => value+1)} /><button className="topology-size-button" type="button" title="Open topology" aria-label="Open topology" onClick={() => onTab('topology')}><TopologySizeIcon /></button></div></div>
        <Topology environment={environment} faults={faults} paused={topologyPaused} centerRequest={topologyCenterRequest} onService={onService} onEdge={(edge) => onTab('traffic', `${edge.source}:${edge.target}`, edge.protocol === 'http' ? 'http' : 'tcp')} />
      </section>
      <section className="panel activity-panel">
        <div className="panel-title"><span>RECENT ACTIVITY</span><button onClick={() => onTab('timeline')}>FULL TIMELINE</button></div>
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

export function overviewServiceEndpoint(environment: Environment, service: Service) {
  const binding = bindingFor(environment, service.name)
  return publicEndpoint(service)?.url || binding?.remote?.url || ''
}

function CopyIcon({ copied }: { copied: boolean }) {
  return copied
    ? <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m3 8 3 3 7-7" /></svg>
    : <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="3" width="8" height="9" /><path d="M10 12v2H2V5h3" /></svg>
}

function TopologyView({ environment, faults, onService, onTab }: { environment: Environment; faults: FaultRule[]; onService: (service: Service) => void; onTab: (tab: Tab, edge?: string, protocol?: 'http' | 'tcp') => void }) {
  const [paused, setPaused] = useState(false)
  const [maximized, setMaximized] = useState(false)
  const [centerRequest, setCenterRequest] = useState(0)

  useEffect(() => {
    if (!maximized) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !document.querySelector('.drawer-backdrop')) setMaximized(false)
    }
    window.addEventListener('keydown', keydown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', keydown)
    }
  }, [maximized])

  return <section className={`panel topology-panel topology-panel--page${maximized ? ' topology-panel--maximized' : ''}`} aria-label="Service topology">
    <div className="panel-title topology-toolbar"><span>TOPOLOGY</span><div><TopologyLiveButton paused={paused} onToggle={() => setPaused((value) => !value)} /><TopologyCenterButton onCenter={() => setCenterRequest((value) => value+1)} /><button className={maximized ? 'icon-button' : 'topology-size-button'} type="button" title={`${maximized ? 'Restore' : 'Maximize'} topology`} aria-label={`${maximized ? 'Restore' : 'Maximize'} topology`} aria-pressed={maximized} onClick={() => setMaximized((value) => !value)}>{maximized ? '×' : <TopologySizeIcon />}</button></div></div>
    <Topology environment={environment} faults={faults} paused={paused} centerRequest={centerRequest} onService={onService} onEdge={(edge) => onTab('traffic', `${edge.source}:${edge.target}`, edge.protocol === 'http' ? 'http' : 'tcp')} />
  </section>
}

function TopologyLiveButton({ paused, onToggle }: { paused: boolean; onToggle: () => void }) {
  return <button className={`topology-live${paused ? ' is-paused' : ''}`} type="button" title={paused ? 'Resume live topology' : 'Pause live topology'} onClick={onToggle}>{paused ? <svg className="topology-live__pause" viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg> : <i className="topology-live__dot" aria-hidden="true" />}{paused ? 'PAUSED' : 'LIVE'}</button>
}

function TopologyCenterButton({ onCenter }: { onCenter: () => void }) {
  return <button className="topology-center-button" type="button" title="Center topology" aria-label="Center topology" onClick={onCenter}><TopologyCenterIcon /></button>
}

const overviewPageSize = 8

type TopologyItem = { kind: 'client'; key: 'external' } | { kind: 'service'; key: string; service: Service }
type TopologySignal = TrafficExchange | TrafficActivity
type TopologyEdgeMetric = {
  samples: Array<{ observedAt: number; duration: number; error: boolean }>
  bytes: number
  activeConnections: number
  lastSeen: number
  latestSequence: number
  fault?: string
  faultSeen?: number
}
type TopologyEdge = ReturnType<typeof buildTopology>['edges'][number]

const topologyWindowMilliseconds = 30_000
const topologyActiveEdgeWidth = 1.77
const topologyInactiveArrowSize = 6
const topologyActiveArrowSize = 10.62
const topologyInactiveEdgeVisual = { strokeWidth: 1, markerID: 'topology-arrow-inactive' } as const
const topologyActiveEdgeVisual = { strokeWidth: topologyActiveEdgeWidth, markerID: 'topology-arrow-active' } as const

export function topologyEdgeKey(source: string, target: string) { return `${source}\u0000${target}` }

export function summarizeTopologyTraffic(events: TrafficExchange[], now = Date.now()) {
  const metrics = new Map<string, TopologyEdgeMetric>()
  for (const event of events) {
    const observedAt = new Date(event.completedAt || event.startedAt).getTime()
    if (!Number.isFinite(observedAt) || now-observedAt > topologyWindowMilliseconds) continue
    const key = topologyEdgeKey(event.source, event.target)
    const current = metrics.get(key) || emptyTopologyMetric()
    if (event.protocol === 'http') current.samples.push({ observedAt, duration: event.durationMs || 0, error: !!event.error || (event.status || 0) >= 500 })
    current.bytes += Math.max(0, event.requestBytes || 0) + Math.max(0, event.responseBytes || 0)
    current.lastSeen = Math.max(current.lastSeen, observedAt)
    current.latestSequence = Math.max(current.latestSequence, event.sequence || 0)
    if (event.fault) { current.fault = event.fault; current.faultSeen = observedAt }
    metrics.set(key, current)
  }
  return metrics
}

function emptyTopologyMetric(): TopologyEdgeMetric {
  return { samples: [], bytes: 0, activeConnections: 0, lastSeen: 0, latestSequence: 0 }
}

function mergeTopologySignal(metrics: Map<string, TopologyEdgeMetric>, signal: TopologySignal) {
  const key = topologyEdgeKey(signal.source, signal.target)
  const next = new Map(metrics)
  const current = { ...(next.get(key) || emptyTopologyMetric()) }
  current.samples = current.samples.filter((sample) => Date.now()-sample.observedAt <= topologyWindowMilliseconds)
  if ('phase' in signal) {
    current.activeConnections = Math.max(0, signal.activeConnections || 0)
    current.bytes += Math.max(0, signal.requestBytes || 0) + Math.max(0, signal.responseBytes || 0)
    current.lastSeen = new Date(signal.observedAt).getTime() || Date.now()
    if (signal.fault) { current.fault = signal.fault; current.faultSeen = current.lastSeen }
  } else {
    const observedAt = new Date(signal.completedAt || signal.startedAt).getTime() || Date.now()
    current.samples.push({ observedAt, duration: signal.durationMs || 0, error: !!signal.error || (signal.status || 0) >= 500 })
    current.bytes += Math.max(0, signal.requestBytes || 0) + Math.max(0, signal.responseBytes || 0)
    current.lastSeen = new Date(signal.completedAt || signal.startedAt).getTime() || Date.now()
    current.latestSequence = Math.max(current.latestSequence, signal.sequence || 0)
    if (signal.fault) { current.fault = signal.fault; current.faultSeen = observedAt }
  }
  next.set(key, current)
  return next
}

export function topologyEdgeTone(metric: TopologyEdgeMetric | undefined, hasFault: boolean, now = Date.now()) {
  if (hasFault) return 'fault'
  if (!metric || now-metric.lastSeen > topologyWindowMilliseconds) return 'idle'
  if (metric.fault && metric.faultSeen && now-metric.faultSeen <= topologyWindowMilliseconds) return 'fault'
  const samples = metric.samples.filter((sample) => now-sample.observedAt <= topologyWindowMilliseconds)
  if (samples.some((sample) => sample.error)) return 'error'
  if (samples.length > 0 && samples.reduce((sum, sample) => sum+sample.duration, 0)/samples.length >= 500) return 'slow'
  return 'active'
}

function topologyEdgeLabel(edge: TopologyEdge, metric: TopologyEdgeMetric | undefined, now: number, activeFault?: string) {
  if (activeFault) return `▲ ${activeFault}`
  if (!metric || now-metric.lastSeen > topologyWindowMilliseconds) return edge.protocol.toUpperCase()
  if (edge.protocol !== 'http') return metric.activeConnections > 0 ? `${metric.activeConnections} OPEN · ${formatBytes(metric.bytes)}` : formatBytes(metric.bytes)
  const samples = metric.samples.filter((sample) => now-sample.observedAt <= topologyWindowMilliseconds)
  const requestsPerSecond = samples.length/(topologyWindowMilliseconds/1000)
  const average = samples.length ? Math.round(samples.reduce((sum, sample) => sum+sample.duration, 0)/samples.length) : 0
  const errors = samples.filter((sample) => sample.error).length
  return `${requestsPerSecond < 0.1 ? requestsPerSecond.toFixed(2) : requestsPerSecond.toFixed(1)} RPS · ${average}MS${errors ? ` · ${errors} ERR` : ''}`
}

function formatBytes(value: number) {
  if (value >= 1_000_000) return `${(value/1_000_000).toFixed(1)} MB`
  if (value >= 1_000) return `${(value/1_000).toFixed(1)} KB`
  return `${value} B`
}

export function topologyEdgeVisualState(metric: TopologyEdgeMetric | undefined, now: number, hasFault: boolean) {
  return hasFault || (metric && now-metric.lastSeen <= topologyWindowMilliseconds) ? topologyActiveEdgeVisual : topologyInactiveEdgeVisual
}

export function topologyParticleMotion(metric: TopologyEdgeMetric | undefined, now: number) {
  if (!metric || now-metric.lastSeen > topologyWindowMilliseconds) return { count: 0, durationSeconds: 0 }
  const recentRequests = metric.samples.filter((sample) => now-sample.observedAt <= topologyWindowMilliseconds).length
  if (recentRequests > 0) {
    const requestsPerSecond = recentRequests/(topologyWindowMilliseconds/1000)
    const count = requestsPerSecond >= 5 ? 4 : requestsPerSecond >= 2 ? 3 : requestsPerSecond >= .75 ? 2 : 1
    return { count, durationSeconds: Math.min(12, Math.max(.9, count/requestsPerSecond)) }
  }
  if (metric.activeConnections > 0) return { count: Math.min(3, metric.activeConnections), durationSeconds: 3.5 }
  if (metric.bytes > 0) return { count: 1, durationSeconds: 4.5 }
  return { count: 0, durationSeconds: 0 }
}

export function topologyPanPosition(origin: { clientX: number; clientY: number; scrollLeft: number; scrollTop: number }, clientX: number, clientY: number) {
  return { scrollLeft: origin.scrollLeft-(clientX-origin.clientX), scrollTop: origin.scrollTop-(clientY-origin.clientY) }
}

export function topologyCenterPosition(viewport: { scrollWidth: number; clientWidth: number; scrollHeight: number; clientHeight: number }) {
  return {
    scrollLeft: Math.max(0, (viewport.scrollWidth-viewport.clientWidth)/2),
    scrollTop: Math.min(120, Math.max(0, (viewport.scrollHeight-viewport.clientHeight)/2)),
  }
}

function TopologyCenterIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="3" /><path d="M8 1v3M8 12v3M1 8h3M12 8h3" /></svg>
}

function TopologySizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}

function Topology({ environment, faults, paused, centerRequest, onService, onEdge }: { environment: Environment; faults: FaultRule[]; paused: boolean; centerRequest: number; onService: (service: Service) => void; onEdge: (edge: TopologyEdge) => void }) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const handledCenterRequest = useRef(centerRequest)
  const pan = useRef<{ pointerId: number; clientX: number; clientY: number; scrollLeft: number; scrollTop: number; dragging: boolean } | null>(null)
  const suppressClick = useRef(false)
  const [isPanning, setIsPanning] = useState(false)
  const [edgeMetrics, setEdgeMetrics] = useState<Map<string, TopologyEdgeMetric>>(new Map())
  const [now, setNow] = useState(Date.now())
  const { levels, edges } = buildTopology(environment)
  const rowGap = 48
  const nodeWidth = 164
  const nodeHeight = 72
  const columnGap = 112
  const sidePadding = 54
  const verticalPadding = 40
  const positions = new Map<string, { x: number; y: number }>()
  const widestLevel = Math.max(1, ...levels.map((level) => level.length))
  const width = sidePadding*2 + levels.length*nodeWidth + Math.max(0, levels.length-1)*columnGap
  const height = Math.max(280, verticalPadding*2 + widestLevel*nodeHeight + Math.max(0, widestLevel-1)*rowGap)
  levels.forEach((level, depth) => {
    const columnHeight = level.length*nodeHeight + Math.max(0, level.length-1)*rowGap
    const start = (height-columnHeight)/2
    level.forEach((item, index) => positions.set(item.key, { x: sidePadding+depth*(nodeWidth+columnGap), y: start+index*(nodeHeight+rowGap) }))
  })
  const activeFaultEdges = useMemo(() => new Map(faults.map((fault) => [topologyEdgeKey(fault.source, fault.target), fault.name])), [faults])

  useEffect(() => {
    let active = true
    api<{ exchanges: TrafficExchange[] }>(environmentPath(environment, '/traffic/exchanges?protocol=all&limit=1000')).then((result) => {
      if (active) setEdgeMetrics(summarizeTopologyTraffic(result.exchanges))
    }).catch(() => undefined)
    return () => { active = false }
  }, [environment.project, environment.name])

  useEffect(() => {
    if (paused) return
    return connectEvents(environment, ['traffic.exchange', 'traffic.tcp.activity'], (type, value) => {
      if (type.startsWith('traffic.')) setEdgeMetrics((metrics) => mergeTopologySignal(metrics, value as TopologySignal))
    })
  }, [environment.project, environment.name, paused])

  useEffect(() => {
    if (paused) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [paused])

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport) return
    const frame = requestAnimationFrame(() => {
      const position = topologyCenterPosition(viewport)
      viewport.scrollTo({ left: position.scrollLeft, top: position.scrollTop })
    })
    return () => cancelAnimationFrame(frame)
  }, [environment.project, environment.name])

  useEffect(() => {
    if (handledCenterRequest.current === centerRequest) return
    handledCenterRequest.current = centerRequest
    const viewport = viewportRef.current
    if (!viewport) return
    const position = topologyCenterPosition(viewport)
    const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    viewport.scrollTo({ left: position.scrollLeft, top: position.scrollTop, behavior: reducedMotion ? 'auto' : 'smooth' })
  }, [centerRequest])

  const startPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement
    if (event.button !== 0 || target.closest('.topology__edge-action')) return
    const viewport = event.currentTarget
    pan.current = {
      pointerId: event.pointerId,
      clientX: event.clientX,
      clientY: event.clientY,
      scrollLeft: viewport.scrollLeft,
      scrollTop: viewport.scrollTop,
      dragging: false,
    }
  }

  const movePan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const origin = pan.current
    if (!origin || origin.pointerId !== event.pointerId) return
    const deltaX = event.clientX-origin.clientX
    const deltaY = event.clientY-origin.clientY
    if (!origin.dragging && Math.hypot(deltaX, deltaY) < 4) return
    if (!origin.dragging) {
      origin.dragging = true
      suppressClick.current = true
      event.currentTarget.setPointerCapture(event.pointerId)
      setIsPanning(true)
    }
    const next = topologyPanPosition(origin, event.clientX, event.clientY)
    event.currentTarget.scrollLeft = next.scrollLeft
    event.currentTarget.scrollTop = next.scrollTop
    event.preventDefault()
  }

  const stopPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const origin = pan.current
    if (origin?.pointerId !== event.pointerId) return
    pan.current = null
    setIsPanning(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
    if (origin.dragging) window.setTimeout(() => { suppressClick.current = false }, 0)
  }

  const selectService = (service: Service) => {
    if (suppressClick.current) {
      suppressClick.current = false
      return
    }
    onService(service)
  }

  const selectEdge = (edge: TopologyEdge) => {
    if (suppressClick.current) {
      suppressClick.current = false
      return
    }
    onEdge(edge)
  }

  return <div
    ref={viewportRef}
    className={`topology${isPanning ? ' is-panning' : ''}`}
    tabIndex={0}
    aria-label="Topology canvas; drag to pan"
    onPointerDown={startPan}
    onPointerMove={movePan}
    onPointerUp={stopPan}
    onPointerCancel={stopPan}
    onLostPointerCapture={stopPan}
  ><div className="topology__pan-surface"><div className="topology__canvas" style={{ width, height }}>
    <svg className="topology__edges" width={width} height={height} aria-hidden="true">
      <defs>
        <marker id={topologyInactiveEdgeVisual.markerID} viewBox="0 0 8 8" refX="7" refY="4" markerUnits="userSpaceOnUse" markerWidth={topologyInactiveArrowSize} markerHeight={topologyInactiveArrowSize} orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker>
        <marker id={topologyActiveEdgeVisual.markerID} viewBox="0 0 8 8" refX="7" refY="4" markerUnits="userSpaceOnUse" markerWidth={topologyActiveArrowSize} markerHeight={topologyActiveArrowSize} orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker>
      </defs>
      {edges.map((edge) => {
        const from = positions.get(edge.source)
        const to = positions.get(edge.target)
        if (!from || !to) return null
        const startX = from.x+nodeWidth
        const startY = from.y+nodeHeight/2
        const endX = to.x
        const endY = to.y+nodeHeight/2
        const middleX = (startX+endX)/2
        const middleY = (startY+endY)/2
        const edgeKey = topologyEdgeKey(edge.source, edge.target)
        const metric = edgeMetrics.get(edgeKey)
        const activeFault = activeFaultEdges.get(edgeKey)
        const tone = topologyEdgeTone(metric, !!activeFault, now)
        const path = `M ${startX} ${startY} C ${middleX} ${startY}, ${middleX} ${endY}, ${endX} ${endY}`
        const particleMotion = topologyParticleMotion(metric, now)
        const visualState = topologyEdgeVisualState(metric, now, !!activeFault)
        return <g key={`${edge.source}:${edge.target}`} className={`topology-edge topology-edge--${tone}`}>
          <path className="topology-edge__line" d={path} style={{ strokeWidth: visualState.strokeWidth, markerEnd: `url(#${visualState.markerID})` }} />
          {!paused && Array.from({ length: particleMotion.count }, (_, index) => <circle key={index} className="topology-edge__pulse" r="3"><animateMotion dur={`${particleMotion.durationSeconds}s`} begin={`${-(index*particleMotion.durationSeconds/particleMotion.count)}s`} repeatCount="indefinite" path={path} /></circle>)}
          <text x={middleX} y={middleY-10}>{topologyEdgeLabel(edge, metric, now, activeFault)}</text>
        </g>
      })}
    </svg>
    <svg className="topology__edge-actions" width={width} height={height} aria-label="Topology connections">
      {edges.map((edge) => {
        const from = positions.get(edge.source)
        const to = positions.get(edge.target)
        if (!from || !to) return null
        const startX = from.x+nodeWidth
        const startY = from.y+nodeHeight/2
        const endX = to.x
        const endY = to.y+nodeHeight/2
        const middleX = (startX+endX)/2
        const middleY = (startY+endY)/2
        return <g key={`${edge.source}:${edge.target}`} className="topology__edge-action" role="button" tabIndex={0} aria-label={`Inspect traffic from ${edge.source} to ${edge.target}`} onClick={() => selectEdge(edge)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectEdge(edge) } }}>
          <path className="topology__edge-hit" d={`M ${startX} ${startY} C ${middleX} ${startY}, ${middleX} ${endY}, ${endX} ${endY}`} />
          <rect className="topology__edge-label-hit" x={middleX-54} y={middleY-30} width="108" height="28" rx="6" />
        </g>
      })}
    </svg>
    {levels.flat().map((item) => {
      const position = positions.get(item.key)!
      if (item.kind === 'client') return <div key={item.key} className="topology__external topology__item" style={{ left: position.x, top: position.y }}><span>INGRESS</span><strong>browser / client</strong><small>localhost</small></div>
      const service = item.service
      return <button key={item.key} style={{ left: position.x, top: position.y }} className={`topology-node topology__item topology-node--${service.kind} ${service.name === environment.primaryService ? 'is-primary' : ''}`} onClick={() => selectService(service)}>
        <span><StatusMark status={service.status} label={false} />{service.kind === 'resource' ? service.resource?.type : service.framework}</span><strong>{service.name}</strong><small>{publicEndpoint(service)?.url.replace(/^[a-z]+:\/\//, '') || service.status}</small>
      </button>
    })}
  </div></div></div>
}

export function buildTopology(environment: Environment) {
  const services = new Map(environment.services.map((service) => [service.name, service]))
  const primary = environment.primaryService && services.has(environment.primaryService) ? environment.primaryService : environment.services[0]?.name
  const graphEdges = environment.connections.filter((connection) => services.has(connection.source) && services.has(connection.target))
  const edges = [...(primary ? [{ source: 'external', target: primary, protocol: 'http' as const }] : []), ...graphEdges]
  const depths = new Map<string, number>(primary ? [[primary, 1]] : [])
  const incoming = new Map<string, string[]>()
  for (const edge of graphEdges) incoming.set(edge.target, [...(incoming.get(edge.target) || []), edge.source])
  const depthFor = (name: string, visiting = new Set<string>()): number => {
    const known = depths.get(name)
    if (known) return known
    if (visiting.has(name)) return 1
    visiting.add(name)
    let depth = 1
    for (const source of incoming.get(name) || []) {
      if (source !== name) depth = Math.max(depth, depthFor(source, visiting) + 1)
    }
    visiting.delete(name)
    depths.set(name, depth)
    return depth
  }
  for (const service of environment.services) depthFor(service.name)
  const maxDepth = Math.max(1, ...depths.values())
  const levels: TopologyItem[][] = [[{ kind: 'client', key: 'external' }]]
  for (let depth = 1; depth <= maxDepth; depth++) {
    const level = environment.services.filter((service) => depths.get(service.name) === depth).map((service): TopologyItem => ({ kind: 'service', key: service.name, service }))
    if (level.length) levels.push(level)
  }
  return { levels, edges }
}

function ServiceDrawer({ environment, service, onClose, onChanged }: { environment: Environment; service: Service; onClose: () => void; onChanged: () => void }) {
  const [configuration, setConfiguration] = useState<{ environment?: Array<{ key: string; value: string; classification: string; source: string }> } | null>(null)
  const [drawerTab, setDrawerTab] = useState<'details' | 'logs' | 'configuration'>('details')
  const [busy, setBusy] = useState<ServiceAction | ''>('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const actionInFlight = useRef(false)
  const [fullScreen, setFullScreen] = useState(false)
  const base = environmentPath(environment, `/services/${encodeURIComponent(service.name)}`)
  useEffect(() => {
    api<typeof configuration>(`${base}/configuration`).then(setConfiguration).catch(() => setConfiguration(null))
  }, [base, environment.name, service.name])
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (fullScreen) setFullScreen(false)
      else onClose()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [fullScreen, onClose])
  const action = async (name: ServiceAction) => {
    if (actionInFlight.current) return
    actionInFlight.current = true
    setBusy(name)
    setError(null)
    try {
      const operation = await api<Operation>(`${base}/${name}`, { method: 'POST' })
      onChanged()
      const completed = await waitForServiceOperation(environment, operation)
      if (completed.state === 'failed') throw new Error(completed.error || `${service.name} ${name} failed`)
      onChanged()
    } catch (value) {
      setError(actionError(`Couldn't ${serviceActionDescription(name)} ${service.name}`, value))
    } finally {
      actionInFlight.current = false
      setBusy('')
      onChanged()
    }
  }
  const endpoints = serviceEndpoints(service, bindingFor(environment, service.name))
  const httpEndpoint = publicEndpoint(service, 'http')
  const localProcess = service.kind === 'process' && bindingFor(environment, service.name)?.provider === 'local'
  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className={`drawer ${fullScreen ? 'drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label={`${service.name} service`} onMouseDown={(event) => event.stopPropagation()}>
      <header><div><span className="eyebrow">{environment.project} / {environment.name} / service</span><h2>{service.name}</h2><StatusMark status={service.status} /></div><div className="drawer-header-actions"><DrawerSizeButton fullScreen={fullScreen} subject={`${service.name} service`} onToggle={() => setFullScreen((value) => !value)} /><button className="icon-button" onClick={onClose} aria-label="Close">×</button></div></header>
      <div className="drawer-actions" aria-busy={!!busy}>{localProcess && service.debug && <button className="button button--primary" type="button" onClick={() => void action(service.launchMode === 'debug' ? 'manage' : 'debug')} disabled={!!busy}>{busy === 'debug' || busy === 'manage' ? serviceActionProgressLabel(busy) : service.launchMode === 'debug' ? 'RUN NORMALLY' : 'DEBUG'}</button>}<button className={`button${!localProcess || !service.debug ? ' button--primary' : ''}`} type="button" onClick={() => void action(service.status === 'ready' ? 'restart' : 'start')} disabled={!!busy}>{busy === 'restart' || busy === 'start' ? serviceActionProgressLabel(busy) : service.status === 'ready' ? 'RESTART' : 'START'}</button><button className="button" type="button" onClick={() => void action('stop')} disabled={!!busy || service.status === 'stopped'}>{busy === 'stop' ? serviceActionProgressLabel(busy) : 'STOP'}</button>{httpEndpoint && <a className="button" href={httpEndpoint.url} target="_blank" rel="noreferrer">OPEN ↗</a>}</div>
      {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
      <nav className="drawer-tabs">{(['details', 'logs', 'configuration'] as const).map((name) => <button key={name} className={drawerTab === name ? 'is-active' : ''} onClick={() => setDrawerTab(name)}>{name}</button>)}</nav>
      <div className="drawer-content">
        {drawerTab === 'details' && <>
          <div className="detail-grid"><Detail label="KIND" value={service.framework || service.resource?.type || service.kind} /><Detail label="MODE" value={displayLaunchMode(environment, service)} /><Detail label="GENERATION" value={String(service.generation || '—')} /><Detail label="PID" value={String(service.pid || '—')} /><Detail label="UPSTREAM" value={service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : '—'} /><Detail label="RESTARTS" value={String(service.restartCount)} /><Detail label="STARTED" value={service.startedAt ? `${relativeTime(service.startedAt)} ago` : '—'} /></div>
          {service.debugger && <section className="drawer-section"><div className="eyebrow">DEBUGGER</div><pre>{service.debugger.adapter} · {service.debugger.host}:{service.debugger.port}</pre><small>{service.debugger.state}. Use your IDE's Attach to Process action and choose the matching Node or JVM process.</small></section>}
          <section className="drawer-section service-endpoints"><div className="eyebrow">ENDPOINTS</div><div className="service-endpoint-list">{endpoints.map((endpoint) => <div className="service-endpoint" key={`${endpoint.label}:${endpoint.value}`}><span>{endpoint.label}</span>{endpoint.href ? <a href={endpoint.href} target="_blank" rel="noreferrer">{endpoint.value} ↗</a> : <code>{endpoint.value}</code>}<small>{endpoint.detail}</small></div>)}{endpoints.length === 0 && <p className="muted">No endpoint is available while this service is stopped.</p>}</div></section>
          <section className="drawer-section"><div className="eyebrow">COMMAND</div><pre>{service.command?.join(' ') || `managed ${service.resource?.type} ${service.resource?.version}`}</pre></section>
          <section className="drawer-section"><div className="eyebrow">HEALTH</div><p><StatusMark status={service.status} /> {service.health.kind}{service.health.path ? ` ${service.health.path}` : ''}</p><small>{service.reason || 'No current readiness error.'}</small></section>
        </>}
        {drawerTab === 'logs' && <ServiceLogs environment={environment} service={service.name} />}
        {drawerTab === 'configuration' && <div className="config-table"><div className="config-row config-row--head"><span>KEY</span><span>EFFECTIVE VALUE</span><span>SOURCE</span></div>{configuration?.environment?.map((item) => <div className="config-row" key={item.key}><code>{item.key}</code><span className={item.classification === 'masked' ? 'masked-value' : ''}>{item.value}</span><small>{item.source} · {item.classification}</small></div>)}{!configuration?.environment?.length && <div className="empty-row">No static environment values were discovered. Connection bindings are generated at runtime.</div>}</div>}
      </div>
    </aside>
  </div>
}

type ServiceAction = 'restart' | 'stop' | 'start' | 'debug' | 'manage'

function serviceActionProgressLabel(action: ServiceAction) {
  switch (action) {
    case 'debug': return 'STARTING DEBUG…'
    case 'manage': return 'RUNNING NORMALLY…'
    case 'restart': return 'RESTARTING…'
    case 'start': return 'STARTING…'
    case 'stop': return 'STOPPING…'
  }
}

function serviceActionDescription(action: ServiceAction) {
  switch (action) {
    case 'debug': return 'start debugging'
    case 'manage': return 'run'
    default: return action
  }
}

async function waitForServiceOperation(environment: Environment, operation: Operation): Promise<Operation> {
  let current = operation
  while (current.state === 'running') {
    await new Promise((resolve) => window.setTimeout(resolve, 250))
    current = await api<Operation>(environmentPath(environment, `/operations/${current.number}`))
  }
  return current
}

type ServiceEndpoint = { label: string; value: string; detail: string; href?: string }

export function serviceEndpoints(service: Service, binding?: ComponentBinding): ServiceEndpoint[] {
  const endpoints: ServiceEndpoint[] = []
  const seen = new Set<string>()
  const add = (endpoint: ServiceEndpoint) => {
    if (!endpoint.value || seen.has(endpoint.value)) return
    seen.add(endpoint.value)
    endpoints.push(endpoint)
  }

  for (const endpoint of service.endpoints || []) {
    if (endpoint.kind !== 'public') continue
    add({
      label: endpoint.protocol === 'http' ? 'CLEAN URL' : 'PUBLIC ENDPOINT',
      value: endpoint.url,
      detail: endpoint.protocol === 'http' ? 'Browser and host access through Portless' : `Stable ${endpoint.protocol} endpoint through Portless`,
      ...(isWebURL(endpoint.url) ? { href: endpoint.url } : {}),
    })
  }
  if (binding?.remote?.url) add({ label: 'REMOTE PROVIDER', value: binding.remote.url, detail: `${binding.remote.classification} · ${binding.remote.writePolicy}`, ...(isWebURL(binding.remote.url) ? { href: binding.remote.url } : {}) })

  return endpoints
}

function publicEndpoint(service: Service, protocol?: Protocol) {
  return (service.endpoints || []).find((endpoint) => endpoint.kind === 'public' && (!protocol || endpoint.protocol === protocol))
}

function isWebURL(value: string) { return /^https?:\/\//.test(value) }

function Detail({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><strong>{value}</strong></div> }

function RecordingsPanel({ environment, recordings, refresh }: { environment: Environment; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('checkout-debug')
  const [scopeID, setScopeID] = useState('')
  const [captureBodies, setCaptureBodies] = useState(false)
  const [maxBodyBytes, setMaxBodyBytes] = useState(65536)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const selectedScope = scopes.find((scope) => scope.id === scopeID)
  useEffect(() => { setScopeID(''); setError(null) }, [environment.project, environment.name])
  const start = async () => {
    setError(null)
    const recordingName = name.trim()
    if (!recordingName) { setError(actionError("Recording wasn't started", 'Enter a recording name.')); return }
    try { await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody({ name: recordingName, source: selectedScope?.source || '', target: selectedScope?.target || '', captureBodies, maxEvents: 10000, maxBodyBytes }) }); await refresh() }
    catch (value) { setError(actionError("Recording wasn't started", value)) }
  }
  const stop = async (recording: Recording) => {
    setError(null)
    try { await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/stop`), { method: 'POST' }); await refresh() }
    catch (value) { setError(actionError("Recording wasn't stopped", value)) }
  }
  const remove = async (recording: Recording) => {
    setError(null)
    try { await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' }); await refresh() }
    catch (value) { setError(actionError("Recording wasn't deleted", value)) }
  }
  return <div className="experiment-layout">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-form"><div className="panel-title"><span>START RECORDING</span></div><label><span>NAME</span><input value={name} onChange={(event) => { setName(event.target.value); setError(null) }} /></label><label><span>TRAFFIC SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} onChange={(event) => { setScopeID(event.target.value); setError(null) }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label><label className="recording-body-toggle"><input type="checkbox" checked={captureBodies} onChange={(event) => setCaptureBodies(event.target.checked)} /><span><strong>CAPTURE BODIES</strong><small>Needed when a recording will become a useful mock response.</small></span></label>{captureBodies && <><label><span>MAXIMUM BODY SIZE</span><select value={maxBodyBytes} onChange={(event) => setMaxBodyBytes(Number(event.target.value))}><option value={16384}>16 KiB</option><option value={65536}>64 KiB</option><option value={262144}>256 KiB</option><option value={1048576}>1 MiB</option></select></label><div className="recording-body-warning"><strong>SENSITIVE DATA</strong><span>Request and response bodies are retained locally. Header redaction does not remove secrets inside a body.</span></div></>}<button className="button button--primary" onClick={start}>● START RECORDING</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>RECORDINGS</span></div>{recordings.map((recording) => <div className="experiment-row" key={recording.name}><StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} /><div><strong>{recording.name}</strong><small>{recordingScopeLabel(recording)} · {recording.eventCount} events{recording.captureBodies ? ' · bodies captured' : ''}</small></div><span>{relativeTime(recording.startedAt)} ago</span><div>{recording.status === 'active' ? <button onClick={() => stop(recording)}>STOP</button> : <><a href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}>EXPORT</a><button onClick={() => remove(recording)}>DELETE</button></>}</div></div>)}{recordings.length === 0 && <div className="empty-row">No recordings. Start one before reproducing a local issue.</div>}</section>
  </div>
}

function FaultsPanel({ environment, faults, refresh }: { environment: Environment; faults: FaultRule[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('slow-downstream')
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const initialScope = preferredFaultScope(environment, scopes)?.id || ''
  const [scopeID, setScopeID] = useState(initialScope)
  const [effect, setEffect] = useState<'latency' | 'status' | 'abort'>('latency')
  const [value, setValue] = useState('2000')
  const [expiryMinutes, setExpiryMinutes] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const selectedScope = scopes.find((scope) => scope.id === scopeID)
  useEffect(() => { setScopeID(preferredFaultScope(environment, scopes)?.id || ''); setError(null) }, [environment.project, environment.name]) // eslint-disable-line react-hooks/exhaustive-deps
  const create = async () => {
    setError(null)
    const faultName = name.trim()
    if (!faultName) { setError(actionError("Fault wasn't enabled", 'Enter a fault name.')); return }
    if (!selectedScope) { setError(actionError("Fault wasn't enabled", 'No configurable connection is available in this environment.')); return }
    const body = {
      name: faultName, source: selectedScope.source, target: selectedScope.target, probability: 1,
      latencyMs: effect === 'latency' ? Number(value) : 0,
      statusCode: effect === 'status' ? Number(value) : 0,
      abort: effect === 'abort',
      ...(expiryMinutes ? { expiresAt: new Date(Date.now() + Number(expiryMinutes) * 60_000).toISOString() } : {}),
    }
    try { await api(environmentPath(environment, '/faults'), { method: 'POST', ...jsonBody(body) }); await refresh() }
    catch (reason) { setError(actionError("Fault wasn't enabled", reason)) }
  }
  const changeRule = async (fault: FaultRule, action: 'enable' | 'disable') => {
    setError(null)
    try { await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}/${action}`), { method: 'POST' }); await refresh() }
    catch (value) { setError(actionError(`Fault wasn't ${action}d`, value)) }
  }
  const remove = async (fault: FaultRule) => {
    setError(null)
    try { await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}`), { method: 'DELETE' }); await refresh() }
    catch (value) { setError(actionError("Fault wasn't deleted", value)) }
  }
  const clear = async () => {
    setError(null)
    try { await api(environmentPath(environment, '/faults/disable-all'), { method: 'POST' }); await refresh() }
    catch (value) { setError(actionError("Faults weren't disabled", value)) }
  }
  const hasActiveFaults = faults.some((fault) => fault.enabled)
  return <div className="experiment-layout">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-form"><div className="panel-title"><span>INTRODUCE FAILURE</span></div><label><span>NAME</span><input value={name} onChange={(event) => { setName(event.target.value); setError(null) }} /></label><label><span>CONNECTION</span><select aria-label="Fault connection" value={scopeID} onChange={(event) => { setScopeID(event.target.value); setError(null) }}>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label><div className="segmented">{(['latency', 'status', 'abort'] as const).map((item) => <button key={item} className={effect === item ? 'is-active' : ''} onClick={() => { setEffect(item); setValue(item === 'latency' ? '2000' : item === 'status' ? '503' : ''); setError(null) }}>{item}</button>)}</div>{effect !== 'abort' && <label><span>{effect === 'latency' ? 'MILLISECONDS' : 'HTTP STATUS'}</span><input type="number" value={value} onChange={(event) => { setValue(event.target.value); setError(null) }} /></label>}<label><span>AUTOMATIC DISABLE</span><select value={expiryMinutes} onChange={(event) => { setExpiryMinutes(event.target.value); setError(null) }}><option value="">Until manually disabled</option><option value="10">After 10 minutes</option><option value="30">After 30 minutes</option><option value="60">After 1 hour</option><option value="240">After 4 hours</option></select></label><button className="button button--warning" disabled={!selectedScope} onClick={create}>ENABLE FAULT</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>FAULT RULES</span><button disabled={!hasActiveFaults} onClick={clear}>DISABLE ALL</button></div>{faults.map((fault) => <div className={`experiment-row ${fault.enabled ? 'is-warning' : ''}`} key={fault.name}><StatusMark status={fault.enabled ? 'degraded' : 'stopped'} label={false} /><div><strong>{fault.name}</strong><small>{fault.scopeSummary}</small><small className="fault-lifetime">{faultLifetime(fault)}</small></div><span>{fault.matchCount} matches</span><div><button onClick={() => changeRule(fault, fault.enabled ? 'disable' : 'enable')}>{fault.enabled ? 'DISABLE' : 'ENABLE'}</button><button onClick={() => remove(fault)}>DELETE</button></div></div>)}{faults.length === 0 && <div className="empty-row">No fault rules have been created.</div>}</section>
  </div>
}

function faultLifetime(fault: FaultRule) {
  if (!fault.enabled) return 'disabled'
  if (!fault.expiresAt) return 'active until disabled'
  return `expires ${new Date(fault.expiresAt).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}`
}

function BindingsPanel({ environment, project, onNavigate, onChanged }: { environment: Environment; project?: Project; onNavigate: (path: string) => void; onChanged: () => void }) {
  const [providerPage, setProviderPage] = useState(0)
  const [checkoutPage, setCheckoutPage] = useState(0)
  const [service, setService] = useState(environment.services[0]?.name || '')
  const [provider, setProvider] = useState<ProviderKind>(environment.services[0]?.kind === 'resource' ? 'container' : 'local')
  const [source, setSource] = useState(environment.sources?.[0]?.name || '')
  const [remoteURL, setRemoteURL] = useState('')
  const [classification, setClassification] = useState<RemoteClassification>('qa')
  const [writePolicy, setWritePolicy] = useState<WritePolicy>('read-only')
  const [healthPath, setHealthPath] = useState('/health')
  const [mockProfile, setMockProfile] = useState('')
  const [mockProfiles, setMockProfiles] = useState<MockProfile[]>([])
  const [busyAction, setBusyAction] = useState<'save' | 'reset' | ''>('')
  const [configureOpen, setConfigureOpen] = useState(false)
  const [checkoutEdit, setCheckoutEdit] = useState<{ source: ProjectSource; checkout?: SourceBinding } | null>(null)
  const [checkoutRemove, setCheckoutRemove] = useState<{ source: ProjectSource; checkout: SourceBinding; usedBy: string[] } | null>(null)
  const [checkoutMutationBusy, setCheckoutMutationBusy] = useState(false)
  const [checkoutMutationError, setCheckoutMutationError] = useState<ActionErrorDetails | null>(null)
  const [checkoutNotice, setCheckoutNotice] = useState('')
  const [serviceLocked, setServiceLocked] = useState(false)
  const [saveError, setSaveError] = useState<ActionErrorDetails | null>(null)
  const configureButton = useRef<HTMLButtonElement>(null)
  const sourceActionFocus = useRef<HTMLButtonElement | null>(null)
  const returnFocus = useRef<HTMLButtonElement | null>(null)
  const serviceSelect = useRef<HTMLSelectElement>(null)
  const providerSelect = useRef<HTMLSelectElement>(null)
  const selected = environment.services.find((item) => item.name === service)
  const currentBinding = bindingFor(environment, service)
  const defaultBinding = selected ? defaultProviderBinding(project, environment, selected) : undefined
  const resetAvailable = !!currentBinding && !!defaultBinding && !providerBindingMatches(currentBinding, defaultBinding)
  const busy = busyAction !== ''
  const transitionBlocked = ['starting', 'stopping', 'recovering', 'unknown'].includes(environment.status)
  const providers = useMemo(() => paginateItems(environment.bindings || [], providerPage, 5), [environment.bindings, providerPage])
  const checkoutRows = useMemo(() => environmentCheckoutRows(project, environment), [project, environment])
  const checkouts = useMemo(() => paginateItems(checkoutRows, checkoutPage, 5), [checkoutRows, checkoutPage])
  const providerUnchanged = !!currentBinding && currentBinding.provider === provider && (
    provider === 'container' ||
    (provider === 'local' && currentBinding.source?.toLowerCase() === source.toLowerCase()) ||
    (provider === 'remote' && currentBinding.remote?.url === remoteURL && currentBinding.remote?.classification === classification && currentBinding.remote?.writePolicy === writePolicy && (currentBinding.remote?.healthPath || '') === healthPath) ||
    (provider === 'mock' && currentBinding.mock?.profile.toLowerCase() === mockProfile.toLowerCase())
  )

  const initializeProviderForm = (serviceName: string) => {
    const target = environment.services.find((item) => item.name === serviceName)
    const current = bindingFor(environment, serviceName)
    setService(serviceName)
    setProvider(current?.provider || (target?.kind === 'resource' ? 'container' : 'local'))
    setSource(current?.source || environment.sources?.[0]?.name || '')
    setRemoteURL(current?.remote?.url || '')
    setClassification(current?.remote?.classification || 'qa')
    setWritePolicy(current?.remote?.writePolicy || 'read-only')
    setHealthPath(current?.remote?.healthPath || '/health')
    setMockProfile(current?.mock?.profile || mockProfiles.find((profile) => profile.service.toLowerCase() === serviceName.toLowerCase())?.name || '')
  }

  useEffect(() => {
    api<{ mocks: MockProfile[] }>(environmentPath(environment, '/mocks')).then((result) => setMockProfiles(result.mocks)).catch(() => setMockProfiles([]))
  }, [environment.project, environment.name])

  useEffect(() => {
    setProviderPage(0)
    setCheckoutPage(0)
  }, [environment.project, environment.name])

  useEffect(() => {
    if (!configureOpen) return
    requestAnimationFrame(() => (serviceLocked ? providerSelect.current : serviceSelect.current)?.focus())
  }, [configureOpen, serviceLocked])

  useEffect(() => {
    if (!configureOpen) return
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) {
        setConfigureOpen(false)
        setSaveError(null)
        requestAnimationFrame(() => returnFocus.current?.focus())
      }
    }
    document.addEventListener('keydown', closeOnEscape)
    return () => document.removeEventListener('keydown', closeOnEscape)
  }, [busy, configureOpen])

  const openConfigure = (serviceName: string | undefined, trigger: HTMLButtonElement) => {
    initializeProviderForm(serviceName || environment.services[0]?.name || '')
    setServiceLocked(!!serviceName)
    returnFocus.current = trigger
    setSaveError(null)
    setConfigureOpen(true)
  }

  const closeConfigure = () => {
    if (busy) return
    setConfigureOpen(false)
    setSaveError(null)
    requestAnimationFrame(() => returnFocus.current?.focus())
  }

  const openCheckoutEdit = (item: { source: ProjectSource; checkout?: SourceBinding }, trigger: HTMLButtonElement) => {
    sourceActionFocus.current = trigger
    setCheckoutMutationError(null)
    setCheckoutEdit(item)
  }

  const closeCheckoutMutation = () => {
    if (checkoutMutationBusy) return
    setCheckoutEdit(null)
    setCheckoutRemove(null)
    setCheckoutMutationError(null)
    requestAnimationFrame(() => sourceActionFocus.current?.focus())
  }

  const saveCheckout = async (path: string) => {
    if (!checkoutEdit) return
    setCheckoutMutationBusy(true)
    setCheckoutMutationError(null)
    try {
      const result = await api<SourcePathMutation>(environmentPath(environment, `/sources/${encodeURIComponent(checkoutEdit.source.name)}`), {
        method: 'PUT', ...jsonBody({ path }),
      })
      await onChanged()
      setCheckoutNotice((result.warnings || []).join(' ') || `${checkoutEdit.source.name} now uses ${path}.`)
      setCheckoutEdit(null)
      requestAnimationFrame(() => sourceActionFocus.current?.focus())
    } catch (reason) {
      setCheckoutMutationError(actionError("Checkout wasn't updated", reason))
    } finally {
      setCheckoutMutationBusy(false)
    }
  }

  const removeCheckout = async () => {
    if (!checkoutRemove) return
    setCheckoutMutationBusy(true)
    setCheckoutMutationError(null)
    try {
      await api<SourcePathMutation>(environmentPath(environment, `/sources/${encodeURIComponent(checkoutRemove.source.name)}`), { method: 'DELETE' })
      await onChanged()
      setCheckoutNotice(`${checkoutRemove.source.name} is no longer checked out in ${environment.project}/${environment.name}.`)
      setCheckoutRemove(null)
      requestAnimationFrame(() => sourceActionFocus.current?.focus())
    } catch (reason) {
      setCheckoutMutationError(actionError("Checkout wasn't removed", reason))
    } finally {
      setCheckoutMutationBusy(false)
    }
  }

  const bind = async () => {
    setBusyAction('save')
    setSaveError(null)
    try {
      const binding: ComponentBinding = { service, provider }
      if (provider === 'local') binding.source = source
      if (provider === 'remote') binding.remote = { url: remoteURL, classification, writePolicy, healthPath }
      if (provider === 'mock') binding.mock = { profile: mockProfile }
      const operation = await api<Operation>(environmentPath(environment, `/bindings/${encodeURIComponent(service)}`), {
        method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify(binding),
      })
      const completed = await waitForServiceOperation(environment, operation)
      if (completed.state !== 'succeeded') throw new Error(completed.error || `Provider change ${completed.state}`)
      await onChanged()
      setConfigureOpen(false)
      requestAnimationFrame(() => returnFocus.current?.focus())
    } catch (reason) {
      setSaveError(actionError("Provider wasn't updated", reason))
    } finally {
      setBusyAction('')
    }
  }

  const reset = async () => {
    if (!defaultBinding) return
    setBusyAction('reset')
    setSaveError(null)
    try {
      const operation = await api<Operation>(environmentPath(environment, `/bindings/${encodeURIComponent(service)}`), {
        method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify(defaultBinding),
      })
      const completed = await waitForServiceOperation(environment, operation)
      if (completed.state !== 'succeeded') throw new Error(completed.error || `Provider reset ${completed.state}`)
      await onChanged()
      setConfigureOpen(false)
      requestAnimationFrame(() => returnFocus.current?.focus())
    } catch (reason) {
      setSaveError(actionError("Provider wasn't reset", reason))
    } finally {
      setBusyAction('')
    }
  }

  return <>
    <div className="experiment-layout bindings-layout">
      {checkoutNotice && <div className="mock-warning source-add-notice"><strong>CHECKOUT CHANGE</strong><span>{checkoutNotice}</span><button type="button" onClick={() => setCheckoutNotice('')}>DISMISS</button></div>}
      <section className="panel experiment-list configured-providers-panel">
        <div className="panel-title"><span>PROVIDERS</span><button ref={configureButton} className="button button--primary button--small panel-create-button configure-provider-button" type="button" aria-haspopup="dialog" disabled={!environment.services.length} onClick={(event) => openConfigure(undefined, event.currentTarget)}>CONFIGURE PROVIDER</button></div>
        <div className="provider-table" role="table" aria-label="Configured providers">
          <div className="provider-row provider-row--header" role="row"><span role="columnheader">Service</span><span role="columnheader">Provider</span><span role="columnheader">Configuration</span><span role="columnheader">Modified</span><span role="columnheader" aria-label="Row actions" /></div>
          {providers.items.map((binding) => <div className={`experiment-row provider-row ${binding.provider === 'remote' ? 'is-warning' : ''}`} role="row" key={binding.service}>
            <div className="provider-service" role="cell"><StatusMark status={environment.services.find((item) => item.name === binding.service)?.status || 'planned'} label={false} /><strong>{binding.service}</strong></div>
            <div className="provider-kind" role="cell">{providerDisplayName(binding.provider)}</div>
            <div className="provider-configuration" role="cell">{binding.provider === 'remote' ? <code>{binding.remote?.url}</code> : binding.provider === 'local' ? <code>{binding.source}</code> : binding.provider === 'mock' ? <code>{binding.mock?.profile}</code> : <span>Portless managed</span>}</div>
            {binding.modifiedAt ? <time role="cell" dateTime={binding.modifiedAt} title={new Date(binding.modifiedAt).toLocaleString()}>{formatTimestamp(binding.modifiedAt)}</time> : <time role="cell">—</time>}
            <div className="provider-actions table-row-actions" role="cell"><button type="button" disabled={busy} onClick={(event) => openConfigure(binding.service, event.currentTarget)}>EDIT</button></div>
          </div>)}
          {!environment.bindings?.length && <div className="empty-row">No providers have been compiled for this environment.</div>}
        </div>
        <PanelPagination label="providers" pagination={providers} onPage={setProviderPage} />
      </section>
      <section className="panel source-checkouts-panel">
        <div className="panel-title"><span>CHECKOUTS</span><button type="button" onClick={() => onNavigate(`/projects/${encodeURIComponent(environment.project)}`)}>MANAGE SOURCES</button></div>
        {checkouts.total > 0 ? <table className="source-table" aria-label="Environment checkouts">
          <thead><tr><th scope="col">Source</th><th scope="col">Path</th><th scope="col">Created</th><th scope="col" aria-label="Row actions" /></tr></thead>
          <tbody>{checkouts.items.map((item) => <tr key={item.source.name}><td><div className="checkout-source"><StatusMark status={item.checkout ? item.checkout.status : item.required ? 'degraded' : 'stopped'} label={false} /><strong>{item.source.name}</strong></div></td><td>{item.checkout ? <code title={item.checkout.path}>{item.checkout.path}</code> : <span className={item.required ? 'warning-text' : 'muted'}>{item.required ? 'Configuration required' : 'Not configured'}</span>}</td><td>{item.checkout ? <time dateTime={item.checkout.createdAt} title={new Date(item.checkout.createdAt).toLocaleString()}>{formatTimestamp(item.checkout.createdAt)}</time> : <span>—</span>}</td><td><div className="table-row-actions">{item.checkout ? <><button type="button" disabled={checkoutMutationBusy} onClick={(event) => openCheckoutEdit(item, event.currentTarget)}>EDIT</button><button type="button" disabled={checkoutMutationBusy} onClick={(event) => { sourceActionFocus.current = event.currentTarget; setCheckoutMutationError(null); setCheckoutRemove({ source: item.source, checkout: item.checkout!, usedBy: item.usedBy }) }}>REMOVE</button></> : <button type="button" disabled={checkoutMutationBusy} onClick={(event) => openCheckoutEdit(item, event.currentTarget)}>CONFIGURE</button>}</div></td></tr>)}</tbody>
        </table> : <div className="empty-row">This project has no sources to configure.</div>}
        <PanelPagination label="checkouts" pagination={checkouts} onPage={setCheckoutPage} />
      </section>
    </div>
    {configureOpen && <div className="modal-backdrop form-modal-backdrop" role="presentation" onMouseDown={closeConfigure}>
      <section className="form-modal configure-provider-modal" role="dialog" aria-modal="true" aria-labelledby="configure-provider-title" aria-describedby="configure-provider-description" onMouseDown={(event) => event.stopPropagation()}>
        <header><div><div className="eyebrow">PROVIDER BINDING</div><h2 id="configure-provider-title">Configure Provider</h2></div><button className="icon-button" type="button" aria-label="Close configure provider" disabled={busy} onClick={closeConfigure}>×</button></header>
        <form onSubmit={(event) => { event.preventDefault(); void bind() }}>
          <p id="configure-provider-description">Choose how Portless should run or route this service in this environment.</p>
          <div className="form-modal__fields configure-provider-form__fields">
            {serviceLocked ? <div className="provider-service-value"><span>SERVICE</span><strong>{service}</strong></div> : <label><span>SERVICE</span><select ref={serviceSelect} aria-label="Service" value={service} disabled={busy} onChange={(event) => { initializeProviderForm(event.target.value); setSaveError(null) }}>{environment.services.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>}
            <label><span>PROVIDER</span><select ref={providerSelect} aria-label="Provider" value={provider} disabled={busy} onChange={(event) => { const next = event.target.value as ProviderKind; setProvider(next); if (next === 'mock' && !mockProfile) setMockProfile(mockProfiles.find((profile) => profile.service.toLowerCase() === service.toLowerCase())?.name || ''); setSaveError(null) }}>{selected?.kind === 'process' && <option value="local">{providerDisplayName('local')}</option>}{selected?.kind === 'resource' && <option value="container">{providerDisplayName('container')}</option>}{selected?.kind === 'process' && <option value="remote">{providerDisplayName('remote')}</option>}{selected?.kind === 'process' && <option value="mock">{providerDisplayName('mock')}</option>}</select></label>
            {provider === 'local' && (environment.sources?.length ? <label className="provider-field--wide"><span>SOURCE CHECKOUT</span><select aria-label="Source checkout" value={source} disabled={busy} onChange={(event) => { setSource(event.target.value); setSaveError(null) }}>{environment.sources.map((item) => <option key={item.name}>{item.name}</option>)}</select></label> : <ProviderInfoCard kind="checkout" title="CHECKOUT REQUIRED" description="Configure a checkout below before using the Checkout provider for this service." />)}
            {provider === 'remote' && <>
              <label className="provider-field--wide"><span>REMOTE URL</span><input aria-label="Remote URL" type="url" placeholder="https://payments.qa.example.com" value={remoteURL} disabled={busy} onChange={(event) => { setRemoteURL(event.target.value); setSaveError(null) }} /></label>
              <label><span>CLASSIFICATION</span><select aria-label="Classification" value={classification} disabled={busy} onChange={(event) => { setClassification(event.target.value as RemoteClassification); setSaveError(null) }}><option value="development">development</option><option value="qa">qa</option><option value="staging">staging</option><option value="unknown">unknown</option></select></label>
              <label><span>WRITE POLICY</span><select aria-label="Write policy" value={writePolicy} disabled={busy} onChange={(event) => { setWritePolicy(event.target.value as WritePolicy); setSaveError(null) }}><option value="read-only">read-only</option><option value="read-write">read-write</option></select></label>
              <label className="provider-field--wide"><span>HEALTH PATH</span><input aria-label="Health path" value={healthPath} disabled={busy} onChange={(event) => { setHealthPath(event.target.value); setSaveError(null) }} placeholder="/health" /></label>
              <ProviderInfoCard kind="remote" title="REMOTE BOUNDARY" description="Traffic still passes through Portless, so recordings and faults remain available. A read-only binding blocks POST, PUT, PATCH, and DELETE before they leave this machine." />
            </>}
            {provider === 'mock' && <>
              <label className="provider-field--wide"><span>MOCK PROFILE</span><select aria-label="Mock profile" value={mockProfile} disabled={busy} onChange={(event) => { setMockProfile(event.target.value); setSaveError(null) }}><option value="">Choose a profile for {service}</option>{mockProfiles.filter((profile) => profile.service.toLowerCase() === service.toLowerCase()).map((profile) => <option value={profile.name} key={profile.name}>{profile.name} · {profile.routes.length} routes</option>)}</select></label>
              <ProviderInfoCard kind="mock" title="LOCAL MOCK" description="Portless stops this service, keeps its clean URL, and serves the selected profile through normal traffic, recording, and fault handling." />
            </>}
            {transitionBlocked && <small className="provider-stop-note provider-field--wide">Wait for the environment to finish {environment.status} before changing a provider.</small>}
          </div>
          {saveError && <ActionErrorNotice error={saveError} onDismiss={() => setSaveError(null)} />}
          <footer>{resetAvailable && <button className="button button--quiet provider-reset-button" type="button" disabled={busy || transitionBlocked} onClick={() => void reset()}>{busyAction === 'reset' ? 'RESETTING…' : 'RESET TO DEFAULT'}</button>}<button className="button button--quiet" type="button" disabled={busy} onClick={closeConfigure}>CANCEL</button><button className={provider === 'remote' ? 'button button--warning' : 'button button--primary'} type="submit" disabled={busy || transitionBlocked || providerUnchanged || !service || (provider === 'remote' && !remoteURL) || (provider === 'local' && !source) || (provider === 'mock' && !mockProfile)}>{busyAction === 'save' ? 'SWITCHING…' : environment.status === 'stopped' ? 'SAVE CHANGES' : 'SWITCH PROVIDER'}</button></footer>
        </form>
      </section>
    </div>}
    {checkoutEdit && <ConfigureCheckoutModal environment={environment} source={checkoutEdit.source} checkout={checkoutEdit.checkout} busy={checkoutMutationBusy} error={checkoutMutationError} onDismissError={() => setCheckoutMutationError(null)} onClose={closeCheckoutMutation} onSave={saveCheckout} />}
    {checkoutRemove && <RemoveCheckoutModal environment={environment} source={checkoutRemove.source} usedBy={checkoutRemove.usedBy} busy={checkoutMutationBusy} error={checkoutMutationError} onDismissError={() => setCheckoutMutationError(null)} onClose={closeCheckoutMutation} onRemove={removeCheckout} />}
  </>
}

function ProviderInfoCard({ kind, title, description }: { kind: 'checkout' | 'remote' | 'mock'; title: string; description: string }) {
  return <aside className={`provider-info-card provider-info-card--${kind} provider-field--wide`} role="note">
    <span className="provider-info-card__icon" aria-hidden="true">
      {kind === 'remote' ? <svg viewBox="0 0 24 24"><path d="M5 18h7a3 3 0 0 0 3-3v-3" /><path d="M11 6h7v7" /><path d="m10 14 8-8" /></svg> : kind === 'mock' ? <svg viewBox="0 0 24 24"><path d="M5 8h14v10H5z" /><path d="m9 11-2 2 2 2" /><path d="m15 11 2 2-2 2" /></svg> : <svg viewBox="0 0 24 24"><path d="M4 7h6l2 2h8v10H4z" /><path d="M4 7v12" /></svg>}
    </span>
    <div><strong>{title}</strong><p>{description}</p></div>
  </aside>
}

export function defaultProviderBinding(project: Project | undefined, environment: Environment, service: Service): ComponentBinding | undefined {
  if (service.kind === 'resource') return { service: service.name, provider: 'container' }
  const owner = project?.sources?.find((source) => source.services?.some((name) => name.toLowerCase() === service.name.toLowerCase()))
  if (!owner || !environment.sources?.some((source) => source.name.toLowerCase() === owner.name.toLowerCase())) return undefined
  return { service: service.name, provider: 'local', source: owner.name }
}

export function providerBindingMatches(binding: ComponentBinding, expected: ComponentBinding) {
  if (binding.provider !== expected.provider) return false
  if (binding.provider === 'local') return binding.source?.toLowerCase() === expected.source?.toLowerCase()
  if (binding.provider === 'mock') return binding.mock?.profile.toLowerCase() === expected.mock?.profile.toLowerCase()
  return binding.provider === 'container'
}

export function providerDisplayName(provider: ProviderKind) {
  if (provider === 'local') return 'Checkout'
  if (provider === 'container') return 'Container'
  if (provider === 'mock') return 'Mock'
  return 'Remote'
}

function environmentCheckoutRows(project: Project | undefined, environment: Environment): EnvironmentCheckoutRow[] {
  const declared = project?.sources?.length
    ? project.sources
    : (environment.sources || []).map((source) => ({ name: source.name, services: [] }))
  return declared.map((source) => {
    const checkout = environment.sources?.find((item) => item.name.toLowerCase() === source.name.toLowerCase())
    const usedBy = (environment.bindings || [])
      .filter((binding) => binding.provider === 'local' && binding.source?.toLowerCase() === source.name.toLowerCase())
      .map((binding) => binding.service)
      .sort()
    const owned = new Set((source.services || []).map((service) => service.toLowerCase()))
    const required = usedBy.length > 0 || (environment.issues || []).some((issue) =>
      (issue.code === 'MISSING_BINDING' || issue.code === 'MISSING_SOURCE') && !!issue.subject && owned.has(issue.subject.toLowerCase()),
    )
    return { source, checkout, usedBy, required }
  })
}

function formatTimestamp(value: string) {
  return new Date(value).toLocaleString([], { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

const timelinePageSizes = [25, 50, 100] as const

export function TimelinePanel({ timeline }: { timeline: TimelineEvent[] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState<number>(timelinePageSizes[0])
  const pagination = useMemo(() => paginateItems(timeline, page, pageSize), [timeline, page, pageSize])
  const groups = useMemo(() => pagination.items.reduce<Record<string, TimelineEvent[]>>((result, event) => {
    const key = new Date(event.timestamp).toLocaleDateString()
    ;(result[key] ||= []).push(event)
    return result
  }, {}), [pagination.items])

  return <section className="panel timeline-panel">
    <div className="panel-title">
      <span>ENVIRONMENT TIMELINE</span>
      <label className="timeline-page-size"><span>ROWS PER PAGE</span><select aria-label="Timeline rows per page" value={pageSize} onChange={(event) => { setPageSize(Number(event.target.value)); setPage(0) }}>{timelinePageSizes.map((size) => <option value={size} key={size}>{size}</option>)}</select></label>
    </div>
    {Object.entries(groups).map(([date, events]) => <div className="timeline-group" key={date}><div className="timeline-date">{date}</div>{events.map((event) => <div className="timeline-event" key={event.sequence}><time>{new Date(event.timestamp).toLocaleTimeString()}</time><span className={`timeline-dot timeline-dot--${event.severity}`} /><div><strong>{event.summary}</strong><small>{event.type} · {event.actor}{event.subject ? ` · ${event.subject}` : ''}</small></div><code>#{event.sequence}</code></div>)}</div>)}
    {timeline.length === 0 && <div className="empty-row">The timeline will capture lifecycle, recording, and fault events.</div>}
    <PanelPagination label="timeline" pagination={pagination} onPage={setPage} />
  </section>
}

function bindingFor(environment: Environment, service: string) {
  return environment.bindings?.find((binding) => binding.service === service)
}

export function displayLaunchMode(environment: Environment, service: Service) {
  const provider = bindingFor(environment, service.name)?.provider
  if (provider === 'mock') return 'mock'
  if (service.kind === 'resource' && provider === 'container') return 'container'
  if (service.kind !== 'process' || provider !== 'local') return '—'
  return service.launchMode || 'managed'
}

function environmentUIPath(environment: Environment, tab: Tab, options: { edge?: string; protocol?: 'http' | 'tcp'; profile?: string } = {}) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  if (tab === 'overview') return base
  const query = new URLSearchParams({ tab })
  if (options.edge) query.set('edge', options.edge)
  if (options.protocol) query.set('protocol', options.protocol)
  if (options.profile) query.set('profile', options.profile)
  return `${base}?${query}`
}
