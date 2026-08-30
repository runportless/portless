import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react'
import { ActionErrorNotice } from '../../components/ActionError'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { SortableGridHeader, type TableSort } from '../../components/SortableTableHeader'
import { StatePanel, StatusMark } from '../../components/Status'
import type { Environment, TimelineEvent } from '../../api/contracts/environments'
import type { FaultRule, Recording } from '../../api/contracts/experiments'
import type { Service } from '../../api/contracts/topology'
import type { EnvironmentNavigationOptions, EnvironmentTab } from './navigation'
import { serviceActionOptions, useServiceActions } from './service/serviceActions'
import { displayLaunchMode, openableServiceURL, overviewServiceEndpoint } from './service/servicePresentation'
import { TopologyPreview } from './topology/TopologyPanel'

const overviewPageSize = 8
type OverviewServiceSortField = 'name' | 'mode' | 'state' | 'restarts' | 'requests' | 'p95' | 'endpoint'
const defaultOverviewServiceSort: TableSort<OverviewServiceSortField> = { key: 'name', direction: 'asc' }

export function OverviewPanel({ environment, timeline, ready, faults, activeRecording, trafficCount, onService, onNavigate, onChanged }: {
  environment: Environment
  timeline: TimelineEvent[]
  ready: number
  faults: FaultRule[]
  activeRecording?: Recording
  trafficCount: number
  onService: (service: Service) => void
  onNavigate: (tab: EnvironmentTab, options?: EnvironmentNavigationOptions) => void
  onChanged: () => void
}) {
  const [servicePage, setServicePage] = useState(0)
  const [serviceSort, setServiceSort] = useState<TableSort<OverviewServiceSortField>>(defaultOverviewServiceSort)
  const [activityPage, setActivityPage] = useState(0)
  const [copiedEndpoint, setCopiedEndpoint] = useState('')
  const [menuService, setMenuService] = useState('')
  const copyReset = useRef<number | undefined>(undefined)
  const serviceMenu = useRef<HTMLDivElement>(null)
  const serviceActions = useServiceActions(environment, onChanged)
  const orderedServices = useMemo(() => sortOverviewServices(environment.services, environment, serviceSort), [environment, serviceSort])
  const services = paginateItems(orderedServices, servicePage, overviewPageSize)
  const activities = paginateItems(timeline, activityPage, overviewPageSize)
  const bindingSummary = summarizeEnvironmentBindings(environment)

  useEffect(() => {
    setServicePage(0)
    setServiceSort(defaultOverviewServiceSort)
    setActivityPage(0)
    setMenuService('')
  }, [environment.project, environment.name])
  useEffect(() => () => window.clearTimeout(copyReset.current), [])
  useEffect(() => {
    if (!menuService) return
    const dismissOutside = (event: MouseEvent) => {
      if (!serviceMenu.current?.contains(event.target as Node)) setMenuService('')
    }
    const dismissWithEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      const trigger = serviceMenu.current?.querySelector<HTMLButtonElement>('.service-row__menu-trigger')
      setMenuService('')
      window.requestAnimationFrame(() => trigger?.focus())
    }
    document.addEventListener('mousedown', dismissOutside)
    window.addEventListener('keydown', dismissWithEscape)
    return () => {
      document.removeEventListener('mousedown', dismissOutside)
      window.removeEventListener('keydown', dismissWithEscape)
    }
  }, [menuService])

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
      {serviceActions.error && <ActionErrorNotice error={serviceActions.error} onDismiss={serviceActions.dismissError} />}
      <div className={`table-row table-row--header service-row sortable-header-row${serviceSort.key === defaultOverviewServiceSort.key && serviceSort.direction === defaultOverviewServiceSort.direction ? ' is-default-sort' : ''}`} role="row">
        <span aria-hidden="true" />
        <SortableGridHeader label="Name" sortKey="name" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <SortableGridHeader label="Mode" sortKey="mode" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <SortableGridHeader label="State" sortKey="state" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <SortableGridHeader label="Restarts" sortKey="restarts" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <SortableGridHeader label="Requests" sortKey="requests" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <SortableGridHeader label="P95" sortKey="p95" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <SortableGridHeader label="Endpoint / reason" sortKey="endpoint" sort={serviceSort} itemCount={environment.services.length} onSort={(sort) => { setServiceSort(sort); setServicePage(0); setMenuService('') }} />
        <span aria-label="Actions" />
      </div>
      {services.items.map((service) => {
        const endpoint = overviewServiceEndpoint(environment, service)
        const openURL = openableServiceURL(environment, service)
        const actions = serviceActionOptions(environment, service)
        const copied = copiedEndpoint === service.name
        const menuOpen = menuService === service.name
        const hasMenu = !!openURL || actions.length > 0
        return <div className="table-row service-row service-row--interactive" key={service.name} onClick={() => onService(service)}>
          <StatusMark status={service.status} label={false} /><button className="service-row__details" type="button" aria-label={`View ${service.name} details`} onClick={(event) => { event.stopPropagation(); onService(service) }}><strong>{service.name}</strong></button><span>{displayLaunchMode(environment, service)}</span><StatusMark status={service.status} /><span className={service.restartCount ? 'warning-text' : ''}>{service.restartCount}</span><span>{service.recentRequests || '—'}</span><span>{service.p95Millis ? `${service.p95Millis}ms` : '—'}</span><span className="service-list-endpoint"><span className="truncate muted" title={service.reason || endpoint || 'not running'}>{service.reason || endpoint || 'not running'}</span>{!service.reason && endpoint && <button className={`service-copy-button${copied ? ' is-copied' : ''}`} type="button" aria-label={`Copy ${service.name} endpoint`} title={copied ? 'Copied' : 'Copy endpoint'} onClick={(event) => void copyServiceEndpoint(event, service.name, endpoint)}><CopyIcon copied={copied} /></button>}</span><div ref={menuOpen ? serviceMenu : undefined} className="service-row__actions">
            {hasMenu && <button className="service-row__menu-trigger" type="button" aria-label={`Service actions for ${service.name}`} aria-haspopup="menu" aria-expanded={menuOpen} disabled={!!serviceActions.busy} onClick={(event) => { event.stopPropagation(); setMenuService(menuOpen ? '' : service.name) }}>•••</button>}
            {menuOpen && <div className="service-row__menu" role="menu" aria-label={`${service.name} actions`} onClick={(event) => event.stopPropagation()}>
              {openURL && <a href={openURL} target="_blank" rel="noreferrer" role="menuitem" onClick={() => setMenuService('')}>OPEN ↗</a>}
              {actions.map((item) => <button className={item.danger ? 'is-danger' : undefined} type="button" role="menuitem" key={item.action} onClick={() => { setMenuService(''); void serviceActions.run(service, item.action) }}>{item.label}</button>)}
            </div>}
          </div>
        </div>
      })}
      <PanelPagination label="services" pagination={services} onPage={(page) => { setServicePage(page); setMenuService('') }} />
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

export function sortOverviewServices(services: Service[], environment: Pick<Environment, 'bindings'>, sort: TableSort<OverviewServiceSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...services].sort((left, right) => {
    const nameOrder = compareOverviewServiceText(left.name, right.name)
    let order = 0

    switch (sort.key) {
      case 'name':
        order = nameOrder
        break
      case 'mode':
        order = compareOverviewServiceText(displayLaunchMode(environment, left), displayLaunchMode(environment, right))
        break
      case 'state':
        order = compareOverviewServiceText(left.status, right.status)
        break
      case 'restarts':
        order = left.restartCount - right.restartCount
        break
      case 'requests':
        order = left.recentRequests - right.recentRequests
        break
      case 'p95':
        order = (left.p95Millis || 0) - (right.p95Millis || 0)
        break
      case 'endpoint':
        order = compareOverviewServiceText(left.reason || overviewServiceEndpoint(environment, left) || '', right.reason || overviewServiceEndpoint(environment, right) || '')
        break
    }

    return direction * order || nameOrder
  })
}

function compareOverviewServiceText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
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
