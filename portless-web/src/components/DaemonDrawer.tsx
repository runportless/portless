import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { APIError } from '../api'
import { DAEMON_RESTART_SLA_MS, daemonRestartDeadline, daemonRestartPollDelay } from '../daemonRestart'
import type { ControlPlaneHealth, DaemonDiagnostics, DaemonHandoffStatus, DaemonRestart, DaemonStatus, RelayStatus, RuntimeStatus } from '../types'
import { DaemonLogs } from './DaemonLogs'
import { DrawerSizeButton } from './DrawerSizeButton'
import { relativeTime, StatusMark } from './Status'

type RestartPhase = 'idle' | 'confirm' | 'restarting' | 'reconnected' | 'failed'
type HandoffPhase = 'idle' | 'checking' | 'ready' | 'blocked' | 'failed'
type StoragePhase = 'idle' | 'loading' | 'ready' | 'failed'
type DaemonDrawerTab = 'status' | 'runtime' | 'storage' | 'logs'

const daemonTabs: Array<{ id: DaemonDrawerTab; label: string }> = [
  { id: 'status', label: 'STATUS' },
  { id: 'runtime', label: 'RUNTIME' },
  { id: 'storage', label: 'STORAGE' },
  { id: 'logs', label: 'LOGS' },
]

export function DaemonDrawer({ status, diagnostics, controlPlaneHealth, runtime, relay, live, onClose, onRefresh, onRefreshDiagnostics, onVerifyHandoff, onRestart, onReconnected }: {
  status: DaemonStatus | null
  diagnostics: DaemonDiagnostics | null
  controlPlaneHealth: ControlPlaneHealth
  runtime: RuntimeStatus | null
  relay: RelayStatus | null
  live: boolean
  onClose: () => void
  onRefresh: () => Promise<DaemonStatus>
  onRefreshDiagnostics: (includeStorage?: boolean) => Promise<DaemonDiagnostics>
  onVerifyHandoff: () => Promise<DaemonHandoffStatus>
  onRestart: (instanceId: string) => Promise<DaemonRestart>
  onReconnected: () => Promise<void>
}) {
  const [phase, setPhase] = useState<RestartPhase>('idle')
  const [error, setError] = useState('')
  const [copyState, setCopyState] = useState('COPY DIAGNOSTICS')
  const [fullScreen, setFullScreen] = useState(false)
  const [tab, setTab] = useState<DaemonDrawerTab>('status')
  const [handoff, setHandoff] = useState<DaemonHandoffStatus | null>(null)
  const [handoffPhase, setHandoffPhase] = useState<HandoffPhase>('idle')
  const [handoffError, setHandoffError] = useState('')
  const [storagePhase, setStoragePhase] = useState<StoragePhase>(diagnostics?.storage ? 'ready' : 'idle')
  const [storageError, setStorageError] = useState('')
  const mounted = useRef(true)
  const tabButtons = useRef<Array<HTMLButtonElement | null>>([])
  const drawerContent = useRef<HTMLDivElement>(null)
  const active = handoff?.activeEnvironments ?? status?.activeEnvironments ?? []
  const handoffReady = handoff?.state === 'ready'
  const restartSafe = active.length === 0 || handoffReady
  const handoffBlocked = active.length > 0 && (handoffPhase === 'blocked' || handoffPhase === 'failed')
  const restarting = phase === 'restarting'
  const effectiveState = restarting ? 'restarting' : phase === 'reconnected' ? 'ready' : live ? status?.state ?? 'unknown' : 'unreachable'

  useEffect(() => () => { mounted.current = false }, [])
  const activeKey = status?.activeEnvironments.join('\u0000') ?? ''
  useEffect(() => {
    if (!status || !live) {
      setHandoff(null)
      setHandoffPhase('idle')
      return
    }
    let current = true
    setHandoff(null)
    setHandoffError('')
    setHandoffPhase('checking')
    void onVerifyHandoff().then((result) => {
      if (!current || !mounted.current) return
      setHandoff(result)
      setHandoffPhase(result.state)
    }).catch((value) => {
      if (!current || !mounted.current) return
      setHandoffError(errorMessage(value))
      setHandoffPhase('failed')
    })
    return () => { current = false }
  }, [activeKey, live, onVerifyHandoff, status?.instanceId])
  useEffect(() => {
    if (diagnostics?.storage) setStoragePhase('ready')
  }, [diagnostics?.storage])
  useEffect(() => {
    if (tab !== 'storage' || diagnostics?.storage || storagePhase !== 'idle' || !live) return
    setStoragePhase('loading')
    setStorageError('')
    void onRefreshDiagnostics(true).then(() => {
      if (mounted.current) setStoragePhase('ready')
    }).catch((value) => {
      if (!mounted.current) return
      setStorageError(errorMessage(value))
      setStoragePhase('failed')
    })
  }, [diagnostics?.storage, live, onRefreshDiagnostics, storagePhase, tab])
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (fullScreen) setFullScreen(false)
      else onClose()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [fullScreen, onClose])

  const selectTab = (next: DaemonDrawerTab) => {
    setTab(next)
    if (drawerContent.current) drawerContent.current.scrollTop = 0
  }

  const tabKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>, index: number) => {
    const next = daemonTabIndexForKey(index, event.key)
    if (next === null) return
    event.preventDefault()
    selectTab(daemonTabs[next].id)
    tabButtons.current[next]?.focus()
  }

  const prepareRestart = async () => {
    if (!status || !live || restarting) return
    selectTab('runtime')
    setPhase('idle')
    setError('')
    setHandoffError('')
    setHandoffPhase('checking')
    const diagnosticsRefresh = onRefreshDiagnostics(false).catch(() => undefined)
    try {
      const result = await onVerifyHandoff()
      await diagnosticsRefresh
      if (!mounted.current) return
      setHandoff(result)
      setHandoffPhase(result.state)
      if (result.activeEnvironments.length > 0 && result.state !== 'ready') {
        setError('The refreshed runtime handoff audit does not permit a safe restart.')
        setPhase('failed')
        return
      }
      setPhase('confirm')
    } catch (value) {
      if (!mounted.current) return
      const message = errorMessage(value)
      setHandoffError(message)
      setHandoffPhase('failed')
      setError(message)
      setPhase('failed')
    }
  }

  const restartDaemon = async () => {
    if (!status || !restartSafe || restarting) return
    const previousInstance = status.instanceId
    setError('')
    setPhase('restarting')
    const initiatedAt = Date.now()
    try {
      const receipt = await onRestart(previousInstance)
      const deadline = daemonRestartDeadline(receipt, initiatedAt)
      let attempt = 0
      while (Date.now() < deadline && mounted.current) {
        await wait(Math.min(daemonRestartPollDelay(attempt++), Math.max(0, deadline - Date.now())))
        if (Date.now() >= deadline) break
        try {
          const replacement = await settleBeforeDeadline(onRefresh(), deadline)
          if (!replacement) break
          if (Date.now() <= deadline && replacement.instanceId !== previousInstance && replacement.state === 'ready') {
            if (!mounted.current) return
            setPhase('reconnected')
            await onReconnected()
            return
          }
        } catch {
          // A refused connection is expected between shutdown and replacement readiness.
        }
      }
      throw new Error(`The replacement daemon did not become ready within the ${DAEMON_RESTART_SLA_MS / 1_000} second SLA.`)
    } catch (value) {
      if (!mounted.current) return
      setError(errorMessage(value))
      setPhase('failed')
    }
  }

  const copyDiagnostics = async () => {
    if (!status || !navigator.clipboard) return
    try {
      await navigator.clipboard.writeText(daemonDiagnostics(status, runtime, relay, handoff, diagnostics, controlPlaneHealth))
      setCopyState('COPIED')
      window.setTimeout(() => mounted.current && setCopyState('COPY DIAGNOSTICS'), 1600)
    } catch {
      setCopyState('COPY FAILED')
    }
  }

  const tabAlert = (candidate: DaemonDrawerTab) => daemonTabAlert(candidate, live, diagnostics, controlPlaneHealth, runtime, relay, handoffPhase)

  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className={`drawer daemon-drawer ${fullScreen ? 'drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label="Portless Daemon" onMouseDown={(event) => event.stopPropagation()}>
      <header><div className="daemon-drawer-heading"><div><h2>Portless Daemon</h2><StatusMark status={effectiveState} /></div></div><div className="drawer-header-actions"><DrawerSizeButton fullScreen={fullScreen} subject="Portless Daemon" onToggle={() => setFullScreen((value) => !value)} /><button className="icon-button" onClick={onClose} aria-label="Close">×</button></div></header>
      <div className="drawer-actions">
        <button className="button button--warning" onClick={() => void prepareRestart()} disabled={!status || !live || !restartSafe || restarting || (active.length > 0 && handoffPhase === 'checking')}>RESTART DAEMON</button>
        <button className="button" onClick={() => void copyDiagnostics()} disabled={!status}>{copyState}</button>
      </div>
      <div className="drawer-tabs daemon-drawer-tabs" role="tablist" aria-label="Daemon details">
        {daemonTabs.map((item, index) => <button ref={(button) => { tabButtons.current[index] = button }} id={`daemon-tab-${item.id}`} aria-controls={`daemon-panel-${item.id}`} className={tab === item.id ? 'is-active' : ''} type="button" role="tab" aria-selected={tab === item.id} tabIndex={tab === item.id ? 0 : -1} onKeyDown={(event) => tabKeyDown(event, index)} onClick={() => selectTab(item.id)} key={item.id}>{item.label}{tabAlert(item.id) && <i className="daemon-tab-alert" aria-hidden="true" />}</button>)}
      </div>
      <div ref={drawerContent} className="drawer-content" id={`daemon-panel-${tab}`} role="tabpanel" aria-labelledby={`daemon-tab-${tab}`} tabIndex={0}>
        {tab === 'status' && <StatusPanel status={status} diagnostics={diagnostics} health={controlPlaneHealth} live={live} />}
        {tab === 'runtime' && <RuntimePanel status={status} diagnostics={diagnostics} runtime={runtime} relay={relay} active={active} handoff={handoff} handoffPhase={handoffPhase} handoffError={handoffError} handoffBlocked={handoffBlocked} handoffReady={handoffReady} />}
        {tab === 'storage' && <StoragePanel storage={diagnostics?.storage ?? null} phase={storagePhase} error={storageError} />}
        {tab === 'logs' && (live ? <DaemonLogs instanceId={status?.instanceId} /> : <Unavailable title="DAEMON LOGS UNAVAILABLE" message="The UI is waiting for the local daemon to become reachable." />)}

        {tab === 'runtime' && phase === 'confirm' && <section className="daemon-confirm" role="alertdialog" aria-label="Confirm daemon restart">
          <h3>Restart the Portless daemon?</h3>
          <ul><li>Services and containers keep running.</li><li>The control plane reconnects automatically.</li><li>Clean URL routing and traffic capture may pause briefly.</li></ul>
          <div><button className="button button--warning" onClick={() => void restartDaemon()}>RESTART AND RECONNECT</button><button className="button" onClick={() => setPhase('idle')}>CANCEL</button></div>
        </section>}

        {tab === 'runtime' && (phase === 'restarting' || phase === 'reconnected') && <section className={`daemon-progress ${phase === 'reconnected' ? 'daemon-progress--complete' : ''}`} aria-live="polite">
          <i aria-hidden="true" /><div><strong>{phase === 'reconnected' ? 'Daemon restarted' : 'Restarting daemon…'}</strong><small>{phase === 'reconnected' ? 'Connected to the replacement instance.' : 'Waiting for the replacement instance.'}</small></div>
        </section>}

        {tab === 'runtime' && (phase === 'failed' || (!live && phase !== 'restarting')) && <Unavailable title={phase === 'failed' ? 'RESTART FAILED' : 'DAEMON UNREACHABLE'} message={error || 'The UI is waiting for the local daemon to become reachable.'} />}
      </div>
    </aside>
  </div>
}

export function daemonTabIndexForKey(index: number, key: string) {
  if (key === 'ArrowRight') return (index + 1) % daemonTabs.length
  if (key === 'ArrowLeft') return (index - 1 + daemonTabs.length) % daemonTabs.length
  if (key === 'Home') return 0
  if (key === 'End') return daemonTabs.length - 1
  return null
}

function StatusPanel({ status, diagnostics, health, live }: { status: DaemonStatus | null; diagnostics: DaemonDiagnostics | null; health: ControlPlaneHealth; live: boolean }) {
  if (!status) return <Unavailable title="DAEMON STATUS UNAVAILABLE" message="Portless could not load daemon identity information." />
  const build = diagnostics?.build
  const recovery = diagnostics?.recovery
  const lastRestart = diagnostics?.lastRestart
  return <>
    <div className="detail-grid daemon-detail-grid">
      <Detail label="PID" value={String(status.pid)} />
      <Detail label="STARTED" value={`${relativeTime(status.startedAt)} ago`} />
      <Detail label="INSTANCE" value={shortFingerprint(status.instanceId)} title={status.instanceId} detail={activeEnvironmentCount(status.activeEnvironments.length)} />
      <Detail label="BUILD" value={shortFingerprint(status.buildId)} title={status.buildId} />
      <Detail label="PROTOCOL" value={status.protocolVersion} />
      <Detail label="API" value={status.apiVersion} />
    </div>
    <section className={`drawer-section daemon-build ${build && !build.current ? 'daemon-section--warning' : ''}`}>
      <div className="daemon-section-heading"><span className="eyebrow">BUILD PROVENANCE</span><StatusMark status={build ? build.current ? 'ready' : 'degraded' : 'unknown'} /></div>
      {build ? <div className="daemon-build-grid">
        <Detail label="VERSION" value={build.version} />
        <Detail label="DISTRIBUTION" value={displayDistribution(build.distribution)} />
        <Detail label="COMMIT" value={shortFingerprint(build.commit)} title={build.commit} />
        <Detail label="INSTALLED BINARY" value={build.problem ? 'Comparison unavailable' : build.current ? 'Current' : 'Replacement pending'} detail={build.onDiskBuildId ? `${shortFingerprint(build.runningBuildId)} running · ${shortFingerprint(build.onDiskBuildId)} installed` : build.problem || 'On-disk identity unavailable'} />
      </div> : <p>Build provenance is loading.</p>}
    </section>
    <section className={`drawer-section daemon-control-health ${!live || health.api.state !== 'ready' ? 'daemon-section--warning' : ''}`}>
      <div className="daemon-section-heading"><span className="eyebrow">CONTROL-PLANE HEALTH</span><StatusMark status={live && health.api.state === 'ready' ? 'ready' : 'degraded'} /></div>
      <div className="daemon-health-grid">
        <HealthDetail label="API ROUND TRIP" value={health.api.latencyMs === undefined ? 'Unavailable' : `${health.api.latencyMs} ms`} detail={health.api.checkedAt ? `Checked ${relativeTime(health.api.checkedAt)} ago` : 'No successful check yet'} healthy={health.api.state === 'ready'} />
        <HealthDetail label="EVENT STREAMS" value={eventHealthLabel(health.events)} detail={eventHealthDetail(health.events)} healthy={health.events.state !== 'reconnecting'} idle={health.events.state === 'idle'} />
      </div>
    </section>
    {lastRestart && <section className={`drawer-section daemon-restart-status ${lastRestart.withinSla ? '' : 'daemon-section--warning'}`}>
      <div className="daemon-section-heading"><span className="eyebrow">LAST RESTART</span><StatusMark status={lastRestart.withinSla ? 'ready' : 'degraded'} /></div>
      <div className="daemon-recovery-grid">
        <Detail label="TRIGGER" value={displayState(lastRestart.reason)} />
        <Detail label="DURATION" value={formatDuration(lastRestart.durationMs)} />
        <Detail label="READY" value={`${relativeTime(lastRestart.readyAt)} ago`} />
        <Detail label="5 SECOND SLA" value={lastRestart.withinSla ? 'Met' : 'Missed'} detail={shortFingerprint(lastRestart.restartId)} title={lastRestart.restartId} />
      </div>
    </section>}
    <section className={`drawer-section daemon-recovery ${recovery && recovery.result !== 'healthy' ? 'daemon-section--warning' : ''}`}>
      <div className="daemon-section-heading"><span className="eyebrow">RECOVERY STATUS</span><StatusMark status={recoveryState(recovery?.result)} /></div>
      {recovery ? <>
        <div className="daemon-recovery-grid">
          <Detail label="LAST RECONCILIATION" value={recovery.completedAt ? `${relativeTime(recovery.completedAt)} ago` : 'Not run'} />
          <Detail label="DURATION" value={formatDuration(recovery.durationMs)} />
          <Detail label="RESULT" value={displayState(recovery.result)} />
          <Detail label="RECOVERED" value={String(recovery.recovered)} detail="active environments" />
        </div>
        <Problems values={[...status.recoveryProblems, ...recovery.problems]} />
      </> : <p>Recovery details are loading.</p>}
    </section>
  </>
}

function RuntimePanel({ status, diagnostics, runtime, relay, active, handoff, handoffPhase, handoffError, handoffBlocked, handoffReady }: {
  status: DaemonStatus | null
  diagnostics: DaemonDiagnostics | null
  runtime: RuntimeStatus | null
  relay: RelayStatus | null
  active: string[]
  handoff: DaemonHandoffStatus | null
  handoffPhase: HandoffPhase
  handoffError: string
  handoffBlocked: boolean
  handoffReady: boolean
}) {
  if (!status) return <Unavailable title="RUNTIME STATUS UNAVAILABLE" message="Portless could not load daemon runtime information." />
  const networkingReady = relay?.healthy === true && relay.helperCurrent === true
  return <>
    <RuntimeEngine runtime={runtime} />
    <ManagedInventory diagnostics={diagnostics} />
    <section className={`drawer-section daemon-network ${networkingReady ? '' : 'daemon-network--degraded'}`}>
      <div className="daemon-section-heading"><span className="eyebrow">LOCAL NETWORKING</span><StatusMark status={networkingReady ? 'ready' : relay?.installed ? 'degraded' : 'missing'} /></div>
      <div className="daemon-network-grid">
        <NetworkDetail label="HTTP INGRESS" value="127.0.0.1:80" healthy={relay?.httpHealthy === true} />
        <NetworkDetail label="ENDPOINT DNS" value={relay?.dnsListenAddress || '127.77.0.1:1053'} healthy={relay?.dnsHealthy === true} />
        <NetworkDetail label="DNS ZONES" value="localhost · portless.test" healthy={relay?.resolverHealthy === true} />
        <NetworkDetail label="TCP ADDRESS POOL" value={relay?.endpointPoolDetail || 'not provisioned'} healthy={relay?.endpointPoolReady === true} />
      </div>
      <RelayRuntime relay={relay} />
      {!relay?.healthy && <p>{relay?.problem || relay?.resolverHealthError || relay?.dnsHealthError || relay?.healthError || 'Run portless doctor relay to inspect clean HTTP URLs and TCP endpoint DNS.'}</p>}
    </section>
    <section className={`drawer-section daemon-handoff ${handoffBlocked ? 'daemon-handoff--blocked' : ''}`}>
      <div className="daemon-section-heading"><span className="eyebrow">RUNTIME HANDOFF</span><div className="daemon-handoff-heading-status">{handoff?.verifiedAt && <time className="daemon-handoff-verified" dateTime={handoff.verifiedAt}><span>VERIFIED</span><strong>{relativeTime(handoff.verifiedAt)} ago</strong></time>}<StatusMark status={handoffDisplayState(active, handoffPhase)} /></div></div>
      <p>{handoffDescription(active, handoffPhase)}</p>
      {active.length > 0 && <div className="daemon-environments">{active.map((environment) => <div key={environment}><StatusMark status={handoffReady ? 'active' : 'unknown'} label={false} /><code>{environment}</code><small>ACTIVE</small></div>)}</div>}
      <Problems values={[handoffError, ...(handoff?.problems ?? [])]} />
    </section>
  </>
}

function ManagedInventory({ diagnostics }: { diagnostics: DaemonDiagnostics | null }) {
  const inventory = diagnostics?.inventory
  return <section className={`drawer-section daemon-inventory ${inventory?.problems.length ? 'daemon-section--warning' : ''}`}>
    <div className="daemon-section-heading"><span className="eyebrow">MANAGED INVENTORY</span><StatusMark status={inventory ? inventory.problems.length ? 'degraded' : 'ready' : 'unknown'} /></div>
    {inventory ? <>
      <div className="daemon-inventory-grid">
        <Metric label="PROCESSES" value={inventory.processes} />
        <Metric label="CONTAINERS" value={inventory.containers} />
        <Metric label="PROXIES" value={inventory.proxyListeners} detail="live listeners" />
        <Metric label="ENVIRONMENTS" value={inventory.activeEnvironments} detail="active" />
      </div>
      <Problems values={inventory.problems} />
    </> : <p>Managed inventory is loading.</p>}
  </section>
}

function StoragePanel({ storage, phase, error }: { storage: DaemonDiagnostics['storage'] | null; phase: StoragePhase; error: string }) {
  if (!storage) {
    if (phase === 'failed') return <Unavailable title="STORAGE INSPECTION FAILED" message={error} />
    return <section className="drawer-section daemon-storage-loading" aria-live="polite"><span className="eyebrow">STORAGE &amp; RETENTION</span><p>Inspecting retained data and configured limits…</p></section>
  }
  const observed = storage.databaseBytes + storage.liveTrafficBytes + storage.serviceLogBytes + storage.daemonLogBytes
  return <>
    <section className={`drawer-section daemon-storage-summary ${storage.problems.length ? 'daemon-section--warning' : ''}`}>
      <div className="daemon-section-heading"><span className="eyebrow">STORAGE &amp; RETENTION</span><StatusMark status={storage.problems.length ? 'degraded' : 'ready'} /></div>
      <div className="daemon-storage-total"><span>OBSERVED FOOTPRINT</span><strong>{formatBytes(observed)}</strong><small>Disk-backed state and logs plus live traffic memory</small></div>
      <div className="daemon-storage-grid">
        <StorageMetric label="STATE DATABASE" value={formatBytes(storage.databaseBytes)} detail="SQLite + WAL + SHM" />
        <StorageMetric label="RECORDINGS" value={formatBytes(storage.recordedBytes)} detail={`${storage.recordingCount} sessions · ${storage.recordedEventCount} events · inside SQLite`} />
        <StorageMetric label="LIVE TRAFFIC" value={formatBytes(storage.liveTrafficBytes)} detail={`${storage.liveTrafficExchanges} exchanges · memory`} />
        <StorageMetric label="SERVICE LOGS" value={formatBytes(storage.serviceLogBytes)} detail="retained generations" />
        <StorageMetric label="DAEMON LOG" value={formatBytes(storage.daemonLogBytes)} detail="private daemon output" />
      </div>
      <Problems values={storage.problems} />
    </section>
    <section className="drawer-section daemon-retention-limits">
      <span className="eyebrow">CONFIGURED LIMITS</span>
      <div className="daemon-retention-rows">
        <RetentionRow label="LIVE TRAFFIC" value={`${storage.trafficExchangeLimitPerEnvironment.toLocaleString()} exchanges · ${formatBytes(storage.trafficPayloadLimitPerEnvironment)}`} detail="per environment" />
        <RetentionRow label="RECORDINGS" value={`${storage.recordingDefaultEventLimit.toLocaleString()} default · ${storage.recordingMaximumEventLimit.toLocaleString()} max events`} detail={`${formatBytes(storage.recordingDefaultPayloadLimit)} default · ${formatBytes(storage.recordingMaximumPayloadLimit)} max payload per exchange`} />
        <RetentionRow label="SERVICE LOGS" value={`${storage.serviceLogGenerationLimit} generations · ${formatBytes(storage.serviceLogStreamLimitBytes)} per stream`} detail="automatic generation pruning" />
        <RetentionRow label="DAEMON LOG" value="No automatic limit" detail="manual retention" warning />
      </div>
    </section>
    <section className="drawer-section daemon-pruning">
      <span className="eyebrow">LAST PRUNING</span>
      <div className="daemon-retention-rows">
        <RetentionRow label="LIVE TRAFFIC" value={storage.trafficPrunedAt ? `${relativeTime(storage.trafficPrunedAt)} ago` : 'Never'} detail="automatic eviction" />
        <RetentionRow label="SERVICE LOGS" value={storage.serviceLogsPrunedAt ? `${relativeTime(storage.serviceLogsPrunedAt)} ago` : 'Never'} detail="automatic generation pruning" />
        <RetentionRow label="RECORDINGS" value="Manual only" detail="deleted explicitly" />
        <RetentionRow label="DAEMON LOG" value="Not configured" detail="no automatic pruning" warning />
      </div>
    </section>
  </>
}

function Unavailable({ title, message }: { title: string; message: string }) {
  return <section className="daemon-restart-error daemon-log-unavailable" role="alert"><span className="eyebrow">{title}</span><p>{message}</p><pre><span>$</span> portless doctor</pre></section>
}

function Detail({ label, value, title, detail }: { label: string; value: string; title?: string; detail?: string }) {
  return <div><span>{label}</span><strong title={title}>{value}</strong>{detail && <small>{detail}</small>}</div>
}

function Metric({ label, value, detail }: { label: string; value: number; detail?: string }) {
  return <div><span>{label}</span><strong>{value.toLocaleString()}</strong>{detail && <small>{detail}</small>}</div>
}

function RuntimeEngine({ runtime }: { runtime: RuntimeStatus | null }) {
  return <section className={`drawer-section daemon-runtime ${runtime?.state === 'failed' ? 'daemon-runtime--failed' : ''}`}>
    <div className="daemon-section-heading"><span className="eyebrow">RUNTIME ENGINE</span><StatusMark status={runtime?.state ?? 'unknown'} /></div>
    {runtime ? <>
      <div className="daemon-runtime-summary">
        <div><span>PREFERENCE</span><strong>{runtimePreference(runtime.preference)}</strong></div>
        <div><span>SELECTED</span><strong>{runtime.selected ? runtimeName(runtime.selected) : 'None'}</strong>{runtime.version && <code>v{runtime.version}</code>}</div>
      </div>
      <div className="daemon-runtime-candidates">{runtime.candidates.map((candidate) => <div className="daemon-runtime-candidate" key={candidate.name}>
        <StatusMark status={candidate.state} label={false} />
        <div><strong>{runtimeName(candidate.name)}{candidate.version && <code>v{candidate.version}</code>}</strong><small>{candidate.reason || (candidate.state === 'ready' ? 'Engine is available.' : 'Engine is unavailable.')}</small></div>
        <span className={candidate.name === runtime.selected ? 'is-selected' : ''}>{candidate.name === runtime.selected ? 'SELECTED' : candidate.state.toUpperCase()}</span>
      </div>)}</div>
      {runtime.reason && <p className="daemon-runtime-problem">{runtime.reason}</p>}
    </> : <p className="daemon-runtime-unavailable">Runtime status is unavailable while the daemon reconnects.</p>}
  </section>
}

function NetworkDetail({ label, value, healthy }: { label: string; value: string; healthy: boolean }) {
  return <div className="daemon-network-detail"><div><span>{label}</span><StatusMark status={healthy ? 'ready' : 'failed'} label={false} /></div><code title={value}>{value}</code></div>
}

function RelayRuntime({ relay }: { relay: RelayStatus | null }) {
  const currency = relayCurrency(relay)
  const socketsConnected = Boolean(relay?.targetSocket && relay?.dnsTargetSocket)
  return <div className="daemon-relay-grid">
    <RelayDetail label="SYSTEM SERVICE" value={relay?.service || 'Not installed'} detail={relay ? `${relay.platform || 'unknown platform'} · ${relay.running ? 'running' : 'stopped'}` : 'Relay status unavailable'} healthy={relay?.running === true} />
    <RelayDetail label="RELAY HELPER" value={currency.value} detail={currency.detail} healthy={relay?.helperCurrent === true} />
    <RelayDetail label="DAEMON SOCKETS" value={socketsConnected ? 'Connected' : 'Unavailable'} detail="HTTP and DNS targets" healthy={socketsConnected} />
  </div>
}

function RelayDetail({ label, value, detail, healthy }: { label: string; value: string; detail: string; healthy: boolean }) {
  return <div className="daemon-relay-detail"><div><span>{label}</span><StatusMark status={healthy ? 'ready' : 'failed'} label={false} /></div><strong>{value}</strong><small>{detail}</small></div>
}

function HealthDetail({ label, value, detail, healthy, idle = false }: { label: string; value: string; detail: string; healthy: boolean; idle?: boolean }) {
  return <div className="daemon-health-detail"><div><span>{label}</span><StatusMark status={idle ? 'not required' : healthy ? 'ready' : 'failed'} label={false} /></div><strong>{value}</strong><small>{detail}</small></div>
}

function StorageMetric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>
}

function RetentionRow({ label, value, detail, warning = false }: { label: string; value: string; detail: string; warning?: boolean }) {
  return <div className={warning ? 'is-warning' : ''}><span>{label}</span><div><strong>{value}</strong><small>{detail}</small></div></div>
}

function Problems({ values }: { values: string[] }) {
  const problems = [...new Set(values.filter(Boolean))]
  return problems.length ? <ul className="daemon-problems">{problems.map((problem) => <li key={problem}>{problem}</li>)}</ul> : null
}

function shortFingerprint(value: string) {
  return value.length > 12 ? value.slice(0, 12) : value
}

function activeEnvironmentCount(count: number) {
  if (count === 0) return 'No active environments'
  return `${count} active environment${count === 1 ? '' : 's'}`
}

function runtimeName(value: 'docker' | 'podman') {
  return value === 'docker' ? 'Docker' : 'Podman'
}

function runtimePreference(value: RuntimeStatus['preference']) {
  return value === 'auto' ? 'Automatic' : runtimeName(value)
}

function relayCurrency(relay: RelayStatus | null) {
  if (!relay) return { value: 'Unavailable', detail: 'Relay status unavailable' }
  if (!relay.installed) return { value: 'Not installed', detail: 'Run portless setup' }
  if (relay.helperCurrent) return {
    value: 'Matches daemon build',
    detail: relay.helperBuildId ? `Build ${shortFingerprint(relay.helperBuildId)}` : 'Installed helper is current',
  }
  if (relay.helperBuildId && relay.currentBuildId) return {
    value: 'Update required',
    detail: `${shortFingerprint(relay.helperBuildId)} installed · ${shortFingerprint(relay.currentBuildId)} current`,
  }
  return { value: 'Build unknown', detail: 'Unable to compare helper and daemon builds' }
}

function runtimeDescription(runtime: RuntimeStatus | null) {
  if (!runtime?.selected) return runtime?.state ?? 'unknown'
  return `${runtime.selected} ${runtime.version ?? ''}`.trim()
}

export function daemonDiagnostics(status: DaemonStatus, runtime: RuntimeStatus | null, relay: RelayStatus | null = null, handoff: DaemonHandoffStatus | null = null, diagnostics: DaemonDiagnostics | null = null, health: ControlPlaneHealth | null = null) {
  const environments = status.activeEnvironments.length ? `\n${status.activeEnvironments.map((value) => `  ${value}`).join('\n')}` : ' none'
  const recoveryProblems = diagnostics?.recovery.problems.length ? `\n${diagnostics.recovery.problems.map((value) => `  ${value}`).join('\n')}` : status.recoveryProblems.length ? `\n${status.recoveryProblems.map((value) => `  ${value}`).join('\n')}` : ' none'
  const handoffProblems = handoff?.problems.length ? `\n${handoff.problems.map((value) => `  ${value}`).join('\n')}` : ' none'
  const runtimeCandidates = runtime?.candidates.length ? `\n${runtime.candidates.map((candidate) => `  ${candidate.name}: ${candidate.state}${candidate.version ? ` v${candidate.version}` : ''}${candidate.reason ? ` — ${candidate.reason}` : ''}`).join('\n')}` : ' none'
  const helper = relayCurrency(relay)
  const inventory = diagnostics?.inventory
  const lastRestart = diagnostics?.lastRestart
  const storage = diagnostics?.storage
  return [
    'Portless daemon',
    `State: ${status.state}`,
    `PID: ${status.pid}`,
    `Started: ${status.startedAt}`,
    `Instance: ${status.instanceId}`,
    `Build: ${status.buildId}`,
    `Version: ${diagnostics?.build.version ?? 'unknown'}`,
    `Distribution: ${diagnostics?.build.distribution ?? 'unknown'}`,
    `Commit: ${diagnostics?.build.commit ?? 'unknown'}`,
    `Installed binary: ${diagnostics?.build.current === undefined ? 'unknown' : diagnostics.build.current ? 'current' : 'replacement pending'}`,
    `Protocol Version: ${status.protocolVersion}`,
    `API Version: ${status.apiVersion}`,
    `API latency: ${health?.api.latencyMs === undefined ? 'unavailable' : `${health.api.latencyMs} ms`}`,
    `Event streams: ${health ? eventHealthLabel(health.events) : 'unknown'}`,
    `Last restart: ${lastRestart?.restartId ?? 'none'}`,
    `Last restart trigger: ${lastRestart?.reason ?? 'unknown'}`,
    `Last restart duration: ${lastRestart ? formatDuration(lastRestart.durationMs) : 'unknown'}`,
    `Last restart SLA: ${lastRestart ? lastRestart.withinSla ? 'met' : 'missed' : 'unknown'}`,
    `Managed processes: ${inventory?.processes ?? 'unknown'}`,
    `Managed containers: ${inventory?.containers ?? 'unknown'}`,
    `Managed proxy listeners: ${inventory?.proxyListeners ?? 'unknown'}`,
    `Runtime: ${runtimeDescription(runtime)}`,
    `Runtime preference: ${runtime?.preference ?? 'unknown'}`,
    `Runtime candidates:${runtimeCandidates}`,
    `Runtime handoff: ${handoff?.state ?? 'unchecked'}`,
    `Handoff verified: ${handoff?.verifiedAt ?? 'not yet'}`,
    `Active environments:${environments}`,
    `Handoff problems:${handoffProblems}`,
    `Recovery result: ${diagnostics?.recovery.result ?? 'unknown'}`,
    `Recovery completed: ${diagnostics?.recovery.completedAt ?? 'not yet'}`,
    `Recovery duration: ${diagnostics ? formatDuration(diagnostics.recovery.durationMs) : 'unknown'}`,
    `Recovery problems:${recoveryProblems}`,
    `HTTP ingress: ${relay?.httpHealthy ? 'ready' : 'not ready'} (127.0.0.1:80)`,
    `Endpoint DNS: ${relay?.dnsHealthy ? 'ready' : 'not ready'} (${relay?.dnsListenAddress || '127.77.0.1:1053'})`,
    `DNS resolver: ${relay?.resolverHealthy ? 'ready' : 'not ready'} (localhost, portless.test)`,
    `TCP endpoint pool: ${relay?.endpointPoolReady ? 'ready' : 'not ready'}${relay?.endpointPoolDetail ? ` (${relay.endpointPoolDetail})` : ''}`,
    `Relay service: ${relay?.service || 'not installed'} (${relay?.platform || 'unknown platform'}, ${relay?.running ? 'running' : 'stopped'})`,
    `Relay helper: ${helper.value} (${helper.detail})`,
    `State database: ${storage ? formatBytes(storage.databaseBytes) : 'not inspected'}`,
    `Recordings: ${storage ? `${storage.recordingCount} sessions, ${storage.recordedEventCount} events, ${formatBytes(storage.recordedBytes)}` : 'not inspected'}`,
    `Live traffic: ${storage ? `${storage.liveTrafficExchanges} exchanges, ${formatBytes(storage.liveTrafficBytes)}` : 'not inspected'}`,
    `Service logs: ${storage ? formatBytes(storage.serviceLogBytes) : 'not inspected'}`,
    `Daemon log: ${storage ? formatBytes(storage.daemonLogBytes) : 'not inspected'}`,
  ].join('\n')
}

export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** index
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`
}

function formatDuration(milliseconds: number) {
  if (milliseconds < 1000) return `${Math.max(0, milliseconds)} ms`
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 1 : 0)} s`
  return `${Math.floor(milliseconds / 60_000)}m ${Math.round((milliseconds % 60_000) / 1000)}s`
}

function eventHealthLabel(health: ControlPlaneHealth['events']) {
  if (health.state === 'idle') return 'Idle'
  if (health.state === 'reconnecting') return 'Reconnecting'
  return health.connections === 1 ? 'Connected' : `${health.connected} connected`
}

function eventHealthDetail(health: ControlPlaneHealth['events']) {
  if (health.state === 'idle') return health.lastConnectedAt ? `No stream required · last connected ${relativeTime(health.lastConnectedAt)} ago` : 'No stream required on this page'
  if (health.state === 'reconnecting') return `${health.connected}/${health.connections} streams connected`
  return `${health.connections} active stream${health.connections === 1 ? '' : 's'}`
}

function displayDistribution(value: string) {
  if (!value) return 'Unknown'
  return value === 'homebrew' ? 'Homebrew' : value[0].toUpperCase() + value.slice(1)
}

function displayState(value: string) {
  return value.split('-').map((part) => part ? part[0].toUpperCase() + part.slice(1) : part).join(' ')
}

function recoveryState(value?: DaemonDiagnostics['recovery']['result']) {
  if (value === 'healthy') return 'ready'
  if (value === 'degraded') return 'degraded'
  if (value === 'failed') return 'failed'
  return 'unknown'
}

function daemonTabAlert(tab: DaemonDrawerTab, live: boolean, diagnostics: DaemonDiagnostics | null, health: ControlPlaneHealth, runtime: RuntimeStatus | null, relay: RelayStatus | null, handoffPhase: HandoffPhase) {
  if (tab === 'status') return !live || health.api.state !== 'ready' || diagnostics?.recovery.result === 'degraded' || diagnostics?.recovery.result === 'failed' || diagnostics?.build.current === false
  if (tab === 'runtime') return runtime?.state === 'failed' || runtime?.state === 'missing' || relay?.healthy === false || relay?.helperCurrent === false || handoffPhase === 'blocked' || handoffPhase === 'failed'
  if (tab === 'storage') return Boolean(diagnostics?.storage?.problems.length)
  return !live
}

function handoffDisplayState(active: string[], phase: HandoffPhase) {
  if (active.length === 0 && phase !== 'checking') return 'not required'
  if (phase === 'checking') return 'checking'
  if (phase === 'ready') return 'ready'
  if (phase === 'blocked') return 'failed'
  return 'unknown'
}

function handoffDescription(active: string[], phase: HandoffPhase) {
  if (phase === 'checking') return 'Verifying supervisors, containers, and proxy listeners…'
  if (active.length === 0) return 'No active environments need to be handed off.'
  if (phase === 'ready') return 'Managed services can be adopted by a replacement daemon without stopping the environment.'
  if (phase === 'blocked') return 'The daemon cannot restart safely while it manages active environments.'
  if (phase === 'failed') return 'Portless could not verify runtime handoff safety.'
  return 'Runtime handoff has not been verified.'
}

function errorMessage(value: unknown) {
  if (value instanceof APIError || value instanceof Error) return value.message
  return 'The daemon request failed.'
}

function wait(milliseconds: number) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds))
}

async function settleBeforeDeadline<T>(operation: Promise<T>, deadline: number): Promise<T | null> {
  const remaining = deadline - Date.now()
  if (remaining <= 0) return null
  let timer = 0
  try {
    return await Promise.race([
      operation,
      new Promise<null>((resolve) => { timer = window.setTimeout(() => resolve(null), remaining) }),
    ])
  } finally {
    window.clearTimeout(timer)
  }
}
