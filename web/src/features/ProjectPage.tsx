import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { api, connectEvents, jsonBody, environmentPath } from '../api'
import type { ComponentBinding, FaultRule, LogEntry, Operation, Environment, ProviderKind, Recording, RemoteClassification, Service, SourceBinding, TimelineEvent, TrafficEvent, WritePolicy } from '../types'
import { duration, relativeTime, StatePanel, StatusMark } from '../components/Status'

type Tab = 'overview' | 'bindings' | 'traffic' | 'recordings' | 'faults' | 'timeline'

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
      api<{ timeline: TimelineEvent[] }>(`${base}/timeline?limit=100`),
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

  return (
    <div className="page project-page">
      <div className="project-heading">
		<div><div className="eyebrow">{environment.project} / ENVIRONMENT</div><div className="title-with-status"><h1>{environment.name}</h1><StatusMark status={environment.status} /></div>{(environment.reason || environment.status === 'stopped') && <p>{environment.reason || 'not running'}</p>}</div>
        <div className="project-actions">
          {activeRecording && <span className="recording-indicator"><i />REC {activeRecording.name}</span>}
          {activeFaults.length > 0 && <span className="fault-indicator">▲ {activeFaults.length} ACTIVE {activeFaults.length === 1 ? 'FAULT' : 'FAULTS'}</span>}
          {environment.status !== 'stopped' ? <button className="button" disabled={!!busy || environment.status === 'recovering'} onClick={() => run('down')}>{busy === 'down' ? 'STOPPING…' : environment.status === 'recovering' ? 'RECOVERING…' : 'STOP ALL'}</button> : <button className="button button--primary" disabled={!!busy} onClick={() => run('up')}>{busy === 'up' ? 'STARTING…' : 'START ALL'}</button>}
          {primaryService?.ingressUrl && <a className="button" href={primaryService.ingressUrl} target="_blank" rel="noreferrer">OPEN APP ↗</a>}
        </div>
      </div>
      {!!environment.issues?.length && <div className="alert alert--danger"><strong>Configuration needs attention</strong><span>{environment.issues.map((issue) => issue.message).join(' · ')}</span></div>}
      {error && <div className="alert alert--danger"><strong>Action failed</strong><span>{error}</span><button onClick={() => setError('')}>DISMISS</button></div>}
      <nav className="tabs" aria-label="Environment views">
        {(['overview', 'bindings', 'traffic', 'recordings', 'faults', 'timeline'] as Tab[]).map((name) => <button key={name} className={tab === name ? 'is-active' : ''} onClick={() => onNavigate(environmentUIPath(environment, name))}>{name}<small>{name === 'recordings' ? recordings.length : name === 'faults' ? activeFaults.length : ''}</small></button>)}
      </nav>
      {tab === 'overview' && <Overview environment={environment} timeline={timeline} ready={ready} activeFaults={activeFaults.length} activeRecording={activeRecording} trafficCount={trafficCount} onService={setSelectedService} onTab={(next) => onNavigate(environmentUIPath(environment, next))} />}
      {tab === 'bindings' && <BindingsPanel environment={environment} onChanged={onChanged} />}
      {tab === 'traffic' && <TrafficPanel environment={environment} />}
      {tab === 'recordings' && <RecordingsPanel environment={environment} recordings={recordings} refresh={refreshSecondary} />}
      {tab === 'faults' && <FaultsPanel environment={environment} faults={faults} refresh={refreshSecondary} />}
      {tab === 'timeline' && <TimelinePanel timeline={timeline} />}
      {selectedService && <ServiceDrawer environment={environment} service={selectedService} onClose={() => setSelectedService(null)} onChanged={onChanged} />}
    </div>
  )
}

function Overview({ environment, timeline, ready, activeFaults, activeRecording, trafficCount, onService, onTab }: {
  environment: Environment; timeline: TimelineEvent[]; ready: number; activeFaults: number; activeRecording?: Recording; trafficCount: number; onService: (service: Service) => void; onTab: (tab: Tab) => void
}) {
  const [topologyMaximized, setTopologyMaximized] = useState(false)
  useEffect(() => {
    if (!topologyMaximized) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !document.querySelector('.drawer-backdrop')) setTopologyMaximized(false)
    }
    window.addEventListener('keydown', keydown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', keydown)
    }
  }, [topologyMaximized])
  return <>
    <div className="state-grid">
      <StatePanel title="READY" value={`${ready}/${environment.services.length}`} detail="required services" />
      <StatePanel title="TRAFFIC" value={trafficCount} detail="recent requests" />
      <StatePanel title="RECORDING" value={activeRecording ? 'ON' : 'OFF'} tone={activeRecording ? 'danger' : undefined} detail={activeRecording?.name || 'capture disabled'} />
      <StatePanel title="FAULTS" value={activeFaults} tone={activeFaults ? 'warning' : undefined} detail={activeFaults ? 'affecting local traffic' : 'none active'} />
      <StatePanel title="REVISION" value={environment.revision} detail={`updated · ${relativeTime(environment.updatedAt)} ago`} />
    </div>
    <div className="overview-grid">
      <section className={`panel topology-panel${topologyMaximized ? ' topology-panel--maximized' : ''}`} aria-label="Service topology">
        <div className="panel-title"><span>TOPOLOGY</span><button className={topologyMaximized ? 'icon-button' : 'topology-size-button'} type="button" title={`${topologyMaximized ? 'Restore' : 'Maximize'} topology`} aria-label={`${topologyMaximized ? 'Restore' : 'Maximize'} topology`} aria-pressed={topologyMaximized} onClick={() => setTopologyMaximized((value) => !value)}>{topologyMaximized ? '×' : <TopologySizeIcon />}</button></div>
        <Topology environment={environment} onService={onService} />
      </section>
      <section className="panel activity-panel">
        <div className="panel-title"><span>RECENT ACTIVITY</span><button onClick={() => onTab('timeline')}>FULL TIMELINE</button></div>
        <div className="activity-list">
          {timeline.slice(0, 7).map((event) => <div className="activity" key={event.sequence}><time>{new Date(event.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</time><span className={`activity__line activity__line--${event.severity}`} /><div><strong>{event.summary}</strong><small>{event.subject || event.type} · {event.actor}</small></div></div>)}
          {timeline.length === 0 && <div className="empty-row">No lifecycle events have been recorded yet.</div>}
        </div>
      </section>
    </div>
    <section className="panel services-panel">
      <div className="panel-title"><span>SERVICES</span><small>{environment.services.length} managed workloads</small></div>
      <div className="table-row table-row--header service-row"><span /><span>Name</span><span>Provider</span><span>State</span><span>Restarts</span><span>Requests</span><span>P95</span><span>Endpoint / reason</span><span /></div>
      {environment.services.map((service) => <button className="table-row service-row" key={service.name} onClick={() => onService(service)}>
        <StatusMark status={service.status} label={false} /><strong>{service.name}</strong><span>{bindingFor(environment, service.name)?.provider || service.kind}</span><StatusMark status={service.status} /><span className={service.restartCount ? 'warning-text' : ''}>{service.restartCount}</span><span>{service.recentRequests || '—'}</span><span>{service.p95Millis ? `${service.p95Millis}ms` : '—'}</span><span className="truncate muted">{service.reason || bindingFor(environment, service.name)?.remote?.url || service.ingressUrl || (service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : 'not running')}</span><span className="row-action">INSPECT</span>
      </button>)}
    </section>
  </>
}

type TopologyItem = { kind: 'client'; key: 'external' } | { kind: 'service'; key: string; service: Service }

function TopologySizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}

function Topology({ environment, onService }: { environment: Environment; onService: (service: Service) => void }) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const pan = useRef<{ pointerId: number; clientX: number; clientY: number; scrollLeft: number; scrollTop: number } | null>(null)
  const [isPanning, setIsPanning] = useState(false)
  const { levels, edges } = buildTopology(environment)
  const rowGap = 62
  const nodeWidth = 150
  const nodeHeight = 74
  const columnGap = 38
  const sidePadding = 30
  const positions = new Map<string, { x: number; y: number }>()
  const widestLevel = Math.max(1, ...levels.map((level) => level.length))
  const width = sidePadding * 2 + widestLevel * nodeWidth + (widestLevel - 1) * columnGap
  levels.forEach((level, depth) => {
    const rowWidth = level.length * nodeWidth + Math.max(0, level.length - 1) * columnGap
    const start = (width - rowWidth) / 2
    level.forEach((item, index) => positions.set(item.key, { x: start + index * (nodeWidth + columnGap), y: depth * (nodeHeight + rowGap) }))
  })
  const height = levels.length * nodeHeight + Math.max(0, levels.length - 1) * rowGap

  const startPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement
    if (event.button !== 0 || target.closest('button, a, input, select, textarea')) return
    const viewport = event.currentTarget
    pan.current = {
      pointerId: event.pointerId,
      clientX: event.clientX,
      clientY: event.clientY,
      scrollLeft: viewport.scrollLeft,
      scrollTop: viewport.scrollTop,
    }
    viewport.setPointerCapture(event.pointerId)
    setIsPanning(true)
    event.preventDefault()
  }

  const movePan = (event: ReactPointerEvent<HTMLDivElement>) => {
    const origin = pan.current
    if (!origin || origin.pointerId !== event.pointerId) return
    event.currentTarget.scrollLeft = origin.scrollLeft - (event.clientX - origin.clientX)
    event.currentTarget.scrollTop = origin.scrollTop - (event.clientY - origin.clientY)
    event.preventDefault()
  }

  const stopPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (pan.current?.pointerId !== event.pointerId) return
    pan.current = null
    setIsPanning(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
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
  ><div className="topology__canvas" style={{ width, height }}>
    <svg className="topology__edges" width={width} height={height} aria-hidden="true">
      <defs><marker id="topology-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto"><path d="M0 0 L8 4 L0 8 Z" /></marker></defs>
      {edges.map((edge) => {
        const from = positions.get(edge.source)
        const to = positions.get(edge.target)
        if (!from || !to) return null
        const startX = from.x + nodeWidth / 2
        const startY = from.y + nodeHeight
        const endX = to.x + nodeWidth / 2
        const endY = to.y
        const middleY = startY + (endY - startY) / 2
        return <g key={`${edge.source}:${edge.target}`}><path d={`M ${startX} ${startY} V ${middleY} H ${endX} V ${endY}`} /><text x={(startX + endX) / 2} y={middleY - 6}>{edge.protocol}</text></g>
      })}
    </svg>
    {levels.flat().map((item) => {
      const position = positions.get(item.key)!
      if (item.kind === 'client') return <div key={item.key} className="topology__external topology__item" style={{ left: position.x, top: position.y }}><span>EXTERNAL</span><strong>browser / client</strong></div>
      const service = item.service
      return <button key={item.key} style={{ left: position.x, top: position.y }} className={`topology-node topology__item topology-node--${service.kind} ${service.name === environment.primaryService ? 'is-primary' : ''}`} onClick={() => onService(service)}>
        <span><StatusMark status={service.status} label={false} />{service.kind === 'container' ? service.template : service.framework}</span><strong>{service.name}</strong><small>{service.ingressUrl ? service.ingressUrl.replace(/^https?:\/\//, '') : service.status}</small>
      </button>
    })}
  </div></div>
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
  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className={`drawer ${fullScreen ? 'drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label={`${service.name} service`} onMouseDown={(event) => event.stopPropagation()}>
      <header><div><span className="eyebrow">{environment.project} / {environment.name} / service</span><h2>{service.name}</h2><StatusMark status={service.status} /></div><div className="drawer-header-actions"><button className="drawer-size-button" type="button" aria-pressed={fullScreen} onClick={() => setFullScreen((value) => !value)}>{fullScreen ? 'RESTORE' : 'FULL SCREEN'}</button><button className="icon-button" onClick={onClose} aria-label="Close">×</button></div></header>
      <div className="drawer-actions"><button className="button button--primary" onClick={() => action(service.status === 'ready' ? 'restart' : 'start')} disabled={!!busy}>{busy || (service.status === 'ready' ? 'RESTART' : 'START')}</button><button className="button" onClick={() => action('stop')} disabled={!!busy || service.status === 'stopped'}>STOP</button>{service.ingressUrl && <a className="button" href={service.ingressUrl} target="_blank" rel="noreferrer">OPEN ↗</a>}</div>
      <nav className="drawer-tabs">{(['details', 'logs', 'configuration'] as const).map((name) => <button key={name} className={drawerTab === name ? 'is-active' : ''} onClick={() => setDrawerTab(name)}>{name}</button>)}</nav>
      <div className="drawer-content">
        {drawerTab === 'details' && <>
          <div className="detail-grid"><Detail label="KIND" value={service.framework || service.template || service.kind} /><Detail label="GENERATION" value={String(service.generation || '—')} /><Detail label="PID" value={String(service.pid || '—')} /><Detail label="UPSTREAM" value={service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : '—'} /><Detail label="RESTARTS" value={String(service.restartCount)} /><Detail label="STARTED" value={service.startedAt ? `${relativeTime(service.startedAt)} ago` : '—'} /></div>
          <section className="drawer-section"><div className="eyebrow">COMMAND</div><pre>{service.command?.join(' ') || `managed ${service.template} container`}</pre></section>
          <section className="drawer-section"><div className="eyebrow">HEALTH</div><p><StatusMark status={service.status} /> {service.health.kind}{service.health.path ? ` ${service.health.path}` : ''}</p><small>{service.reason || 'No current readiness error.'}</small></section>
        </>}
        {drawerTab === 'logs' && <div className="log-view"><div className="log-view__meta">last {logs.length} lines · stdout + stderr</div><pre>{logs.length ? logs.map((entry, index) => <span key={`${entry.timestamp}-${entry.stream}-${index}`}><i>{new Date(entry.timestamp).toLocaleTimeString()}</i>{entry.message}{'\n'}</span>) : 'No logs captured for this service.'}</pre></div>}
        {drawerTab === 'configuration' && <div className="config-table"><div className="config-row config-row--head"><span>KEY</span><span>EFFECTIVE VALUE</span><span>SOURCE</span></div>{configuration?.environment?.map((item) => <div className="config-row" key={item.key}><code>{item.key}</code><span className={item.classification === 'masked' ? 'masked-value' : ''}>{item.value}</span><small>{item.source} · {item.classification}</small></div>)}{!configuration?.environment?.length && <div className="empty-row">No static environment values were discovered. Connection bindings are generated at runtime.</div>}</div>}
      </div>
    </aside>
  </div>
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><strong>{value}</strong></div> }

function TrafficPanel({ environment }: { environment: Environment }) {
  const [traffic, setTraffic] = useState<TrafficEvent[]>([])
  const [selected, setSelected] = useState<TrafficEvent | null>(null)
  const [filter, setFilter] = useState('')
  const [paused, setPaused] = useState(false)
  useEffect(() => {
    api<{ traffic: TrafficEvent[] }>(environmentPath(environment, '/traffic?protocol=http&limit=500')).then((value) => setTraffic(value.traffic)).catch(() => setTraffic([]))
    return connectEvents(environment, ['traffic.http'], (type, value) => {
      if (type === 'traffic.http' && !paused) setTraffic((items) => [value as TrafficEvent, ...items].slice(0, 1000))
    })
  }, [environment.project, environment.name, paused])
  const inspect = async (event: TrafficEvent) => {
    try {
      setSelected(await api<TrafficEvent>(environmentPath(environment, `/traffic/${event.sequence}`)))
    } catch {
      setSelected(event)
    }
  }
  const filtered = traffic.filter((event) => `${event.method} ${event.path} ${event.source} ${event.target} ${event.status}`.toLowerCase().includes(filter.toLowerCase()))
  return <section className="panel traffic-panel">
    <div className="panel-title traffic-toolbar"><span>LIVE HTTP TRAFFIC</span><div><span className="live-count"><i />{paused ? 'PAUSED' : 'STREAMING'}</span><button className="button button--small" onClick={() => setPaused((value) => !value)}>{paused ? 'RESUME' : 'PAUSE'}</button><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="filter method, path, edge…" /></div></div>
    <div className="table-row table-row--header traffic-row"><span>Seq</span><span>When</span><span>Method</span><span>Path</span><span>Edge</span><span>Status</span><span>Duration</span><span>Fault / recording</span></div>
    {filtered.map((event) => <button className="table-row traffic-row" key={event.sequence} onClick={() => inspect(event)}><code>#{event.sequence}</code><span>{new Date(event.startedAt).toLocaleTimeString()}</span><strong>{event.method || event.protocol.toUpperCase()}</strong><code className="truncate">{event.path || 'TCP session'}</code><span>{event.source}<i className="edge-arrow">→</i>{event.target}</span><span className={(event.status || 0) >= 500 ? 'danger-text' : (event.status || 0) >= 400 ? 'warning-text' : ''}>{event.status || '—'}</span><span>{duration(event.durationMs)}</span><span>{event.fault ? <b className="fault-chip">▲ {event.fault}</b> : event.recording ? <b className="record-chip">● {event.recording}</b> : '—'}</span></button>)}
    {filtered.length === 0 && <div className="empty-row">No matching HTTP traffic yet. Requests through <code>service.{environment.name}.{environment.project}.localhost</code> or a discovered HTTP edge appear here.</div>}
    {selected && <div className="traffic-detail"><header><div><span className="eyebrow">HTTP TRAFFIC #{selected.sequence}</span><h3>{selected.method} {selected.path}</h3></div><button onClick={() => setSelected(null)}>×</button></header><div className="detail-grid"><Detail label="EDGE" value={`${selected.source} → ${selected.target}`} /><Detail label="STATUS" value={String(selected.status || '—')} /><Detail label="DURATION" value={duration(selected.durationMs)} /><Detail label="REQUEST" value={`${selected.requestBytes} B`} /><Detail label="RESPONSE" value={`${selected.responseBytes} B`} /><Detail label="FAULT" value={selected.fault || 'none'} /></div><div className="drawer-section"><div className="eyebrow">REDACTED REQUEST HEADERS</div><pre>{JSON.stringify(selected.requestHeaders || {}, null, 2)}</pre></div><div className="drawer-section"><div className="eyebrow">REDACTED RESPONSE HEADERS</div><pre>{JSON.stringify(selected.responseHeaders || {}, null, 2)}</pre></div></div>}
  </section>
}

function RecordingsPanel({ environment, recordings, refresh }: { environment: Environment; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('checkout-debug')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [error, setError] = useState('')
  const start = async () => {
    setError('')
    try { await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody({ name, source, target, captureBodies: false, maxEvents: 10000, maxBodyBytes: 65536 }) }); await refresh() }
    catch (value) { setError(value instanceof Error ? value.message : String(value)) }
  }
  const stop = async (recording: Recording) => { await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/stop`), { method: 'POST' }); await refresh() }
  const remove = async (recording: Recording) => { await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' }); await refresh() }
  return <div className="experiment-layout">
    <section className="panel experiment-form"><div className="panel-title"><span>START RECORDING</span><small>bounded, metadata-first capture</small></div><label><span>NAME</span><input value={name} onChange={(event) => setName(event.target.value)} /></label><div className="form-pair"><label><span>SOURCE</span><select value={source} onChange={(event) => setSource(event.target.value)}><option value="">any source</option><option value="external">external</option>{environment.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label><label><span>TARGET</span><select value={target} onChange={(event) => setTarget(event.target.value)}><option value="">any target</option>{environment.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label></div>{error && <p className="danger-text">{error}</p>}<button className="button button--primary" onClick={start}>● START RECORDING</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>RECORDINGS</span><small>{recordings.length} retained locally</small></div>{recordings.map((recording) => <div className="experiment-row" key={recording.name}><StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} /><div><strong>{recording.name}</strong><small>{recording.source || 'any'} → {recording.target || 'any'} · {recording.eventCount} events</small></div><span>{relativeTime(recording.startedAt)} ago</span><div>{recording.status === 'active' ? <button onClick={() => stop(recording)}>STOP</button> : <><a href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}>EXPORT</a><button onClick={() => remove(recording)}>DELETE</button></>}</div></div>)}{recordings.length === 0 && <div className="empty-row">No recordings. Start one before reproducing a local issue.</div>}</section>
  </div>
}

function FaultsPanel({ environment, faults, refresh }: { environment: Environment; faults: FaultRule[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('slow-downstream')
  const [source, setSource] = useState(environment.primaryService || 'external')
  const [target, setTarget] = useState(environment.services.find((service) => service.name !== source)?.name || environment.services[0]?.name || '')
  const [effect, setEffect] = useState<'latency' | 'status' | 'abort'>('latency')
  const [value, setValue] = useState('2000')
  const [expiryMinutes, setExpiryMinutes] = useState('')
  const [error, setError] = useState('')
  const create = async () => {
    setError('')
    const body = {
      name, source, target, probability: 1,
      latencyMs: effect === 'latency' ? Number(value) : 0,
      statusCode: effect === 'status' ? Number(value) : 0,
      abort: effect === 'abort',
      ...(expiryMinutes ? { expiresAt: new Date(Date.now() + Number(expiryMinutes) * 60_000).toISOString() } : {}),
    }
    try { await api(environmentPath(environment, '/faults'), { method: 'POST', ...jsonBody(body) }); await refresh() }
    catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
  }
  const disable = async (fault: FaultRule) => { await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}`), { method: 'DELETE' }); await refresh() }
  const clear = async () => { await api(environmentPath(environment, '/faults/disable-all'), { method: 'POST' }); await refresh() }
  return <div className="experiment-layout">
    <section className="panel experiment-form"><div className="panel-title"><span>INTRODUCE FAILURE</span><small>scoped to one local edge</small></div><label><span>NAME</span><input value={name} onChange={(event) => setName(event.target.value)} /></label><div className="form-pair"><label><span>SOURCE</span><select value={source} onChange={(event) => setSource(event.target.value)}><option value="external">external</option>{environment.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label><label><span>TARGET</span><select value={target} onChange={(event) => setTarget(event.target.value)}>{environment.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label></div><div className="segmented">{(['latency', 'status', 'abort'] as const).map((item) => <button key={item} className={effect === item ? 'is-active' : ''} onClick={() => { setEffect(item); setValue(item === 'latency' ? '2000' : item === 'status' ? '503' : '') }}>{item}</button>)}</div>{effect !== 'abort' && <label><span>{effect === 'latency' ? 'MILLISECONDS' : 'HTTP STATUS'}</span><input type="number" value={value} onChange={(event) => setValue(event.target.value)} /></label>}<label><span>AUTOMATIC DISABLE</span><select value={expiryMinutes} onChange={(event) => setExpiryMinutes(event.target.value)}><option value="">Until manually disabled</option><option value="10">After 10 minutes</option><option value="30">After 30 minutes</option><option value="60">After 1 hour</option><option value="240">After 4 hours</option></select></label>{error && <p className="danger-text">{error}</p>}<button className="button button--warning" onClick={create}>▲ ENABLE FAULT</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>FAULT RULES</span><button onClick={clear}>DISABLE ALL</button></div>{faults.map((fault) => <div className={`experiment-row ${fault.enabled ? 'is-warning' : ''}`} key={fault.name}><StatusMark status={fault.enabled ? 'degraded' : 'stopped'} label={false} /><div><strong>{fault.name}</strong><small>{fault.scopeSummary}</small><small className="fault-lifetime">{faultLifetime(fault)}</small></div><span>{fault.matchCount} matches</span><div>{fault.enabled && <button onClick={() => disable(fault)}>DISABLE</button>}</div></div>)}{faults.length === 0 && <div className="empty-row">No fault rules have been created.</div>}</section>
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
			<label><span>PROVIDER</span><select value={provider} onChange={(event) => setProvider(event.target.value as ProviderKind)}>{selected?.kind === 'process' && <option value="local">local source</option>}{selected?.kind === 'container' && <option value="container">managed container</option>}{selected?.kind === 'process' && <option value="remote">remote HTTP(S)</option>}</select></label>
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

function TimelinePanel({ timeline }: { timeline: TimelineEvent[] }) {
  const groups = useMemo(() => timeline.reduce<Record<string, TimelineEvent[]>>((result, event) => { const key = new Date(event.timestamp).toLocaleDateString(); (result[key] ||= []).push(event); return result }, {}), [timeline])
  return <section className="panel timeline-panel"><div className="panel-title"><span>ENVIRONMENT TIMELINE</span><small>durable local history · actors and outcomes retained</small></div>{Object.entries(groups).map(([date, events]) => <div className="timeline-group" key={date}><div className="timeline-date">{date}</div>{events.map((event) => <div className="timeline-event" key={event.sequence}><time>{new Date(event.timestamp).toLocaleTimeString()}</time><span className={`timeline-dot timeline-dot--${event.severity}`} /><div><strong>{event.summary}</strong><small>{event.type} · {event.actor}{event.subject ? ` · ${event.subject}` : ''}</small></div><code>#{event.sequence}</code></div>)}</div>)}{timeline.length === 0 && <div className="empty-row">The timeline will capture lifecycle, recording, and fault events.</div>}</section>
}

function bindingFor(environment: Environment, service: string) {
  return environment.bindings?.find((binding) => binding.service === service)
}

function environmentUIPath(environment: Environment, tab: Tab) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  return tab === 'overview' ? base : `${base}?tab=${tab}`
}
