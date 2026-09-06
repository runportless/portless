import { useId, useRef, useState } from 'react'
import type { MockRoute, MockScenario } from '../../api/contracts/mocks'
import { ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { httpStatusGroups } from '../httpStatuses'
import { MockRoutePreview } from './MockRoutePreview'
import { MockNameValueEditor, type MockNameValueDraft } from './MockNameValueEditor'
import { mockResponseHeaderDrafts } from './mockResponseHeaders'
import { mockQueryParameterDrafts } from './mockQueryParameters'
import { useMockRoutePreview, type RunMockPreview } from './useMockRoutePreview'

const mockMethods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const
const mockNamePattern = '[a-z0-9][a-z0-9._-]{0,63}'
const pathParameter = /^\{[A-Za-z_][A-Za-z0-9_]*\}$/

export interface MockRouteDraft {
  name: string
  nameCustomized: boolean
  service: string
  method: string
  path: string
  pathMatch: 'exact' | 'template'
  query: MockNameValueDraft[]
  status: number
  headers: MockNameValueDraft[]
  body: string
  delayMs: number
  enabled: boolean
}

export function MockRouteEditor({ scenario, services, routeName, draft, dirty, busy, error, onDismissError, onChange, onCancel, onSave, onPreview }: {
  scenario: MockScenario
  services: string[]
  routeName?: string
  draft: MockRouteDraft
  dirty: boolean
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onChange: (draft: MockRouteDraft) => void
  onCancel: () => void
  onSave: (route: MockRouteDraft, originalName?: string) => Promise<void>
  onPreview: RunMockPreview
}) {
  const [previewOpen, setPreviewOpen] = useState(false)
  const [configurationTab, setConfigurationTab] = useState<'request' | 'response'>('request')
  const configurationID = useId()
  const configurationTabs = useRef<HTMLDivElement>(null)
  const focusNewPath = useRef(!routeName)
  const previewOpened = useRef(false)
  const previewButton = useRef<HTMLButtonElement>(null)
  const preview = useMockRoutePreview(draft, scenario, routeName, onPreview)
  const existing = routeName ? scenario.routes.find((route) => route.name === routeName) : undefined
  const missing = !!routeName && !existing
  const title = existing ? 'Edit Route' : 'Create Route'
  const ready = !!draft.name.trim() && !!draft.service && draft.path.startsWith('/')
  const openPreview = () => {
    focusNewPath.current = false
    if (!previewOpened.current) {
      preview.resetRequest()
      previewOpened.current = true
    }
    setPreviewOpen(true)
  }
  const selectConfigurationTab = (tab: 'request' | 'response') => {
    focusNewPath.current = false
    setConfigurationTab(tab)
  }

  const change = <K extends keyof MockRouteDraft>(key: K, value: MockRouteDraft[K]) => onChange({ ...draft, [key]: value })
  const changeEndpoint = (field: 'method' | 'path', value: string) => {
    const previousSuggestion = suggestMockRouteName(draft.method, draft.path, scenario.routes.length + 1)
    const next = { ...draft, [field]: value }
    if (field === 'path' && /[{}]/.test(value)) next.pathMatch = 'template'
    if (!existing && (!draft.nameCustomized || draft.name === previousSuggestion)) {
      next.name = suggestMockRouteName(next.method, next.path, scenario.routes.length + 1)
      next.nameCustomized = false
    }
    onChange(next)
  }

  return <section className="mock-route-workspace" role="region" aria-label={title}>
    {error && <div className="mock-workspace-error"><ActionErrorNotice error={error} onDismiss={onDismissError} /></div>}

    {missing ? <div className="mock-workspace-missing"><strong>ROUTE NOT FOUND</strong><p>{routeName} is no longer part of this mock scenario.</p><button className="button" type="button" onClick={onCancel}>BACK TO ROUTES</button></div> : previewOpen ? <MockRoutePreview preview={preview} services={services} enabled={draft.enabled} busy={busy} dirty={dirty} existing={!!existing} onEdit={() => { preview.cancel(); setPreviewOpen(false); window.requestAnimationFrame(() => previewButton.current?.focus()) }} onSave={() => { if (!busy && (!existing || dirty)) void onSave(draft, existing?.name) }} /> : <form className="mock-route-form" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); if (ready && !busy && (!existing || dirty)) void onSave(draft, existing?.name) }}>
      <div className="mock-route-configuration-bar">
        <div ref={configurationTabs} className="mock-route-configuration-tabs" role="tablist" aria-label="Mock route configuration">
          {(['request', 'response'] as const).map((tab) => <button key={tab} id={`${configurationID}-tab-${tab}`} data-configuration-tab={tab} type="button" role="tab" aria-selected={configurationTab === tab} aria-controls={`${configurationID}-panel-${tab}`} tabIndex={configurationTab === tab ? 0 : -1} onClick={() => selectConfigurationTab(tab)} onKeyDown={(event) => {
            if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
              event.preventDefault()
              const next = event.key === 'Home' ? 'request' : event.key === 'End' ? 'response' : tab === 'request' ? 'response' : 'request'
              selectConfigurationTab(next)
              configurationTabs.current?.querySelector<HTMLButtonElement>(`[data-configuration-tab="${next}"]`)?.focus()
            }
          }}>{tab === 'request' ? 'Request' : 'Response'}</button>)}
        </div>
        <label className="mock-route-enabled"><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={(event) => change('enabled', event.target.checked)} /><span>ROUTE ENABLED</span></label>
      </div>
      <div className="mock-route-form__scroll" role="tabpanel" id={`${configurationID}-panel-request`} aria-labelledby={`${configurationID}-tab-request`} hidden={configurationTab !== 'request'} tabIndex={0}>
        {configurationTab === 'request' && <>
          <section className="mock-route-form__section">
            <label className="mock-route-identity"><span>ROUTE NAME</span><input aria-label="ROUTE NAME" name="portless-mock-route-name" required pattern={mockNamePattern} maxLength={64} autoComplete="off" spellCheck="false" value={draft.name} disabled={busy} title="Use a lowercase URL-safe name." data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => onChange({ ...draft, name: event.target.value, nameCustomized: true })} /></label>
            <div className="mock-route-endpoint mock-route-endpoint--matching">
              <label className="mock-route-service"><span>SERVICE</span><select aria-label="SERVICE" value={draft.service} disabled={busy} onChange={(event) => change('service', event.target.value)}>{services.map((service) => <option key={service}>{service}</option>)}</select></label>
              <label><span>METHOD</span><select aria-label="METHOD" value={draft.method} disabled={busy} onChange={(event) => changeEndpoint('method', event.target.value)}>{mockMethods.map((method) => <option key={method}>{method}</option>)}</select></label>
              <div className="mock-route-path">
                <label htmlFor={`${configurationID}-path`}>PATH</label>
                <div className="mock-route-path__input">
                  <select aria-label="Path match" value={draft.pathMatch} disabled={busy} onChange={(event) => change('pathMatch', event.target.value as MockRouteDraft['pathMatch'])}>
                    <option value="exact">Exact</option><option value="template">Template {'{…}'}</option>
                  </select>
                  <input id={`${configurationID}-path`} autoFocus={focusNewPath.current} aria-label="PATH" required value={draft.path} disabled={busy} autoComplete="off" spellCheck="false" placeholder={draft.pathMatch === 'template' ? '/inventory/{sku}' : '/inventory/coffee-mug'} onChange={(event) => changeEndpoint('path', event.target.value)} />
                </div>
              </div>
            </div>
          </section>

          <section className="mock-route-form__section">
            <MockNameValueEditor rows={draft.query} label="Required query parameters" rowLabel="Query parameter" matching disabled={busy} onChange={(query) => change('query', query)} />
          </section>
        </>}
      </div>
      <div className="mock-route-form__scroll" role="tabpanel" id={`${configurationID}-panel-response`} aria-labelledby={`${configurationID}-tab-response`} hidden={configurationTab !== 'response'} tabIndex={0}>
        {configurationTab === 'response' && <>
          <section className="mock-route-form__section mock-route-form__section--response">
            <div className="mock-route-response-settings">
              <label><span>STATUS</span><select aria-label="RESPONSE STATUS" value={draft.status} disabled={busy} onChange={(event) => change('status', Number(event.target.value))}>{httpStatusGroups.map((group) => <optgroup label={group.label} key={group.label}>{group.statuses.map(([code, text]) => <option value={code} key={code}>{code} · {text}</option>)}</optgroup>)}</select></label>
              <label><span>DELAY (MS)</span><input aria-label="DELAY (MS)" type="number" min="0" max="300000" value={draft.delayMs} disabled={busy} onChange={(event) => change('delayMs', Number(event.target.value))} /></label>
            </div>
            <label className="mock-route-body"><span>BODY</span><textarea aria-label="RESPONSE BODY" value={draft.body} disabled={busy} placeholder={'{"available": false}'} onChange={(event) => change('body', event.target.value)} /></label>
          </section>
          <section className="mock-route-form__section">
            <MockNameValueEditor rows={draft.headers} label="Response headers" rowLabel="Header" disabled={busy} onChange={(headers) => change('headers', headers)} />
          </section>
        </>}
      </div>
      <footer className="mock-workspace-footer"><span role="status">{dirty ? 'Unsaved changes' : existing ? '' : 'New route'}</span><div><button ref={previewButton} className="button button--quiet" type="button" disabled={busy} onClick={openPreview}>PREVIEW</button><button className="button button--quiet" type="button" disabled={busy || (!!existing && !dirty)} aria-label={existing ? 'Discard route changes' : 'Cancel new route'} onClick={onCancel}>{existing ? 'DISCARD' : 'CANCEL'}</button><button className="button button--primary" type="submit" disabled={busy || !ready || (!!existing && !dirty)}>{busy ? 'SAVING…' : 'SAVE ROUTE'}</button></div></footer>
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
    pathMatch: 'exact',
    query: [],
    status: 200,
    headers: mockResponseHeaderDrafts({ 'Content-Type': 'application/json' }),
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
    pathMatch: /[{}]/.test(route.path) ? 'template' : 'exact',
    query: mockQueryParameterDrafts(route.query),
    status: route.status,
    headers: mockResponseHeaderDrafts(route.headers),
    body: route.body || '',
    delayMs: route.delayMs || 0,
    enabled: route.enabled,
  }
}

export function mockRouteDraftHasChanges(draft: MockRouteDraft, route: MockRoute): boolean {
  const saved = mockRouteDraft(route)
  const values = (rows: MockNameValueDraft[]) => rows.filter(({ name, value }) => name.trim() || value.trim()).map(({ name, value }) => [name, value])
  return (Object.keys(saved) as Array<keyof MockRouteDraft>).some((key) => {
    if (key === 'nameCustomized') return false
    if (key === 'headers') return JSON.stringify(values(draft[key])) !== JSON.stringify(values(saved[key]))
    if (key === 'query') {
      const queryValues = (rows: MockNameValueDraft[]) => rows.filter(({ name, value }) => name.trim() || value.trim()).map(({ name, value, match }) => [name, match || (value === '' ? 'exists' : 'equals'), match === 'exists' ? '' : value])
      return JSON.stringify(queryValues(draft.query)) !== JSON.stringify(queryValues(saved.query))
    }
    return draft[key] !== saved[key]
  })
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
