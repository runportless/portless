import { type ReactNode, useEffect, useId, useState } from 'react'
import { DrawerSizeButton } from '../../components/DrawerSizeButton'
import { duration } from '../../components/Status'
import type { ComponentBinding, TrafficExchange } from '../../types'

type TrafficDirection = 'request' | 'response'
type TrafficDetailView = TrafficDirection | 'compare'
export type TrafficPayloadView = 'body' | 'headers' | 'raw'

function CopyIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="3" width="8" height="9" /><path d="M10 12v2H2V5h3" /></svg>
}

function CompareIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="3" width="5" height="10" /><rect x="9" y="3" width="5" height="10" /></svg>
}

function OverviewDetail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>
}

export function formattedTrafficHeaders(headers: Record<string, string[]> | undefined, host?: string) {
  const values = Object.entries(headers || {}).filter(([name]) => !host || name.toLowerCase() !== 'host')
  if (host) values.push(['Host', [host]])
  return values.length
    ? values.sort(([left], [right]) => left.localeCompare(right)).flatMap(([name, entries]) => entries.map((value) => `${name}: ${value}`)).join('\n')
    : 'No headers captured'
}

function highlightedTrafficHeaders(headers: string) {
  const lines = headers.split('\n')
  const nodes: ReactNode[] = []
  for (const [index, line] of lines.entries()) {
    const separator = line.indexOf(':')
    if (separator > 0) {
      nodes.push(<span className="traffic-headers__key" key={`${index}:key`}>{line.slice(0, separator)}</span>)
      nodes.push(<span className="traffic-headers__separator" key={`${index}:separator`}>:</span>)
      nodes.push(<span className="traffic-headers__value" key={`${index}:value`}>{line.slice(separator + 1)}</span>)
    } else {
      nodes.push(line)
    }
    if (index < lines.length - 1) nodes.push('\n')
  }
  return nodes
}

export function formatTrafficBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

export function trafficBodySummary(bytes: number, capturedBytes: number, direction: TrafficDirection) {
  if (bytes <= 0) return `No ${direction} body`
  if (capturedBytes <= 0) return `Body content was not captured · ${formatTrafficBytes(bytes)} transferred`
  return `${formatTrafficBytes(capturedBytes)} captured from ${formatTrafficBytes(bytes)} transferred`
}

function trafficBodyPresentation(body: string, contentType = '') {
  const trimmed = body.trim()
  const mediaType = contentType.split(';', 1)[0].trim().toLowerCase()
  const declaredJSON = mediaType === 'application/json' || mediaType === 'text/json' || mediaType.endsWith('+json')
  if (declaredJSON || trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { return { text: JSON.stringify(JSON.parse(body), null, 2), json: true } } catch { /* Preserve malformed or streaming JSON as received. */ }
  }
  return { text: body, json: false }
}

export function formatTrafficBody(body: string) {
  return trafficBodyPresentation(body).text
}

const jsonTokenPattern = /("(?:\\.|[^"\\])*")(?=\s*:)|("(?:\\.|[^"\\])*")|(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false)\b|\b(null)\b/g

function highlightedJSON(value: string) {
  const nodes: ReactNode[] = []
  let cursor = 0
  let key = 0
  for (const match of value.matchAll(jsonTokenPattern)) {
    const start = match.index
    if (start > cursor) nodes.push(value.slice(cursor, start))
    const kind = match[1] ? 'key' : match[2] ? 'string' : match[3] ? 'number' : match[4] ? 'boolean' : 'null'
    nodes.push(<span className={`traffic-json__${kind}`} key={key++}>{match[0]}</span>)
    cursor = start + match[0].length
  }
  if (cursor < value.length) nodes.push(value.slice(cursor))
  return nodes
}

function trafficStartLine(exchange: TrafficExchange, direction: TrafficDirection) {
  if (direction === 'request') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  if (exchange.status) return `HTTP ${exchange.status}`
  return exchange.error ? 'HTTP ERROR' : 'HTTP response'
}

export function rawTrafficMessage(exchange: TrafficExchange, direction: TrafficDirection) {
  const request = direction === 'request'
  const headers = formattedTrafficHeaders(request ? exchange.requestHeaders : exchange.responseHeaders, request ? exchange.host : undefined)
  const body = request ? exchange.requestBody : exchange.responseBody
  return `${trafficStartLine(exchange, direction)}\n${headers}${body ? `\n\n${body}` : ''}`
}

function headerValue(headers: Record<string, string[]> | undefined, name: string) {
  const match = Object.entries(headers || {}).find(([candidate]) => candidate.toLowerCase() === name.toLowerCase())
  return match?.[1]?.[0]
}

function messageValues(exchange: TrafficExchange, direction: TrafficDirection) {
  const request = direction === 'request'
  const bytes = Math.max(0, request ? exchange.requestBytes : exchange.responseBytes)
  const capturedBytes = Math.max(0, (request ? exchange.requestCapturedBytes : exchange.responseCapturedBytes) || 0)
  const body = request ? exchange.requestBody : exchange.responseBody
  const truncated = Boolean(request ? exchange.requestBodyTruncated : exchange.responseBodyTruncated)
  const headerValues = request ? exchange.requestHeaders : exchange.responseHeaders
  const headers = formattedTrafficHeaders(headerValues, request ? exchange.host : undefined)
  const contentType = headerValue(headerValues, 'content-type')
  return { bytes, capturedBytes, body, truncated, headers, contentType }
}

export function defaultTrafficPayloadView(exchange: TrafficExchange, direction: TrafficDirection): TrafficPayloadView {
  return messageValues(exchange, direction).body ? 'body' : 'headers'
}

function captureSummary(bytes: number, capturedBytes: number) {
  if (bytes <= 0) return '0 B transferred'
  if (capturedBytes <= 0) return `${formatTrafficBytes(bytes)} transferred`
  if (capturedBytes < bytes) return `${formatTrafficBytes(capturedBytes)} of ${formatTrafficBytes(bytes)} captured`
  return `${formatTrafficBytes(bytes)} captured`
}

function TrafficMessageInspector({ exchange, direction, view, onView, compact = false }: {
  exchange: TrafficExchange
  direction: TrafficDirection
  view: TrafficPayloadView
  onView: (view: TrafficPayloadView) => void
  compact?: boolean
}) {
  const panelId = useId()
  const values = messageValues(exchange, direction)
  const startLine = trafficStartLine(exchange, direction)
  const bodyPresentation = values.body ? trafficBodyPresentation(values.body, values.contentType || '') : { text: '', json: false }
  const formattedBody = bodyPresentation.text
  const raw = rawTrafficMessage(exchange, direction)
  const copyValue = view === 'body' ? (formattedBody || trafficBodySummary(values.bytes, values.capturedBytes, direction)) : view === 'headers' ? values.headers : raw
  const copy = () => navigator.clipboard.writeText(copyValue).catch(() => undefined)

  return <section className={`traffic-message-workbench traffic-message-workbench--${direction}${compact ? ' traffic-message-workbench--compact' : ''}`} aria-label={`${direction} details`}>
    <div className="traffic-message-workbench__summary">
      <code>{startLine}</code>
      <div>{values.contentType && <span>{values.contentType}</span>}<span>{formatTrafficBytes(values.bytes)}</span><button className="traffic-copy-button" type="button" onClick={copy} aria-label={`Copy ${direction} ${view}`} title={`Copy ${direction} ${view}`}><CopyIcon /><span>COPY</span></button></div>
    </div>
    <div className="traffic-payload-tabs" role="tablist" aria-label={`${direction} representation`}>
      {(['body', 'headers', 'raw'] as const).map((candidate) => <button key={candidate} type="button" role="tab" aria-selected={view === candidate} aria-controls={panelId} className={view === candidate ? 'is-active' : ''} onClick={() => onView(candidate)}>{candidate.toUpperCase()}{candidate === 'body' && values.truncated ? ' · TRUNCATED' : ''}</button>)}
    </div>
    <div className="traffic-payload" id={panelId} role="tabpanel">
      {view === 'body' && (formattedBody
        ? <><pre className={bodyPresentation.json ? 'traffic-json' : undefined}>{bodyPresentation.json ? highlightedJSON(formattedBody) : formattedBody}</pre>{values.truncated && <small>Showing the first {formatTrafficBytes(values.capturedBytes)} of the {direction} body.</small>}</>
        : <div className="traffic-payload__empty"><strong>{trafficBodySummary(values.bytes, values.capturedBytes, direction)}</strong></div>)}
      {view === 'headers' && <pre className="traffic-headers">{highlightedTrafficHeaders(values.headers)}</pre>}
      {view === 'raw' && <pre>{raw}</pre>}
    </div>
    {values.truncated && <footer><span>CAPTURE TRUNCATED</span><span>{captureSummary(values.bytes, values.capturedBytes)}</span></footer>}
  </section>
}

function InterventionBadges({ exchange }: { exchange: TrafficExchange }) {
  const mock = [exchange.mockProfile, exchange.mockRoute].filter(Boolean).join(' / ')
  if (!exchange.fault && !exchange.recording && !mock) return null
  return <div className="traffic-intervention-badges" role="list" aria-label="Exchange interventions">
    {exchange.fault && <span className="traffic-intervention-badge traffic-intervention-badge--fault" role="listitem" aria-label={`FAULT ${exchange.fault}`}><b>FAULT</b><span>{exchange.fault}</span></span>}
    {exchange.recording && <span className="traffic-intervention-badge traffic-intervention-badge--recording" role="listitem" aria-label={`RECORDING ${exchange.recording}`}><b>RECORDING</b><span>{exchange.recording}</span></span>}
    {mock && <span className="traffic-intervention-badge traffic-intervention-badge--mock" role="listitem" aria-label={`MOCK ${mock}`}><b>MOCK</b><span>{mock}</span></span>}
  </div>
}

export function trafficTargetBinding(exchange: TrafficExchange, binding?: ComponentBinding) {
  const provider = exchange.targetProvider || binding?.provider
  const matchingBinding = binding && (!exchange.targetProvider || binding.provider === exchange.targetProvider) ? binding : undefined
  let configuration = exchange.target
  if (provider === 'local') configuration = matchingBinding?.source || exchange.target
  if (provider === 'container') configuration = 'Portless managed'
  if (provider === 'remote') configuration = matchingBinding?.remote?.url || (exchange.remoteClassification ? `${exchange.remoteClassification} target` : exchange.target)
  if (provider === 'mock') configuration = matchingBinding?.mock?.profile || exchange.mockProfile || exchange.target
  return [configuration, provider].filter(Boolean).join(' · ') || 'not reported'
}

function TrafficOverview({ exchange, targetBinding }: { exchange: TrafficExchange; targetBinding?: ComponentBinding }) {
  return <section className="traffic-overview" aria-label="Exchange overview">
    <div className="traffic-overview__context">
      <OverviewDetail label="ENVIRONMENT" value={exchange.environment} />
      <OverviewDetail label="TARGET BINDING" value={trafficTargetBinding(exchange, targetBinding)} />
      <OverviewDetail label="STARTED" value={new Date(exchange.startedAt).toLocaleTimeString()} />
      <OverviewDetail label="COMPLETED" value={duration(exchange.durationMs)} />
    </div>
    {exchange.error && <div className="traffic-detail__error"><span>REQUEST ERROR</span><strong>{exchange.error}</strong></div>}
    {exchange.protocol !== 'http' && <section className="traffic-tcp-summary"><span>TCP SESSION</span><strong>Payload content is not captured.</strong><small>{formatTrafficBytes(Math.max(0, exchange.requestBytes))} sent · {formatTrafficBytes(Math.max(0, exchange.responseBytes))} received</small></section>}
  </section>
}

function statusTone(exchange: TrafficExchange) {
  if (exchange.error || (exchange.status || 0) >= 500) return 'is-error'
  if ((exchange.status || 0) >= 400) return 'is-warning'
  return 'is-success'
}

export function TrafficDetail({ exchange, targetBinding, onClose }: { exchange: TrafficExchange; targetBinding?: ComponentBinding; onClose: () => void }) {
  const http = exchange.protocol === 'http'
  const [maximized, setMaximized] = useState(false)
  const [view, setView] = useState<TrafficDetailView>('request')
  const [requestView, setRequestView] = useState<TrafficPayloadView>(() => defaultTrafficPayloadView(exchange, 'request'))
  const [responseView, setResponseView] = useState<TrafficPayloadView>(() => defaultTrafficPayloadView(exchange, 'response'))

  useEffect(() => {
    setMaximized(false)
    setView('request')
    setRequestView(defaultTrafficPayloadView(exchange, 'request'))
    setResponseView(defaultTrafficPayloadView(exchange, 'response'))
  }, [exchange.project, exchange.environment, exchange.sequence])

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (maximized) { setMaximized(false); setView((current) => current === 'compare' ? 'request' : current) } else onClose()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [maximized, onClose])

  const toggleMaximized = () => {
    if (maximized) {
      setMaximized(false)
      setView((current) => current === 'compare' ? 'request' : current)
      return
    }
    setMaximized(true)
    if (http) setView('compare')
  }

  const status = exchange.error ? 'ERROR' : exchange.status ? String(exchange.status) : http ? 'OK' : 'SESSION'
  const totalBytes = Math.max(0, exchange.requestBytes) + Math.max(0, exchange.responseBytes)
  const requestTarget = exchange.requestTarget || exchange.path || '/'

  return <aside className={`traffic-detail${maximized ? ' traffic-detail--maximized' : ''}`} role="dialog" aria-label={`Traffic request and response ${exchange.sequence}`}>
    <header className="traffic-detail__header">
      <div className="traffic-detail__heading"><span className="eyebrow">{exchange.protocol.toUpperCase()} EXCHANGE #{exchange.sequence}</span><h3>{http && <span>{exchange.method || 'HTTP'}</span>} <code>{http ? requestTarget : 'TCP session'}</code></h3><small><code>{exchange.source}</code><i>→</i><code>{exchange.target}</code></small></div>
      <div className="traffic-detail__outcome"><b className={statusTone(exchange)}>{status}</b><span><strong>{duration(exchange.durationMs)}</strong></span><span><strong>{formatTrafficBytes(totalBytes)}</strong></span></div>
      <div className="traffic-detail__actions"><DrawerSizeButton fullScreen={maximized} subject="traffic details" onToggle={toggleMaximized} /><button type="button" onClick={onClose} aria-label="Close traffic details" title="Close">×</button></div>
      <InterventionBadges exchange={exchange} />
    </header>

    <div className="traffic-detail__content">
      {(!maximized || !http) && <TrafficOverview exchange={exchange} targetBinding={targetBinding} />}
      {http && <>
        <nav className="traffic-detail__tabs" role="tablist" aria-label="Exchange payload">
          <button type="button" role="tab" aria-selected={view === 'request'} className={view === 'request' ? 'is-active' : ''} onClick={() => setView('request')}>REQUEST</button>
          <button type="button" role="tab" aria-selected={view === 'response'} className={view === 'response' ? 'is-active' : ''} onClick={() => setView('response')}>RESPONSE</button>
          {maximized && <button className="traffic-detail__compare" type="button" role="tab" aria-selected={view === 'compare'} onClick={() => setView('compare')}><CompareIcon />COMPARE</button>}
        </nav>
        {view === 'request' && <div className="traffic-detail__message" role="tabpanel"><TrafficMessageInspector exchange={exchange} direction="request" view={requestView} onView={setRequestView} /></div>}
        {view === 'response' && <div className="traffic-detail__message" role="tabpanel"><TrafficMessageInspector exchange={exchange} direction="response" view={responseView} onView={setResponseView} /></div>}
        {view === 'compare' && <div className="traffic-detail__comparison" role="tabpanel"><TrafficMessageInspector compact exchange={exchange} direction="request" view={requestView} onView={setRequestView} /><TrafficMessageInspector compact exchange={exchange} direction="response" view={responseView} onView={setResponseView} /></div>}
      </>}
    </div>
  </aside>
}
