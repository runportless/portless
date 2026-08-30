import { useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath, jsonBody } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { DrawerShell } from '../../components/overlays/DrawerShell'
import { FormDialog } from '../../components/overlays/FormDialog'
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

type RouteDraft = Pick<MockRoute, 'name' | 'method' | 'path' | 'status' | 'body' | 'delayMs' | 'enabled'> & {
  queryText: string
  headersText: string
}

type MockProfileSortField = 'state' | 'name' | 'service' | 'routes' | 'createdAt' | 'enabledAt' | 'modifiedAt'

const defaultMockProfileSort: TableSort<MockProfileSortField> = { key: 'state', direction: 'asc' }

const emptyRoute = (): RouteDraft => ({
  name: '', method: 'GET', path: '/', status: 200, body: '', delayMs: 0, enabled: true,
  queryText: '', headersText: 'Content-Type: application/json',
})

export const mockHTTPStatusGroups = httpStatusGroups

export function MocksPanel({ environment, project, selectedProfile, onSelectProfile, onChanged }: {
  environment: Environment
  project?: Project
  selectedProfile?: string
  onSelectProfile: (profile?: string) => void
  onChanged: () => void | Promise<void>
}) {
  const [profiles, setProfiles] = useState<MockProfile[]>([])
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [warnings, setWarnings] = useState<string[]>([])
  const [createOpen, setCreateOpen] = useState(false)
  const [routeDraft, setRouteDraft] = useState<RouteDraft | null>(null)
  const [routeOriginalName, setRouteOriginalName] = useState('')
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
    setRouteDraft(null)
    setRouteOriginalName('')
    setPreviewOpen(false)
    setDeleteName('')
    setError(null)
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

  const openRoute = (route?: MockRoute) => {
    setRouteOriginalName(route?.name || '')
    setRouteDraft(route ? {
      name: route.name, method: route.method, path: route.path, status: route.status, body: route.body || '',
      delayMs: route.delayMs || 0, enabled: route.enabled,
      queryText: formatPairs(route.query, '='), headersText: formatPairs(route.headers, ': '),
    } : emptyRoute())
    setError(null)
  }

  const saveRoute = async () => {
    if (!selected || !routeDraft) return
    setBusy('route'); setError(null)
    try {
      const routeName = routeDraft.name.trim()
      if (!routeName) throw new Error('Enter a route name.')
      if (routeOriginalName && routeOriginalName.toLowerCase() !== routeName.toLowerCase()) {
        throw new Error('Route names cannot be changed in place. Create a new route, then delete the old route.')
      }
      const updated = await api<MockProfile>(environmentPath(environment, `/mocks/${encodeURIComponent(selected.name)}/routes/${encodeURIComponent(routeName)}`), {
        method: 'PUT', ...jsonBody({
          name: routeName, method: routeDraft.method, path: routeDraft.path, status: Number(routeDraft.status),
          query: parseMockPairs(routeDraft.queryText, '='), headers: parseMockPairs(routeDraft.headersText, ':'),
          body: routeDraft.body, delayMs: Number(routeDraft.delayMs || 0), enabled: routeDraft.enabled,
        }),
      })
      setProfiles((current) => current.map((profile) => profile.name === updated.name ? updated : profile))
      setRouteDraft(null)
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

  return <div className="mocks-page">
    {error && !createOpen && !routeDraft && !selected && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    {warnings.length > 0 && <div className="mock-warning"><strong>IMPORT FINISHED WITH NOTES</strong><span>{warnings.join(' ')}</span><button type="button" onClick={() => setWarnings([])}>DISMISS</button></div>}
    <MockProfilesList
      environment={environment}
      profiles={profiles}
      selectedProfile={selected?.name}
      loading={loading}
      busy={busy}
      deleteName={deleteName}
      transitionBlocked={transitionBlocked}
      onCreate={() => { setCreateOpen(true); setDeleteName(''); setError(null) }}
      onOpen={(profile) => { setDeleteName(''); setError(null); onSelectProfile(profile.name) }}
      onToggle={(profile, enabled) => { void setProfileEnabled(profile, enabled) }}
      onDelete={(profile) => { void removeProfile(profile) }}
      onDisableAll={() => { void disableAllProfiles() }}
    />

    {selected && <MockProfileDrawer
      environment={environment}
      profile={selected}
      active={mockProfileIsActive(environment, selected)}
      busy={busy}
      deleteName={deleteName}
      error={!createOpen && !routeDraft ? error : null}
      onDismissError={() => setError(null)}
      onClose={() => { setDeleteName(''); onSelectProfile() }}
      onPreview={() => setPreviewOpen(true)}
      onAddRoute={() => openRoute()}
      onEditRoute={openRoute}
      onDeleteRoute={(route) => { void removeRoute(route) }}
    />}

    {createOpen && <CreateProfileModal environment={environment} recordings={recordings} busy={busy === 'create'} error={error} onDismissError={() => setError(null)} onClose={() => setCreateOpen(false)} onCreate={async (input) => {
      setBusy('create'); setError(null)
      let result: Awaited<ReturnType<typeof createAndEnableMockProfile>>
      try {
        result = await createAndEnableMockProfile(
          () => api<MockMutation>(environmentPath(environment, '/mocks'), { method: 'POST', ...jsonBody(input) }),
          async (profile) => {
            await replaceBinding({ service: profile.service, provider: 'mock', mock: { profile: profile.name } })
            await onChanged()
          },
        )
      } catch (reason) {
        setError(actionError("Mock profile wasn't created", reason))
        setBusy('')
        return
      }
      const { created } = result
      setWarnings(created.warnings || [])
      setCreateOpen(false)
      try {
        await refresh()
      } catch (reason) {
        setError(actionError("Mock profile was created, but the profile list couldn't be refreshed", reason))
        setBusy('')
        return
      }
      setBusy('')
      if (!result.activated) {
        setError(actionError("Mock profile was created but wasn't enabled", result.activationFailure))
        return
      }
      onSelectProfile(created.mock.name)
    }} />}
    {routeDraft && <RouteModal draft={routeDraft} editing={!!routeOriginalName} busy={busy === 'route'} error={error} onDismissError={() => setError(null)} onChange={setRouteDraft} onClose={() => setRouteDraft(null)} onSave={saveRoute} />}
    {previewOpen && selected && <PreviewModal environment={environment} profile={selected} onClose={() => setPreviewOpen(false)} />}
  </div>
}

export async function createAndEnableMockProfile(create: () => Promise<MockMutation>, enable: (profile: MockProfile) => Promise<void>) {
  const created = await create()
  try {
    await enable(created.mock)
    return { created, activated: true as const }
  } catch (activationFailure) {
    return { created, activated: false as const, activationFailure }
  }
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

export function MockProfilesList({ environment, profiles, selectedProfile, loading, busy, deleteName, transitionBlocked, onCreate, onOpen, onToggle, onDelete, onDisableAll }: {
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
  onDisableAll: () => void
}) {
  const activeBindings = mockProfileBindings(environment)
  const [profileSort, setProfileSort] = useState<TableSort<MockProfileSortField>>(defaultMockProfileSort)
  const orderedProfiles = useMemo(() => sortMockProfiles(profiles, environment, profileSort), [profiles, environment, profileSort])

  useEffect(() => {
    setProfileSort(defaultMockProfileSort)
  }, [environment.project, environment.name])

  return <section className="panel mock-profiles-panel">
    <div className="panel-title"><span>MOCK PROFILES</span><button className="button button--primary button--small panel-create-button" type="button" disabled={!!busy} onClick={onCreate}>CREATE PROFILE</button></div>
    {profiles.length > 0 && <div className="mock-profiles-bulk-actions">
      <button className="mock-profiles-disable-all-link" type="button" disabled={!!busy || transitionBlocked || activeBindings.length === 0} onClick={onDisableAll}>{busy === 'disable-all' ? 'DISABLING…' : 'DISABLE ALL'}</button>
    </div>}
    <div className="mock-profile-row mock-profile-row--header sortable-header-row" role="row">
      <SortableGridHeader label="State" sortKey="state" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <SortableGridHeader label="Name" sortKey="name" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <SortableGridHeader label="Service" sortKey="service" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <SortableGridHeader label="Routes" sortKey="routes" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <SortableGridHeader label="Created at" sortKey="createdAt" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <SortableGridHeader label="Enabled at" sortKey="enabledAt" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <SortableGridHeader label="Modified at" sortKey="modifiedAt" sort={profileSort} defaultSort={defaultMockProfileSort} itemCount={profiles.length} onSort={setProfileSort} />
      <span aria-label="Actions" />
    </div>
    {orderedProfiles.map((profile) => {
      const activeBinding = mockProfileActiveBinding(environment, profile)
      const active = !!activeBinding
      const toggleAction = `${active ? 'disable' : 'enable'}:${profile.name}`
      return <div className={`mock-profile-row${selectedProfile === profile.name ? ' is-selected' : ''}`} key={profile.name} onClick={() => onOpen(profile)}>
        <MockEnabledState enabled={active} />
        <div className="mock-profile-row__name"><strong>{profile.name}</strong></div>
        <span>{profile.service}</span><span>{profile.routes.length}</span>
        <MockTimestamp className="mock-profile-row__created" value={profile.createdAt} />
        <MockTimestamp className="mock-profile-row__enabled" value={activeBinding?.modifiedAt} />
        <MockTimestamp className="mock-profile-row__modified" value={profile.modifiedAt} />
        <div className="mock-row-actions table-row-actions">
          <button type="button" disabled={!!busy || transitionBlocked} aria-label={`${active ? 'Disable' : 'Enable'} ${profile.name}`} onClick={(event) => { event.stopPropagation(); onToggle(profile, !active) }}>{busy === toggleAction ? active ? 'DISABLING…' : 'ENABLING…' : active ? 'DISABLE' : 'ENABLE'}</button>
          <button type="button" disabled={!!busy} onClick={(event) => { event.stopPropagation(); onOpen(profile) }}>OPEN</button>
          <button className={deleteName === profile.name ? 'is-confirming' : ''} type="button" disabled={!!busy} aria-label={deleteName === profile.name ? `Confirm delete ${profile.name}` : `Delete ${profile.name}`} onClick={(event) => { event.stopPropagation(); onDelete(profile) }}>{busy === `delete-profile:${profile.name}` ? 'DELETING…' : deleteName === profile.name ? 'CONFIRM' : 'DELETE'}</button>
        </div>
      </div>
    })}
    {!loading && profiles.length === 0 && <div className="empty-row">No mock profiles. Create one for a service you do not want to run locally.</div>}
    {loading && <div className="empty-row">Loading mock profiles…</div>}
  </section>
}

export function MockProfileDrawer({ environment, profile, active, busy, deleteName, error, onDismissError, onClose, onPreview, onAddRoute, onEditRoute, onDeleteRoute }: {
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
}) {
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
            <div className="mock-row-actions table-row-actions"><button type="button" disabled={!!busy} onClick={() => onEditRoute(route)}>EDIT</button><button className={deleteName === `delete-route:${route.name}` ? 'is-confirming' : ''} type="button" disabled={!!busy} onClick={() => onDeleteRoute(route)}>{deleteName === `delete-route:${route.name}` ? 'CONFIRM' : 'DELETE'}</button></div>
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

export function CreateProfileModal({ environment, recordings, busy, error, onDismissError, onClose, onCreate }: {
  environment: Environment
  recordings: Recording[]
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onCreate: (input: { name: string; service: string; description?: string; fromRecording?: string; openapiDocument?: string }) => Promise<void>
}) {
  const services = environment.services.filter((service) => service.kind === 'process')
  const [name, setName] = useState('')
  const [service, setService] = useState(services[0]?.name || '')
  const [description, setDescription] = useState('')
  const [source, setSource] = useState<'empty' | 'recording' | 'openapi'>('empty')
  const [recording, setRecording] = useState('')
  const [document, setDocument] = useState('')
  const nameInput = useRef<HTMLInputElement>(null)
  return <FormDialog
    className="mock-form-modal"
    titleID="create-mock-title"
    closeLabel="Close create mock"
    closeBlocked={busy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">HTTP MOCK</div><h2 id="create-mock-title">Create Mock Profile</h2></div>}
    onClose={onClose}
  >
    <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); void onCreate({ name: name.trim(), service, description: description.trim(), ...(source === 'recording' ? { fromRecording: recording } : {}), ...(source === 'openapi' ? { openapiDocument: document } : {}) }) }}>
      <p>A profile belongs to one service and can be selected as that service's provider in this environment.</p>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} name="portless-mock-profile-name" required autoComplete="off" spellCheck="false" placeholder="sold-out" value={name} disabled={busy} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => setName(event.target.value)} /></label>
        <label><span>SERVICE</span><select value={service} disabled={busy} onChange={(event) => setService(event.target.value)}>{services.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
        <label className="provider-field--wide"><span>DESCRIPTION</span><input placeholder="Inventory has no available stock" value={description} disabled={busy} onChange={(event) => setDescription(event.target.value)} /></label>
        <label className="provider-field--wide"><span>START WITH</span><select value={source} disabled={busy} onChange={(event) => setSource(event.target.value as typeof source)}><option value="empty">Empty profile</option><option value="recording">Retained recording</option><option value="openapi">OpenAPI document</option></select></label>
        {source === 'recording' && <label className="provider-field--wide"><span>RECORDING</span><select value={recording} disabled={busy} onChange={(event) => setRecording(event.target.value)}><option value="">Choose a stopped recording</option>{recordings.filter((item) => item.status !== 'active').map((item) => <option key={item.name}>{item.name}</option>)}</select></label>}
        {source === 'openapi' && <label className="provider-field--wide mock-file-field"><span>OPENAPI 3.0 OR 3.1</span><input type="file" accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml" disabled={busy} onChange={(event) => { const file = event.target.files?.[0]; if (file) void file.text().then(setDocument) }} /><small>Local files only. External references are not fetched.</small></label>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !name.trim() || !service || (source === 'recording' && !recording) || (source === 'openapi' && !document)}>{busy ? 'CREATING…' : 'CREATE PROFILE'}</button></footer>
    </form>
  </FormDialog>
}

export function RouteModal({ draft, editing, busy, error, onDismissError, onChange, onClose, onSave }: { draft: RouteDraft; editing: boolean; busy: boolean; error: ActionErrorDetails | null; onDismissError: () => void; onChange: (draft: RouteDraft) => void; onClose: () => void; onSave: () => Promise<void> }) {
  const nameInput = useRef<HTMLInputElement>(null)
  const change = <K extends keyof RouteDraft>(key: K, value: RouteDraft[K]) => onChange({ ...draft, [key]: value })
  return <FormDialog
    className="mock-form-modal"
    titleID="mock-route-title"
    closeLabel="Close route editor"
    closeBlocked={busy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">CONFIGURE MOCK</div><h2 id="mock-route-title">{editing ? 'Edit mock route' : 'Add mock route'}</h2></div>}
    onClose={onClose}
  >
    <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); void onSave() }}>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} name="portless-mock-route-name" required autoComplete="off" spellCheck="false" value={draft.name} disabled={busy || editing} placeholder="get-product" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => change('name', event.target.value)} /></label>
        <label><span>METHOD</span><select value={draft.method} disabled={busy} onChange={(event) => change('method', event.target.value)}>{['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((method) => <option key={method}>{method}</option>)}</select></label>
        <label className="provider-field--wide"><span>PATH</span><input value={draft.path} disabled={busy} placeholder="/inventory/{sku}" onChange={(event) => change('path', event.target.value)} /></label>
        <label className="provider-field--wide"><span>REQUIRED QUERY · ONE NAME=VALUE PER LINE</span><textarea value={draft.queryText} disabled={busy} placeholder={'warehouse=central\ninclude=availability'} onChange={(event) => change('queryText', event.target.value)} /></label>
        <label><span>RESPONSE STATUS</span><select value={draft.status} disabled={busy} onChange={(event) => change('status', Number(event.target.value))}>{mockHTTPStatusGroups.map((group) => <optgroup label={group.label} key={group.label}>{group.statuses.map(([code, text]) => <option value={code} key={code}>{code} · {text}</option>)}</optgroup>)}</select></label>
        <label><span>DELAY (MS)</span><input type="number" min="0" max="300000" value={draft.delayMs} disabled={busy} onChange={(event) => change('delayMs', Number(event.target.value))} /></label>
        <label className="provider-field--wide"><span>RESPONSE HEADERS · ONE NAME: VALUE PER LINE</span><textarea value={draft.headersText} disabled={busy} onChange={(event) => change('headersText', event.target.value)} /></label>
        <label className="provider-field--wide"><span>RESPONSE BODY</span><textarea className="mock-body-editor" value={draft.body} disabled={busy} placeholder={'{"available": false}'} onChange={(event) => change('body', event.target.value)} /></label>
        <label className="mock-check-field provider-field--wide"><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={(event) => change('enabled', event.target.checked)} /><span>ENABLED</span></label>
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !draft.name.trim() || !draft.path.startsWith('/')}>{busy ? 'SAVING…' : 'SAVE ROUTE'}</button></footer>
    </form>
  </FormDialog>
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

function formatPairs(value: Record<string, string> | undefined, separator: string) {
  return Object.entries(value || {}).map(([name, item]) => `${name}${separator}${item}`).join('\n')
}

function formatQuerySummary(query?: Record<string, string>) {
  const value = new URLSearchParams(query || {}).toString()
  return value ? `?${value}` : ''
}

function formatTimestamp(value: string) {
  return new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}
