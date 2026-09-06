import { useState } from 'react'
import type { MockPreview } from '../../api/contracts/mocks'
import { ActionErrorNotice } from '../../components/ActionError'
import { formatMockPreviewBody } from './mockPreview'
import { MockNameValueEditor } from './MockNameValueEditor'
import type { useMockRoutePreview } from './useMockRoutePreview'

export function MockRoutePreview({ preview, services, enabled, busy, dirty, existing, onEdit, onSave }: {
  preview: ReturnType<typeof useMockRoutePreview>
  services: string[]
  enabled: boolean
  busy: boolean
  dirty: boolean
  existing: boolean
  onEdit: () => void
  onSave: () => void
}) {
  const { request, result, error, running, outdated } = preview
  const [queryOpen, setQueryOpen] = useState(() => request.query.length > 0)
  return <form className="mock-route-form mock-preview" aria-label="Mock request preview" onSubmit={(event) => { event.preventDefault(); if (!busy) void preview.run() }}>
    <div className="mock-preview__scroll">
      <section className="mock-preview__request" aria-label="Preview request">
        <div className="mock-route-form__section-title"><h3>REQUEST TO TEST</h3><button className="button button--small button--quiet" type="button" disabled={busy} onClick={preview.resetRequest}>RESET REQUEST</button></div>
        <p className="mock-preview__scope">Current route draft with saved scenario routes. Nothing is saved or sent to your application.</p>
        {!enabled && <p className="mock-preview__disabled">This route is disabled and will not match.</p>}
        <div className="mock-route-endpoint mock-route-endpoint--service">
          <label><span>SERVICE</span><select aria-label="PREVIEW SERVICE" value={request.service} disabled={busy} onChange={(event) => preview.changeRequest({ ...request, service: event.target.value })}>{services.map((service) => <option key={service}>{service}</option>)}</select></label>
          <label><span>METHOD</span><select aria-label="PREVIEW METHOD" value={request.method} disabled={busy} onChange={(event) => preview.changeRequest({ ...request, method: event.target.value })}>{['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((method) => <option key={method}>{method}</option>)}</select></label>
          <label><span>PATH</span><input autoFocus aria-label="PREVIEW PATH" value={request.path} disabled={busy} autoComplete="off" spellCheck="false" placeholder="/inventory/sku-123" onChange={(event) => preview.changeRequest({ ...request, path: event.target.value })} /></label>
        </div>
        <details className="mock-preview__query" open={queryOpen} onToggle={(event) => setQueryOpen(event.currentTarget.open)}><summary>QUERY PARAMETERS</summary><MockNameValueEditor rows={request.query} label="Preview query parameters" rowLabel="Query parameter" hideCaption description="Repeat a name for multiple values." disabled={busy} onChange={(query) => preview.changeRequest({ ...request, query })} /></details>
        <div className="mock-preview__run"><span role="status">{running ? 'Evaluating request…' : outdated ? 'Preview is outdated. Run it again.' : result ? 'Preview is up to date.' : 'Run a preview to inspect the expected response.'}</span><button className="button button--primary" type="submit" disabled={busy || running}>{running ? 'RUNNING…' : 'RUN PREVIEW'}</button></div>
      </section>
      {error && <div className="mock-workspace-error"><ActionErrorNotice error={error} onDismiss={preview.dismissError} /></div>}
      <MockPreviewResponse result={result} outdated={outdated} />
    </div>
    <footer className="mock-workspace-footer"><span>{dirty ? 'Unsaved changes' : existing ? '' : 'New route'}</span><div><button className="button button--quiet" type="button" disabled={busy} onClick={onEdit}>EDIT</button><button className="button button--primary" type="button" disabled={busy || (existing && !dirty)} onClick={onSave}>{busy ? 'SAVING…' : 'SAVE ROUTE'}</button></div></footer>
  </form>
}

export function MockPreviewResponse({ result, outdated }: { result: MockPreview | null; outdated: boolean }) {
  const [tab, setTab] = useState<'body' | 'headers'>('body')
  const [raw, setRaw] = useState(false)
  const body = result?.body || ''
  const headers = Object.entries(result?.headers || {}).sort(([left], [right]) => left.localeCompare(right))
  return <section className={`mock-preview__response${outdated ? ' is-outdated' : ''}`} aria-label="Preview response">
    <div className="mock-preview__response-heading"><h3>EXPECTED RESPONSE</h3>{result && <div aria-live="polite"><strong>{result.status}</strong><span>{result.matched ? `Matched ${result.route}` : 'No route matched'}</span>{!!result.delayMs && <span>{result.delayMs.toLocaleString()} ms configured delay</span>}</div>}</div>
    {result ? <>
      <div className="mock-preview__toolbar"><div role="tablist" aria-label="Preview response views">{(['body', 'headers'] as const).map((item) => <button key={item} id={`mock-preview-tab-${item}`} role="tab" type="button" aria-selected={tab === item} aria-controls="mock-preview-response-content" tabIndex={tab === item ? 0 : -1} onClick={() => setTab(item)} onKeyDown={(event) => {
        if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
          event.preventDefault()
          const next = event.key === 'Home' ? 'body' : event.key === 'End' ? 'headers' : tab === 'body' ? 'headers' : 'body'
          setTab(next)
          document.getElementById(`mock-preview-tab-${next}`)?.focus()
        }
      }}>{item === 'body' ? 'Body' : 'Headers'}</button>)}</div>{tab === 'body' && body && <div className="mock-preview__format" aria-label="Body format"><button type="button" aria-pressed={!raw} onClick={() => setRaw(false)}>Formatted</button><button type="button" aria-pressed={raw} onClick={() => setRaw(true)}>Raw</button></div>}</div>
      <div className="mock-preview__content" id="mock-preview-response-content" role="tabpanel" tabIndex={0} aria-labelledby={`mock-preview-tab-${tab}`}>
        {tab === 'body' ? body ? <pre>{raw ? body : formatMockPreviewBody(body)}</pre> : <p className="mock-preview__empty">This response has no body.</p> : headers.length ? <dl className="mock-preview__headers">{headers.map(([name, value]) => <div key={name}><dt>{name}</dt><dd>{value}</dd></div>)}</dl> : <p className="mock-preview__empty">No response headers.</p>}
      </div>
    </> : <div className="mock-preview__content"><p className="mock-preview__empty">The expected status, headers, and body will appear here.</p></div>}
  </section>
}
