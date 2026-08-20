import { useEffect, useRef, useState } from 'react'
import { api, connectEvents, environmentPath, jsonBody } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { DrawerSizeButton } from '../../components/DrawerSizeButton'
import { StatusMark } from '../../components/Status'
import type { Environment, MockPreview, MockProfile, MockRoute, Recording } from '../../types'

type MockMutation = { mock: MockProfile; warnings: string[] }
type RouteDraft = Pick<MockRoute, 'name' | 'method' | 'path' | 'status' | 'body' | 'delayMs' | 'enabled'> & {
  queryText: string
  headersText: string
}

const emptyRoute = (): RouteDraft => ({
  name: '', method: 'GET', path: '/', status: 200, body: '', delayMs: 0, enabled: true,
  queryText: '', headersText: 'Content-Type: application/json',
})

export const mockHTTPStatusGroups = [
  { label: '2xx Success', statuses: [
    [200, 'OK'], [201, 'Created'], [202, 'Accepted'], [203, 'Non-Authoritative Information'],
    [204, 'No Content'], [205, 'Reset Content'], [206, 'Partial Content'], [207, 'Multi-Status'],
    [208, 'Already Reported'], [226, 'IM Used'],
  ] },
  { label: '3xx Redirection', statuses: [
    [300, 'Multiple Choices'], [301, 'Moved Permanently'], [302, 'Found'], [303, 'See Other'],
    [304, 'Not Modified'], [305, 'Use Proxy'], [307, 'Temporary Redirect'], [308, 'Permanent Redirect'],
  ] },
  { label: '4xx Client Error', statuses: [
    [400, 'Bad Request'], [401, 'Unauthorized'], [402, 'Payment Required'], [403, 'Forbidden'],
    [404, 'Not Found'], [405, 'Method Not Allowed'], [406, 'Not Acceptable'], [407, 'Proxy Authentication Required'],
    [408, 'Request Timeout'], [409, 'Conflict'], [410, 'Gone'], [411, 'Length Required'],
    [412, 'Precondition Failed'], [413, 'Request Entity Too Large'], [414, 'Request URI Too Long'],
    [415, 'Unsupported Media Type'], [416, 'Requested Range Not Satisfiable'], [417, 'Expectation Failed'],
    [418, "I'm a teapot"], [421, 'Misdirected Request'], [422, 'Unprocessable Entity'], [423, 'Locked'],
    [424, 'Failed Dependency'], [425, 'Too Early'], [426, 'Upgrade Required'], [428, 'Precondition Required'],
    [429, 'Too Many Requests'], [431, 'Request Header Fields Too Large'], [451, 'Unavailable For Legal Reasons'],
  ] },
  { label: '5xx Server Error', statuses: [
    [500, 'Internal Server Error'], [501, 'Not Implemented'], [502, 'Bad Gateway'], [503, 'Service Unavailable'],
    [504, 'Gateway Timeout'], [505, 'HTTP Version Not Supported'], [506, 'Variant Also Negotiates'],
    [507, 'Insufficient Storage'], [508, 'Loop Detected'], [510, 'Not Extended'], [511, 'Network Authentication Required'],
  ] },
] as const

export function MocksPanel({ environment, selectedProfile, onSelectProfile }: { environment: Environment; selectedProfile?: string; onSelectProfile: (profile?: string) => void }) {
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

  const refresh = async () => {
    const [mockResult, recordingResult] = await Promise.all([
      api<{ mocks: MockProfile[] }>(environmentPath(environment, '/mocks')),
      api<{ recordings: Recording[] }>(environmentPath(environment, '/recordings')),
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
    <section className="panel mock-profiles-panel">
      <div className="panel-title"><span>MOCK PROFILES</span><button className="button button--primary button--small panel-create-button" type="button" onClick={() => { setCreateOpen(true); setError(null) }}>CREATE PROFILE</button></div>
      <div className="mock-profile-row mock-profile-row--header" role="row"><span>Profile</span><span>Service</span><span>Routes</span><span>State</span><span>Modified</span><span aria-hidden="true" /></div>
      {profiles.map((profile) => {
        const active = environment.bindings?.some((binding) => binding.provider === 'mock' && binding.mock?.profile === profile.name)
        return <div className={`mock-profile-row${selected?.name === profile.name ? ' is-selected' : ''}`} key={profile.name} onClick={() => { setDeleteName(''); setError(null); onSelectProfile(profile.name) }}>
          <div><StatusMark status={active ? 'ready' : 'stopped'} label={false} /><strong>{profile.name}</strong></div>
          <span>{profile.service}</span><span>{profile.routes.length}</span><span>{active ? 'bound' : 'available'}</span>
          <time dateTime={profile.modifiedAt}>{formatTimestamp(profile.modifiedAt)}</time>
          <div className="mock-row-actions table-row-actions"><button type="button" disabled={busy !== ''} onClick={(event) => { event.stopPropagation(); setError(null); onSelectProfile(profile.name) }}>OPEN</button><button className={deleteName === profile.name ? 'is-confirming' : ''} type="button" disabled={busy !== ''} onClick={(event) => { event.stopPropagation(); void removeProfile(profile) }}>{deleteName === profile.name ? 'CONFIRM' : 'DELETE'}</button></div>
        </div>
      })}
      {!loading && profiles.length === 0 && <div className="empty-row">No mock profiles. Create one for a service you do not want to run locally.</div>}
      {loading && <div className="empty-row">Loading mock profiles…</div>}
    </section>

    {selected && <MockProfileDrawer
      environment={environment}
      profile={selected}
      active={!!environment.bindings?.some((binding) => binding.provider === 'mock' && binding.mock?.profile === selected.name)}
      busy={busy}
      deleteName={deleteName}
      error={!createOpen && !routeDraft ? error : null}
      modalOpen={!!routeDraft || previewOpen}
      onDismissError={() => setError(null)}
      onClose={() => { setDeleteName(''); onSelectProfile() }}
      onPreview={() => setPreviewOpen(true)}
      onAddRoute={() => openRoute()}
      onEditRoute={openRoute}
      onDeleteRoute={(route) => { void removeRoute(route) }}
    />}

    {createOpen && <CreateProfileModal environment={environment} recordings={recordings} busy={busy === 'create'} error={error} onDismissError={() => setError(null)} onClose={() => setCreateOpen(false)} onCreate={async (input) => {
      setBusy('create'); setError(null)
      try {
        const created = await api<MockMutation>(environmentPath(environment, '/mocks'), { method: 'POST', ...jsonBody(input) })
        setWarnings(created.warnings || []); setCreateOpen(false); await refresh(); onSelectProfile(created.mock.name)
      } catch (reason) { setError(actionError("Mock profile wasn't created", reason)) }
      finally { setBusy('') }
    }} />}
    {routeDraft && <RouteModal draft={routeDraft} editing={!!routeOriginalName} busy={busy === 'route'} error={error} onDismissError={() => setError(null)} onChange={setRouteDraft} onClose={() => setRouteDraft(null)} onSave={saveRoute} />}
    {previewOpen && selected && <PreviewModal environment={environment} profile={selected} onClose={() => setPreviewOpen(false)} />}
  </div>
}

export function MockProfileDrawer({ environment, profile, active, busy, deleteName, error, modalOpen, onDismissError, onClose, onPreview, onAddRoute, onEditRoute, onDeleteRoute }: {
  environment: Environment
  profile: MockProfile
  active: boolean
  busy: string
  deleteName: string
  error: ActionErrorDetails | null
  modalOpen: boolean
  onDismissError: () => void
  onClose: () => void
  onPreview: () => void
  onAddRoute: () => void
  onEditRoute: (route: MockRoute) => void
  onDeleteRoute: (route: MockRoute) => void
}) {
  const [fullScreen, setFullScreen] = useState(false)
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || modalOpen || busy) return
      if (fullScreen) setFullScreen(false)
      else onClose()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [busy, fullScreen, modalOpen, onClose])
  return <div className="drawer-backdrop" role="presentation" onMouseDown={() => { if (!busy && !modalOpen) onClose() }}>
    <aside className={`drawer mock-profile-drawer${fullScreen ? ' drawer--fullscreen' : ''}`} role="dialog" aria-modal="true" aria-label={`${profile.name} mock profile`} onMouseDown={(event) => event.stopPropagation()}>
      <header>
        <div><span className="eyebrow">{environment.project} / {environment.name} / mock profile</span><div className="mock-profile-drawer__title"><h2>{profile.name}</h2><StatusMark status={active ? 'ready' : 'stopped'} label={false} /><small>{active ? 'bound' : 'available'}</small></div></div>
        <div className="drawer-header-actions"><DrawerSizeButton fullScreen={fullScreen} subject={`${profile.name} mock profile`} onToggle={() => setFullScreen((value) => !value)} /><button className="icon-button" type="button" disabled={!!busy} onClick={onClose} aria-label="Close mock profile">×</button></div>
      </header>
      <div className="drawer-actions" aria-busy={!!busy}><button className="button" type="button" disabled={!!busy} onClick={onPreview}>PREVIEW REQUEST</button><button className="button button--primary" type="button" disabled={!!busy} onClick={onAddRoute}>ADD ROUTE</button></div>
      {error && <div className="mock-profile-drawer__error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}
      <div className="drawer-content">
        <div className="mock-profile-summary"><div><span>SERVICE</span><strong>{profile.service}</strong></div><div><span>DESCRIPTION</span><strong>{profile.description || '—'}</strong></div><div><span>FALLBACK</span><strong>501 · no route matched</strong></div><div><span>MODIFIED</span><strong>{formatTimestamp(profile.modifiedAt)}</strong></div></div>
        <section className="mock-route-table" aria-label={`${profile.name} routes`}>
          <div className="mock-route-table__title"><span>ROUTES</span><small>{profile.routes.length}</small></div>
          <div className="mock-route-row mock-route-row--header"><span>Route</span><span>Match</span><span>Response</span><span>Delay</span><span>State</span><span aria-hidden="true" /></div>
          {profile.routes.map((route) => <div className="mock-route-row" key={route.name}>
            <strong>{route.name}</strong><code>{route.method} {route.path}{formatQuerySummary(route.query)}</code><span>{route.status}{route.body ? ` · ${new Blob([route.body]).size} B` : ''}</span><span>{route.delayMs ? `${route.delayMs} ms` : '—'}</span><StatusMark status={route.enabled ? 'ready' : 'stopped'} />
            <div className="mock-row-actions table-row-actions"><button type="button" disabled={!!busy} onClick={() => onEditRoute(route)}>EDIT</button><button className={deleteName === `delete-route:${route.name}` ? 'is-confirming' : ''} type="button" disabled={!!busy} onClick={() => onDeleteRoute(route)}>{deleteName === `delete-route:${route.name}` ? 'CONFIRM' : 'DELETE'}</button></div>
          </div>)}
          {profile.routes.length === 0 && <div className="empty-row">This profile has no routes. Unmatched requests return 501 so missing behavior is visible.</div>}
        </section>
      </div>
    </aside>
  </div>
}

function CreateProfileModal({ environment, recordings, busy, error, onDismissError, onClose, onCreate }: {
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
  useModalEscape(busy, onClose)
  useEffect(() => { nameInput.current?.focus() }, [])
  return <div className="modal-backdrop form-modal-backdrop" role="presentation" onMouseDown={() => !busy && onClose()}><section className="form-modal mock-form-modal" role="dialog" aria-modal="true" aria-labelledby="create-mock-title" onMouseDown={(event) => event.stopPropagation()}>
    <header><div><div className="eyebrow">HTTP MOCK</div><h2 id="create-mock-title">Create mock profile</h2></div><button className="icon-button" type="button" aria-label="Close create mock" disabled={busy} onClick={onClose}>×</button></header>
    <form onSubmit={(event) => { event.preventDefault(); void onCreate({ name: name.trim(), service, description: description.trim(), ...(source === 'recording' ? { fromRecording: recording } : {}), ...(source === 'openapi' ? { openapiDocument: document } : {}) }) }}>
      <p>A profile belongs to one service and can be selected as that service's provider in this environment.</p>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} name="mock-profile-name" autoComplete="off" placeholder="sold-out" value={name} disabled={busy} onChange={(event) => setName(event.target.value)} /></label>
        <label><span>SERVICE</span><select value={service} disabled={busy} onChange={(event) => setService(event.target.value)}>{services.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
        <label className="provider-field--wide"><span>DESCRIPTION</span><input placeholder="Inventory has no available stock" value={description} disabled={busy} onChange={(event) => setDescription(event.target.value)} /></label>
        <label className="provider-field--wide"><span>START WITH</span><select value={source} disabled={busy} onChange={(event) => setSource(event.target.value as typeof source)}><option value="empty">Empty profile</option><option value="recording">Retained recording</option><option value="openapi">OpenAPI document</option></select></label>
        {source === 'recording' && <label className="provider-field--wide"><span>RECORDING</span><select value={recording} disabled={busy} onChange={(event) => setRecording(event.target.value)}><option value="">Choose a stopped recording</option>{recordings.filter((item) => item.status !== 'active').map((item) => <option key={item.name}>{item.name}</option>)}</select></label>}
        {source === 'openapi' && <label className="provider-field--wide mock-file-field"><span>OPENAPI 3.0 OR 3.1</span><input type="file" accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml" disabled={busy} onChange={(event) => { const file = event.target.files?.[0]; if (file) void file.text().then(setDocument) }} /><small>Local files only. External references are not fetched.</small></label>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !name.trim() || !service || (source === 'recording' && !recording) || (source === 'openapi' && !document)}>{busy ? 'CREATING…' : 'CREATE PROFILE'}</button></footer>
    </form>
  </section></div>
}

function RouteModal({ draft, editing, busy, error, onDismissError, onChange, onClose, onSave }: { draft: RouteDraft; editing: boolean; busy: boolean; error: ActionErrorDetails | null; onDismissError: () => void; onChange: (draft: RouteDraft) => void; onClose: () => void; onSave: () => Promise<void> }) {
  useModalEscape(busy, onClose)
  const change = <K extends keyof RouteDraft>(key: K, value: RouteDraft[K]) => onChange({ ...draft, [key]: value })
  return <div className="modal-backdrop form-modal-backdrop" role="presentation" onMouseDown={() => !busy && onClose()}><section className="form-modal mock-form-modal" role="dialog" aria-modal="true" aria-labelledby="mock-route-title" onMouseDown={(event) => event.stopPropagation()}>
    <header><div><div className="eyebrow">CONFIGURE MOCK</div><h2 id="mock-route-title">{editing ? 'Edit mock route' : 'Add mock route'}</h2></div><button className="icon-button" type="button" aria-label="Close route editor" disabled={busy} onClick={onClose}>×</button></header>
    <form onSubmit={(event) => { event.preventDefault(); void onSave() }}>
      <div className="form-modal__fields">
        <label><span>NAME</span><input value={draft.name} disabled={busy || editing} placeholder="get-product" onChange={(event) => change('name', event.target.value)} /></label>
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
  </section></div>
}

function PreviewModal({ environment, profile, onClose }: { environment: Environment; profile: MockProfile; onClose: () => void }) {
  const [method, setMethod] = useState('GET')
  const [target, setTarget] = useState('/')
  const [headersText, setHeadersText] = useState('')
  const [body, setBody] = useState('')
  const [result, setResult] = useState<MockPreview | null>(null)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [busy, setBusy] = useState(false)
  const supportsBody = mockRequestSupportsBody(method)
  useModalEscape(busy, onClose)
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
  return <div className="modal-backdrop form-modal-backdrop" role="presentation" onMouseDown={() => !busy && onClose()}><section className="form-modal mock-form-modal" role="dialog" aria-modal="true" aria-labelledby="mock-preview-title" onMouseDown={(event) => event.stopPropagation()}>
    <header><div><div className="eyebrow">{profile.name}</div><h2 id="mock-preview-title">Preview request</h2></div><button className="icon-button" type="button" aria-label="Close request preview" disabled={busy} onClick={onClose}>×</button></header>
    <form onSubmit={(event) => { event.preventDefault(); void preview() }}>
      <p>Evaluate the matcher without sending traffic or changing trace history.</p>
      <div className="form-modal__fields">
        <label><span>METHOD</span><select value={method} disabled={busy} onChange={(event) => { setMethod(event.target.value); setResult(null) }}>{['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((item) => <option key={item}>{item}</option>)}</select></label>
        <label><span>PATH AND QUERY</span><input value={target} disabled={busy} onChange={(event) => { setTarget(event.target.value); setResult(null) }} /></label>
        <label className="provider-field--wide"><span>REQUEST HEADERS · ONE NAME: VALUE PER LINE</span><textarea value={headersText} disabled={busy} placeholder={'Content-Type: application/json\nX-Request-ID: preview-123'} onChange={(event) => { setHeadersText(event.target.value); setResult(null) }} /></label>
        {supportsBody && <label className="provider-field--wide"><span>REQUEST BODY</span><textarea className="mock-body-editor" value={body} maxLength={262144} disabled={busy} placeholder={'{"sku":"coffee-mug"}'} onChange={(event) => { setBody(event.target.value); setResult(null) }} /></label>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
      {result && <div className={`mock-preview-result${result.matched ? '' : ' is-unmatched'}`}><span>{result.matched ? `MATCHED · ${result.route}` : 'NO MATCH'}</span><strong>{result.status}</strong><pre>{result.body || '(empty response body)'}</pre></div>}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CLOSE</button><button className="button button--primary" type="submit" disabled={busy || !target.startsWith('/')}>{busy ? 'MATCHING…' : 'PREVIEW'}</button></footer>
    </form>
  </section></div>
}

function useModalEscape(busy: boolean, close: () => void) {
  useEffect(() => {
    const listener = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) close() }
    document.addEventListener('keydown', listener)
    return () => document.removeEventListener('keydown', listener)
  }, [busy, close])
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
