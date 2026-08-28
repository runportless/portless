import { useEffect, useId, useState, type ReactNode } from 'react'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import { captureSummary, CompareIcon, CopyIcon, formatTrafficBytes, highlightedJSON, trafficBodyPresentation, trafficBodySummary } from '../detail/TrafficFormatting'
import type { TrafficDetailView, TrafficDirection, TrafficPayloadView } from '../detail/trafficDetailTypes'

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

export function formatTrafficBody(body: string) {
  return trafficBodyPresentation(body).text
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

export function HttpTrafficDetail({ exchange, maximized, view, onView }: {
  exchange: TrafficExchange
  maximized: boolean
  view: TrafficDetailView
  onView: (view: TrafficDetailView) => void
}) {
  const [requestView, setRequestView] = useState<TrafficPayloadView>(() => defaultTrafficPayloadView(exchange, 'request'))
  const [responseView, setResponseView] = useState<TrafficPayloadView>(() => defaultTrafficPayloadView(exchange, 'response'))

  useEffect(() => {
    setRequestView(defaultTrafficPayloadView(exchange, 'request'))
    setResponseView(defaultTrafficPayloadView(exchange, 'response'))
  }, [exchange.project, exchange.environment, exchange.sequence])

  return <>
    <nav className="traffic-detail__tabs" role="tablist" aria-label="Exchange payload">
      <button type="button" role="tab" aria-selected={view === 'request'} className={view === 'request' ? 'is-active' : ''} onClick={() => onView('request')}>REQUEST</button>
      <button type="button" role="tab" aria-selected={view === 'response'} className={view === 'response' ? 'is-active' : ''} onClick={() => onView('response')}>RESPONSE</button>
      {maximized && <button className="traffic-detail__compare" type="button" role="tab" aria-selected={view === 'compare'} onClick={() => onView('compare')}><CompareIcon />COMPARE</button>}
    </nav>
    {view === 'request' && <div className="traffic-detail__message" role="tabpanel"><TrafficMessageInspector exchange={exchange} direction="request" view={requestView} onView={setRequestView} /></div>}
    {view === 'response' && <div className="traffic-detail__message" role="tabpanel"><TrafficMessageInspector exchange={exchange} direction="response" view={responseView} onView={setResponseView} /></div>}
    {view === 'compare' && <div className="traffic-detail__comparison" role="tabpanel"><TrafficMessageInspector compact exchange={exchange} direction="request" view={requestView} onView={setRequestView} /><TrafficMessageInspector compact exchange={exchange} direction="response" view={responseView} onView={setResponseView} /></div>}
  </>
}
