import { useEffect, useRef, useState } from 'react'
import { APIError } from '../api'
import type { DaemonRestart, DaemonStatus, RelayStatus, RuntimeStatus } from '../types'
import { relativeTime, StatusMark } from './Status'

type RestartPhase = 'idle' | 'confirm' | 'restarting' | 'reconnected' | 'failed'

export function DaemonDrawer({ status, runtime, relay, live, onClose, onRefresh, onRestart, onReconnected }: {
  status: DaemonStatus | null
  runtime: RuntimeStatus | null
  relay: RelayStatus | null
  live: boolean
  onClose: () => void
  onRefresh: () => Promise<DaemonStatus>
  onRestart: (instanceId: string) => Promise<DaemonRestart>
  onReconnected: () => Promise<void>
}) {
  const [phase, setPhase] = useState<RestartPhase>('idle')
  const [error, setError] = useState('')
  const [copyState, setCopyState] = useState('COPY DIAGNOSTICS')
  const [fullScreen, setFullScreen] = useState(false)
  const mounted = useRef(true)
  const active = status?.activeEnvironments ?? []
  const restartSafe = active.length === 0 || status?.handoffReady === true
  const restarting = phase === 'restarting'
  const effectiveState = restarting ? 'restarting' : phase === 'reconnected' ? 'ready' : live ? status?.state ?? 'unknown' : 'unreachable'

  useEffect(() => () => { mounted.current = false }, [])
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
      await navigator.clipboard.writeText(daemonDiagnostics(status, runtime, relay))
      setCopyState('COPIED')
      window.setTimeout(() => mounted.current && setCopyState('COPY DIAGNOSTICS'), 1600)
    } catch {
      setCopyState('COPY FAILED')
    }
  }

  return <div className="drawer-backdrop" role="presentation" onMouseDown={onClose}>
    <aside className={`drawer daemon-drawer ${fullScreen ? 'drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label="Portless Daemon" onMouseDown={(event) => event.stopPropagation()}>
      <header><div className="daemon-drawer-heading"><div><h2>Portless Daemon</h2><StatusMark status={effectiveState} /></div></div><div className="drawer-header-actions"><button className="drawer-size-button" type="button" aria-pressed={fullScreen} onClick={() => setFullScreen((value) => !value)}>{fullScreen ? 'RESTORE' : 'FULL SCREEN'}</button><button className="icon-button" onClick={onClose} aria-label="Close">×</button></div></header>
      <div className="drawer-actions">
        <button className="button button--warning" onClick={() => setPhase('confirm')} disabled={!status || !live || !restartSafe || restarting}>RESTART DAEMON</button>
        <button className="button" onClick={() => void copyDiagnostics()} disabled={!status}>{copyState}</button>
      </div>
      <div className="drawer-content">
        {status ? <>
          <div className="detail-grid daemon-detail-grid">
            <Detail label="PID" value={String(status.pid)} />
            <Detail label="STARTED" value={`${relativeTime(status.startedAt)} ago`} />
            <Detail label="BUILD" value={shortFingerprint(status.buildId)} title={status.buildId} />
            <Detail label="PROTOCOL" value={status.protocolVersion} />
            <Detail label="API" value={status.apiVersion} />
            <Detail label="RUNTIME" value={runtimeDescription(runtime)} />
          </div>
          <section className={`drawer-section daemon-handoff ${restartSafe ? '' : 'daemon-handoff--blocked'}`}>
            <div className="daemon-section-heading"><span className="eyebrow">RUNTIME HANDOFF</span><StatusMark status={active.length === 0 ? 'not required' : status.handoffReady ? 'ready' : 'failed'} /></div>
            <p>{active.length === 0 ? 'No active environments need to be handed off.' : status.handoffReady ? 'Managed services can be adopted by a replacement daemon without stopping the environment.' : 'The daemon cannot restart safely while it manages active environments.'}</p>
            {active.length > 0 && <div className="daemon-environments">{active.map((environment) => <div key={environment}><StatusMark status={status.handoffReady ? 'active' : 'unknown'} label={false} /><code>{environment}</code><small>ACTIVE</small></div>)}</div>}
            {status.recoveryProblems.length > 0 && <ul className="daemon-problems">{status.recoveryProblems.map((problem) => <li key={problem}>{problem}</li>)}</ul>}
          </section>
          <section className={`drawer-section daemon-network ${relay?.healthy ? '' : 'daemon-network--degraded'}`}>
            <div className="daemon-section-heading"><span className="eyebrow">LOCAL NETWORKING</span><StatusMark status={relay?.healthy ? 'ready' : relay?.installed ? 'degraded' : 'missing'} /></div>
            <div className="daemon-network-grid">
              <NetworkDetail label="HTTP INGRESS" value="127.0.0.1:80" healthy={relay?.httpHealthy === true} />
              <NetworkDetail label="TCP DNS" value={relay?.dnsListenAddress || '127.77.0.1:1053'} healthy={relay?.dnsHealthy === true} />
              <NetworkDetail label="DNS ZONE" value="portless.test" healthy={relay?.resolverHealthy === true} />
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

export function daemonDiagnostics(status: DaemonStatus, runtime: RuntimeStatus | null, relay: RelayStatus | null = null) {
  const environments = status.activeEnvironments.length ? `\n${status.activeEnvironments.map((value) => `  ${value}`).join('\n')}` : ' none'
  const problems = status.recoveryProblems.length ? `\n${status.recoveryProblems.map((value) => `  ${value}`).join('\n')}` : ' none'
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
    `Runtime handoff: ${status.handoffReady ? 'ready' : 'not ready'}`,
    `Active environments:${environments}`,
    `Recovery problems:${problems}`,
    `HTTP ingress: ${relay?.httpHealthy ? 'ready' : 'not ready'} (127.0.0.1:80)`,
    `TCP DNS: ${relay?.dnsHealthy ? 'ready' : 'not ready'} (${relay?.dnsListenAddress || '127.77.0.1:1053'})`,
    `DNS resolver: ${relay?.resolverHealthy ? 'ready' : 'not ready'} (portless.test)`,
    `TCP endpoint pool: ${relay?.endpointPoolReady ? 'ready' : 'not ready'}${relay?.endpointPoolDetail ? ` (${relay.endpointPoolDetail})` : ''}`,
  ].join('\n')
}

function errorMessage(value: unknown) {
  if (value instanceof APIError || value instanceof Error) return value.message
  return 'The daemon restart failed.'
}

function wait(milliseconds: number) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds))
}
