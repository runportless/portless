import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { api, connectEvents, jsonBody, environmentPath } from '../api'
import type { ComponentBinding, FaultRule, LogEntry, Operation, Environment, Protocol, ProviderKind, Recording, RemoteClassification, Service, SourceBinding, TimelineEvent, TrafficActivity, TrafficEvent, WritePolicy } from '../types'
import { duration, relativeTime, StatePanel, StatusMark } from '../components/Status'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import { experimentScopes, preferredFaultScope, recordingScopeLabel } from './experimentScopes'

type Tab = 'overview' | 'topology' | 'bindings' | 'traffic' | 'recordings' | 'faults' | 'timeline'

export function EnvironmentPage({ environment, tab, onNavigate, onChanged }: { environment: Environment; tab: Tab; onNavigate: (path: string) => void; onChanged: () => void }) {
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
        {(['overview', 'topology', 'bindings', 'traffic', 'recordings', 'faults', 'timeline'] as Tab[]).map((name) => <button key={name} className={tab === name ? 'is-active' : ''} onClick={() => onNavigate(environmentUIPath(environment, name))}>{name}<small>{name === 'recordings' ? recordings.length : name === 'faults' ? activeFaults.length : ''}</small></button>)}
      </nav>
      {tab === 'overview' && <Overview environment={environment} timeline={timeline} ready={ready} faults={activeFaults} activeRecording={activeRecording} trafficCount={trafficCount} onService={setSelectedService} onTab={(next, edge, protocol) => onNavigate(environmentUIPath(environment, next, edge, protocol))} />}
      {tab === 'topology' && <TopologyView environment={environment} faults={activeFaults} onService={setSelectedService} onTab={(next, edge, protocol) => onNavigate(environmentUIPath(environment, next, edge, protocol))} />}
      {tab === 'bindings' && <BindingsPanel environment={environment} onChanged={onChanged} />}
      {tab === 'traffic' && <TrafficPanel environment={environment} />}
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
  const [servicePage, setServicePage] = useState(0)
  const [activityPage, setActivityPage] = useState(0)
  const [copiedEndpoint, setCopiedEndpoint] = useState('')
  const copyReset = useRef<number | undefined>(undefined)
  const services = paginateOverview(environment.services, servicePage)
  const activities = paginateOverview(timeline, activityPage)
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
      <StatePanel title="REVISION" value={environment.revision} detail={`updated · ${relativeTime(environment.updatedAt)} ago`} />
    </div>
    <section className="panel services-panel">
      <div className="panel-title"><span>SERVICES</span><small>{environment.services.length} managed workloads</small></div>
      <div className="table-row table-row--header service-row"><span /><span>Name</span><span>Provider</span><span>State</span><span>Restarts</span><span>Requests</span><span>P95</span><span>Endpoint / reason</span><span /></div>
      {services.items.map((service) => {
        const endpoint = overviewServiceEndpoint(environment, service)
        const copied = copiedEndpoint === service.name
        return <div className="table-row service-row service-row--interactive" key={service.name} onClick={() => onService(service)}>
          <StatusMark status={service.status} label={false} /><strong>{service.name}</strong><span>{bindingFor(environment, service.name)?.provider || service.kind}</span><StatusMark status={service.status} /><span className={service.restartCount ? 'warning-text' : ''}>{service.restartCount}</span><span>{service.recentRequests || '—'}</span><span>{service.p95Millis ? `${service.p95Millis}ms` : '—'}</span><span className="service-list-endpoint"><span className="truncate muted" title={service.reason || endpoint || 'not running'}>{service.reason || endpoint || 'not running'}</span>{!service.reason && endpoint && <button className={`service-copy-button${copied ? ' is-copied' : ''}`} type="button" aria-label={`Copy ${service.name} endpoint`} title={copied ? 'Copied' : 'Copy endpoint'} onClick={(event) => void copyServiceEndpoint(event, service.name, endpoint)}><CopyIcon copied={copied} /></button>}</span><button className="row-action" type="button" onClick={(event) => { event.stopPropagation(); onService(service) }}>INSPECT</button>
        </div>
      })}
      <PanelPagination label="services" pagination={services} onPage={setServicePage} />
    </section>
    <div className="overview-grid">
      <section className="panel topology-panel topology-panel--preview" aria-label="Service topology">
        <div className="panel-title topology-toolbar"><span>TOPOLOGY</span><div><TopologyLiveButton paused={topologyPaused} onToggle={() => setTopologyPaused((value) => !value)} /><button className="topology-size-button" type="button" title="Open topology" aria-label="Open topology" onClick={() => onTab('topology')}><TopologySizeIcon /></button></div></div>
        <Topology environment={environment} faults={faults} paused={topologyPaused} onService={onService} onEdge={(edge) => onTab('traffic', `${edge.source}:${edge.target}`, edge.protocol === 'http' ? 'http' : 'tcp')} />
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
    <div className="panel-title topology-toolbar"><span>TOPOLOGY</span><div><TopologyLiveButton paused={paused} onToggle={() => setPaused((value) => !value)} /><button className={maximized ? 'icon-button' : 'topology-size-button'} type="button" title={`${maximized ? 'Restore' : 'Maximize'} topology`} aria-label={`${maximized ? 'Restore' : 'Maximize'} topology`} aria-pressed={maximized} onClick={() => setMaximized((value) => !value)}>{maximized ? '×' : <TopologySizeIcon />}</button></div></div>
    <Topology environment={environment} faults={faults} paused={paused} onService={onService} onEdge={(edge) => onTab('traffic', `${edge.source}:${edge.target}`, edge.protocol === 'http' ? 'http' : 'tcp')} />
  </section>
}

function TopologyLiveButton({ paused, onToggle }: { paused: boolean; onToggle: () => void }) {
  return <button className={`topology-live${paused ? ' is-paused' : ''}`} type="button" title={paused ? 'Resume live topology' : 'Pause live topology'} onClick={onToggle}>{paused ? <svg className="topology-live__pause" viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg> : <i className="topology-live__dot" aria-hidden="true" />}{paused ? 'PAUSED' : 'LIVE'}</button>
}

const overviewPageSize = 8

type OverviewPagination<T> = { items: T[]; page: number; pageCount: number; start: number; end: number; total: number }

export function paginateOverview<T>(items: T[], requestedPage: number, pageSize = overviewPageSize): OverviewPagination<T> {
  const pageCount = Math.max(1, Math.ceil(items.length/pageSize))
  const page = Math.min(Math.max(0, requestedPage), pageCount-1)
  const start = page*pageSize
  const end = Math.min(items.length, start+pageSize)
  return { items: items.slice(start, end), page, pageCount, start, end, total: items.length }
}

function PanelPagination<T>({ label, pagination, onPage }: { label: string; pagination: OverviewPagination<T>; onPage: (page: number) => void }) {
  if (pagination.pageCount <= 1) return null
  return <footer className="panel-pagination" aria-label={`${label} pagination`}>
    <span>{pagination.start+1}–{pagination.end} of {pagination.total}</span>
    <div>
      <button type="button" aria-label={`Previous ${label} page`} disabled={pagination.page === 0} onClick={() => onPage(pagination.page-1)}>← PREV</button>
      <small>{pagination.page+1} / {pagination.pageCount}</small>
      <button type="button" aria-label={`Next ${label} page`} disabled={pagination.page === pagination.pageCount-1} onClick={() => onPage(pagination.page+1)}>NEXT →</button>
    </div>
  </footer>
}

type TopologyItem = { kind: 'client'; key: 'external' } | { kind: 'service'; key: string; service: Service }
type TopologySignal = TrafficEvent | TrafficActivity
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

export function topologyEdgeKey(source: string, target: string) { return `${source}\u0000${target}` }

export function summarizeTopologyTraffic(events: TrafficEvent[], now = Date.now()) {
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

function topologyEdgeWidth(metric: TopologyEdgeMetric | undefined, now: number, hasFault: boolean) {
  if (hasFault) return 2
  if (!metric || now-metric.lastSeen > topologyWindowMilliseconds) return 1
  const volume = metric.samples.filter((sample) => now-sample.observedAt <= topologyWindowMilliseconds).length || metric.activeConnections
  return Math.min(3.2, 1.35+Math.log2(volume+1)*.42)
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

function TopologySizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}

function Topology({ environment, faults, paused, onService, onEdge }: { environment: Environment; faults: FaultRule[]; paused: boolean; onService: (service: Service) => void; onEdge: (edge: TopologyEdge) => void }) {
  const viewportRef = useRef<HTMLDivElement>(null)
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
    Promise.all([
      api<{ traffic: TrafficEvent[] }>(environmentPath(environment, '/traffic?protocol=http&limit=1000')),
      api<{ traffic: TrafficEvent[] }>(environmentPath(environment, '/traffic?protocol=tcp&limit=1000')),
    ]).then(([http, tcp]) => {
      if (active) setEdgeMetrics(summarizeTopologyTraffic([...http.traffic, ...tcp.traffic]))
    }).catch(() => undefined)
    return () => { active = false }
  }, [environment.project, environment.name])

  useEffect(() => {
    if (paused) return
    return connectEvents(environment, ['traffic.http', 'traffic.tcp.activity'], (type, value) => {
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
      viewport.scrollLeft = Math.max(0, (viewport.scrollWidth-viewport.clientWidth)/2)
      viewport.scrollTop = Math.min(120, Math.max(0, (viewport.scrollHeight-viewport.clientHeight)/2))
    })
    return () => cancelAnimationFrame(frame)
  }, [environment.project, environment.name])

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
      <defs><marker id="topology-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker></defs>
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
        return <g key={`${edge.source}:${edge.target}`} className={`topology-edge topology-edge--${tone}`}>
          <path className="topology-edge__line" d={path} style={{ strokeWidth: topologyEdgeWidth(metric, now, !!activeFault) }} />
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
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [configuration, setConfiguration] = useState<{ environment?: Array<{ key: string; value: string; classification: string; source: string }> } | null>(null)
  const [drawerTab, setDrawerTab] = useState<'details' | 'logs' | 'configuration'>('details')
  const [busy, setBusy] = useState('')
  const [fullScreen, setFullScreen] = useState(false)
  const base = environmentPath(environment, `/services/${encodeURIComponent(service.name)}`)
  useEffect(() => {
    api<{ entries: LogEntry[] }>(`${environmentPath(environment, '/logs')}?service=${encodeURIComponent(service.name)}&limit=500`).then((value) => setLogs(value.entries)).catch(() => setLogs([]))
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
  const action = async (name: 'restart' | 'stop' | 'start') => {
    setBusy(name)
    try { await api<Operation>(`${base}/${name}`, { method: 'POST' }); onChanged() } finally { setBusy('') }
  }
  const endpoints = serviceEndpoints(service, bindingFor(environment, service.name))
  const httpEndpoint = publicEndpoint(service, 'http')
  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className={`drawer ${fullScreen ? 'drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label={`${service.name} service`} onMouseDown={(event) => event.stopPropagation()}>
      <header><div><span className="eyebrow">{environment.project} / {environment.name} / service</span><h2>{service.name}</h2><StatusMark status={service.status} /></div><div className="drawer-header-actions"><button className="drawer-size-button" type="button" aria-pressed={fullScreen} onClick={() => setFullScreen((value) => !value)}>{fullScreen ? 'RESTORE' : 'FULL SCREEN'}</button><button className="icon-button" onClick={onClose} aria-label="Close">×</button></div></header>
      <div className="drawer-actions"><button className="button button--primary" onClick={() => action(service.status === 'ready' ? 'restart' : 'start')} disabled={!!busy}>{busy || (service.status === 'ready' ? 'RESTART' : 'START')}</button><button className="button" onClick={() => action('stop')} disabled={!!busy || service.status === 'stopped'}>STOP</button>{httpEndpoint && <a className="button" href={httpEndpoint.url} target="_blank" rel="noreferrer">OPEN ↗</a>}</div>
      <nav className="drawer-tabs">{(['details', 'logs', 'configuration'] as const).map((name) => <button key={name} className={drawerTab === name ? 'is-active' : ''} onClick={() => setDrawerTab(name)}>{name}</button>)}</nav>
      <div className="drawer-content">
        {drawerTab === 'details' && <>
          <div className="detail-grid"><Detail label="KIND" value={service.framework || service.resource?.type || service.kind} /><Detail label="GENERATION" value={String(service.generation || '—')} /><Detail label="PID" value={String(service.pid || '—')} /><Detail label="UPSTREAM" value={service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : '—'} /><Detail label="RESTARTS" value={String(service.restartCount)} /><Detail label="STARTED" value={service.startedAt ? `${relativeTime(service.startedAt)} ago` : '—'} /></div>
          <section className="drawer-section service-endpoints"><div className="eyebrow">ENDPOINTS</div><div className="service-endpoint-list">{endpoints.map((endpoint) => <div className="service-endpoint" key={`${endpoint.label}:${endpoint.value}`}><span>{endpoint.label}</span>{endpoint.href ? <a href={endpoint.href} target="_blank" rel="noreferrer">{endpoint.value} ↗</a> : <code>{endpoint.value}</code>}<small>{endpoint.detail}</small></div>)}{endpoints.length === 0 && <p className="muted">No endpoint is available while this service is stopped.</p>}</div></section>
          <section className="drawer-section"><div className="eyebrow">COMMAND</div><pre>{service.command?.join(' ') || `managed ${service.resource?.type} ${service.resource?.version}`}</pre></section>
          <section className="drawer-section"><div className="eyebrow">HEALTH</div><p><StatusMark status={service.status} /> {service.health.kind}{service.health.path ? ` ${service.health.path}` : ''}</p><small>{service.reason || 'No current readiness error.'}</small></section>
        </>}
        {drawerTab === 'logs' && <div className="log-view"><div className="log-view__meta">last {logs.length} lines · stdout + stderr</div><pre>{logs.length ? logs.map((entry, index) => <span key={`${entry.timestamp}-${entry.stream}-${index}`}><i>{new Date(entry.timestamp).toLocaleTimeString()}</i>{entry.message}{'\n'}</span>) : 'No logs captured for this service.'}</pre></div>}
        {drawerTab === 'configuration' && <div className="config-table"><div className="config-row config-row--head"><span>KEY</span><span>EFFECTIVE VALUE</span><span>SOURCE</span></div>{configuration?.environment?.map((item) => <div className="config-row" key={item.key}><code>{item.key}</code><span className={item.classification === 'masked' ? 'masked-value' : ''}>{item.value}</span><small>{item.source} · {item.classification}</small></div>)}{!configuration?.environment?.length && <div className="empty-row">No static environment values were discovered. Connection bindings are generated at runtime.</div>}</div>}
      </div>
    </aside>
  </div>
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

function trafficHeaders(headers: Record<string, string> | undefined, host?: string) {
  const values = Object.entries(headers || {}).filter(([name]) => !host || name.toLowerCase() !== 'host')
  if (host) values.push(['Host', host])
  return values.length
    ? values.sort(([left], [right]) => left.localeCompare(right)).map(([name, value]) => `${name}: ${value}`).join('\n')
    : 'No headers captured'
}

function trafficBodySummary(bytes: number, direction: 'request' | 'response') {
  if (bytes <= 0) return `No ${direction} body`
  return `Body content is not available · ${formatBytes(bytes)} transferred`
}

function formatTrafficBody(body: string) {
  const trimmed = body.trim()
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { return JSON.stringify(JSON.parse(body), null, 2) } catch { /* Preserve malformed or streaming JSON as received. */ }
  }
  return body
}

function TrafficMessage({ event, direction }: { event: TrafficEvent; direction: 'request' | 'response' }) {
  const request = direction === 'request'
  const bytes = request ? event.requestBytes : event.responseBytes
  const body = request ? event.requestBody : event.responseBody
  const truncated = request ? event.requestBodyTruncated : event.responseBodyTruncated
  const startLine = request
    ? `${event.method || 'HTTP'} ${event.path || '/'}`
    : event.status ? `HTTP ${event.status}` : event.error ? 'HTTP ERROR' : 'HTTP response'
  const headers = trafficHeaders(request ? event.requestHeaders : event.responseHeaders, request ? event.host : undefined)
  return <section className={`traffic-message traffic-message--${direction}`}>
    <div className="traffic-message__title"><span>{direction.toUpperCase()}</span><small>{formatBytes(Math.max(0, bytes))}</small></div>
    <div className="traffic-message__line"><code>{startLine}</code></div>
    <div className="traffic-message__headers"><span>HEADERS · REDACTED</span><pre>{headers}</pre></div>
    <div className="traffic-message__body"><span>BODY{truncated ? ' · TRUNCATED' : ''}</span>{body ? <><pre>{formatTrafficBody(body)}</pre>{truncated && <small>Showing the first 64 KB of the {direction} body.</small>}</> : <strong>{trafficBodySummary(bytes, direction)}</strong>}</div>
  </section>
}

export function TrafficDetail({ event, onClose }: { event: TrafficEvent; onClose: () => void }) {
  return <aside className="traffic-detail" role="dialog" aria-label={`Traffic request and response ${event.sequence}`}>
    <header><div><span className="eyebrow">{event.protocol.toUpperCase()} TRAFFIC #{event.sequence}</span><h3>{event.method || event.protocol.toUpperCase()} {event.path || `${event.source} → ${event.target}`}</h3></div><button onClick={onClose} aria-label="Close traffic details" title="Close">×</button></header>
    <div className="detail-grid"><Detail label="EDGE" value={`${event.source} → ${event.target}`} /><Detail label="STATUS" value={event.error ? 'error' : String(event.status || 'ok')} /><Detail label="DURATION" value={duration(event.durationMs)} /><Detail label="PROVIDER" value={event.targetProvider || '—'} /><Detail label="FAULT" value={event.fault || 'none'} /><Detail label="RECORDING" value={event.recording || 'none'} /></div>
    {event.error && <div className="traffic-detail__error"><span>REQUEST ERROR</span><strong>{event.error}</strong></div>}
    {event.protocol === 'http'
      ? <div className="traffic-exchange"><TrafficMessage event={event} direction="request" /><TrafficMessage event={event} direction="response" /></div>
      : <div className="traffic-detail__notice"><span>TCP SESSION</span><strong>Payload content is not captured.</strong><small>{formatBytes(Math.max(0, event.requestBytes))} sent · {formatBytes(Math.max(0, event.responseBytes))} received</small></div>}
  </aside>
}

function TrafficPanel({ environment }: { environment: Environment }) {
  const requested = new URLSearchParams(location.search)
  const [traffic, setTraffic] = useState<TrafficEvent[]>([])
  const [selected, setSelected] = useState<TrafficEvent | null>(null)
  const [filter, setFilter] = useState(() => requested.get('edge') || '')
  const [protocol, setProtocol] = useState<'http' | 'tcp'>(() => requested.get('protocol') === 'tcp' ? 'tcp' : 'http')
  const [paused, setPaused] = useState(false)
  useEffect(() => {
    const topic = protocol === 'http' ? 'traffic.http' : 'traffic.tcp'
    setSelected(null)
    api<{ traffic: TrafficEvent[] }>(environmentPath(environment, `/traffic?protocol=${protocol}&limit=500`)).then((value) => setTraffic(value.traffic)).catch(() => setTraffic([]))
    return connectEvents(environment, [topic], (type, value) => {
      if (type === topic && !paused) setTraffic((items) => [value as TrafficEvent, ...items].slice(0, 1000))
    })
  }, [environment.project, environment.name, paused, protocol])
  const inspect = async (event: TrafficEvent) => {
    try {
      setSelected(await api<TrafficEvent>(environmentPath(environment, `/traffic/${event.sequence}`)))
    } catch {
      setSelected(event)
    }
  }
  const filtered = traffic.filter((event) => `${event.method} ${event.path} ${event.source} ${event.target} ${event.source}:${event.target} ${event.status}`.toLowerCase().includes(filter.toLowerCase()))
  return <section className="panel traffic-panel">
    <div className="panel-title traffic-toolbar"><span>LIVE {protocol.toUpperCase()} TRAFFIC</span><div><div className="traffic-protocol" role="group" aria-label="Traffic protocol"><button className={protocol === 'http' ? 'is-active' : ''} onClick={() => setProtocol('http')}>HTTP</button><button className={protocol === 'tcp' ? 'is-active' : ''} onClick={() => setProtocol('tcp')}>TCP</button></div><span className="live-count"><i />{paused ? 'PAUSED' : 'STREAMING'}</span><button className="button button--small" onClick={() => setPaused((value) => !value)}>{paused ? 'RESUME' : 'PAUSE'}</button><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="filter method, path, edge…" /></div></div>
    <div className="table-row table-row--header traffic-row"><span>Seq</span><span>When</span><span>Method</span><span>Path</span><span>Edge</span><span>Status</span><span>Duration</span><span>Fault / recording</span></div>
    {filtered.map((event) => <button className="table-row traffic-row" key={event.sequence} onClick={() => inspect(event)}><code>#{event.sequence}</code><span>{new Date(event.startedAt).toLocaleTimeString()}</span><strong>{event.method || event.protocol.toUpperCase()}</strong><code className="truncate">{event.path || 'TCP session'}</code><span>{event.source}<i className="edge-arrow">→</i>{event.target}</span><span className={event.error || (event.status || 0) >= 500 ? 'danger-text' : (event.status || 0) >= 400 ? 'warning-text' : ''}>{event.error ? 'ERR' : event.status || (event.protocol === 'tcp' ? 'OK' : '—')}</span><span>{duration(event.durationMs)}</span><span>{event.fault ? <b className="fault-chip">▲ {event.fault}</b> : event.recording ? <b className="record-chip">● {event.recording}</b> : '—'}</span></button>)}
    {filtered.length === 0 && <div className="empty-row">No matching {protocol.toUpperCase()} traffic yet.{protocol === 'http' && <> Requests through <code>service.{environment.name}.{environment.project}.localhost</code> or a discovered HTTP edge appear here.</>}</div>}
    {selected && <TrafficDetail event={selected} onClose={() => setSelected(null)} />}
  </section>
}

function RecordingsPanel({ environment, recordings, refresh }: { environment: Environment; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('checkout-debug')
  const [scopeID, setScopeID] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const selectedScope = scopes.find((scope) => scope.id === scopeID)
  useEffect(() => { setScopeID(''); setError(null) }, [environment.project, environment.name])
  const start = async () => {
    setError(null)
    const recordingName = name.trim()
    if (!recordingName) { setError(actionError("Recording wasn't started", 'Enter a recording name.')); return }
    try { await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody({ name: recordingName, source: selectedScope?.source || '', target: selectedScope?.target || '', captureBodies: false, maxEvents: 10000, maxBodyBytes: 65536 }) }); await refresh() }
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
    <section className="panel experiment-form"><div className="panel-title"><span>START RECORDING</span></div><label><span>NAME</span><input value={name} onChange={(event) => { setName(event.target.value); setError(null) }} /></label><label><span>TRAFFIC SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} onChange={(event) => { setScopeID(event.target.value); setError(null) }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label><button className="button button--primary" onClick={start}>● START RECORDING</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>RECORDINGS</span></div>{recordings.map((recording) => <div className="experiment-row" key={recording.name}><StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} /><div><strong>{recording.name}</strong><small>{recordingScopeLabel(recording)} · {recording.eventCount} events</small></div><span>{relativeTime(recording.startedAt)} ago</span><div>{recording.status === 'active' ? <button onClick={() => stop(recording)}>STOP</button> : <><a href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}>EXPORT</a><button onClick={() => remove(recording)}>DELETE</button></>}</div></div>)}{recordings.length === 0 && <div className="empty-row">No recordings. Start one before reproducing a local issue.</div>}</section>
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

function BindingsPanel({ environment, onChanged }: { environment: Environment; onChanged: () => void }) {
	const [service, setService] = useState(environment.services[0]?.name || '')
	const [provider, setProvider] = useState<ProviderKind>('remote')
	const [source, setSource] = useState(environment.sources?.[0]?.name || '')
	const [remoteURL, setRemoteURL] = useState('')
  const [classification, setClassification] = useState<RemoteClassification>('qa')
  const [writePolicy, setWritePolicy] = useState<WritePolicy>('read-only')
  const [healthPath, setHealthPath] = useState('/health')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
	const selected = environment.services.find((item) => item.name === service)
	useEffect(() => {
		const current = bindingFor(environment, service)
		if (!current) return
		setProvider(current.provider)
		setSource(current.source || environment.sources?.[0]?.name || '')
		setRemoteURL(current.remote?.url || '')
		setClassification(current.remote?.classification || 'qa')
		setWritePolicy(current.remote?.writePolicy || 'read-only')
		setHealthPath(current.remote?.healthPath || '/health')
	}, [environment, service])
	const bind = async () => {
		setBusy(true); setMessage('')
		try {
			const binding: ComponentBinding = { service, provider }
			if (provider === 'local') binding.source = source
			if (provider === 'remote') binding.remote = { url: remoteURL, classification, writePolicy, healthPath }
			await api(environmentPath(environment, `/bindings/${encodeURIComponent(service)}`), {
				method: 'PUT', ...jsonBody(binding),
			})
			setMessage(`${service} now uses the ${provider} provider`)
			onChanged()
    } catch (reason) { setMessage(reason instanceof Error ? reason.message : String(reason)) }
    finally { setBusy(false) }
  }
  return <div className="experiment-layout bindings-layout">
    <section className="panel experiment-list">
		<div className="panel-title"><span>CONFIGURED PROVIDERS</span><small>specific to {environment.project}/{environment.name}</small></div>
      {(environment.bindings || []).map((binding) => <div className={`experiment-row ${binding.provider === 'remote' ? 'is-warning' : ''}`} key={binding.service}>
        <StatusMark status={binding.provider === 'remote' ? 'degraded' : 'healthy'} label={false} />
        <div><strong>{binding.service}</strong><small>{binding.provider === 'remote' ? binding.remote?.url : binding.provider === 'local' ? `source: ${binding.source}` : 'managed container'}</small></div>
        <span>{binding.provider}</span>
        <div>{binding.remote && <><b>{binding.remote.classification}</b><small>{binding.remote.writePolicy}</small></>}</div>
      </div>)}
      {!environment.bindings?.length && <div className="empty-row">No providers have been compiled for this environment.</div>}
    </section>
		<section className="panel experiment-form">
			<div className="panel-title"><span>CONFIGURE PROVIDER</span><small>one choice per component</small></div>
			<label><span>SERVICE</span><select value={service} onChange={(event) => setService(event.target.value)}>{environment.services.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
			<label><span>PROVIDER</span><select value={provider} onChange={(event) => setProvider(event.target.value as ProviderKind)}>{selected?.kind === 'process' && <option value="local">local source</option>}{selected?.kind === 'resource' && <option value="container">managed container</option>}{selected?.kind === 'process' && <option value="remote">remote HTTP(S)</option>}</select></label>
			{provider === 'local' && <label><span>SOURCE</span><select value={source} onChange={(event) => setSource(event.target.value)}>{environment.sources?.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>}
			{provider === 'remote' && <><label><span>REMOTE URL</span><input type="url" placeholder="https://payments.qa.example.com" value={remoteURL} onChange={(event) => setRemoteURL(event.target.value)} /></label>
				<div className="form-pair"><label><span>CLASSIFICATION</span><select value={classification} onChange={(event) => setClassification(event.target.value as RemoteClassification)}><option value="development">development</option><option value="qa">qa</option><option value="staging">staging</option><option value="unknown">unknown</option></select></label><label><span>WRITE POLICY</span><select value={writePolicy} onChange={(event) => setWritePolicy(event.target.value as WritePolicy)}><option value="read-only">read-only</option><option value="read-write">read-write</option></select></label></div>
				<label><span>HEALTH PATH</span><input value={healthPath} onChange={(event) => setHealthPath(event.target.value)} placeholder="/health" /></label>
				<div className="scope-preview scope-preview--warning"><span className="eyebrow">REMOTE BOUNDARY</span><p>Traffic still passes through Portless, so recordings and faults remain available. A read-only binding blocks POST, PUT, PATCH, and DELETE before they leave this machine.</p></div></>}
			{message && <p className={message.includes('now uses') ? 'success-text' : 'danger-text'}>{message}</p>}
			<button className={provider === 'remote' ? 'button button--warning' : 'button button--primary'} disabled={busy || !service || (provider === 'remote' && !remoteURL) || (provider === 'local' && !source) || environment.status !== 'stopped'} onClick={bind}>{busy ? 'SAVING…' : 'SAVE PROVIDER'}</button>
			{environment.status !== 'stopped' && <small className="muted">Stop the environment before changing providers.</small>}
			<div className="panel-title sub-panel-title"><span>SOURCE CHECKOUTS</span><small>change a path to a Git worktree</small></div>
			{environment.sources?.map((item) => <SourceEditor key={item.name} environment={environment} source={item} disabled={environment.status !== 'stopped'} onChanged={onChanged} />)}
		</section>
	</div>
}

function SourceEditor({ environment, source, disabled, onChanged }: { environment: Environment; source: SourceBinding; disabled: boolean; onChanged: () => void }) {
	const [path, setPath] = useState(source.path)
	const [message, setMessage] = useState('')
	const save = async () => {
		setMessage('')
		try {
			await api(environmentPath(environment, `/sources/${encodeURIComponent(source.name)}`), { method: 'PUT', ...jsonBody({ path }) })
			setMessage('saved')
			onChanged()
		} catch (reason) { setMessage(reason instanceof Error ? reason.message : String(reason)) }
	}
	return <div className="source-editor"><label><span>{source.name}</span><input value={path} onChange={(event) => setPath(event.target.value)} /></label><button className="button button--small" disabled={disabled || path === source.path} onClick={save}>UPDATE</button>{message && <small className={message === 'saved' ? 'success-text' : 'danger-text'}>{message}</small>}</div>
}

const timelinePageSizes = [25, 50, 100] as const

export function TimelinePanel({ timeline }: { timeline: TimelineEvent[] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState<number>(timelinePageSizes[0])
  const pagination = useMemo(() => paginateOverview(timeline, page, pageSize), [timeline, page, pageSize])
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

function environmentUIPath(environment: Environment, tab: Tab, edge?: string, protocol?: 'http' | 'tcp') {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  if (tab === 'overview') return base
  const query = new URLSearchParams({ tab })
  if (edge) query.set('edge', edge)
  if (protocol) query.set('protocol', protocol)
  return `${base}?${query}`
}
