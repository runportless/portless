import { useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath, jsonBody } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { DrawerShell } from '../../components/overlays/DrawerShell'
import { FormDialog } from '../../components/overlays/FormDialog'
import { RowActionsMenu } from '../../components/RowActionsMenu'
import { SortableGridHeader, type TableSort } from '../../components/SortableTableHeader'
import { StatusMark } from '../../components/Status'
import type { Environment, Operation } from '../../api/contracts/environments'
import type { Recording, RecordingList } from '../../api/contracts/experiments'
import type { MockMutation, MockPreview, MockProfile, MockProfileList, MockRoute } from '../../api/contracts/mocks'
import type { Project } from '../../api/contracts/projects'
import type { ComponentBinding } from '../../api/contracts/topology'
import { defaultProviderBinding } from '../environment/bindings/bindingPresentation'
import { waitForEnvironmentOperation } from '../environment/operationPolling'
import { httpStatusGroups } from '../httpStatuses'
import { MockCreationWorkspace, MockRouteWorkspace, mockRouteDraft, type MockCreationInput, type MockCreationRouteDraft } from './MockCreationWorkspace'

type MockProfileSortField = 'state' | 'name' | 'service' | 'routes' | 'createdAt' | 'enabledAt' | 'modifiedAt'

const defaultMockProfileSort: TableSort<MockProfileSortField> = { key: 'state', direction: 'asc' }

export const mockHTTPStatusGroups = httpStatusGroups

export function MocksPanel({ environment, project, selectedProfile, workspace, workspaceRoute, onCreateWorkspace, onRouteWorkspace, onSelectProfile, onChanged }: {
  environment: Environment
  project?: Project
  selectedProfile?: string
  workspace?: 'create' | 'route'
  workspaceRoute?: string
  onCreateWorkspace?: (open: boolean) => void
  onRouteWorkspace?: (profile: string, route?: string) => void
  onSelectProfile: (profile?: string) => void
  onChanged: () => void | Promise<void>
}) {
  const [profiles, setProfiles] = useState<MockProfile[]>([])
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [warnings, setWarnings] = useState<string[]>([])
  const [previewOpen, setPreviewOpen] = useState(false)
  const [deleteName, setDeleteName] = useState('')
  const [busy, setBusy] = useState('')
  const selected = selectedProfile ? profiles.find((profile) => profile.name === selectedProfile) : undefined
  const transitionBlocked = ['starting', 'stopping', 'recovering', 'unknown'].includes(environment.status)

  const refresh = async () => {
    const [mockResult, recordingResult] = await Promise.all([
      api<MockProfileList>(environmentPath(environment, '/mocks')),
      api<RecordingList>(environmentPath(environment, '/recordings')),
    ])
    setProfiles(mockResult.mocks)
    setRecordings(recordingResult.recordings)
  }

  useEffect(() => {
    setLoading(true)
    setError(null)
    refresh().catch((reason) => setError(actionError("Mocks couldn't be loaded", reason))).finally(() => setLoading(false))
    return connectEvents(environment, ['mock.state'], () => { refresh().catch(() => undefined) })
  }, [environment.project, environment.name]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    setPreviewOpen(false)
    setDeleteName('')
  }, [selectedProfile])

  const removeProfile = async (profile: MockProfile) => {
    if (deleteName !== profile.name) { setDeleteName(profile.name); return }
    setBusy(`delete-profile:${profile.name}`); setError(null)
    try {
      await api(environmentPath(environment, `/mocks/${encodeURIComponent(profile.name)}`), { method: 'DELETE' })
      setDeleteName(''); await refresh()
    } catch (reason) { setError(actionError("Mock profile wasn't deleted", reason)) }
    finally { setBusy('') }
  }

  const replaceBinding = async (binding: ComponentBinding) => {
    const operation = await api<Operation>(environmentPath(environment, `/bindings/${encodeURIComponent(binding.service)}`), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
      body: JSON.stringify(binding),
    })
    const completed = await waitForEnvironmentOperation(environment, operation)
    if (completed.state !== 'succeeded') throw new Error(completed.error || `Provider change ${completed.state}`)
  }

  const defaultBindingFor = (serviceName: string) => {
    const service = environment.services.find((item) => item.name.toLowerCase() === serviceName.toLowerCase())
    if (!service) throw new Error(`Service ${serviceName} is no longer available.`)
    const binding = defaultProviderBinding(project, environment, service)
    if (!binding) throw new Error(`Configure a checkout for ${service.name} before disabling its mock profile.`)
    return binding
  }

  const setProfileEnabled = async (profile: MockProfile, enabled: boolean) => {
    const action = `${enabled ? 'enable' : 'disable'}:${profile.name}`
    setBusy(action); setDeleteName(''); setError(null)
    try {
      await replaceBinding(enabled
        ? { service: profile.service, provider: 'mock', mock: { profile: profile.name } }
        : defaultBindingFor(profile.service))
      await onChanged()
    } catch (reason) { setError(actionError(`Mock profile wasn't ${enabled ? 'enabled' : 'disabled'}`, reason)) }
    finally { setBusy('') }
  }

  const disableAllProfiles = async () => {
    const activeBindings = mockProfileBindings(environment)
    if (activeBindings.length === 0) return
    setBusy('disable-all'); setDeleteName(''); setError(null)
    let changed = false
    try {
      for (const binding of activeBindings) {
        await replaceBinding(defaultBindingFor(binding.service))
        changed = true
      }
      await onChanged()
    } catch (reason) {
      if (changed) void Promise.resolve(onChanged()).catch(() => undefined)
      setError(actionError("Mock profiles weren't disabled", reason))
    } finally { setBusy('') }
  }

  const saveRoute = async (draft: MockCreationRouteDraft, originalName?: string) => {
    if (!selected) return
    setBusy('route'); setError(null)
    try {
      const routeName = draft.name.trim()
      if (originalName && originalName.toLowerCase() !== routeName.toLowerCase()) {
        throw new Error('Route names cannot be changed in place. Create a new route, then delete the old route.')
      }
      const candidateDrafts = selected.routes
        .filter((route) => route.name !== originalName)
        .map((route, index) => mockRouteDraft(route, index + 1))
      candidateDrafts.push(draft)
      const route = mockCreationRoutes(candidateDrafts).find((item) => item.name.toLowerCase() === routeName.toLowerCase())
      if (!route) throw new Error('The route could not be validated.')
      const updated = await api<MockProfile>(environmentPath(environment, `/mocks/${encodeURIComponent(selected.name)}/routes/${encodeURIComponent(routeName)}`), {
        method: 'PUT', ...jsonBody(route),
      })
      setProfiles((current) => current.map((profile) => profile.name === updated.name ? updated : profile))
      onSelectProfile(updated.name)
    } catch (reason) { setError(actionError("Mock route wasn't saved", reason)) }
    finally { setBusy('') }
  }

  const removeRoute = async (route: MockRoute) => {
    if (!selected) return
    const key = `delete-route:${route.name}`
    if (deleteName !== key) { setDeleteName(key); return }
    setBusy(key); setError(null)
    try {
      const updated = await api<MockProfile>(environmentPath(environment, `/mocks/${encodeURIComponent(selected.name)}/routes/${encodeURIComponent(route.name)}`), { method: 'DELETE' })
      setProfiles((current) => current.map((profile) => profile.name === updated.name ? updated : profile))
      setDeleteName('')
    } catch (reason) { setError(actionError("Mock route wasn't deleted", reason)) }
    finally { setBusy('') }
  }

  const createMock = async (input: MockCreationInput) => {
    setBusy('create'); setError(null)
    let routes: MockRoute[]
    try {
      routes = input.routes.length ? mockCreationRoutes(input.routes) : []
    } catch (reason) {
      setError(actionError("Mock draft couldn't be created", reason))
      setBusy('')
      return
    }

    let result: Awaited<ReturnType<typeof createConfiguredMockProfile>>
    try {
      result = await createConfiguredMockProfile(
        () => api<MockMutation>(environmentPath(environment, '/mocks'), { method: 'POST', ...jsonBody(input.profile) }),
        routes,
        (route) => api<MockProfile>(environmentPath(environment, `/mocks/${encodeURIComponent(input.profile.name)}/routes/${encodeURIComponent(route.name)}`), { method: 'PUT', ...jsonBody(route) }),
        input.activate ? async (profile) => {
          await replaceBinding({ service: profile.service, provider: 'mock', mock: { profile: profile.name } })
          await onChanged()
        } : undefined,
      )
    } catch (reason) {
      setError(actionError("Mock wasn't created", reason))
      setBusy('')
      return
    }

    setWarnings(result.created.warnings || [])
    setProfiles((current) => [...current.filter((profile) => profile.name.toLowerCase() !== result.profile.name.toLowerCase()), result.profile])
    try {
      await refresh()
    } catch (reason) {
      setError(actionError("Mock was created, but the mock list couldn't be refreshed", reason))
      setBusy('')
      onSelectProfile(result.profile.name)
      return
    }
    setBusy('')
    if (result.state === 'configuration-failed') {
      setError(actionError(`Mock was created, but route ${result.failedRoute.name} wasn't saved`, result.configurationFailure))
    } else if (result.state === 'activation-failed') {
      setError(actionError("Mock was created but wasn't enabled", result.activationFailure))
    }
    onSelectProfile(result.profile.name)
  }

  if (workspace === 'create') return <div className="mocks-page">
    <MockCreationWorkspace
      environment={environment}
      recordings={recordings}
      busy={busy === 'create'}
      activationBlocked={transitionBlocked}
      error={error}
      onDismissError={() => setError(null)}
      onCancel={() => { if (!busy) { setError(null); onCreateWorkspace?.(false) } }}
      onCreate={createMock}
    />
  </div>

  if (workspace === 'route') return <div className="mocks-page">
    {loading ? <section className="panel mock-route-workspace__loading">Loading route editor…</section> : selected ? <MockRouteWorkspace
      key={`${selected.name}/${workspaceRoute || 'new'}`}
      profile={selected}
      routeName={workspaceRoute}
      busy={busy === 'route'}
      error={error}
      onDismissError={() => setError(null)}
      onCancel={() => { if (!busy) { setError(null); onSelectProfile(selected.name) } }}
      onOpenRoute={(route) => { setError(null); onRouteWorkspace?.(selected.name, route) }}
      onSave={saveRoute}
    /> : <section className="panel mock-route-workspace__missing">
      {error ? <ActionErrorNotice error={error} onDismiss={() => setError(null)} /> : <><strong>MOCK PROFILE NOT FOUND</strong><p>The selected profile is no longer available.</p></>}
      <button className="button" type="button" onClick={() => onSelectProfile()}>BACK TO MOCKS</button>
    </section>}
  </div>

  return <div className="mocks-page">
    {error && !selected && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    {warnings.length > 0 && <div className="mock-warning"><strong>IMPORT FINISHED WITH NOTES</strong><span>{warnings.join(' ')}</span><button type="button" onClick={() => setWarnings([])}>DISMISS</button></div>}
    <MockProfilesList
      environment={environment}
      profiles={profiles}
      selectedProfile={selected?.name}
      loading={loading}
      busy={busy}
      deleteName={deleteName}
      transitionBlocked={transitionBlocked}
      onCreate={() => { setDeleteName(''); setError(null); onCreateWorkspace?.(true) }}
      onOpen={(profile) => { setDeleteName(''); setError(null); onSelectProfile(profile.name) }}
      onToggle={(profile, enabled) => { void setProfileEnabled(profile, enabled) }}
      onDelete={(profile) => { void removeProfile(profile) }}
      onDismissDelete={() => setDeleteName('')}
      onDisableAll={() => { void disableAllProfiles() }}
    />

    {selected && <MockProfileDrawer
      environment={environment}
      profile={selected}
      active={mockProfileIsActive(environment, selected)}
      busy={busy}
      deleteName={deleteName}
      error={error}
      onDismissError={() => setError(null)}
      onClose={() => { setDeleteName(''); onSelectProfile() }}
      onPreview={() => setPreviewOpen(true)}
      onAddRoute={() => onRouteWorkspace?.(selected.name)}
      onEditRoute={(route) => onRouteWorkspace?.(selected.name, route.name)}
      onDeleteRoute={(route) => { void removeRoute(route) }}
      onDismissDelete={() => setDeleteName('')}
    />}

    {previewOpen && selected && <PreviewModal environment={environment} profile={selected} onClose={() => setPreviewOpen(false)} />}
  </div>
}

export async function createConfiguredMockProfile(create: () => Promise<MockMutation>, routes: MockRoute[], saveRoute: (route: MockRoute) => Promise<MockProfile>, activate?: (profile: MockProfile) => Promise<void>) {
  const created = await create()
  let profile = created.mock
  for (const route of routes) {
    try {
      profile = await saveRoute(route)
    } catch (configurationFailure) {
      return { created, profile, state: 'configuration-failed' as const, failedRoute: route, configurationFailure }
    }
  }
  if (!activate) return { created, profile, state: 'inactive' as const }
  try {
    await activate(profile)
    return { created, profile, state: 'activated' as const }
  } catch (activationFailure) {
    return { created, profile, state: 'activation-failed' as const, activationFailure }
  }
}

export function mockCreationRoutes(drafts: MockCreationRouteDraft[]): MockRoute[] {
  if (drafts.length === 0) throw new Error('Add at least one route.')
  if (drafts.length > 1000) throw new Error('A mock cannot contain more than 1000 routes.')
  const names = new Set<string>()
  const registeredStatuses = new Set<number>(httpStatusGroups.flatMap((group) => group.statuses.map(([code]) => code)))
  const encoder = new TextEncoder()
  let totalBodyBytes = 0
  const routes = drafts.map((draft) => {
    const name = draft.name.trim()
    if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(name)) throw new Error(`Route ${name || '(unnamed)'} must use a lowercase URL-safe name.`)
    if (names.has(name.toLowerCase())) throw new Error(`Route name ${name} is duplicated.`)
    names.add(name.toLowerCase())
    if (!/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(draft.method)) throw new Error(`Route ${name} method must be an HTTP token.`)
    if (!draft.path.startsWith('/') || /[?#]/.test(draft.path)) throw new Error(`Route ${name} path must be an absolute URL path without a query or fragment.`)
    const parameters = new Set<string>()
    for (const segment of draft.path.replace(/^\//, '').split('/')) {
      if (!segment.startsWith('{') && !segment.endsWith('}')) continue
      if (!/^\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(segment)) throw new Error(`Route ${name} path segment ${segment} is not a valid {parameter}.`)
      const parameter = segment.slice(1, -1).toLowerCase()
      if (parameters.has(parameter)) throw new Error(`Route ${name} path parameter ${parameter} is duplicated.`)
      parameters.add(parameter)
    }
    const delayMs = Number(draft.delayMs || 0)
    if (delayMs < 0 || delayMs > 300_000) throw new Error(`Route ${name} delay must be between 0 and 300000 ms.`)
    if (!registeredStatuses.has(Number(draft.status))) throw new Error(`Route ${name} status must be a registered final HTTP response status.`)
    const bodyBytes = encoder.encode(draft.body).length
    if (bodyBytes > 1_048_576) throw new Error(`Route ${name} response body exceeds 1048576 bytes.`)
    totalBodyBytes += bodyBytes
    if (totalBodyBytes > 8_388_608) throw new Error('Mock response bodies exceed 8388608 bytes.')
    return {
      name, method: draft.method, path: draft.path, status: Number(draft.status),
      query: parseMockPairs(draft.queryText, '='), headers: parseMockResponseHeaderPairs(draft.headersText),
      body: draft.body, delayMs, enabled: draft.enabled,
    }
  })
  for (let left = 0; left < routes.length; left++) {
    for (let right = left + 1; right < routes.length; right++) {
      if (routes[left].enabled && routes[right].enabled && mockRoutesAreAmbiguous(routes[left], routes[right])) {
        throw new Error(`Routes ${routes[left].name} and ${routes[right].name} are ambiguous; make one path or query matcher more specific.`)
      }
    }
  }
  return routes
}

export function mockProfileBindings(environment: Pick<Environment, 'bindings'>) {
  return (environment.bindings || []).filter((binding) => binding.provider === 'mock')
}

export function mockProfileIsActive(environment: Pick<Environment, 'bindings'>, profile: Pick<MockProfile, 'name' | 'service'>) {
  return !!mockProfileActiveBinding(environment, profile)
}

function mockProfileActiveBinding(environment: Pick<Environment, 'bindings'>, profile: Pick<MockProfile, 'name' | 'service'>) {
  return mockProfileBindings(environment).find((binding) =>
    binding.service.toLowerCase() === profile.service.toLowerCase() &&
    binding.mock?.profile.toLowerCase() === profile.name.toLowerCase(),
  )
}

export function MockProfilesList({ environment, profiles, selectedProfile, loading, busy, deleteName, transitionBlocked, onCreate, onOpen, onToggle, onDelete, onDismissDelete, onDisableAll }: {
  environment: Environment
  profiles: MockProfile[]
  selectedProfile?: string
  loading: boolean
  busy: string
  deleteName: string
  transitionBlocked: boolean
  onCreate: () => void
  onOpen: (profile: MockProfile) => void
  onToggle: (profile: MockProfile, enabled: boolean) => void
  onDelete: (profile: MockProfile) => void
  onDismissDelete: () => void
  onDisableAll: () => void
}) {
  const activeBindings = mockProfileBindings(environment)
  const [profileSort, setProfileSort] = useState<TableSort<MockProfileSortField>>(defaultMockProfileSort)
  const [profileMenu, setProfileMenu] = useState('')
  const orderedProfiles = useMemo(() => sortMockProfiles(profiles, environment, profileSort), [profiles, environment, profileSort])

  useEffect(() => {
    setProfileSort(defaultMockProfileSort)
    setProfileMenu('')
  }, [environment.project, environment.name])

  const changeProfileSort = (sort: TableSort<MockProfileSortField>) => {
    setProfileSort(sort)
    setProfileMenu('')
    onDismissDelete()
  }

  return <section className="panel mock-profiles-panel">
    <div className="panel-title"><span>MOCK PROFILES</span><button className="button button--primary button--small panel-create-button" type="button" disabled={!!busy} onClick={onCreate}>CREATE MOCK</button></div>
    {profiles.length > 0 && <div className="mock-profiles-bulk-actions">
      <button className="mock-profiles-disable-all-link" type="button" disabled={!!busy || transitionBlocked || activeBindings.length === 0} onClick={onDisableAll}>{busy === 'disable-all' ? 'DISABLING…' : 'DISABLE ALL'}</button>
    </div>}
    <div className={`mock-profile-row mock-profile-row--header sortable-header-row${profileSort.key === defaultMockProfileSort.key && profileSort.direction === defaultMockProfileSort.direction ? ' is-default-sort' : ''}`} role="row">
      <SortableGridHeader label="State" sortKey="state" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <SortableGridHeader label="Name" sortKey="name" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <SortableGridHeader label="Service" sortKey="service" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <SortableGridHeader label="Routes" sortKey="routes" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <SortableGridHeader label="Created at" sortKey="createdAt" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <SortableGridHeader label="Enabled at" sortKey="enabledAt" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <SortableGridHeader label="Modified at" sortKey="modifiedAt" sort={profileSort} itemCount={profiles.length} onSort={changeProfileSort} />
      <span aria-label="Actions" />
    </div>
    {orderedProfiles.map((profile) => {
      const activeBinding = mockProfileActiveBinding(environment, profile)
      const active = !!activeBinding
      const toggleAction = `${active ? 'disable' : 'enable'}:${profile.name}`
      const menuOpen = profileMenu === profile.name
      return <div className={`mock-profile-row${selectedProfile === profile.name ? ' is-selected' : ''}`} key={profile.name} onClick={() => { if (!busy) onOpen(profile) }}>
        <MockEnabledState enabled={active} />
        <div className="mock-profile-row__name"><button type="button" disabled={!!busy} aria-label={`Open ${profile.name} mock profile`} onClick={(event) => { event.stopPropagation(); onOpen(profile) }}><strong>{profile.name}</strong></button></div>
        <span>{profile.service}</span><span>{profile.routes.length}</span>
        <MockTimestamp className="mock-profile-row__created" value={profile.createdAt} />
        <MockTimestamp className="mock-profile-row__enabled" value={activeBinding?.modifiedAt} />
        <MockTimestamp className="mock-profile-row__modified" value={profile.modifiedAt} />
        <div className="mock-row-actions table-row-actions">
          <button type="button" disabled={!!busy || transitionBlocked} aria-label={`${active ? 'Disable' : 'Enable'} ${profile.name}`} onClick={(event) => { event.stopPropagation(); onToggle(profile, !active) }}>{busy === toggleAction ? active ? 'DISABLING…' : 'ENABLING…' : active ? 'DISABLE' : 'ENABLE'}</button>
          <RowActionsMenu
            label={`Mock profile actions for ${profile.name}`}
            menuLabel={`${profile.name} mock profile actions`}
            open={menuOpen}
            disabled={!!busy}
            onOpenChange={(open) => {
              setProfileMenu(open ? profile.name : '')
              if (!open || profileMenu !== profile.name) onDismissDelete()
            }}
          >
            <button className={`is-danger${deleteName === profile.name ? ' is-confirming' : ''}`} type="button" role="menuitem" disabled={!!busy} aria-label={deleteName === profile.name ? `Confirm delete ${profile.name}` : `Delete ${profile.name}`} onClick={() => onDelete(profile)}>{busy === `delete-profile:${profile.name}` ? 'DELETING…' : deleteName === profile.name ? 'CONFIRM' : 'DELETE'}</button>
          </RowActionsMenu>
        </div>
      </div>
    })}
    {!loading && profiles.length === 0 && <div className="empty-row">No mocks. Create one for a service you do not want to run locally.</div>}
    {loading && <div className="empty-row">Loading mock profiles…</div>}
  </section>
}

export function MockProfileDrawer({ environment, profile, active, busy, deleteName, error, onDismissError, onClose, onPreview, onAddRoute, onEditRoute, onDeleteRoute, onDismissDelete }: {
  environment: Environment
  profile: MockProfile
  active: boolean
  busy: string
  deleteName: string
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onPreview: () => void
  onAddRoute: () => void
  onEditRoute: (route: MockRoute) => void
  onDeleteRoute: (route: MockRoute) => void
  onDismissDelete: () => void
}) {
  const [routeMenu, setRouteMenu] = useState('')

  useEffect(() => {
    setRouteMenu('')
  }, [environment.project, environment.name, profile.name])

  return <DrawerShell
    label={`${profile.name} mock profile`}
    subject={`${profile.name} mock profile`}
    className="mock-profile-drawer"
    closeLabel="Close mock profile"
    closeBlocked={!!busy}
    header={<div><span className="eyebrow">{environment.project} / {environment.name} / mock profile</span><div className="mock-profile-drawer__title"><h2>{profile.name}</h2><MockEnabledState enabled={active} /></div></div>}
    actions={<><button className="button" type="button" disabled={!!busy} onClick={onPreview}>PREVIEW REQUEST</button><button className="button button--primary" type="button" disabled={!!busy} onClick={onAddRoute}>ADD ROUTE</button></>}
    actionProps={{ 'aria-busy': !!busy }}
    notice={error && <div className="mock-profile-drawer__error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}
    onClose={onClose}
  >
        <div className="mock-profile-summary"><div><span>SERVICE</span><strong>{profile.service}</strong></div><div><span>DESCRIPTION</span><strong>{profile.description || '—'}</strong></div><div><span>FALLBACK</span><strong>501 · no route matched</strong></div><div><span>MODIFIED</span><strong>{formatTimestamp(profile.modifiedAt)}</strong></div></div>
        <section className="mock-route-table" aria-label={`${profile.name} routes`}>
          <div className="mock-route-table__title"><span>ROUTES</span><small>{profile.routes.length}</small></div>
          <div className="mock-route-row mock-route-row--header"><span>Route</span><span>Match</span><span>Response</span><span>Delay</span><span>State</span><span aria-hidden="true" /></div>
          {profile.routes.map((route) => <div className="mock-route-row" key={route.name}>
            <strong>{route.name}</strong><code>{route.method} {route.path}{formatQuerySummary(route.query)}</code><span>{route.status}{route.body ? ` · ${new Blob([route.body]).size} B` : ''}</span><span>{route.delayMs ? `${route.delayMs} ms` : '—'}</span><MockEnabledState enabled={route.enabled} />
            <div className="mock-row-actions table-row-actions">
              <button type="button" disabled={!!busy} onClick={() => onEditRoute(route)}>EDIT</button>
              <RowActionsMenu
                label={`Route actions for ${route.name}`}
                menuLabel={`${route.name} route actions`}
                open={routeMenu === route.name}
                disabled={!!busy}
                onOpenChange={(open) => {
                  setRouteMenu(open ? route.name : '')
                  if (!open || routeMenu !== route.name) onDismissDelete()
                }}
              >
                <button className={`is-danger${deleteName === `delete-route:${route.name}` ? ' is-confirming' : ''}`} type="button" role="menuitem" disabled={!!busy} aria-label={deleteName === `delete-route:${route.name}` ? `Confirm delete ${route.name}` : `Delete ${route.name}`} onClick={() => onDeleteRoute(route)}>{busy === `delete-route:${route.name}` ? 'DELETING…' : deleteName === `delete-route:${route.name}` ? 'CONFIRM' : 'DELETE'}</button>
              </RowActionsMenu>
            </div>
          </div>)}
          {profile.routes.length === 0 && <div className="empty-row">This profile has no routes. Unmatched requests return 501 so missing behavior is visible.</div>}
        </section>
  </DrawerShell>
}

function MockEnabledState({ enabled }: { enabled: boolean }) {
  const label = enabled ? 'Enabled' : 'Disabled'
  return <div className={`mock-enabled-state${enabled ? ' is-enabled' : ''}`}>
    <StatusMark status={label.toLowerCase()} label={false} />
    <span>{label}</span>
  </div>
}

function MockTimestamp({ className, value }: { className: string; value?: string }) {
  const classes = `mock-profile-row__timestamp ${className}`
  if (!value) return <span className={classes}>—</span>
  return <time className={classes} dateTime={value} title={new Date(value).toLocaleString()}>{formatTimestamp(value)}</time>
}

export function sortMockProfiles(profiles: MockProfile[], environment: Pick<Environment, 'bindings'>, sort: TableSort<MockProfileSortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...profiles].sort((left, right) => {
    const leftBinding = mockProfileActiveBinding(environment, left)
    const rightBinding = mockProfileActiveBinding(environment, right)
    const nameOrder = compareMockText(left.name, right.name)
    let order = 0

    switch (sort.key) {
      case 'state':
        order = !!leftBinding === !!rightBinding ? 0 : leftBinding ? -1 : 1
        break
      case 'name':
        order = nameOrder
        break
      case 'service':
        order = compareMockText(left.service, right.service)
        break
      case 'routes':
        order = left.routes.length - right.routes.length
        break
      case 'createdAt':
        order = mockTimestampValue(left.createdAt) - mockTimestampValue(right.createdAt)
        break
      case 'enabledAt':
        order = compareMockEnabledAt(leftBinding?.modifiedAt, rightBinding?.modifiedAt)
        break
      case 'modifiedAt':
        order = mockTimestampValue(left.modifiedAt) - mockTimestampValue(right.modifiedAt)
        break
    }

    return direction * order || nameOrder
  })
}

function compareMockText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
}

function mockTimestampValue(value: string) {
  const timestamp = Date.parse(value)
  return Number.isNaN(timestamp) ? 0 : timestamp
}

function compareMockEnabledAt(left?: string, right?: string) {
  if (!!left !== !!right) return left ? 1 : -1
  if (!left || !right) return 0
  return mockTimestampValue(left) - mockTimestampValue(right)
}

function PreviewModal({ environment, profile, onClose }: { environment: Environment; profile: MockProfile; onClose: () => void }) {
  const [method, setMethod] = useState('GET')
  const [target, setTarget] = useState('/')
  const [headersText, setHeadersText] = useState('')
  const [body, setBody] = useState('')
  const [result, setResult] = useState<MockPreview | null>(null)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [busy, setBusy] = useState(false)
  const targetInput = useRef<HTMLInputElement>(null)
  const supportsBody = mockRequestSupportsBody(method)
  const preview = async () => {
    setBusy(true); setError(null)
    try {
      const parsed = new URL(target, 'http://mock.localhost')
      const query: Record<string, string[]> = {}
      parsed.searchParams.forEach((value, key) => { (query[key] ||= []).push(value) })
      const headers = parseMockHeaderPairs(headersText)
      setResult(await api<MockPreview>(environmentPath(environment, `/mocks/${encodeURIComponent(profile.name)}/preview`), { method: 'POST', ...jsonBody({ method, path: parsed.pathname, query, ...(Object.keys(headers).length ? { headers } : {}), ...(supportsBody && body ? { body } : {}) }) }))
    } catch (reason) { setError(actionError("Request couldn't be previewed", reason)) }
    finally { setBusy(false) }
  }
  return <FormDialog
    className="mock-form-modal"
    titleID="mock-preview-title"
    closeLabel="Close request preview"
    closeBlocked={busy}
    initialFocusRef={targetInput}
    header={<div><div className="eyebrow">{profile.name}</div><h2 id="mock-preview-title">Preview request</h2></div>}
    onClose={onClose}
  >
    <form onSubmit={(event) => { event.preventDefault(); void preview() }}>
      <p>Evaluate the matcher without sending traffic or changing trace history.</p>
      <div className="form-modal__fields">
        <label><span>METHOD</span><select value={method} disabled={busy} onChange={(event) => { setMethod(event.target.value); setResult(null) }}>{['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((item) => <option key={item}>{item}</option>)}</select></label>
        <label><span>PATH AND QUERY</span><input ref={targetInput} value={target} disabled={busy} onChange={(event) => { setTarget(event.target.value); setResult(null) }} /></label>
        <label className="provider-field--wide"><span>REQUEST HEADERS · ONE NAME: VALUE PER LINE</span><textarea value={headersText} disabled={busy} placeholder={'Content-Type: application/json\nX-Request-ID: preview-123'} onChange={(event) => { setHeadersText(event.target.value); setResult(null) }} /></label>
        {supportsBody && <label className="provider-field--wide"><span>REQUEST BODY</span><textarea className="mock-body-editor" value={body} maxLength={262144} disabled={busy} placeholder={'{"sku":"coffee-mug"}'} onChange={(event) => { setBody(event.target.value); setResult(null) }} /></label>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
      {result && <div className={`mock-preview-result${result.matched ? '' : ' is-unmatched'}`}><span>{result.matched ? `MATCHED · ${result.route}` : 'NO MATCH'}</span><strong>{result.status}</strong><pre>{result.body || '(empty response body)'}</pre></div>}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CLOSE</button><button className="button button--primary" type="submit" disabled={busy || !target.startsWith('/')}>{busy ? 'MATCHING…' : 'PREVIEW'}</button></footer>
    </form>
  </FormDialog>
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

export function parseMockHeaderPairs(value: string) {
  const result: Record<string, string[]> = {}
  for (const line of value.split('\n').map((item) => item.trim()).filter(Boolean)) {
    const index = line.indexOf(':')
    const name = line.slice(0, index).trim()
    if (index < 1 || !/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(name)) throw new Error('Expected Name: value with a valid HTTP header name on every non-empty line.')
    const existing = Object.keys(result).find((item) => item.toLowerCase() === name.toLowerCase()) || name
    ;(result[existing] ||= []).push(line.slice(index + 1).trim())
  }
  return result
}

export function mockRequestSupportsBody(method: string) {
  return ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method.toUpperCase())
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
