import { useEffect, useMemo, useRef, useState } from 'react'
import type { Environment } from '../../api/contracts/environments'
import type { Recording } from '../../api/contracts/experiments'
import type { MockProfile, MockRoute } from '../../api/contracts/mocks'
import { ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { httpStatusGroups } from '../httpStatuses'

const mockMethods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const
const mockNamePattern = '[a-z0-9][a-z0-9._-]{0,63}'
const pathParameter = /^\{[A-Za-z_][A-Za-z0-9_]*\}$/

type MockCreationSource = 'routes' | 'recording' | 'openapi'

export interface MockCreationRouteDraft {
  key: string
  name: string
  nameCustomized: boolean
  method: string
  path: string
  queryText: string
  status: number
  headersText: string
  body: string
  delayMs: number
  enabled: boolean
}

export interface MockCreationInput {
  profile: {
    name: string
    service: string
    description?: string
    fromRecording?: string
    openapiDocument?: string
  }
  routes: MockCreationRouteDraft[]
  activate: boolean
}

export interface MockDraftPreview {
  matched: boolean
  route?: string
  status: number
  body?: string
  delayMs?: number
}

type RouteWorkbenchProps = {
  routes: MockCreationRouteDraft[]
  selectedKey: string
  busy: boolean
  nameLocked?: boolean
  focusPath?: boolean
  onChange: (route: MockCreationRouteDraft) => void
  onSelect: (route: MockCreationRouteDraft) => void
  onAdd: () => void
  onRemove?: (route: MockCreationRouteDraft) => void
}

export function MockCreationWorkspace({ environment, recordings, busy, activationBlocked, error, onDismissError, onCancel, onCreate }: {
  environment: Environment
  recordings: Recording[]
  busy: boolean
  activationBlocked: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onCancel: () => void
  onCreate: (input: MockCreationInput) => Promise<void>
}) {
  const services = useMemo(() => environment.services.filter((item) => item.kind === 'process'), [environment.services])
  const nextRouteID = useRef(2)
  const [name, setName] = useState('')
  const [service, setService] = useState(services[0]?.name || '')
  const [description, setDescription] = useState('')
  const [source, setSource] = useState<MockCreationSource>('routes')
  const [recording, setRecording] = useState('')
  const [document, setDocument] = useState('')
  const [documentName, setDocumentName] = useState('')
  const [activate, setActivate] = useState(false)
  const [routes, setRoutes] = useState<MockCreationRouteDraft[]>(() => [newMockRouteDraft(1)])
  const [selectedRoute, setSelectedRoute] = useState('draft-route-1')
  const retainedRecordings = recordings.filter((item) => item.status !== 'active')

  useEffect(() => {
    if (activationBlocked) setActivate(false)
  }, [activationBlocked])

  const addRoute = () => {
    const route = newMockRouteDraft(nextRouteID.current++)
    setRoutes((current) => [...current, route])
    setSelectedRoute(route.key)
  }

  const removeRoute = (route: MockCreationRouteDraft) => {
    if (routes.length === 1) return
    const index = routes.findIndex((item) => item.key === route.key)
    const next = routes.filter((item) => item.key !== route.key)
    setRoutes(next)
    if (selectedRoute === route.key) setSelectedRoute(next[Math.min(index, next.length - 1)].key)
  }

  const sourceReady = source === 'routes' || (source === 'recording' && !!recording) || (source === 'openapi' && !!document)
  const routesReady = source !== 'routes' || (routes.length > 0 && routes.every((route) => route.name.trim() && route.path.startsWith('/')))
  const ready = !!name.trim() && !!service && sourceReady && routesReady

  return <section className="panel mock-create-workspace" aria-labelledby="mock-create-workspace-title">
    <header className="mock-create-workspace__header">
      <div>
        <div className="eyebrow">MOCKS / NEW</div>
        <div className="mock-create-workspace__title"><h2 id="mock-create-workspace-title">Create mock</h2><span>{activate ? 'DRAFT · WILL ENABLE' : 'DRAFT · INACTIVE'}</span></div>
        <p>Choose a service, then describe only the requests and responses this mock needs.</p>
      </div>
      <button className="button button--quiet" type="button" disabled={busy} onClick={onCancel}>BACK TO MOCKS</button>
    </header>

    <form className="mock-create-workspace__form" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => {
      event.preventDefault()
      if (!ready) return
      void onCreate({
        profile: {
          name: name.trim(), service, ...(description.trim() ? { description: description.trim() } : {}),
          ...(source === 'recording' ? { fromRecording: recording } : {}),
          ...(source === 'openapi' ? { openapiDocument: document } : {}),
        },
        routes: source === 'routes' ? routes : [],
        activate,
      })
    }}>
      <section className="mock-create-setup" aria-label="Mock setup">
        <div className="mock-create-fields">
          <label><span>NAME</span><input autoFocus name="portless-mock-profile-name" required pattern={mockNamePattern} maxLength={64} autoComplete="off" spellCheck="false" placeholder="sold-out" value={name} disabled={busy} title="Use a lowercase URL-safe name." data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => setName(event.target.value)} /></label>
          <label><span>SERVICE</span><select aria-label="SERVICE" value={service} disabled={busy} onChange={(event) => setService(event.target.value)}>{services.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
          <label className="mock-create-fields__description"><span>DESCRIPTION</span><input placeholder="Inventory has no available stock" value={description} disabled={busy} onChange={(event) => setDescription(event.target.value)} /></label>
          <label><span>START WITH</span><select aria-label="START WITH" value={source} disabled={busy} onChange={(event) => setSource(event.target.value as MockCreationSource)}><option value="routes">Build routes</option><option value="recording">Recording</option><option value="openapi">OpenAPI</option></select></label>
        </div>
        {source === 'recording' && <div className="mock-create-import"><label><span>RECORDING</span><select aria-label="RECORDING" value={recording} disabled={busy} onChange={(event) => setRecording(event.target.value)}><option value="">Choose a stopped recording</option>{retainedRecordings.map((item) => <option value={item.name} key={item.name}>{item.name} · {item.eventCount} exchanges</option>)}</select></label><p>Matching HTTP exchanges become deterministic routes. Conflicts are reported after creation.</p></div>}
        {source === 'openapi' && <div className="mock-create-import"><label className="mock-file-field"><span>OPENAPI 3.0 OR 3.1</span><input aria-label="OPENAPI 3.0 OR 3.1" type="file" accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml" disabled={busy} onChange={(event) => { const file = event.target.files?.[0]; setDocumentName(file?.name || ''); setDocument(''); if (file) void file.text().then(setDocument) }} /><small>{documentName || 'Local files only. External references are not fetched.'}</small></label><p>Operations with response examples become routes that you can refine after creation.</p></div>}
      </section>

      <div className={`mock-create-safety${activate ? ' is-activating' : ''}`} role="note">
        <strong>{activate ? 'WILL ENABLE' : 'INACTIVE'}</strong>
        <span>{activate ? `Portless switches ${service || 'the service'} only after every route is saved.` : `${service || 'The service'} keeps its current provider.`}</span>
      </div>

      {source === 'routes' ? <RouteWorkbench
        routes={routes}
        selectedKey={selectedRoute}
        busy={busy}
        onChange={(route) => setRoutes((current) => current.map((item) => item.key === route.key ? route : item))}
        onSelect={(route) => setSelectedRoute(route.key)}
        onAdd={addRoute}
        onRemove={removeRoute}
      /> : <section className="mock-import-summary" aria-label="Generated routes"><div><span>ROUTES</span><strong>Generated after validation</strong></div><p>{source === 'recording' ? 'Portless will convert the selected recording into ordered route matchers.' : 'Portless will use response examples from the selected OpenAPI document.'} The service remains unchanged unless you enable the result below.</p></section>}

      {error && <div className="mock-create-workspace__error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}
      <footer className="mock-create-workspace__footer">
        <label className={`mock-create-activation${activate ? ' is-selected' : ''}${activationBlocked ? ' is-disabled' : ''}`}><input type="checkbox" checked={activate} disabled={busy || activationBlocked} onChange={(event) => setActivate(event.target.checked)} /><span><strong>Enable for {service || 'this service'} after creation</strong><small>{activationBlocked ? `Activation is unavailable while the environment is ${environment.status}.` : 'Routes save first; the provider switch happens last.'}</small></span></label>
        <div><button className="button button--quiet" type="button" disabled={busy} onClick={onCancel}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !ready}>{busy ? activate ? 'CREATING & ENABLING…' : 'CREATING…' : activate ? 'CREATE & ENABLE' : 'CREATE MOCK'}</button></div>
      </footer>
    </form>
  </section>
}

export function MockRouteWorkspace({ profile, routeName, busy, error, onDismissError, onCancel, onOpenRoute, onSave }: {
  profile: MockProfile
  routeName?: string
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onCancel: () => void
  onOpenRoute: (routeName?: string) => void
  onSave: (route: MockCreationRouteDraft, originalName?: string) => Promise<void>
}) {
  const existing = routeName ? profile.routes.find((route) => route.name === routeName) : undefined
  const missing = !!routeName && !existing
  const [draft, setDraft] = useState<MockCreationRouteDraft>(() => existing ? mockRouteDraft(existing, profile.routes.indexOf(existing) + 1) : newMockRouteDraft(profile.routes.length + 1))

  const routes = profile.routes.map((route, index) => route.name === existing?.name ? draft : mockRouteDraft(route, index + 1))
  if (!existing && !missing) routes.push(draft)
  const ready = !!draft.name.trim() && draft.path.startsWith('/')
  const title = existing ? 'Edit Route' : 'Create Route'

  return <section className="panel mock-create-workspace mock-route-workspace" aria-labelledby="mock-route-workspace-title">
    <header className="mock-create-workspace__header">
      <div>
        <div className="eyebrow">{profile.name} / {existing ? draft.name : 'NEW ROUTE'}</div>
        <div className="mock-create-workspace__title"><h2 id="mock-route-workspace-title">{title}</h2><span>{profile.service}</span></div>
        <p>{existing ? 'Refine the request matcher or response without changing the route identity.' : `Add one request and response to ${profile.name}.`}</p>
      </div>
      <button className="button button--quiet" type="button" disabled={busy} onClick={onCancel}>BACK TO {profile.name.toUpperCase()}</button>
    </header>

    {missing ? <div className="mock-route-workspace__missing"><strong>ROUTE NOT FOUND</strong><p>{routeName} is no longer part of this mock profile.</p><button className="button" type="button" onClick={onCancel}>BACK TO PROFILE</button></div> : <form className="mock-create-workspace__form" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); if (ready) void onSave(draft, existing?.name) }}>
      <RouteWorkbench
        routes={routes}
        selectedKey={draft.key}
        busy={busy}
        nameLocked={!!existing}
        focusPath={!existing}
        onChange={setDraft}
        onSelect={(route) => {
          if (route.key === draft.key) return
          onOpenRoute(route.name)
        }}
        onAdd={() => onOpenRoute()}
      />
      {error && <div className="mock-create-workspace__error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}
      <footer className="mock-create-workspace__footer mock-route-workspace__footer">
        <span>Changes apply to <strong>{profile.name}</strong>; enabling the profile remains a separate action.</span>
        <div><button className="button button--quiet" type="button" disabled={busy} onClick={onCancel}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !ready}>{busy ? 'SAVING…' : 'SAVE ROUTE'}</button></div>
      </footer>
    </form>}
  </section>
}

function RouteWorkbench({ routes, selectedKey, busy, nameLocked = false, focusPath = false, onChange, onSelect, onAdd, onRemove }: RouteWorkbenchProps) {
  const route = routes.find((item) => item.key === selectedKey) || routes[0]
  const routeIndex = Math.max(0, routes.findIndex((item) => item.key === route?.key))
  const [previewOpen, setPreviewOpen] = useState(false)
  const [maximized, setMaximized] = useState(false)
  const [previewMethod, setPreviewMethod] = useState(route?.method || 'GET')
  const [previewTarget, setPreviewTarget] = useState(route ? mockRoutePreviewTarget(route) : '/')
  const [previewResult, setPreviewResult] = useState<MockDraftPreview | null>(null)
  const [previewError, setPreviewError] = useState('')

  useEffect(() => {
    if (!route) return
    setPreviewMethod(route.method)
    setPreviewTarget(mockRoutePreviewTarget(route))
    setPreviewResult(null)
    setPreviewError('')
  }, [route])

  useEffect(() => {
    if (!maximized) return
    const restore = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMaximized(false)
    }
    window.addEventListener('keydown', restore)
    return () => window.removeEventListener('keydown', restore)
  }, [maximized])

  if (!route) return null

  const change = <K extends keyof MockCreationRouteDraft>(key: K, value: MockCreationRouteDraft[K]) => onChange({ ...route, [key]: value })
  const changeEndpoint = (field: 'method' | 'path', value: string) => {
    const previousSuggestion = suggestMockRouteName(route.method, route.path, routeIndex + 1)
    const next = { ...route, [field]: value }
    if (!nameLocked && (!route.nameCustomized || route.name === previousSuggestion)) {
      next.name = suggestMockRouteName(next.method, next.path, routeIndex + 1)
      next.nameCustomized = false
    }
    onChange(next)
  }
  const preview = () => {
    setPreviewError('')
    try {
      setPreviewResult(previewMockDraftRoutes(routes, previewMethod, previewTarget))
    } catch (reason) {
      setPreviewResult(null)
      setPreviewError(reason instanceof Error ? reason.message : String(reason))
    }
  }

  return <div className={`mock-route-builder${maximized ? ' is-maximized' : ''}`} role="region" aria-label="Route editor">
    <aside className="mock-route-builder__list" aria-label="Routes">
      <div className="mock-route-builder__list-header"><span>ROUTES <small>{routes.length}</small></span><button type="button" disabled={busy} onClick={onAdd}>+ ADD ROUTE</button></div>
      <div role="tablist" aria-label="Mock routes">
        {routes.map((item) => <div className={`mock-route-draft${item.key === route.key ? ' is-selected' : ''}`} key={item.key}>
          <button type="button" role="tab" aria-selected={item.key === route.key} disabled={busy} onClick={() => onSelect(item)}><span><b>{item.method}</b><code>{item.path || '/'}</code></span><small>{item.status} · {item.enabled ? 'enabled' : 'disabled'}</small></button>
          {onRemove && <button className="mock-route-draft__remove" type="button" aria-label={`Remove ${item.name || 'draft route'}`} title="Remove route" disabled={busy || routes.length === 1} onClick={() => onRemove(item)}>×</button>}
        </div>)}
      </div>
    </aside>

    <section className="mock-route-builder__editor" aria-label={`Edit ${route.name}`}>
      <header>
        <div><span>ROUTE</span><strong>{route.method} <code>{route.path || '/'}</code></strong></div>
        <div className="mock-route-editor__toolbar">
          <label className="mock-route-enabled"><input type="checkbox" checked={route.enabled} disabled={busy} onChange={(event) => change('enabled', event.target.checked)} /><span>ENABLED</span></label>
          <button className="mock-route-editor__maximize" type="button" aria-pressed={maximized} aria-label={maximized ? 'Restore route editor' : 'Maximize route editor'} onClick={() => setMaximized((value) => !value)}>{maximized ? 'RESTORE' : 'MAXIMIZE'}</button>
        </div>
      </header>

      <div className="mock-route-editor__canvas">
        <section className="mock-route-editor__request" aria-labelledby={`request-${route.key}`}>
          <h3 id={`request-${route.key}`}>REQUEST</h3>
          <div>
            <label><span>METHOD</span><select aria-label="METHOD" value={route.method} disabled={busy} onChange={(event) => changeEndpoint('method', event.target.value)}>{mockMethods.map((method) => <option key={method}>{method}</option>)}</select></label>
            <label className="mock-route-builder__path"><span>PATH</span><input autoFocus={focusPath} aria-label="PATH" required value={route.path} disabled={busy} placeholder="/inventory/{sku}" onChange={(event) => changeEndpoint('path', event.target.value)} /></label>
          </div>
        </section>

        <section className="mock-route-editor__response" aria-labelledby={`response-${route.key}`}>
          <div className="mock-route-editor__response-header"><h3 id={`response-${route.key}`}>RESPONSE</h3><label><span>STATUS</span><select aria-label="RESPONSE STATUS" value={route.status} disabled={busy} onChange={(event) => change('status', Number(event.target.value))}>{httpStatusGroups.map((group) => <optgroup label={group.label} key={group.label}>{group.statuses.map(([code, text]) => <option value={code} key={code}>{code} · {text}</option>)}</optgroup>)}</select></label></div>
          <label className="mock-route-editor__body"><span>BODY</span><textarea aria-label="RESPONSE BODY" value={route.body} disabled={busy} placeholder={'{"available": false}'} onChange={(event) => change('body', event.target.value)} /></label>
        </section>

        <details className="mock-route-builder__advanced">
          <summary>ADVANCED MATCHING &amp; RESPONSE</summary>
          <div>
            <label><span>ROUTE NAME</span><input aria-label="ROUTE NAME" name="portless-mock-route-name" required pattern={mockNamePattern} maxLength={64} autoComplete="off" spellCheck="false" value={route.name} disabled={busy || nameLocked} title={nameLocked ? "Route names can't be changed in place." : undefined} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => onChange({ ...route, name: event.target.value, nameCustomized: true })} /></label>
            <label><span>DELAY (MS)</span><input aria-label="DELAY (MS)" type="number" min="0" max="300000" value={route.delayMs} disabled={busy} onChange={(event) => change('delayMs', Number(event.target.value))} /></label>
            <label><span>REQUIRED QUERY · ONE NAME=VALUE PER LINE</span><textarea aria-label="REQUIRED QUERY · ONE NAME=VALUE PER LINE" value={route.queryText} disabled={busy} placeholder={'warehouse=central\ninclude=availability'} onChange={(event) => change('queryText', event.target.value)} /></label>
            <label><span>RESPONSE HEADERS · ONE NAME: VALUE PER LINE</span><textarea aria-label="RESPONSE HEADERS · ONE NAME: VALUE PER LINE" value={route.headersText} disabled={busy} onChange={(event) => change('headersText', event.target.value)} /></label>
          </div>
        </details>

        <div className="mock-route-editor__preview-toggle"><button className="button button--quiet" type="button" aria-expanded={previewOpen} disabled={busy} onClick={() => setPreviewOpen((value) => !value)}>{previewOpen ? 'HIDE PREVIEW' : 'PREVIEW'}</button></div>
        {previewOpen && <section className="mock-route-editor__preview" aria-label="Draft preview">
          <div><select aria-label="PREVIEW METHOD" value={previewMethod} disabled={busy} onChange={(event) => { setPreviewMethod(event.target.value); setPreviewResult(null) }}>{mockMethods.map((method) => <option key={method}>{method}</option>)}</select><input aria-label="PREVIEW PATH AND QUERY" value={previewTarget} disabled={busy} onChange={(event) => { setPreviewTarget(event.target.value); setPreviewResult(null); setPreviewError('') }} /><button className="button" type="button" disabled={busy || !previewTarget.startsWith('/')} onClick={preview}>RUN</button></div>
          <p>Matches locally against every route in this draft. No traffic is sent.</p>
          {previewError && <div className="mock-draft-preview-error" role="alert">{previewError}</div>}
          {previewResult && <div className={`mock-draft-preview-result${previewResult.matched ? '' : ' is-unmatched'}`} aria-live="polite"><span>{previewResult.matched ? `MATCHED · ${previewResult.route}` : 'NO MATCH · FALLBACK'}</span><strong>{previewResult.status}</strong><pre>{previewResult.body || '(empty response body)'}</pre>{!!previewResult.delayMs && <small>{previewResult.delayMs} ms delay</small>}</div>}
        </section>}
      </div>
    </section>
  </div>
}

export function newMockRouteDraft(sequence: number): MockCreationRouteDraft {
  const path = sequence === 1 ? '/' : `/route-${sequence}`
  return {
    key: `draft-route-${sequence}`,
    name: suggestMockRouteName('GET', path, sequence),
    nameCustomized: false,
    method: 'GET',
    path,
    queryText: '',
    status: 200,
    headersText: 'Content-Type: application/json',
    body: '',
    delayMs: 0,
    enabled: true,
  }
}

export function mockRouteDraft(route: MockRoute, sequence: number): MockCreationRouteDraft {
  return {
    key: `saved-route-${sequence}`,
    name: route.name,
    nameCustomized: true,
    method: route.method,
    path: route.path,
    queryText: formatDraftPairs(route.query, '='),
    status: route.status,
    headersText: formatDraftPairs(route.headers, ': '),
    body: route.body || '',
    delayMs: route.delayMs || 0,
    enabled: route.enabled,
  }
}

export function suggestMockRouteName(method: string, path: string, fallback: number) {
  const parts = path.split('/').filter(Boolean).map((part) => {
    if (pathParameter.test(part)) return `by-${part.slice(1, -1).toLowerCase()}`
    return part.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '')
  }).filter(Boolean)
  const suffix = parts.length ? parts.join('-') : 'root'
  const suggestion = `${method.toLowerCase()}-${suffix}`.replace(/-+/g, '-').slice(0, 64).replace(/[-._]+$/g, '')
  return suggestion || `route-${fallback}`
}

export function previewMockDraftRoutes(routes: MockCreationRouteDraft[], method: string, target: string): MockDraftPreview {
  if (!target.startsWith('/')) throw new Error('Preview path must start with /.')
  const request = new URL(target, 'http://mock.localhost')
  const requestSegments = splitMockPath(request.pathname)
  const candidates = routes.map((route) => ({
    route,
    segments: splitMockPath(route.path),
    query: parseDraftPairs(route.queryText, '='),
    literalCount: splitMockPath(route.path).filter((segment) => !pathParameter.test(segment)).length,
  })).sort((left, right) => right.literalCount - left.literalCount || Object.keys(right.query).length - Object.keys(left.query).length || right.segments.length - left.segments.length || left.route.name.localeCompare(right.route.name, undefined, { sensitivity: 'base' }))

  for (const candidate of candidates) {
    if (!candidate.route.enabled || candidate.route.method.toUpperCase() !== method.toUpperCase() || candidate.segments.length !== requestSegments.length) continue
    if (!candidate.segments.every((segment, index) => pathParameter.test(segment) ? !!requestSegments[index] : segment === requestSegments[index])) continue
    const queryMatches = Object.entries(candidate.query).every(([name, expected]) => {
      const values = request.searchParams.getAll(name)
      return values.length > 0 && (!expected || values.includes(expected))
    })
    if (!queryMatches) continue
    return { matched: true, route: candidate.route.name, status: candidate.route.status, body: candidate.route.body, delayMs: candidate.route.delayMs }
  }
  return { matched: false, status: 501 }
}

function mockRoutePreviewTarget(route: MockCreationRouteDraft) {
  const path = route.path.startsWith('/') ? route.path.replace(/\{[A-Za-z_][A-Za-z0-9_]*\}/g, 'sample') : '/'
  try {
    const query = new URLSearchParams(parseDraftPairs(route.queryText, '='))
    const suffix = query.toString()
    return suffix ? `${path}?${suffix}` : path
  } catch {
    return path
  }
}

function parseDraftPairs(value: string, separator: ':' | '=') {
  const result: Record<string, string> = {}
  for (const line of value.split('\n').map((item) => item.trim()).filter(Boolean)) {
    const index = line.indexOf(separator)
    if (index < 1) throw new Error(`Expected ${separator === ':' ? 'Name: value' : 'name=value'} on every non-empty line.`)
    result[line.slice(0, index).trim()] = line.slice(index + 1).trim()
  }
  return result
}

function formatDraftPairs(value: Record<string, string> | undefined, separator: string) {
  return Object.entries(value || {}).map(([name, item]) => `${name}${separator}${item}`).join('\n')
}

function splitMockPath(path: string) {
  if (path === '/') return []
  return path.replace(/^\//, '').split('/')
}
