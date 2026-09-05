import { useEffect, useState } from 'react'
import { api, environmentPath } from '../../../api'
import { ActionErrorNotice } from '../../../components/ActionError'
import { ServiceLogs } from '../../../components/logs/ServiceLogs'
import { DrawerShell } from '../../../components/overlays/DrawerShell'
import { relativeTime, StatusMark } from '../../../components/Status'
import type { Environment } from '../../../api/contracts/environments'
import type { Service, ServiceConfiguration } from '../../../api/contracts/topology'
import { environmentUIPath } from '../navigation'
import { serviceActionProgressLabel, useServiceActions } from './serviceActions'
import { bindingFor, displayLaunchMode, mockScenarioFor, publicEndpoint, serviceEndpoints } from './servicePresentation'

export function ServiceDrawer({ environment, service, onClose, onChanged, onNavigate }: {
  environment: Environment
  service: Service
  onClose: () => void
  onChanged: () => void
  onNavigate: (path: string) => void
}) {
  const [configuration, setConfiguration] = useState<ServiceConfiguration | null>(null)
  const [drawerTab, setDrawerTab] = useState<'details' | 'logs' | 'configuration'>('details')
  const serviceActions = useServiceActions(environment, onChanged)
  const busy = serviceActions.busy?.service === service.name ? serviceActions.busy.action : ''
  const base = environmentPath(environment, `/services/${encodeURIComponent(service.name)}`)

  useEffect(() => {
    api<ServiceConfiguration>(`${base}/configuration`).then(setConfiguration).catch(() => setConfiguration(null))
  }, [base, environment.name, service.name])

  const endpoints = serviceEndpoints(service, bindingFor(environment, service.name))
  const httpEndpoint = publicEndpoint(service, 'http')
  const mockScenario = mockScenarioFor(environment, service.name)
  const mockScenarioPath = mockScenario ? environmentUIPath(environment, 'mocks', { scenario: mockScenario }) : ''
  const localProcess = service.kind === 'process' && bindingFor(environment, service.name)?.provider === 'local'
  return <DrawerShell
    label={`${service.name} service`}
    subject={`${service.name} service`}
    className="service-drawer"
    header={<div><span className="eyebrow">{environment.project} / {environment.name} / service</span><h2>{service.name}</h2><StatusMark status={service.status} /></div>}
    actions={<>{localProcess && service.debug && <button className="button button--primary" type="button" onClick={() => void serviceActions.run(service, service.launchMode === 'debug' ? 'manage' : 'debug')} disabled={!!busy}>{busy === 'debug' || busy === 'manage' ? serviceActionProgressLabel(busy) : service.launchMode === 'debug' ? 'RUN NORMALLY' : 'DEBUG'}</button>}<button className={`button${!localProcess || !service.debug ? ' button--primary' : ''}`} type="button" onClick={() => void serviceActions.run(service, service.status === 'ready' ? 'restart' : 'start')} disabled={!!busy}>{busy === 'restart' || busy === 'start' ? serviceActionProgressLabel(busy) : service.status === 'ready' ? 'RESTART' : 'START'}</button><button className="button" type="button" onClick={() => void serviceActions.run(service, 'stop')} disabled={!!busy || service.status === 'stopped'}>{busy === 'stop' ? serviceActionProgressLabel(busy) : 'STOP'}</button>{httpEndpoint && <a className="button" href={httpEndpoint.url} target="_blank" rel="noreferrer">OPEN ↗</a>}</>}
    actionProps={{ 'aria-busy': !!busy }}
    notice={serviceActions.error && <ActionErrorNotice error={serviceActions.error} onDismiss={serviceActions.dismissError} />}
    tabs={<nav className="drawer-tabs service-drawer-tabs">{(['details', 'logs', 'configuration'] as const).map((name) => <button type="button" key={name} className={drawerTab === name ? 'is-active' : ''} onClick={() => setDrawerTab(name)}>{name}</button>)}</nav>}
    contentClassName={`service-drawer-content service-drawer-content--${drawerTab}`}
    onClose={onClose}
  >
    {drawerTab === 'details' && <>
      <section className="drawer-section service-identity"><div className="eyebrow">SERVICE IDENTITY</div><div className="detail-grid service-detail-grid"><Detail label="KIND" value={service.framework || service.resource?.type || service.kind} /><Detail label="MODE" value={displayLaunchMode(environment, service)} /><Detail label="GENERATION" value={String(service.generation || '—')} /><Detail label="PID" value={String(service.pid || '—')} /><Detail label="UPSTREAM" value={service.upstreamPort ? `127.0.0.1:${service.upstreamPort}` : '—'} wide /><Detail label="RESTARTS" value={String(service.restartCount)} /><Detail label="STARTED" value={service.startedAt ? `${relativeTime(service.startedAt)} ago` : '—'} /></div></section>
      {service.debugger && <section className="drawer-section service-debugger"><div className="eyebrow">DEBUGGER</div><pre>{service.debugger.adapter} · {service.debugger.host}:{service.debugger.port}</pre><small>{service.debugger.state}. Use your IDE's Attach to Process action and choose the matching Node or JVM process.</small></section>}
      <section className="drawer-section service-endpoints"><div className="eyebrow">ENDPOINTS</div>{mockScenario && <div className="service-mock-binding"><span>Mock scenario: <strong title={mockScenario}>{mockScenario}</strong></span><a href={mockScenarioPath} onClick={(event) => {
        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
        event.preventDefault()
        onNavigate(mockScenarioPath)
      }}>VIEW SCENARIO →</a></div>}<div className="service-endpoint-list">{endpoints.map((endpoint) => <div className="service-endpoint" key={`${endpoint.label}:${endpoint.value}`}><span>{endpoint.label}</span>{endpoint.href ? <a href={endpoint.href} target="_blank" rel="noreferrer">{endpoint.value} ↗</a> : <code>{endpoint.value}</code>}<small>{endpoint.detail}</small></div>)}{endpoints.length === 0 && <p className="muted">No endpoint is available while this service is stopped.</p>}</div></section>
      <section className="drawer-section service-command"><div className="eyebrow">COMMAND</div><pre>{service.command?.join(' ') || `managed ${service.resource?.type} ${service.resource?.version}`}</pre></section>
      <section className="drawer-section service-health"><div className="eyebrow">HEALTH</div><div className="service-health-summary"><div><StatusMark status={service.status} label={false} /><strong>{service.health.kind}{service.health.path ? ` ${service.health.path}` : ''}</strong></div><small>{service.reason || 'No current readiness error.'}</small></div></section>
    </>}
    {drawerTab === 'logs' && <ServiceLogs environment={environment} service={service.name} />}
    {drawerTab === 'configuration' && <section className="drawer-section service-configuration"><div className="eyebrow">ENVIRONMENT CONFIGURATION</div><div className="config-table"><div className="config-row config-row--head"><span>KEY</span><span>EFFECTIVE VALUE</span><span>SOURCE</span></div>{configuration?.environment?.map((item) => <div className="config-row" key={item.key}><code>{item.key}</code><span className={item.classification === 'masked' ? 'masked-value' : ''}>{item.value}</span><small>{item.source} · {item.classification}</small></div>)}{!configuration?.environment.length && <div className="empty-row">No static environment values were discovered. Connection bindings are generated at runtime.</div>}</div></section>}
  </DrawerShell>
}

function Detail({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return <div className={wide ? 'service-detail--wide' : undefined}><span>{label}</span><strong>{value}</strong></div>
}
