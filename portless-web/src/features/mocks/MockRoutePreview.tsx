import { useEffect, useRef, useState } from 'react'
import type { MockPreview } from '../../api/contracts/mocks'
import { ActionErrorNotice } from '../../components/ActionError'
import { formatMockPreviewBody } from './mockPreview'
import { MockNameValueSection } from './MockNameValueSection'
import type { useMockRoutePreview } from './useMockRoutePreview'

export function MockRoutePreview({ preview, services, enabled, busy, hidden }: {
  preview: ReturnType<typeof useMockRoutePreview>
  services: string[]
  enabled: boolean
  busy: boolean
  hidden: boolean
}) {
  const { request, result, error, running, outdated } = preview
  const [queryExpanded, setQueryExpanded] = useState<boolean | null>(null)
  const errorNotice = useRef<HTMLDivElement>(null)
  const queryOpen = queryExpanded ?? request.query.length > 0
  useEffect(() => {
    if (error) {
      if (error.message.toLowerCase().includes('query')) setQueryExpanded(true)
      if (!hidden) errorNotice.current?.scrollIntoView({ block: 'nearest' })
    }
  }, [error, hidden])

  return <form className="mock-route-form mock-preview" aria-label="Mock request preview" hidden={hidden} onSubmit={(event) => { event.preventDefault(); if (!busy) void preview.run() }}>
    <div className="mock-preview__heading">
      <h3>PREVIEW</h3>
      <span className="mock-preview__status" role="status">{outdated ? 'OUTDATED' : ''}</span>
      <div className="mock-preview__actions"><button className="button button--small button--quiet" type="button" disabled={busy || running} onClick={preview.resetRequest}>RESET</button><button className="button button--small button--primary" type="submit" disabled={busy || running}>{running ? 'RUNNING…' : 'PREVIEW'}</button></div>
    </div>
    <div className="mock-preview__panes">
      <section className="mock-preview__request" aria-label="Preview request">
        {!enabled && <p className="mock-preview__disabled">This route is disabled and will not match.</p>}
        <div className="mock-route-endpoint mock-preview__endpoint">
          <label className="mock-route-service"><span>SERVICE</span><select aria-label="PREVIEW SERVICE" value={request.service} disabled={busy} onChange={(event) => preview.changeRequest({ ...request, service: event.target.value })}>{services.map((service) => <option key={service}>{service}</option>)}</select></label>
          <label><span>METHOD</span><select aria-label="PREVIEW METHOD" value={request.method} disabled={busy} onChange={(event) => preview.changeRequest({ ...request, method: event.target.value })}>{['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((method) => <option key={method}>{method}</option>)}</select></label>
          <label><span>PATH</span><input aria-label="PREVIEW PATH" value={request.path} disabled={busy} autoComplete="off" spellCheck="false" placeholder="/inventory/sku-123" onChange={(event) => preview.changeRequest({ ...request, path: event.target.value })} /></label>
        </div>
        <MockNameValueSection rows={request.query} label="Preview query parameters" open={queryOpen} onOpenChange={setQueryExpanded} disabled={busy} onChange={(query) => { setQueryExpanded(true); preview.changeRequest({ ...request, query }) }} />
        {error && <div ref={errorNotice} className="mock-workspace-error"><ActionErrorNotice error={error} onDismiss={preview.dismissError} /></div>}
      </section>
      <MockPreviewResponse result={result} outdated={outdated} />
    </div>
  </form>
}

export function MockPreviewResponse({ result, outdated }: { result: MockPreview | null; outdated: boolean }) {
  const response = useRef<HTMLElement>(null)
  const [tab, setTab] = useState<'body' | 'headers'>('body')
  const [raw, setRaw] = useState(false)
  const body = result?.body || ''
  const headers = Object.entries(result?.headers || {}).sort(([left], [right]) => left.localeCompare(right))
  useEffect(() => {
    if (result) response.current?.scrollIntoView({ block: 'nearest' })
  }, [result])
  return <section ref={response} className={`mock-preview__response${outdated ? ' is-outdated' : ''}`} aria-label="Preview response">
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
