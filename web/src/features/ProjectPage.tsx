import { useEffect, useMemo, useState } from 'react'
import { api, connectEvents, jsonBody, projectPath } from '../api'
import type { FaultRule, Operation, Project, Recording, Service, TimelineEvent, TrafficEvent } from '../types'
import { duration, relativeTime, StatePanel, StatusMark } from '../components/Status'

type Tab = 'overview' | 'traffic' | 'recordings' | 'faults' | 'timeline'

export function ProjectPage({ project, tab, onNavigate, onChanged }: { project: Project; tab: Tab; onNavigate: (path: string) => void; onChanged: () => void }) {
  const [selectedService, setSelectedService] = useState<Service | null>(null)
  const [timeline, setTimeline] = useState<TimelineEvent[]>([])
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [faults, setFaults] = useState<FaultRule[]>([])
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  const refreshSecondary = async () => {
    const base = projectPath(project.name)
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
    return connectEvents(project.name, ['project.state', 'service.state', 'recording.state', 'fault.state', 'operation.state'], () => {
      onChanged(); refreshSecondary().catch(() => undefined)
    })
  }, [project.name]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!selectedService) return
    const updated = project.services.find((service) => service.name === selectedService.name)
    if (updated) setSelectedService(updated)
  }, [project.services]) // eslint-disable-line react-hooks/exhaustive-deps

  const run = async (action: 'up' | 'down') => {
    setBusy(action); setError('')
    try {
      await api<Operation>(projectPath(project.name, `/${action}`), { method: 'POST', ...(action === 'down' ? jsonBody({ removeVolumes: false }) : {}) })
      onChanged()
    } catch (value) { setError(value instanceof Error ? value.message : String(value)) }
    finally { setBusy('') }
  }

  const activeRecording = recordings.find((recording) => recording.status === 'active')
  const activeFaults = faults.filter((fault) => fault.enabled)
  const ready = project.services.filter((service) => service.status === 'ready').length
  const trafficCount = project.services.reduce((sum, service) => sum + (service.recentRequests || 0), 0)
	const primaryService = project.services.find((service) => service.name === project.primaryService)

  return (
    <div className="page project-page">
      <div className="project-heading">
        <div><div className="title-with-status"><h1>{project.name}</h1><StatusMark status={project.status} /></div><p>{project.reason || `${ready} required services ready`}</p></div>
        <div className="project-actions">
          {activeRecording && <span className="recording-indicator"><i />REC {activeRecording.name}</span>}
          {activeFaults.length > 0 && <span className="fault-indicator">▲ {activeFaults.length} ACTIVE {activeFaults.length === 1 ? 'FAULT' : 'FAULTS'}</span>}
          {project.status === 'healthy' || project.status === 'degraded' || project.status === 'failed' ? <button className="button" disabled={!!busy} onClick={() => run('down')}>{busy === 'down' ? 'STOPPING…' : 'STOP ALL'}</button> : <button className="button button--primary" disabled={!!busy} onClick={() => run('up')}>{busy === 'up' ? 'STARTING…' : 'START ALL'}</button>}
          {primaryService?.ingressUrl && <a className="button" href={primaryService.ingressUrl} target="_blank" rel="noreferrer">OPEN APP ↗</a>}
        </div>
      </div>
      {error && <div className="alert alert--danger"><strong>Action failed</strong><span>{error}</span><button onClick={() => setError('')}>DISMISS</button></div>}
      <nav className="tabs" aria-label="Project views">
        {(['overview', 'traffic', 'recordings', 'faults', 'timeline'] as Tab[]).map((name) => <button key={name} className={tab === name ? 'is-active' : ''} onClick={() => onNavigate(`/projects/${project.name}${name === 'overview' ? '' : `?tab=${name}`}`)}>{name}<small>{name === 'recordings' ? recordings.length : name === 'faults' ? activeFaults.length : ''}</small></button>)}
      </nav>
      {tab === 'overview' && <Overview project={project} timeline={timeline} ready={ready} activeFaults={activeFaults.length} activeRecording={activeRecording} trafficCount={trafficCount} onService={setSelectedService} onTab={(next) => onNavigate(`/projects/${project.name}?tab=${next}`)} />}
      {tab === 'traffic' && <TrafficPanel project={project} />}
      {tab === 'recordings' && <RecordingsPanel project={project} recordings={recordings} refresh={refreshSecondary} />}
      {tab === 'faults' && <FaultsPanel project={project} faults={faults} refresh={refreshSecondary} />}
      {tab === 'timeline' && <TimelinePanel timeline={timeline} />}
      {selectedService && <ServiceDrawer project={project} service={selectedService} onClose={() => setSelectedService(null)} onChanged={onChanged} />}
    </div>
  )
}

function Overview({ project, timeline, ready, activeFaults, activeRecording, trafficCount, onService, onTab }: {
  project: Project; timeline: TimelineEvent[]; ready: number; activeFaults: number; activeRecording?: Recording; trafficCount: number; onService: (service: Service) => void; onTab: (tab: Tab) => void
}) {
  return <>
    <div className="state-grid">
      <StatePanel title="READY" value={`${ready}/${project.services.length}`} detail="required services" />
      <StatePanel title="TRAFFIC" value={trafficCount} detail="recent requests" />
      <StatePanel title="RECORDING" value={activeRecording ? 'ON' : 'OFF'} tone={activeRecording ? 'danger' : undefined} detail={activeRecording?.name || 'capture disabled'} />
      <StatePanel title="FAULTS" value={activeFaults} tone={activeFaults ? 'warning' : undefined} detail={activeFaults ? 'affecting local traffic' : 'none active'} />
      <StatePanel title="REVISION" value={project.revision} detail={`updated · ${relativeTime(project.updatedAt)} ago`} />
    </div>
    <div className="overview-grid">
      <section className="panel topology-panel">
        <div className="panel-title"><span>TOPOLOGY</span><small>effective local routing · click a service to inspect</small></div>
        <div className="topology">
          <div className="topology__external"><span>EXTERNAL</span><strong>browser / client</strong></div>
          <div className="topology__rail" />
          <div className="topology__nodes">
            {project.services.map((service) => <button key={service.name} className={`topology-node topology-node--${service.kind} ${service.name === project.primaryService ? 'is-primary' : ''}`} onClick={() => onService(service)}>
              <span><StatusMark status={service.status} label={false} />{service.kind === 'container' ? service.template : service.framework}</span><strong>{service.name}</strong><small>{service.ingressUrl ? service.ingressUrl.replace(/^https?:\/\//, '') : service.status}</small>
            </button>)}
          </div>
          <div className="topology__connections">
            {project.connections.map((connection) => <span key={`${connection.source}:${connection.target}`}><b>{connection.source}</b><i>→</i><b>{connection.target}</b><small>{connection.protocol}</small></span>)}
          </div>
        </div>
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
      <div className="panel-title"><span>SERVICES</span><small>{project.services.length} managed workloads</small></div>
      <div className="table-row table-row--header service-row"><span /><span>Name</span><span>Kind</span><span>State</span><span>Restarts</span><span>Requests</span><span>P95</span><span>Endpoint / reason</span><span /></div>
      {project.services.map((service) => <button className="table-row service-row" key={service.name} onClick={() => onService(service)}>
        <StatusMark status={service.status} label={false} /><strong>{service.name}</strong><span>{service.framework || service.template || service.kind}</span><StatusMark status={service.status} /><span className={service.restartCount ? 'warning-text' : ''}>{service.restartCount}</span><span>{service.recentRequests || '—'}</span><span>{service.p95Millis ? `${service.p95Millis}ms` : '—'}</span><span className="truncate muted">{service.reason || service.ingressUrl || (service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : 'not running')}</span><span className="row-action">INSPECT</span>
      </button>)}
    </section>
  </>
}

function ServiceDrawer({ project, service, onClose, onChanged }: { project: Project; service: Service; onClose: () => void; onChanged: () => void }) {
  const [logs, setLogs] = useState<string[]>([])
  const [configuration, setConfiguration] = useState<{ environment?: Array<{ key: string; value: string; classification: string; source: string }> } | null>(null)
  const [drawerTab, setDrawerTab] = useState<'details' | 'logs' | 'configuration'>('details')
  const [busy, setBusy] = useState('')
  const base = projectPath(project.name, `/services/${encodeURIComponent(service.name)}`)
  useEffect(() => {
    api<{ lines: string[] }>(`${projectPath(project.name, '/logs')}?service=${encodeURIComponent(service.name)}&limit=500`).then((value) => setLogs(value.lines)).catch(() => setLogs([]))
    api<typeof configuration>(`${base}/configuration`).then(setConfiguration).catch(() => setConfiguration(null))
  }, [base, project.name, service.name])
  const action = async (name: 'restart' | 'stop' | 'start') => {
    setBusy(name)
    try { await api<Operation>(`${base}/${name}`, { method: 'POST' }); onChanged() } finally { setBusy('') }
  }
  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className="drawer" role="dialog" aria-modal="true" aria-label={`${service.name} service`} onMouseDown={(event) => event.stopPropagation()}>
      <header><div><span className="eyebrow">{project.name} / service</span><h2>{service.name}</h2><StatusMark status={service.status} /></div><button className="icon-button" onClick={onClose} aria-label="Close">×</button></header>
      <div className="drawer-actions"><button className="button button--primary" onClick={() => action(service.status === 'ready' ? 'restart' : 'start')} disabled={!!busy}>{busy || (service.status === 'ready' ? 'RESTART' : 'START')}</button><button className="button" onClick={() => action('stop')} disabled={!!busy || service.status === 'stopped'}>STOP</button>{service.ingressUrl && <a className="button" href={service.ingressUrl} target="_blank" rel="noreferrer">OPEN ↗</a>}</div>
      <nav className="drawer-tabs">{(['details', 'logs', 'configuration'] as const).map((name) => <button key={name} className={drawerTab === name ? 'is-active' : ''} onClick={() => setDrawerTab(name)}>{name}</button>)}</nav>
      <div className="drawer-content">
        {drawerTab === 'details' && <>
          <div className="detail-grid"><Detail label="KIND" value={service.framework || service.template || service.kind} /><Detail label="GENERATION" value={String(service.generation || '—')} /><Detail label="PID" value={String(service.pid || '—')} /><Detail label="UPSTREAM" value={service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : '—'} /><Detail label="RESTARTS" value={String(service.restartCount)} /><Detail label="STARTED" value={service.startedAt ? `${relativeTime(service.startedAt)} ago` : '—'} /></div>
          <section className="drawer-section"><div className="eyebrow">COMMAND</div><pre>{service.command?.join(' ') || `managed ${service.template} container`}</pre></section>
          <section className="drawer-section"><div className="eyebrow">HEALTH</div><p><StatusMark status={service.status} /> {service.health.kind}{service.health.path ? ` ${service.health.path}` : ''}</p><small>{service.reason || 'No current readiness error.'}</small></section>
        </>}
        {drawerTab === 'logs' && <div className="log-view"><div className="log-view__meta">last {logs.length} lines · stdout + stderr</div><pre>{logs.length ? logs.map((line, index) => <span key={index}><i>{String(index + 1).padStart(3, '0')}</i>{line}{'\n'}</span>) : 'No logs captured for this generation.'}</pre></div>}
        {drawerTab === 'configuration' && <div className="config-table"><div className="config-row config-row--head"><span>KEY</span><span>EFFECTIVE VALUE</span><span>SOURCE</span></div>{configuration?.environment?.map((item) => <div className="config-row" key={item.key}><code>{item.key}</code><span className={item.classification === 'masked' ? 'masked-value' : ''}>{item.value}</span><small>{item.source} · {item.classification}</small></div>)}{!configuration?.environment?.length && <div className="empty-row">No static environment values were discovered. Connection bindings are generated at runtime.</div>}</div>}
      </div>
    </aside>
  </div>
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><strong>{value}</strong></div> }

function TrafficPanel({ project }: { project: Project }) {
  const [traffic, setTraffic] = useState<TrafficEvent[]>([])
  const [selected, setSelected] = useState<TrafficEvent | null>(null)
  const [filter, setFilter] = useState('')
  const [paused, setPaused] = useState(false)
  useEffect(() => {
    api<{ traffic: TrafficEvent[] }>(projectPath(project.name, '/traffic/http?limit=500')).then((value) => setTraffic(value.traffic)).catch(() => setTraffic([]))
    return connectEvents(project.name, ['traffic.http'], (type, value) => {
      if (type === 'traffic.http' && !paused) setTraffic((items) => [value as TrafficEvent, ...items].slice(0, 1000))
    })
  }, [project.name, paused])
  const filtered = traffic.filter((event) => `${event.method} ${event.path} ${event.source} ${event.target} ${event.status}`.toLowerCase().includes(filter.toLowerCase()))
  return <section className="panel traffic-panel">
    <div className="panel-title traffic-toolbar"><span>LIVE HTTP TRAFFIC</span><div><span className="live-count"><i />{paused ? 'PAUSED' : 'STREAMING'}</span><button className="button button--small" onClick={() => setPaused((value) => !value)}>{paused ? 'RESUME' : 'PAUSE'}</button><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="filter method, path, edge…" /></div></div>
    <div className="table-row table-row--header traffic-row"><span>Seq</span><span>When</span><span>Method</span><span>Path</span><span>Edge</span><span>Status</span><span>Duration</span><span>Fault / recording</span></div>
    {filtered.map((event) => <button className="table-row traffic-row" key={event.sequence} onClick={() => setSelected(event)}><code>#{event.sequence}</code><span>{new Date(event.startedAt).toLocaleTimeString()}</span><strong>{event.method || event.protocol.toUpperCase()}</strong><code className="truncate">{event.path || 'TCP session'}</code><span>{event.source}<i className="edge-arrow">→</i>{event.target}</span><span className={(event.status || 0) >= 500 ? 'danger-text' : (event.status || 0) >= 400 ? 'warning-text' : ''}>{event.status || '—'}</span><span>{duration(event.durationMs)}</span><span>{event.fault ? <b className="fault-chip">▲ {event.fault}</b> : event.recording ? <b className="record-chip">● {event.recording}</b> : '—'}</span></button>)}
    {filtered.length === 0 && <div className="empty-row">No matching HTTP traffic yet. Requests through a <code>service.{project.name}.localhost</code> ingress or discovered HTTP edge appear here.</div>}
    {selected && <div className="traffic-detail"><header><div><span className="eyebrow">HTTP TRAFFIC #{selected.sequence}</span><h3>{selected.method} {selected.path}</h3></div><button onClick={() => setSelected(null)}>×</button></header><div className="detail-grid"><Detail label="EDGE" value={`${selected.source} → ${selected.target}`} /><Detail label="STATUS" value={String(selected.status || '—')} /><Detail label="DURATION" value={duration(selected.durationMs)} /><Detail label="REQUEST" value={`${selected.requestBytes} B`} /><Detail label="RESPONSE" value={`${selected.responseBytes} B`} /><Detail label="FAULT" value={selected.fault || 'none'} /></div><div className="drawer-section"><div className="eyebrow">REDACTED REQUEST HEADERS</div><pre>{JSON.stringify(selected.headers || {}, null, 2)}</pre></div></div>}
  </section>
}

function RecordingsPanel({ project, recordings, refresh }: { project: Project; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('checkout-debug')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [error, setError] = useState('')
  const start = async () => {
    setError('')
    try { await api(projectPath(project.name, '/recordings'), { method: 'POST', ...jsonBody({ name, source, target, captureBodies: false, maxEvents: 10000, maxBodyBytes: 65536 }) }); await refresh() }
    catch (value) { setError(value instanceof Error ? value.message : String(value)) }
  }
  const stop = async (recording: Recording) => { await api(projectPath(project.name, `/recordings/${encodeURIComponent(recording.name)}/stop`), { method: 'POST' }); await refresh() }
  const remove = async (recording: Recording) => { await api(projectPath(project.name, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' }); await refresh() }
  return <div className="experiment-layout">
    <section className="panel experiment-form"><div className="panel-title"><span>START RECORDING</span><small>bounded, metadata-first capture</small></div><label><span>NAME</span><input value={name} onChange={(event) => setName(event.target.value)} /></label><div className="form-pair"><label><span>SOURCE</span><select value={source} onChange={(event) => setSource(event.target.value)}><option value="">any source</option><option value="external">external</option>{project.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label><label><span>TARGET</span><select value={target} onChange={(event) => setTarget(event.target.value)}><option value="">any target</option>{project.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label></div><div className="scope-preview"><span className="eyebrow">EFFECTIVE SCOPE</span><p>Capture request metadata for <strong>{source || 'any source'}</strong> → <strong>{target || 'any target'}</strong>. Authorization, cookies, API keys, tokens, and secret-like headers are redacted.</p></div>{error && <p className="danger-text">{error}</p>}<button className="button button--primary" onClick={start}>● START RECORDING</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>RECORDINGS</span><small>{recordings.length} retained locally</small></div>{recordings.map((recording) => <div className="experiment-row" key={recording.name}><StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} /><div><strong>{recording.name}</strong><small>{recording.source || 'any'} → {recording.target || 'any'} · {recording.eventCount} events</small></div><span>{relativeTime(recording.startedAt)} ago</span><div>{recording.status === 'active' ? <button onClick={() => stop(recording)}>STOP</button> : <><a href={`/api/v1/projects/${project.name}/recordings/${recording.name}/export`}>EXPORT</a><button onClick={() => remove(recording)}>DELETE</button></>}</div></div>)}{recordings.length === 0 && <div className="empty-row">No recordings. Start one before reproducing a local issue.</div>}</section>
  </div>
}

function FaultsPanel({ project, faults, refresh }: { project: Project; faults: FaultRule[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('slow-downstream')
  const [source, setSource] = useState(project.primaryService || 'external')
  const [target, setTarget] = useState(project.services.find((service) => service.name !== source)?.name || project.services[0]?.name || '')
  const [effect, setEffect] = useState<'latency' | 'status' | 'abort'>('latency')
  const [value, setValue] = useState('2000')
  const [error, setError] = useState('')
  const create = async () => {
    setError('')
    const body = { name, source, target, probability: 1, latencyMs: effect === 'latency' ? Number(value) : 0, statusCode: effect === 'status' ? Number(value) : 0, abort: effect === 'abort' }
    try { await api(projectPath(project.name, '/faults'), { method: 'POST', ...jsonBody(body) }); await refresh() }
    catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
  }
  const disable = async (fault: FaultRule) => { await api(projectPath(project.name, `/faults/${encodeURIComponent(fault.name)}`), { method: 'DELETE' }); await refresh() }
  const clear = async () => { await api(projectPath(project.name, '/faults/disable-all'), { method: 'POST' }); await refresh() }
  return <div className="experiment-layout">
    <section className="panel experiment-form"><div className="panel-title"><span>INTRODUCE FAILURE</span><small>scoped to one local edge</small></div><label><span>NAME</span><input value={name} onChange={(event) => setName(event.target.value)} /></label><div className="form-pair"><label><span>SOURCE</span><select value={source} onChange={(event) => setSource(event.target.value)}><option value="external">external</option>{project.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label><label><span>TARGET</span><select value={target} onChange={(event) => setTarget(event.target.value)}>{project.services.map((service) => <option key={service.name}>{service.name}</option>)}</select></label></div><div className="segmented">{(['latency', 'status', 'abort'] as const).map((item) => <button key={item} className={effect === item ? 'is-active' : ''} onClick={() => { setEffect(item); setValue(item === 'latency' ? '2000' : item === 'status' ? '503' : '') }}>{item}</button>)}</div>{effect !== 'abort' && <label><span>{effect === 'latency' ? 'MILLISECONDS' : 'HTTP STATUS'}</span><input type="number" value={value} onChange={(event) => setValue(event.target.value)} /></label>}<div className="scope-preview scope-preview--warning"><span className="eyebrow">BLAST RADIUS</span><p>Only requests from <strong>{source}</strong> to <strong>{target}</strong> are affected. The rule expires automatically after 10 minutes and all rules are disabled on project shutdown.</p></div>{error && <p className="danger-text">{error}</p>}<button className="button button--warning" onClick={create}>▲ ENABLE FAULT</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>FAULT RULES</span><button onClick={clear}>DISABLE ALL</button></div>{faults.map((fault) => <div className={`experiment-row ${fault.enabled ? 'is-warning' : ''}`} key={fault.name}><StatusMark status={fault.enabled ? 'degraded' : 'stopped'} label={false} /><div><strong>{fault.name}</strong><small>{fault.scopeSummary}</small></div><span>{fault.matchCount} matches</span><div>{fault.enabled && <button onClick={() => disable(fault)}>DISABLE</button>}</div></div>)}{faults.length === 0 && <div className="empty-row">No fault rules have been created.</div>}</section>
  </div>
}

function TimelinePanel({ timeline }: { timeline: TimelineEvent[] }) {
  const groups = useMemo(() => timeline.reduce<Record<string, TimelineEvent[]>>((result, event) => { const key = new Date(event.timestamp).toLocaleDateString(); (result[key] ||= []).push(event); return result }, {}), [timeline])
  return <section className="panel timeline-panel"><div className="panel-title"><span>PROJECT TIMELINE</span><small>durable local history · actors and outcomes retained</small></div>{Object.entries(groups).map(([date, events]) => <div className="timeline-group" key={date}><div className="timeline-date">{date}</div>{events.map((event) => <div className="timeline-event" key={event.sequence}><time>{new Date(event.timestamp).toLocaleTimeString()}</time><span className={`timeline-dot timeline-dot--${event.severity}`} /><div><strong>{event.summary}</strong><small>{event.type} · {event.actor}{event.subject ? ` · ${event.subject}` : ''}</small></div><code>#{event.sequence}</code></div>)}</div>)}{timeline.length === 0 && <div className="empty-row">The timeline will capture review, lifecycle, recording, and fault events.</div>}</section>
}
