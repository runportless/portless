import { useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath, jsonBody } from '../../api'
import type { Environment, Operation } from '../../api/contracts/environments'
import type { MockRoute, MockScenario, MockScenarioList } from '../../api/contracts/mocks'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { FormDialog } from '../../components/overlays/FormDialog'
import { paginateItems, PanelPagination } from '../../components/PanelPagination'
import { RowActionsMenu } from '../../components/RowActionsMenu'
import { SortableGridHeader, type TableSort } from '../../components/SortableTableHeader'
import { StatusMark } from '../../components/Status'
import { waitForEnvironmentOperation } from '../environment/operationPolling'
import { httpStatusGroups } from '../httpStatuses'
import { MockRouteEditor, mockRouteDraft, mockRouteDraftHasChanges, newMockRouteDraft, type MockRouteDraft } from './MockRouteEditor'

type MockScenarioSortField = 'state' | 'name' | 'services' | 'routes' | 'modifiedAt'
type MockRouteSortField = 'service' | 'route' | 'match' | 'response' | 'delay' | 'state'

const defaultMockScenarioSort: TableSort<MockScenarioSortField> = { key: 'state', direction: 'asc' }
const defaultMockRouteSort: TableSort<MockRouteSortField> = { key: 'service', direction: 'asc' }
const mockRoutePageSize = 10

export const mockHTTPStatusGroups = httpStatusGroups

export function MocksPanel({ environment, selectedScenario, creatingRoute, selectedRoute, onSelectRoute, onCreateRoute, onSelectScenario, onChanged }: {
  environment: Environment
  selectedScenario?: string
  creatingRoute?: boolean
  selectedRoute?: string
  onSelectRoute: (scenario: string, route?: string) => void
  onCreateRoute: (scenario: string) => void
  onSelectScenario: (scenario?: string) => void
  onChanged: () => void | Promise<void>
}) {
  const [scenarios, setScenarios] = useState<MockScenario[]>([])
  const [loading, setLoading] = useState(true)
  const [createOpen, setCreateOpen] = useState(false)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [deleteName, setDeleteName] = useState('')
  const [busy, setBusy] = useState('')
  const selected = selectedScenario ? scenarios.find((scenario) => scenario.name === selectedScenario) : undefined
  const transitionBlocked = ['starting', 'stopping', 'recovering', 'unknown'].includes(environment.status)

  const refresh = async () => {
    const result = await api<MockScenarioList>(environmentPath(environment, '/mocks'))
    setScenarios(result.scenarios)
  }

  useEffect(() => {
    setLoading(true)
    setCreateOpen(false)
    setError(null)
    refresh().catch((reason) => setError(actionError("Mocks couldn't be loaded", reason))).finally(() => setLoading(false))
    return connectEvents(environment, ['mock.state'], () => { refresh().catch(() => undefined) })
  }, [environment.project, environment.name]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    setDeleteName('')
    if (selectedScenario) setCreateOpen(false)
  }, [selectedScenario])

  const createScenario = async (input: { name: string; description?: string }) => {
    setBusy('create-scenario'); setError(null)
    try {
      const created = await api<MockScenario>(environmentPath(environment, '/mocks'), { method: 'POST', ...jsonBody(input) })
      setScenarios((current) => [...current.filter((scenario) => scenario.name.toLowerCase() !== created.name.toLowerCase()), created])
      setCreateOpen(false)
      onSelectScenario(created.name)
    } catch (reason) { setError(actionError("Mock scenario wasn't created", reason)) }
    finally { setBusy('') }
  }

  const removeScenario = async (scenario: MockScenario) => {
    if (deleteName !== scenario.name) { setDeleteName(scenario.name); return }
    setBusy(`delete-scenario:${scenario.name}`); setError(null)
    try {
      await api(environmentPath(environment, `/mocks/${encodeURIComponent(scenario.name)}`), { method: 'DELETE' })
      setDeleteName('')
      await refresh()
    } catch (reason) { setError(actionError("Mock scenario wasn't deleted", reason)) }
    finally { setBusy('') }
  }

  const setScenarioEnabled = async (scenario: MockScenario, enabled: boolean) => {
    if (enabled && scenario.routes.length === 0) return
    const action = `${enabled ? 'enable' : 'disable'}:${scenario.name}`
    setBusy(action); setDeleteName(''); setError(null)
    try {
      const operation = await api<Operation>(environmentPath(environment, `/mocks/${encodeURIComponent(scenario.name)}/activation`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
        body: JSON.stringify({ enabled }),
      })
      const completed = await waitForEnvironmentOperation(environment, operation)
      if (completed.state !== 'succeeded') throw new Error(completed.error || `Scenario transition ${completed.state}`)
      await Promise.all([refresh(), Promise.resolve(onChanged())])
    } catch (reason) { setError(actionError(`Mock scenario wasn't ${enabled ? 'enabled' : 'disabled'}`, reason)) }
    finally { setBusy('') }
  }

  const saveRoute = async (draft: MockRouteDraft, originalName?: string) => {
    if (!selected || busy) return false
    setBusy('route'); setError(null)
    try {
      const routeName = draft.name.trim()
      if (originalName && originalName.toLowerCase() !== routeName.toLowerCase()) {
        throw new Error('Route names cannot be changed in place. Create a new route, then delete the old route.')
      }
      const candidateDrafts = selected.routes.filter((route) => route.name !== originalName).map(mockRouteDraft)
      candidateDrafts.push(draft)
      const route = mockRoutesFromDrafts(candidateDrafts).find((item) => item.name.toLowerCase() === routeName.toLowerCase())
      if (!route) throw new Error('The route could not be validated.')
      const updated = await api<MockScenario>(environmentPath(environment, `/mocks/${encodeURIComponent(selected.name)}/routes/${encodeURIComponent(routeName)}`), {
        method: 'PUT', ...jsonBody(route),
      })
      setScenarios((current) => current.map((scenario) => scenario.name === updated.name ? updated : scenario))
      return true
    } catch (reason) { setError(actionError("Mock route wasn't saved", reason)); return false }
    finally { setBusy('') }
  }

  const removeRoute = async (route: MockRoute) => {
    if (!selected || busy) return false
    const key = `delete-route:${route.name}`
    if (deleteName !== key) { setDeleteName(key); return false }
    setBusy(key); setError(null)
    try {
      const updated = await api<MockScenario>(environmentPath(environment, `/mocks/${encodeURIComponent(selected.name)}/routes/${encodeURIComponent(route.name)}`), { method: 'DELETE' })
      setScenarios((current) => current.map((scenario) => scenario.name === updated.name ? updated : scenario))
      setDeleteName('')
      return true
    } catch (reason) { setError(actionError("Mock route wasn't deleted", reason)); return false }
    finally { setBusy('') }
  }

  const setRouteEnabled = async (route: MockRoute, enabled: boolean) => {
    if (!selected || busy || route.enabled === enabled) return false
    const action = `toggle-route:${route.name}`
    setBusy(action); setDeleteName(''); setError(null)
    try {
      if (!selected.routes.some((item) => item.name === route.name)) throw new Error(`Route ${route.name} is no longer part of this scenario.`)
      const updatedRoute = { ...route, enabled }
      mockRoutesFromDrafts(selected.routes.map((item) => mockRouteDraft(item.name === route.name ? updatedRoute : item)))
      const updated = await api<MockScenario>(environmentPath(environment, `/mocks/${encodeURIComponent(selected.name)}/routes/${encodeURIComponent(route.name)}`), {
        method: 'PUT', ...jsonBody(updatedRoute),
      })
      setScenarios((current) => current.map((scenario) => scenario.name === updated.name ? updated : scenario))
      return true
    } catch (reason) { setError(actionError(`Mock route wasn't ${enabled ? 'enabled' : 'disabled'}`, reason)); return false }
    finally { setBusy('') }
  }

  const processServices = environment.services.filter((service) => service.kind === 'process' && !environment.connections.some((connection) => connection.target.toLowerCase() === service.name.toLowerCase() && connection.protocol !== 'http')).map((service) => service.name)
  const routeEditorServices = selected && mockScenarioIsActive(selected)
    ? processServices.filter((service) => selected.activation.targetServices.some((target) => target.toLowerCase() === service.toLowerCase()))
    : processServices

  if (selectedScenario) return <div className="mocks-page mocks-page--workspace">
    {loading ? <section className="panel mock-workspace-loading">Loading mock scenario…</section> : selected ? <MockScenarioWorkspace
      key={`${environment.project}/${environment.name}/${selected.name}`}
      environment={environment}
      scenario={selected}
      services={routeEditorServices}
      selectedRoute={selectedRoute}
      creatingRoute={creatingRoute}
      busy={busy}
      deleteName={deleteName}
      transitionBlocked={transitionBlocked}
      error={error}
      onDismissError={() => setError(null)}
      onBack={() => { setDeleteName(''); setError(null); onSelectScenario() }}
      onToggle={(enabled) => { void setScenarioEnabled(selected, enabled) }}
      onAddRoute={() => onCreateRoute(selected.name)}
      onSelectRoute={(route) => onSelectRoute(selected.name, route)}
      onSaveRoute={saveRoute}
      onToggleRoute={setRouteEnabled}
      onDeleteRoute={removeRoute}
      onDismissDelete={() => setDeleteName('')}
    /> : <MockWorkspaceMissing error={error} subject="MOCK SCENARIO" onDismissError={() => setError(null)} onBack={() => onSelectScenario()} />}
  </div>

  return <div className="mocks-page">
    {error && !createOpen && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <MockScenariosList
      scenarios={scenarios}
      loading={loading}
      busy={busy}
      deleteName={deleteName}
      transitionBlocked={transitionBlocked}
      onCreate={() => { setDeleteName(''); setError(null); setCreateOpen(true) }}
      onOpen={(scenario) => { setDeleteName(''); setError(null); onSelectScenario(scenario.name) }}
      onToggle={(scenario, enabled) => { void setScenarioEnabled(scenario, enabled) }}
      onDelete={(scenario) => { void removeScenario(scenario) }}
      onDismissDelete={() => setDeleteName('')}
    />
    {createOpen && <MockScenarioCreateDialog
      busy={busy === 'create-scenario'}
      error={error}
      onDismissError={() => setError(null)}
      onClose={() => { if (!busy) { setCreateOpen(false); setError(null) } }}
      onCreate={createScenario}
    />}
  </div>
}

function MockWorkspaceMissing({ error, subject, onDismissError, onBack }: {
  error: ActionErrorDetails | null
  subject: string
  onDismissError: () => void
  onBack: () => void
}) {
  return <section className="panel mock-workspace-missing">
    {error ? <ActionErrorNotice error={error} onDismiss={onDismissError} /> : <><strong>{subject} NOT FOUND</strong><p>The selected item is no longer available.</p></>}
    <button className="button" type="button" onClick={onBack}>BACK TO MOCK SCENARIOS</button>
  </section>
}

export function MockScenarioCreateDialog({ busy, error, onDismissError, onClose, onCreate }: {
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onCreate: (input: { name: string; description?: string }) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const nameInput = useRef<HTMLInputElement>(null)
  const ready = !!name.trim()

  return <FormDialog
    className="mock-scenario-create-dialog"
    titleID="mock-scenario-create-title"
    descriptionID="mock-scenario-create-description"
    closeLabel="Close mock scenario creation"
    closeBlocked={busy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">MOCKS / NEW SCENARIO</div><h2 id="mock-scenario-create-title">Create Mock Scenario</h2></div>}
    onClose={onClose}
  >
    <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => {
      event.preventDefault()
      if (!ready) return
      void onCreate({ name: name.trim(), ...(description.trim() ? { description: description.trim() } : {}) })
    }}>
      <p id="mock-scenario-create-description">Create the scenario, then add one or more service routes.</p>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} aria-label="NAME" name="portless-mock-scenario-name" required pattern="[a-z0-9][a-z0-9._-]{0,63}" maxLength={64} autoComplete="off" spellCheck="false" value={name} disabled={busy} title="Use a lowercase URL-safe name." data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => { setName(event.target.value); onDismissError() }} /></label>
        <label className="provider-field--wide"><span>DESCRIPTION <small>OPTIONAL</small></span><input aria-label="DESCRIPTION" value={description} disabled={busy} onChange={(event) => { setDescription(event.target.value); onDismissError() }} /></label>
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !ready}>{busy ? 'CREATING…' : 'CREATE SCENARIO'}</button></footer>
    </form>
  </FormDialog>
}

export function MockScenariosList({ scenarios, loading, busy, deleteName, transitionBlocked, onCreate, onOpen, onToggle, onDelete, onDismissDelete }: {
  scenarios: MockScenario[]
  loading: boolean
  busy: string
  deleteName: string
  transitionBlocked: boolean
  onCreate: () => void
  onOpen: (scenario: MockScenario) => void
  onToggle: (scenario: MockScenario, enabled: boolean) => void
  onDelete: (scenario: MockScenario) => void
  onDismissDelete: () => void
}) {
  const [scenarioSort, setScenarioSort] = useState<TableSort<MockScenarioSortField>>(defaultMockScenarioSort)
  const [scenarioMenu, setScenarioMenu] = useState('')
  const orderedScenarios = useMemo(() => sortMockScenarios(scenarios, scenarioSort), [scenarios, scenarioSort])

  const changeScenarioSort = (sort: TableSort<MockScenarioSortField>) => {
    setScenarioSort(sort)
    setScenarioMenu('')
    onDismissDelete()
  }

  return <section className="panel mock-scenarios-panel">
    <div className="panel-title"><span>SCENARIOS</span><button className="button button--primary button--small panel-create-button" type="button" disabled={!!busy} onClick={onCreate}>CREATE SCENARIO</button></div>
    <div className={`mock-scenario-row mock-scenario-row--header sortable-header-row${scenarioSort.key === defaultMockScenarioSort.key && scenarioSort.direction === defaultMockScenarioSort.direction ? ' is-default-sort' : ''}`} role="row">
      <SortableGridHeader label="State" sortKey="state" sort={scenarioSort} itemCount={scenarios.length} onSort={changeScenarioSort} />
      <SortableGridHeader label="Scenario" sortKey="name" sort={scenarioSort} itemCount={scenarios.length} onSort={changeScenarioSort} />
      <SortableGridHeader label="Services" sortKey="services" sort={scenarioSort} itemCount={scenarios.length} onSort={changeScenarioSort} />
      <SortableGridHeader label="Routes" sortKey="routes" sort={scenarioSort} itemCount={scenarios.length} onSort={changeScenarioSort} />
      <SortableGridHeader label="Modified" sortKey="modifiedAt" sort={scenarioSort} itemCount={scenarios.length} onSort={changeScenarioSort} />
      <span aria-label="Actions" />
    </div>
    {orderedScenarios.map((scenario) => {
      const active = mockScenarioIsActive(scenario)
      const toggleAction = `${active ? 'disable' : 'enable'}:${scenario.name}`
      const menuOpen = scenarioMenu === scenario.name
      const enableBlocked = !active && scenario.routes.length === 0
      const services = scenario.activation.targetServices
      return <div className="mock-scenario-row" key={scenario.name} onClick={() => { if (!busy) onOpen(scenario) }}>
        <MockEnabledState state={scenario.activation.state} />
        <div className="mock-scenario-row__name"><button type="button" disabled={!!busy} aria-label={`Open ${scenario.name} mock scenario`} onClick={(event) => { event.stopPropagation(); onOpen(scenario) }}><strong>{scenario.name}</strong></button></div>
        <span className="mock-scenario-services" title={services.join(', ')}>{services.length ? services.join(', ') : '—'}</span>
        <span>{scenario.routes.length}</span>
        <MockTimestamp className="mock-scenario-row__modified" value={scenario.modifiedAt} />
        <div className="mock-row-actions table-row-actions">
          <button type="button" disabled={!!busy || transitionBlocked || enableBlocked} title={enableBlocked ? 'Add a route before enabling this scenario.' : undefined} aria-label={`${active ? 'Disable' : 'Enable'} ${scenario.name}`} onClick={(event) => { event.stopPropagation(); onToggle(scenario, !active) }}>{busy === toggleAction ? active ? 'DISABLING…' : 'ENABLING…' : active ? 'DISABLE' : 'ENABLE'}</button>
          <RowActionsMenu
            label={`Mock scenario actions for ${scenario.name}`}
            menuLabel={`${scenario.name} mock scenario actions`}
            open={menuOpen}
            disabled={!!busy}
            onOpenChange={(open) => {
              setScenarioMenu(open ? scenario.name : '')
              if (!open || scenarioMenu !== scenario.name) onDismissDelete()
            }}
          >
            <button className={`is-danger${deleteName === scenario.name ? ' is-confirming' : ''}`} type="button" role="menuitem" disabled={!!busy || active} title={active ? 'Disable this scenario before deleting it.' : undefined} aria-label={deleteName === scenario.name ? `Confirm delete ${scenario.name}` : `Delete ${scenario.name}`} onClick={() => onDelete(scenario)}>{busy === `delete-scenario:${scenario.name}` ? 'DELETING…' : deleteName === scenario.name ? 'CONFIRM' : 'DELETE'}</button>
          </RowActionsMenu>
        </div>
      </div>
    })}
    {!loading && scenarios.length === 0 && <div className="empty-row">No mock scenarios. Create one, then add routes for the services it should replace.</div>}
    {loading && <div className="empty-row">Loading mock scenarios…</div>}
  </section>
}

export function MockScenarioWorkspace({ scenario, services, selectedRoute, creatingRoute = false, busy, deleteName, transitionBlocked, error, onDismissError, onBack, onToggle, onAddRoute, onSelectRoute, onSaveRoute, onToggleRoute, onDeleteRoute, onDismissDelete }: {
  environment: Environment
  scenario: MockScenario
  services: string[]
  selectedRoute?: string
  creatingRoute?: boolean
  busy: string
  deleteName: string
  transitionBlocked: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onBack: () => void
  onToggle: (enabled: boolean) => void
  onAddRoute: () => void
  onSelectRoute: (route?: string) => void
  onSaveRoute: (route: MockRouteDraft, originalName?: string) => Promise<boolean>
  onToggleRoute: (route: MockRoute, enabled: boolean) => Promise<boolean>
  onDeleteRoute: (route: MockRoute) => Promise<boolean>
  onDismissDelete: () => void
}) {
  const [routeMenu, setRouteMenu] = useState('')
  const [routePage, setRoutePage] = useState(0)
  const [routeSort, setRouteSort] = useState<TableSort<MockRouteSortField>>(defaultMockRouteSort)
  const [drafts, setDrafts] = useState<Record<string, MockRouteDraft>>({})
  const [leaveOpen, setLeaveOpen] = useState(false)
  const active = mockScenarioIsActive(scenario)
  const enableBlocked = !active && scenario.routes.length === 0
  const toggleBusy = busy === `${active ? 'disable' : 'enable'}:${scenario.name}`
  const toggleDisabled = !!busy || transitionBlocked || enableBlocked
  const orderedRoutes = useMemo(() => sortMockRoutes(scenario.routes, routeSort), [scenario.routes, routeSort])
  const routePagination = useMemo(() => paginateItems(orderedRoutes, routePage, mockRoutePageSize), [orderedRoutes, routePage])
  const defaultRoute = useMemo(() => sortMockRoutes(scenario.routes, defaultMockRouteSort)[0]?.name, [scenario.routes])
  const routeName = creatingRoute ? undefined : selectedRoute || defaultRoute
  const existing = scenario.routes.find((route) => route.name === routeName)
  const draftKey = creatingRoute ? 'new' : routeName ? `route:${routeName}` : undefined
  const draft = draftKey && drafts[draftKey] || (existing ? mockRouteDraft(existing) : newMockRouteDraft(scenario.routes.length + 1, services[0] || ''))
  const dirty = !!draftKey && !!drafts[draftKey]
  const hasDrafts = Object.keys(drafts).length > 0
  const selectedPage = Math.max(0, Math.floor(orderedRoutes.findIndex((route) => route.name === routeName) / mockRoutePageSize))

  useEffect(() => {
    setRouteMenu('')
    setRoutePage(selectedPage)
  }, [routeName, creatingRoute, selectedPage])

  useEffect(() => {
    setRoutePage((current) => Math.min(current, Math.max(0, Math.ceil(scenario.routes.length / mockRoutePageSize) - 1)))
  }, [scenario.routes.length])

  useEffect(() => {
    if (!hasDrafts) return
    const warnOnUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', warnOnUnload)
    return () => window.removeEventListener('beforeunload', warnOnUnload)
  }, [hasDrafts])

  const clearDraft = (key: string) => setDrafts((current) => {
    const next = { ...current }
    delete next[key]
    return next
  })
  const changeDraft = (next: MockRouteDraft) => {
    if (!draftKey) return
    onDismissError()
    if (existing && !mockRouteDraftHasChanges(next, existing)) clearDraft(draftKey)
    else setDrafts((current) => ({ ...current, [draftKey]: next }))
  }
  const selectRoute = (name?: string) => {
    if (busy) return
    setRouteMenu('')
    onDismissDelete()
    onDismissError()
    if (creatingRoute || name !== selectedRoute) onSelectRoute(name)
  }
  const saveDraft = async (next: MockRouteDraft, originalName?: string) => {
    if (!draftKey || !await onSaveRoute(next, originalName)) return
    clearDraft(draftKey)
    if (creatingRoute || selectedRoute !== next.name.trim()) onSelectRoute(next.name.trim())
  }
  const discardDraft = () => {
    if (busy || !draftKey) return
    clearDraft(draftKey)
    onDismissError()
    if (creatingRoute || !existing) onSelectRoute()
  }
  const toggleRoute = async (route: MockRoute, enabled: boolean) => {
    if (!await onToggleRoute(route, enabled)) return
    setDrafts((current) => {
      const key = `route:${route.name}`
      if (!current[key]) return current
      const updated = { ...current[key], enabled }
      const next = { ...current }
      if (mockRouteDraftHasChanges(updated, { ...route, enabled })) next[key] = updated
      else delete next[key]
      return next
    })
  }
  const deleteRoute = async (route: MockRoute) => {
    if (!await onDeleteRoute(route)) return
    clearDraft(`route:${route.name}`)
    setRouteMenu('')
    if (route.name === routeName) onSelectRoute()
  }
  const changeRouteSort = (sort: TableSort<MockRouteSortField>) => {
    setRouteSort(sort)
    setRoutePage(0)
    setRouteMenu('')
    onDismissDelete()
  }

  return <section className="panel mock-scenario-workspace" role="region" aria-label={`${scenario.name} mock scenario`}>
    <header className="mock-scenario-header">
      <button className="mock-workspace-back" type="button" disabled={!!busy} aria-label="Back to mock scenarios" onClick={() => { if (hasDrafts) setLeaveOpen(true); else onBack() }}>
          <svg aria-hidden="true" viewBox="0 0 16 16"><path d="M7 3 2 8l5 5" /><path d="M2 8h12" /></svg>
          <span>BACK</span>
      </button>
      <div className="mock-scenario-heading">
        {scenario.activation.targetServices.length > 0 && <div className="eyebrow mock-scenario-service-eyebrow">SERVICES / {scenario.activation.targetServices.join(' · ')}</div>}
        <h2 id="mock-scenario-workspace-title">{scenario.name}</h2>
        {scenario.description && <p title={scenario.description}>{scenario.description}</p>}
      </div>
        <label className={`mock-scenario-toggle${active ? ' is-active' : ''}${toggleDisabled ? ' is-disabled' : ''}`} title={enableBlocked ? 'Add a route before enabling this scenario.' : undefined}>
          <span className="mock-scenario-toggle__label">{toggleBusy ? active ? 'DISABLING…' : 'ENABLING…' : scenario.activation.state.toUpperCase()}</span>
          <input className="sr-only" type="checkbox" role="switch" checked={active} disabled={toggleDisabled} aria-label={`${scenario.name} enabled`} onChange={(event) => onToggle(event.target.checked)} />
          <span className="mock-scenario-toggle__track" aria-hidden="true"><span /></span>
        </label>
    </header>
    {scenario.activation.state === 'degraded' && <ActionErrorNotice error={{ title: 'Scenario is partially active', message: `${scenario.activation.activeServices.length} of ${scenario.activation.targetServices.length} services currently use this scenario. Disable it to restore the saved providers.` }} />}
    {error && !draftKey && <div className="mock-workspace-error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}
    <div className="mock-scenario-split">
      <section className="mock-route-browser" aria-label={`${scenario.name} routes`}>
        <div className="mock-route-browser__title">
          <span>ROUTES</span>
          <button className="button button--primary button--small" type="button" disabled={!!busy || creatingRoute} onClick={() => { onDismissError(); onDismissDelete(); onAddRoute() }}>ADD ROUTE</button>
        </div>
        {scenario.routes.length > 0 && <div className="mock-route-sort">
          <label><span>SORT BY</span><select aria-label="Sort routes by" value={routeSort.key} onChange={(event) => changeRouteSort({ key: event.target.value as MockRouteSortField, direction: 'asc' })}>
            <option value="service">Service</option><option value="route">Route</option><option value="match">Match</option><option value="response">Response</option><option value="delay">Delay</option><option value="state">State</option>
          </select></label>
          <button type="button" aria-label={`Sort routes ${routeSort.direction === 'asc' ? 'descending' : 'ascending'}`} onClick={() => changeRouteSort({ ...routeSort, direction: routeSort.direction === 'asc' ? 'desc' : 'asc' })}>{routeSort.direction === 'asc' ? '↑' : '↓'}</button>
        </div>}
        {(creatingRoute || drafts.new) && <button className={`mock-route-new${creatingRoute ? ' is-selected' : ''}`} type="button" disabled={!!busy} aria-current={creatingRoute ? 'true' : undefined} onClick={() => { if (!creatingRoute) onAddRoute() }}>NEW ROUTE <span>{drafts.new ? 'UNSAVED' : 'DRAFT'}</span></button>}
        <div className="mock-route-browser__scroll">
          <div className="mock-route-list" role="list" aria-label="Routes">
            {routePagination.items.map((route) => <div className={`mock-route-item${route.name === routeName ? ' is-selected' : ''}`} role="listitem" key={route.name} onClick={() => selectRoute(route.name)}>
              <button className="mock-route-select" type="button" disabled={!!busy} aria-label={`Edit ${route.name} route`} aria-current={route.name === routeName ? 'true' : undefined} onClick={(event) => { event.stopPropagation(); selectRoute(route.name) }}>
                <span className="mock-route-select__name"><strong>{route.name}</strong>{drafts[`route:${route.name}`] && <small>UNSAVED</small>}</span>
                <code title={`${route.method} ${route.path}${formatQuerySummary(route.query)}`}>{route.method} {route.path}{formatQuerySummary(route.query)}</code>
                <span className="mock-route-select__meta"><span>{route.service}</span><span>{route.status}{route.delayMs ? ` · ${route.delayMs} ms` : ''}</span></span>
              </button>
              <div className="mock-route-item__actions table-row-actions">
                <RowActionsMenu label={`Route actions for ${route.name}`} menuLabel={`${route.name} route actions`} open={routeMenu === route.name} disabled={!!busy} onOpenChange={(open) => { setRouteMenu(open ? route.name : ''); if (!open || routeMenu !== route.name) onDismissDelete() }}>
                  <button type="button" role="menuitem" disabled={!!busy} onClick={() => selectRoute(route.name)}>EDIT</button>
                  <button className={`is-danger${deleteName === `delete-route:${route.name}` ? ' is-confirming' : ''}`} type="button" role="menuitem" disabled={!!busy} aria-label={deleteName === `delete-route:${route.name}` ? `Confirm delete ${route.name}` : `Delete ${route.name}`} onClick={() => { void deleteRoute(route) }}>{busy === `delete-route:${route.name}` ? 'DELETING…' : deleteName === `delete-route:${route.name}` ? 'CONFIRM' : 'DELETE'}</button>
                </RowActionsMenu>
                <MockRouteEnabledToggle route={route} busy={busy === `toggle-route:${route.name}`} disabled={!!busy} onToggle={(enabled) => { void toggleRoute(route, enabled) }} />
              </div>
            </div>)}
          </div>
          {scenario.routes.length === 0 && <div className="empty-row">No routes. Add one to define a request and response for a service.</div>}
        </div>
        <PanelPagination label="routes" pagination={routePagination} onPage={(page) => { setRoutePage(page); setRouteMenu(''); onDismissDelete() }} />
      </section>
      {draftKey ? <MockRouteEditor key={draftKey} scenario={scenario} services={services} routeName={routeName} draft={draft} dirty={dirty} busy={!!busy} error={error} onDismissError={onDismissError} onChange={changeDraft} onCancel={discardDraft} onSave={saveDraft} /> : <div className="mock-route-editor-empty"><span>Add a route to configure its request and response.</span></div>}
    </div>
    {leaveOpen && <FormDialog className="mock-scenario-leave-dialog" titleID="mock-scenario-leave-title" descriptionID="mock-scenario-leave-description" closeLabel="Keep editing routes" onClose={() => setLeaveOpen(false)} header={<h2 id="mock-scenario-leave-title">Discard unsaved changes?</h2>}>
      <p id="mock-scenario-leave-description">Your route drafts will be discarded when you leave this scenario.</p>
      <footer><button className="button button--quiet" type="button" onClick={() => setLeaveOpen(false)}>KEEP EDITING</button><button className="button button--primary" type="button" onClick={onBack}>DISCARD AND LEAVE</button></footer>
    </FormDialog>}
  </section>
}

function MockRouteEnabledToggle({ route, busy, disabled, onToggle }: { route: MockRoute; busy: boolean; disabled: boolean; onToggle: (enabled: boolean) => void }) {
  return <label className={`mock-route-toggle${route.enabled ? ' is-active' : ''}${disabled ? ' is-disabled' : ''}`} title={`${route.name}: ${route.enabled ? 'enabled' : 'disabled'}`} onClick={(event) => event.stopPropagation()}>
    <input className="sr-only" type="checkbox" role="switch" checked={route.enabled} disabled={disabled} aria-label={`${route.name} route enabled`} onChange={(event) => onToggle(event.target.checked)} />
    <span className="mock-route-toggle__track" aria-hidden="true"><span /></span>
    <span className="sr-only">{busy ? route.enabled ? 'DISABLING…' : 'ENABLING…' : route.enabled ? 'ENABLED' : 'DISABLED'}</span>
  </label>
}

function MockEnabledState({ state }: { state: MockScenario['activation']['state'] }) {
  const label = state[0].toUpperCase() + state.slice(1)
  return <div className={`mock-enabled-state${state === 'enabled' ? ' is-enabled' : ''}${state === 'degraded' ? ' is-degraded' : ''}`}>
    <StatusMark status={state === 'disabled' ? 'disabled' : state === 'enabled' ? 'enabled' : 'degraded'} label={false} />
    <span>{label}</span>
  </div>
}

function MockTimestamp({ className, value }: { className: string; value?: string }) {
  const classes = `mock-scenario-row__timestamp ${className}`
  if (!value) return <span className={classes}>—</span>
  return <time className={classes} dateTime={value} title={new Date(value).toLocaleString()}>{formatTimestamp(value)}</time>
}

export function mockRoutesFromDrafts(drafts: MockRouteDraft[]): MockRoute[] {
  if (drafts.length === 0) throw new Error('Add at least one route.')
  if (drafts.length > 1000) throw new Error('A mock scenario cannot contain more than 1000 routes.')
  const names = new Set<string>()
  const registeredStatuses = new Set<number>(httpStatusGroups.flatMap((group) => group.statuses.map(([code]) => code)))
  const encoder = new TextEncoder()
  let totalBodyBytes = 0
  const routes = drafts.map((draft) => {
    const name = draft.name.trim()
    const service = draft.service.trim()
    if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(name)) throw new Error(`Route ${name || '(unnamed)'} must use a lowercase URL-safe name.`)
    if (names.has(name.toLowerCase())) throw new Error(`Route name ${name} is duplicated.`)
    names.add(name.toLowerCase())
    if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(service)) throw new Error(`Route ${name} must target a valid service.`)
    if (!/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(draft.method)) throw new Error(`Route ${name} method must be an HTTP token.`)
    if (!draft.path.startsWith('/') || /[?#]/.test(draft.path)) throw new Error(`Route ${name} path must be an absolute URL path without a query or fragment.`)
    const parameters = new Set<string>()
    for (const segment of draft.path.split('/')) {
      if (!/[{}]/.test(segment)) continue
      if (!/^\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(segment)) throw new Error(`Route ${name} path parameters must use a whole segment such as {id}.`)
      if (parameters.has(segment)) throw new Error(`Route ${name} path parameter ${segment} is duplicated.`)
      parameters.add(segment)
    }
    const delayMs = Number(draft.delayMs)
    if (!Number.isInteger(delayMs) || delayMs < 0 || delayMs > 300_000) throw new Error(`Route ${name} delay must be between 0 and 300000 ms.`)
    if (!registeredStatuses.has(Number(draft.status))) throw new Error(`Route ${name} status must be a registered final HTTP response status.`)
    const bodyBytes = encoder.encode(draft.body).length
    if (bodyBytes > 1_048_576) throw new Error(`Route ${name} response body exceeds 1048576 bytes.`)
    totalBodyBytes += bodyBytes
    if (totalBodyBytes > 8_388_608) throw new Error('Mock response bodies exceed 8388608 bytes.')
    return {
      name, service, method: draft.method, path: draft.path, status: Number(draft.status),
      query: parseMockPairs(draft.queryText, '='), headers: parseMockResponseHeaderPairs(draft.headersText),
      body: draft.body, delayMs, enabled: draft.enabled,
    }
  })
  for (let left = 0; left < routes.length; left++) {
    for (let right = left + 1; right < routes.length; right++) {
      if (routes[left].enabled && routes[right].enabled && routes[left].service.toLowerCase() === routes[right].service.toLowerCase() && mockRoutesAreAmbiguous(routes[left], routes[right])) {
        throw new Error(`Routes ${routes[left].name} and ${routes[right].name} are ambiguous for ${routes[left].service}; make one path or query matcher more specific.`)
      }
    }
  }
  return routes
}

export function parseMockPairs(value: string, separator: ':' | '=') {
  const result: Record<string, string> = {}
  for (const line of value.split('\n').map((item) => item.trim()).filter(Boolean)) {
    const index = line.indexOf(separator)
    if (index < 1) throw new Error(`Expected ${separator === ':' ? 'Name: value' : 'name=value'} on every non-empty line.`)
    result[line.slice(0, index).trim()] = line.slice(index + 1).trim()
  }
  return result
}

export function parseMockResponseHeaderPairs(value: string) {
  const result: Record<string, string> = {}
  const names = new Set<string>()
  const managed = new Set(['connection', 'content-length', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade'])
  for (const line of value.split('\n').map((item) => item.trim()).filter(Boolean)) {
    const index = line.indexOf(':')
    const name = line.slice(0, index).trim()
    if (index < 1 || !/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(name)) throw new Error('Expected Name: value with a valid HTTP header name on every non-empty line.')
    const canonical = name.toLowerCase()
    if (names.has(canonical)) throw new Error(`Response header ${name} is duplicated.`)
    if (managed.has(canonical)) throw new Error(`Response header ${name} is managed by the HTTP transport.`)
    names.add(canonical)
    result[name] = line.slice(index + 1).trim()
  }
  return result
}

export function mockScenarioIsActive(scenario: Pick<MockScenario, 'activation'>) {
  return scenario.activation.state !== 'disabled'
}

export function sortMockScenarios(scenarios: MockScenario[], sort: TableSort<MockScenarioSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...scenarios].sort((left, right) => {
    const nameOrder = compareMockText(left.name, right.name)
    let order = 0
    switch (sort.key) {
      case 'state':
        order = mockScenarioStateRank(left) - mockScenarioStateRank(right)
        break
      case 'name':
        order = nameOrder
        break
      case 'services':
        order = compareMockText(left.activation.targetServices.join(','), right.activation.targetServices.join(','))
        break
      case 'routes':
        order = left.routes.length - right.routes.length
        break
      case 'modifiedAt':
        order = mockTimestampValue(left.modifiedAt) - mockTimestampValue(right.modifiedAt)
        break
    }
    return direction * order || nameOrder
  })
}

export function sortMockRoutes(routes: MockRoute[], sort: TableSort<MockRouteSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...routes].sort((left, right) => {
    const nameOrder = compareMockText(left.name, right.name)
    let order = 0
    switch (sort.key) {
      case 'service':
        order = compareMockText(left.service, right.service)
        break
      case 'route':
        order = nameOrder
        break
      case 'match':
        order = compareMockText(`${left.method} ${left.path}${formatQuerySummary(left.query)}`, `${right.method} ${right.path}${formatQuerySummary(right.query)}`)
        break
      case 'response':
        order = left.status - right.status || mockRouteBodyBytes(left) - mockRouteBodyBytes(right)
        break
      case 'delay':
        order = (left.delayMs || 0) - (right.delayMs || 0)
        break
      case 'state':
        order = left.enabled === right.enabled ? 0 : left.enabled ? -1 : 1
        break
    }
    return direction * order || nameOrder
  })
}

function mockScenarioStateRank(scenario: MockScenario) {
  switch (scenario.activation.state) {
    case 'enabled': return 0
    case 'degraded': return 1
    case 'disabled': return 2
  }
}

function mockRouteBodyBytes(route: MockRoute) {
  return route.body ? new TextEncoder().encode(route.body).length : 0
}

function compareMockText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
}

function mockTimestampValue(value: string) {
  const timestamp = Date.parse(value)
  return Number.isNaN(timestamp) ? 0 : timestamp
}

function mockRoutesAreAmbiguous(left: MockRoute, right: MockRoute) {
  const leftSegments = splitMockRoutePath(left.path)
  const rightSegments = splitMockRoutePath(right.path)
  const leftLiteralCount = leftSegments.filter((segment) => !/^\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(segment)).length
  const rightLiteralCount = rightSegments.filter((segment) => !/^\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(segment)).length
  const leftQuery = left.query || {}
  const rightQuery = right.query || {}
  if (left.method.toUpperCase() !== right.method.toUpperCase() || leftSegments.length !== rightSegments.length || leftLiteralCount !== rightLiteralCount || Object.keys(leftQuery).length !== Object.keys(rightQuery).length) return false
  for (let index = 0; index < leftSegments.length; index++) {
    const leftParameter = /^\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(leftSegments[index])
    const rightParameter = /^\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(rightSegments[index])
    if (!leftParameter && !rightParameter && leftSegments[index] !== rightSegments[index]) return false
  }
  for (const [name, leftValue] of Object.entries(leftQuery)) {
    const rightValue = rightQuery[name]
    if (rightValue !== undefined && leftValue && rightValue && leftValue !== rightValue) return false
  }
  return true
}

function splitMockRoutePath(path: string) {
  return path === '/' ? [] : path.replace(/^\//, '').split('/')
}

function formatQuerySummary(query?: Record<string, string>) {
  const value = new URLSearchParams(query || {}).toString()
  return value ? `?${value}` : ''
}

function formatTimestamp(value: string) {
  return new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}
