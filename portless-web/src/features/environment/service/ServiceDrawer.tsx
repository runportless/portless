import { useEffect, useRef, useState } from 'react'
import { api, environmentPath } from '../../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { ServiceLogs } from '../../../components/logs/ServiceLogs'
import { DrawerShell } from '../../../components/overlays/DrawerShell'
import { relativeTime, StatusMark } from '../../../components/Status'
import type { Environment, Operation, Service } from '../../../types'
import { waitForEnvironmentOperation } from '../operationPolling'
import { bindingFor, displayLaunchMode, publicEndpoint, serviceEndpoints } from './servicePresentation'

type ServiceAction = 'restart' | 'stop' | 'start' | 'debug' | 'manage'

export function ServiceDrawer({ environment, service, onClose, onChanged }: {
  environment: Environment
  service: Service
  onClose: () => void
  onChanged: () => void
}) {
  const [configuration, setConfiguration] = useState<{ environment?: Array<{ key: string; value: string; classification: string; source: string }> } | null>(null)
  const [drawerTab, setDrawerTab] = useState<'details' | 'logs' | 'configuration'>('details')
  const [busy, setBusy] = useState<ServiceAction | ''>('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const actionInFlight = useRef(false)
  const base = environmentPath(environment, `/services/${encodeURIComponent(service.name)}`)

  useEffect(() => {
    api<typeof configuration>(`${base}/configuration`).then(setConfiguration).catch(() => setConfiguration(null))
  }, [base, environment.name, service.name])

  const action = async (name: ServiceAction) => {
    if (actionInFlight.current) return
    actionInFlight.current = true
    setBusy(name)
    setError(null)
    try {
      const operation = await api<Operation>(`${base}/${name}`, { method: 'POST' })
      onChanged()
      const completed = await waitForEnvironmentOperation(environment, operation)
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
  return <DrawerShell
    label={`${service.name} service`}
    subject={`${service.name} service`}
    header={<div><span className="eyebrow">{environment.project} / {environment.name} / service</span><h2>{service.name}</h2><StatusMark status={service.status} /></div>}
    actions={<>{localProcess && service.debug && <button className="button button--primary" type="button" onClick={() => void action(service.launchMode === 'debug' ? 'manage' : 'debug')} disabled={!!busy}>{busy === 'debug' || busy === 'manage' ? serviceActionProgressLabel(busy) : service.launchMode === 'debug' ? 'RUN NORMALLY' : 'DEBUG'}</button>}<button className={`button${!localProcess || !service.debug ? ' button--primary' : ''}`} type="button" onClick={() => void action(service.status === 'ready' ? 'restart' : 'start')} disabled={!!busy}>{busy === 'restart' || busy === 'start' ? serviceActionProgressLabel(busy) : service.status === 'ready' ? 'RESTART' : 'START'}</button><button className="button" type="button" onClick={() => void action('stop')} disabled={!!busy || service.status === 'stopped'}>{busy === 'stop' ? serviceActionProgressLabel(busy) : 'STOP'}</button>{httpEndpoint && <a className="button" href={httpEndpoint.url} target="_blank" rel="noreferrer">OPEN ↗</a>}</>}
    actionProps={{ 'aria-busy': !!busy }}
    notice={error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    tabs={<nav className="drawer-tabs">{(['details', 'logs', 'configuration'] as const).map((name) => <button key={name} className={drawerTab === name ? 'is-active' : ''} onClick={() => setDrawerTab(name)}>{name}</button>)}</nav>}
    onClose={onClose}
  >
    {drawerTab === 'details' && <>
      <div className="detail-grid"><Detail label="KIND" value={service.framework || service.resource?.type || service.kind} /><Detail label="MODE" value={displayLaunchMode(environment, service)} /><Detail label="GENERATION" value={String(service.generation || '—')} /><Detail label="PID" value={String(service.pid || '—')} /><Detail label="UPSTREAM" value={service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : '—'} /><Detail label="RESTARTS" value={String(service.restartCount)} /><Detail label="STARTED" value={service.startedAt ? `${relativeTime(service.startedAt)} ago` : '—'} /></div>
      {service.debugger && <section className="drawer-section"><div className="eyebrow">DEBUGGER</div><pre>{service.debugger.adapter} · {service.debugger.host}:{service.debugger.port}</pre><small>{service.debugger.state}. Use your IDE's Attach to Process action and choose the matching Node or JVM process.</small></section>}
      <section className="drawer-section service-endpoints"><div className="eyebrow">ENDPOINTS</div><div className="service-endpoint-list">{endpoints.map((endpoint) => <div className="service-endpoint" key={`${endpoint.label}:${endpoint.value}`}><span>{endpoint.label}</span>{endpoint.href ? <a href={endpoint.href} target="_blank" rel="noreferrer">{endpoint.value} ↗</a> : <code>{endpoint.value}</code>}<small>{endpoint.detail}</small></div>)}{endpoints.length === 0 && <p className="muted">No endpoint is available while this service is stopped.</p>}</div></section>
      <section className="drawer-section"><div className="eyebrow">COMMAND</div><pre>{service.command?.join(' ') || `managed ${service.resource?.type} ${service.resource?.version}`}</pre></section>
      <section className="drawer-section"><div className="eyebrow">HEALTH</div><p><StatusMark status={service.status} /> {service.health.kind}{service.health.path ? ` ${service.health.path}` : ''}</p><small>{service.reason || 'No current readiness error.'}</small></section>
    </>}
    {drawerTab === 'logs' && <ServiceLogs environment={environment} service={service.name} />}
    {drawerTab === 'configuration' && <div className="config-table"><div className="config-row config-row--head"><span>KEY</span><span>EFFECTIVE VALUE</span><span>SOURCE</span></div>{configuration?.environment?.map((item) => <div className="config-row" key={item.key}><code>{item.key}</code><span className={item.classification === 'masked' ? 'masked-value' : ''}>{item.value}</span><small>{item.source} · {item.classification}</small></div>)}{!configuration?.environment?.length && <div className="empty-row">No static environment values were discovered. Connection bindings are generated at runtime.</div>}</div>}
  </DrawerShell>
}

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

function Detail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>
}
