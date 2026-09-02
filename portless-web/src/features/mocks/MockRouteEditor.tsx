import { useEffect, useState } from 'react'
import type { MockRoute, MockScenario } from '../../api/contracts/mocks'
import { ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { httpStatusGroups } from '../httpStatuses'

const mockMethods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const
const mockNamePattern = '[a-z0-9][a-z0-9._-]{0,63}'
const pathParameter = /^\{[A-Za-z_][A-Za-z0-9_]*\}$/

export interface MockRouteDraft {
  name: string
  nameCustomized: boolean
  service: string
  method: string
  path: string
  queryText: string
  status: number
  headersText: string
  body: string
  delayMs: number
  enabled: boolean
}

export function MockRouteEditor({ scenario, services, routeName, busy, error, onDismissError, onCancel, onSave }: {
  scenario: MockScenario
  services: string[]
  routeName?: string
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onCancel: () => void
  onSave: (route: MockRouteDraft, originalName?: string) => Promise<void>
}) {
  const existing = routeName ? scenario.routes.find((route) => route.name === routeName) : undefined
  const missing = !!routeName && !existing
  const defaultService = existing?.service || services[0] || ''
  const [draft, setDraft] = useState<MockRouteDraft>(() => existing ? mockRouteDraft(existing) : newMockRouteDraft(scenario.routes.length + 1, defaultService))
  const [maximized, setMaximized] = useState(false)
  const title = existing ? 'Edit Route' : 'Create Route'
  const ready = !!draft.name.trim() && !!draft.service && draft.path.startsWith('/')

  useEffect(() => {
    if (!maximized) return
    const restore = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMaximized(false)
    }
    window.addEventListener('keydown', restore)
    return () => window.removeEventListener('keydown', restore)
  }, [maximized])

  const change = <K extends keyof MockRouteDraft>(key: K, value: MockRouteDraft[K]) => setDraft((current) => ({ ...current, [key]: value }))
  const changeEndpoint = (field: 'method' | 'path', value: string) => {
    setDraft((current) => {
      const previousSuggestion = suggestMockRouteName(current.method, current.path, scenario.routes.length + 1)
      const next = { ...current, [field]: value }
      if (!existing && (!current.nameCustomized || current.name === previousSuggestion)) {
        next.name = suggestMockRouteName(next.method, next.path, scenario.routes.length + 1)
        next.nameCustomized = false
      }
      return next
    })
  }

  return <section className={`panel mock-route-workspace${maximized ? ' is-maximized' : ''}`} role="region" aria-label={title} aria-labelledby="mock-route-workspace-title">
    <header className="mock-workspace-header">
      <div>
        <div className="eyebrow">{scenario.name} / {existing ? existing.name : 'NEW ROUTE'}</div>
        <div className="mock-workspace-title"><h2 id="mock-route-workspace-title">{title}</h2></div>
        <p>{existing ? 'Change the service, request match, or response.' : `Add one service request and response to ${scenario.name}.`}</p>
      </div>
      <div className="mock-workspace-header__actions">
        <button className="button button--quiet" type="button" disabled={busy} onClick={onCancel}>BACK TO SCENARIO</button>
        {!missing && <button className="button mock-route-maximize" type="button" aria-pressed={maximized} aria-label={maximized ? 'Restore route editor' : 'Maximize route editor'} onClick={() => setMaximized((value) => !value)}>{maximized ? 'RESTORE' : 'MAXIMIZE'}</button>}
      </div>
    </header>

    {missing ? <div className="mock-workspace-missing"><strong>ROUTE NOT FOUND</strong><p>{routeName} is no longer part of this mock scenario.</p><button className="button" type="button" onClick={onCancel}>BACK TO SCENARIO</button></div> : <form className="mock-route-form" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); if (ready) void onSave(draft, existing?.name) }}>
      <div className="mock-route-form__scroll">
        <div className="mock-route-form__canvas">
          <section className="mock-route-form__section" aria-labelledby="mock-route-request-title">
            <div className="mock-route-form__section-title"><div><h3 id="mock-route-request-title">REQUEST</h3><p>Choose the service, method, and path that should match.</p></div><label className="mock-route-enabled"><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={(event) => change('enabled', event.target.checked)} /><span>ROUTE ENABLED</span></label></div>
            <div className="mock-route-endpoint mock-route-endpoint--service">
              <label><span>SERVICE</span><select aria-label="SERVICE" value={draft.service} disabled={busy} onChange={(event) => change('service', event.target.value)}>{services.map((service) => <option key={service}>{service}</option>)}</select></label>
              <label><span>METHOD</span><select aria-label="METHOD" value={draft.method} disabled={busy} onChange={(event) => changeEndpoint('method', event.target.value)}>{mockMethods.map((method) => <option key={method}>{method}</option>)}</select></label>
              <label><span>PATH</span><input autoFocus={!existing} aria-label="PATH" required value={draft.path} disabled={busy} placeholder="/inventory/{sku}" onChange={(event) => changeEndpoint('path', event.target.value)} /></label>
            </div>
          </section>

          <section className="mock-route-form__section" aria-labelledby="mock-route-response-title">
            <div className="mock-route-form__section-title"><div><h3 id="mock-route-response-title">RESPONSE</h3><p>Define the response returned when this route matches.</p></div><label className="mock-route-status"><span>STATUS</span><select aria-label="RESPONSE STATUS" value={draft.status} disabled={busy} onChange={(event) => change('status', Number(event.target.value))}>{httpStatusGroups.map((group) => <optgroup label={group.label} key={group.label}>{group.statuses.map(([code, text]) => <option value={code} key={code}>{code} · {text}</option>)}</optgroup>)}</select></label></div>
            <label className="mock-route-body"><span>BODY</span><textarea aria-label="RESPONSE BODY" value={draft.body} disabled={busy} placeholder={'{"available": false}'} onChange={(event) => change('body', event.target.value)} /></label>
          </section>

          <details className="mock-route-advanced">
            <summary>ADVANCED OPTIONS</summary>
            <div>
              <label><span>ROUTE NAME</span><input aria-label="ROUTE NAME" name="portless-mock-route-name" required pattern={mockNamePattern} maxLength={64} autoComplete="off" spellCheck="false" value={draft.name} disabled={busy || !!existing} title={existing ? "Route names can't be changed in place." : 'Use a lowercase URL-safe name.'} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value, nameCustomized: true }))} /></label>
              <label><span>DELAY (MS)</span><input aria-label="DELAY (MS)" type="number" min="0" max="300000" value={draft.delayMs} disabled={busy} onChange={(event) => change('delayMs', Number(event.target.value))} /></label>
              <label><span>REQUIRED QUERY · ONE NAME=VALUE PER LINE</span><textarea aria-label="REQUIRED QUERY · ONE NAME=VALUE PER LINE" value={draft.queryText} disabled={busy} placeholder={'warehouse=central\ninclude=availability'} onChange={(event) => change('queryText', event.target.value)} /></label>
              <label><span>RESPONSE HEADERS · ONE NAME: VALUE PER LINE</span><textarea aria-label="RESPONSE HEADERS · ONE NAME: VALUE PER LINE" value={draft.headersText} disabled={busy} onChange={(event) => change('headersText', event.target.value)} /></label>
            </div>
          </details>

          {error && <div className="mock-workspace-error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}
        </div>
      </div>
      <footer className="mock-workspace-footer"><span>Saving this route does not enable <strong>{scenario.name}</strong>.</span><div><button className="button button--quiet" type="button" disabled={busy} onClick={onCancel}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !ready}>{busy ? 'SAVING…' : 'SAVE ROUTE'}</button></div></footer>
    </form>}
  </section>
}

export function newMockRouteDraft(sequence: number, service = ''): MockRouteDraft {
  const path = sequence === 1 ? '/' : `/route-${sequence}`
  return {
    name: suggestMockRouteName('GET', path, sequence),
    nameCustomized: false,
    service,
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

export function mockRouteDraft(route: MockRoute): MockRouteDraft {
  return {
    name: route.name,
    nameCustomized: true,
    service: route.service,
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

function formatDraftPairs(value: Record<string, string> | undefined, separator: string) {
  return Object.entries(value || {}).map(([name, item]) => `${name}${separator}${item}`).join('\n')
}
