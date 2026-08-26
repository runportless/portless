import { useEffect, useRef, useState } from 'react'
import { APIError } from '../api'
import type { DaemonHandoffStatus, DaemonRestart, DaemonStatus, RelayStatus, RuntimeStatus } from '../types'
import { DaemonLogs } from './DaemonLogs'
import { DrawerSizeButton } from './DrawerSizeButton'
import { relativeTime, StatusMark } from './Status'

type RestartPhase = 'idle' | 'confirm' | 'restarting' | 'reconnected' | 'failed'
type HandoffPhase = 'idle' | 'checking' | 'ready' | 'blocked' | 'failed'
type DaemonDrawerTab = 'overview' | 'logs'

export function DaemonDrawer({ status, runtime, relay, live, onClose, onRefresh, onVerifyHandoff, onRestart, onReconnected }: {
  status: DaemonStatus | null
  runtime: RuntimeStatus | null
  relay: RelayStatus | null
  live: boolean
  onClose: () => void
  onRefresh: () => Promise<DaemonStatus>
  onVerifyHandoff: () => Promise<DaemonHandoffStatus>
  onRestart: (instanceId: string) => Promise<DaemonRestart>
  onReconnected: () => Promise<void>
}) {
  const [phase, setPhase] = useState<RestartPhase>('idle')
  const [error, setError] = useState('')
  const [copyState, setCopyState] = useState('COPY DIAGNOSTICS')
  const [fullScreen, setFullScreen] = useState(false)
  const [tab, setTab] = useState<DaemonDrawerTab>('overview')
  const [handoff, setHandoff] = useState<DaemonHandoffStatus | null>(null)
  const [handoffPhase, setHandoffPhase] = useState<HandoffPhase>('idle')
  const [handoffError, setHandoffError] = useState('')
  const mounted = useRef(true)
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
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (fullScreen) setFullScreen(false)
      else onClose()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [fullScreen, onClose])

  const restartDaemon = async () => {
    if (!status || !restartSafe || restarting) return
    const previousInstance = status.instanceId
    setError('')
    setPhase('restarting')
    try {
      await onRestart(previousInstance)
      const deadline = Date.now() + 30_000
      while (Date.now() < deadline && mounted.current) {
        await wait(400)
        try {
          const replacement = await onRefresh()
          if (replacement.instanceId !== previousInstance && replacement.state === 'ready') {
            if (!mounted.current) return
            setPhase('reconnected')
            await onReconnected()
            return
          }
        } catch {
          // A refused connection is expected between shutdown and replacement readiness.
        }
      }
      throw new Error('The replacement daemon did not become ready within 30 seconds.')
    } catch (value) {
      if (!mounted.current) return
      setError(errorMessage(value))
      setPhase('failed')
    }
  }

  const copyDiagnostics = async () => {
    if (!status || !navigator.clipboard) return
    try {
      await navigator.clipboard.writeText(daemonDiagnostics(status, runtime, relay, handoff))
      setCopyState('COPIED')
      window.setTimeout(() => mounted.current && setCopyState('COPY DIAGNOSTICS'), 1600)
    } catch {
      setCopyState('COPY FAILED')
    }
  }

  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className={`drawer daemon-drawer ${fullScreen ? 'drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label="Portless Daemon" onMouseDown={(event) => event.stopPropagation()}>
      <header><div className="daemon-drawer-heading"><div><h2>Portless Daemon</h2><StatusMark status={effectiveState} /></div></div><div className="drawer-header-actions"><DrawerSizeButton fullScreen={fullScreen} subject="Portless Daemon" onToggle={() => setFullScreen((value) => !value)} /><button className="icon-button" onClick={onClose} aria-label="Close">×</button></div></header>
      <div className="drawer-actions">
        <button className="button button--warning" onClick={() => { setTab('overview'); setPhase('confirm') }} disabled={!status || !live || !restartSafe || restarting || (active.length > 0 && handoffPhase === 'checking')}>RESTART DAEMON</button>
        <button className="button" onClick={() => void copyDiagnostics()} disabled={!status}>{copyState}</button>
      </div>
      <div className="drawer-tabs daemon-drawer-tabs" role="tablist" aria-label="Daemon details">
        <button className={tab === 'overview' ? 'is-active' : ''} type="button" role="tab" aria-selected={tab === 'overview'} onClick={() => setTab('overview')}>OVERVIEW</button>
        <button className={tab === 'logs' ? 'is-active' : ''} type="button" role="tab" aria-selected={tab === 'logs'} onClick={() => setTab('logs')}>LOGS</button>
      </div>
      <div className="drawer-content">
        {tab === 'overview' ? <>
        {status ? <>
          <div className="detail-grid daemon-detail-grid">
            <Detail label="PID" value={String(status.pid)} />
            <Detail label="STARTED" value={`${relativeTime(status.startedAt)} ago`} />
            <Detail label="BUILD" value={shortFingerprint(status.buildId)} title={status.buildId} />
            <Detail label="PROTOCOL" value={status.protocolVersion} />
            <Detail label="API" value={status.apiVersion} />
            <Detail label="RUNTIME" value={runtimeDescription(runtime)} />
          </div>
          <section className={`drawer-section daemon-handoff ${handoffBlocked ? 'daemon-handoff--blocked' : ''}`}>
            <div className="daemon-section-heading"><span className="eyebrow">RUNTIME HANDOFF</span><StatusMark status={handoffDisplayState(active, handoffPhase)} /></div>
            <p>{handoffDescription(active, handoffPhase)}</p>
            {handoff?.verifiedAt && <small>Verified {relativeTime(handoff.verifiedAt)} ago</small>}
            {active.length > 0 && <div className="daemon-environments">{active.map((environment) => <div key={environment}><StatusMark status={handoffReady ? 'active' : 'unknown'} label={false} /><code>{environment}</code><small>ACTIVE</small></div>)}</div>}
            {handoffError && <ul className="daemon-problems"><li>{handoffError}</li></ul>}
            {handoff?.problems.length ? <ul className="daemon-problems">{handoff.problems.map((problem) => <li key={problem}>{problem}</li>)}</ul> : null}
            {status.recoveryProblems.length > 0 && <ul className="daemon-problems">{status.recoveryProblems.map((problem) => <li key={problem}>{problem}</li>)}</ul>}
          </section>
          <section className={`drawer-section daemon-network ${relay?.healthy ? '' : 'daemon-network--degraded'}`}>
            <div className="daemon-section-heading"><span className="eyebrow">LOCAL NETWORKING</span><StatusMark status={relay?.healthy ? 'ready' : relay?.installed ? 'degraded' : 'missing'} /></div>
            <div className="daemon-network-grid">
              <NetworkDetail label="HTTP INGRESS" value="127.0.0.1:80" healthy={relay?.httpHealthy === true} />
              <NetworkDetail label="ENDPOINT DNS" value={relay?.dnsListenAddress || '127.77.0.1:1053'} healthy={relay?.dnsHealthy === true} />
              <NetworkDetail label="DNS ZONES" value="localhost · portless.test" healthy={relay?.resolverHealthy === true} />
              <NetworkDetail label="TCP ADDRESS POOL" value={relay?.endpointPoolDetail || 'not provisioned'} healthy={relay?.endpointPoolReady === true} />
              <NetworkDetail label="DAEMON SOCKETS" value={relay?.targetSocket && relay?.dnsTargetSocket ? 'connected' : 'unavailable'} healthy={Boolean(relay?.targetSocket && relay?.dnsTargetSocket)} />
              <NetworkDetail label="SYSTEM SERVICE" value={relay?.service || 'not installed'} healthy={relay?.running === true} />
            </div>
            {!relay?.healthy && <p>{relay?.problem || relay?.resolverHealthError || relay?.dnsHealthError || relay?.healthError || 'Run portless doctor relay to inspect clean HTTP URLs and TCP endpoint DNS.'}</p>}
          </section>
        </> : <section className="drawer-section daemon-empty"><span className="eyebrow">DAEMON STATUS UNAVAILABLE</span><p>Portless could not load daemon identity information.</p></section>}

        {phase === 'confirm' && <section className="daemon-confirm" role="alertdialog" aria-label="Confirm daemon restart">
          <h3>Restart the Portless daemon?</h3>
          <ul><li>Services and containers keep running.</li><li>The control plane reconnects automatically.</li><li>Clean URL routing and traffic capture may pause briefly.</li></ul>
          <div><button className="button button--warning" onClick={() => void restartDaemon()}>RESTART AND RECONNECT</button><button className="button" onClick={() => setPhase('idle')}>CANCEL</button></div>
        </section>}

        {(phase === 'restarting' || phase === 'reconnected') && <section className={`daemon-progress ${phase === 'reconnected' ? 'daemon-progress--complete' : ''}`} aria-live="polite">
          <i aria-hidden="true" /><div><strong>{phase === 'reconnected' ? 'Daemon restarted' : 'Restarting daemon…'}</strong><small>{phase === 'reconnected' ? 'Connected to the replacement instance.' : 'Waiting for the replacement instance.'}</small></div>
        </section>}

        {(phase === 'failed' || (!live && phase !== 'restarting')) && <section className="daemon-restart-error" role="alert">
          <span className="eyebrow">{phase === 'failed' ? 'RESTART FAILED' : 'DAEMON UNREACHABLE'}</span>
          <p>{error || 'The UI is waiting for the local daemon to become reachable.'}</p>
          <pre><span>$</span> portless doctor</pre>
        </section>}
        </> : live ? <DaemonLogs instanceId={status?.instanceId} /> : <section className="daemon-restart-error daemon-log-unavailable" role="alert"><span className="eyebrow">DAEMON LOGS UNAVAILABLE</span><p>The UI is waiting for the local daemon to become reachable.</p><pre><span>$</span> portless doctor</pre></section>}
      </div>
    </aside>
  </div>
}

function Detail({ label, value, title }: { label: string; value: string; title?: string }) {
  return <div><span>{label}</span><strong title={title}>{value}</strong></div>
}

function NetworkDetail({ label, value, healthy }: { label: string; value: string; healthy: boolean }) {
  return <div><StatusMark status={healthy ? 'ready' : 'failed'} label={false} /><span>{label}</span><code title={value}>{value}</code></div>
}

function shortFingerprint(value: string) {
  return value.length > 12 ? value.slice(0, 12) : value
}

function runtimeDescription(runtime: RuntimeStatus | null) {
  if (!runtime?.selected) return runtime?.state ?? 'unknown'
  return `${runtime.selected} ${runtime.version ?? ''}`.trim()
}

export function daemonDiagnostics(status: DaemonStatus, runtime: RuntimeStatus | null, relay: RelayStatus | null = null, handoff: DaemonHandoffStatus | null = null) {
  const environments = status.activeEnvironments.length ? `\n${status.activeEnvironments.map((value) => `  ${value}`).join('\n')}` : ' none'
  const recoveryProblems = status.recoveryProblems.length ? `\n${status.recoveryProblems.map((value) => `  ${value}`).join('\n')}` : ' none'
  const handoffProblems = handoff?.problems.length ? `\n${handoff.problems.map((value) => `  ${value}`).join('\n')}` : ' none'
  return [
    'Portless daemon',
    `State: ${status.state}`,
    `PID: ${status.pid}`,
    `Started: ${status.startedAt}`,
    `Instance: ${status.instanceId}`,
    `Build: ${status.buildId}`,
    `Protocol Version: ${status.protocolVersion}`,
    `API Version: ${status.apiVersion}`,
    `Runtime: ${runtimeDescription(runtime)}`,
    `Runtime handoff: ${handoff?.state ?? 'unchecked'}`,
    `Handoff verified: ${handoff?.verifiedAt ?? 'not yet'}`,
    `Active environments:${environments}`,
    `Handoff problems:${handoffProblems}`,
    `Recovery problems:${recoveryProblems}`,
    `HTTP ingress: ${relay?.httpHealthy ? 'ready' : 'not ready'} (127.0.0.1:80)`,
    `Endpoint DNS: ${relay?.dnsHealthy ? 'ready' : 'not ready'} (${relay?.dnsListenAddress || '127.77.0.1:1053'})`,
    `DNS resolver: ${relay?.resolverHealthy ? 'ready' : 'not ready'} (localhost, portless.test)`,
    `TCP endpoint pool: ${relay?.endpointPoolReady ? 'ready' : 'not ready'}${relay?.endpointPoolDetail ? ` (${relay.endpointPoolDetail})` : ''}`,
  ].join('\n')
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
  return 'The daemon restart failed.'
}

function wait(milliseconds: number) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds))
}
