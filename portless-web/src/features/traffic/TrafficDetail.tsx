import { type ReactNode, useEffect, useId, useState } from 'react'
import { DrawerSizeButton } from '../../components/DrawerSizeButton'
import { duration } from '../../components/Status'
import type { ComponentBinding, TrafficExchange, TrafficMessage, TrafficTrace } from '../../types'
import { traceTransactionCommandSpans, type TraceNavigationItem } from './TraceWaterfall'

type TrafficDirection = 'request' | 'response'
type TrafficDetailView = TrafficDirection | 'summary' | 'compare'
export type TrafficPayloadView = 'body' | 'headers' | 'raw'

export function defaultTrafficDetailView(_exchange: TrafficExchange): TrafficDetailView {
  return 'request'
}

function CopyIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="3" width="8" height="9" /><path d="M10 12v2H2V5h3" /></svg>
}

function CompareIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="3" width="5" height="10" /><rect x="9" y="3" width="5" height="10" /></svg>
}

function TraceArrowIcon({ direction, boundary = false }: { direction: 'previous' | 'next'; boundary?: boolean }) {
  const previous = direction === 'previous'
  return <svg viewBox="0 0 16 16" aria-hidden="true">
    {boundary && <path d={previous ? 'M3 3v10' : 'M13 3v10'} />}
    <path d={previous ? 'm10.5 3.5-4.5 4.5 4.5 4.5' : 'm5.5 3.5 4.5 4.5-4.5 4.5'} />
  </svg>
}

export function orderedTraceExchanges(trace: TrafficTrace | null | undefined) {
  return [...(trace?.spans || [])]
    .sort((left, right) => left.startOffsetMs - right.startOffsetMs || left.exchange.sequence - right.exchange.sequence)
    .map((span) => span.exchange)
}

function TraceNavigator({ trace, exchange, items, itemKey, pending, onNavigate }: {
  trace?: TrafficTrace | null
  exchange: TrafficExchange
  items?: TraceNavigationItem[]
  itemKey?: string
  pending: boolean
  onNavigate?: (item: TraceNavigationItem) => void
}) {
  const navigationItems: TraceNavigationItem[] = items || orderedTraceExchanges(trace).map((candidate) => ({ kind: 'exchange', key: `exchange:${candidate.sequence}`, exchange: candidate }))
  const index = itemKey ? navigationItems.findIndex((candidate) => candidate.key === itemKey) : navigationItems.findIndex((candidate) => candidate.exchange.sequence === exchange.sequence)
  if (!onNavigate || navigationItems.length < 2 || index < 0) return null
  const first = index === 0
  const last = index === navigationItems.length - 1
  const navigate = (target: number) => onNavigate(navigationItems[target])

  return <nav className="traffic-trace-navigator" aria-label="Trace span navigation" aria-busy={pending}>
    <div role="group" aria-label="Navigate visible trace spans">
      <button type="button" title="First visible span in trace" aria-label="First visible span in trace" disabled={pending || first} onClick={() => navigate(0)}><TraceArrowIcon direction="previous" boundary /></button>
      <button type="button" title="Previous visible span in trace" aria-label="Previous visible span in trace" disabled={pending || first} onClick={() => navigate(index - 1)}><TraceArrowIcon direction="previous" /></button>
      <output aria-live="polite" aria-label={`Span ${index + 1} of ${navigationItems.length}`}><strong>{index + 1}</strong><span>OF</span><strong>{navigationItems.length}</strong></output>
      <button type="button" title="Next visible span in trace" aria-label="Next visible span in trace" disabled={pending || last} onClick={() => navigate(index + 1)}><TraceArrowIcon direction="next" /></button>
      <button type="button" title="Last visible span in trace" aria-label="Last visible span in trace" disabled={pending || last} onClick={() => navigate(navigationItems.length - 1)}><TraceArrowIcon direction="next" boundary /></button>
    </div>
  </nav>
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

export function trafficStartedTime(timestamp: string) {
  return new Date(timestamp).toLocaleTimeString([], {
    hour: 'numeric', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3,
  })
}

function TrafficOverview({ exchange, targetBinding }: { exchange: TrafficExchange; targetBinding?: ComponentBinding }) {
	const tcp = exchange.tcp
	const http = exchange.protocol === 'http'
	const showTCPNotice = exchange.protocol !== 'http' && (!tcp || tcp.kind === 'session' || (tcp.inspection !== 'decoded' && tcp.inspection !== 'limited'))
  if (!http && !exchange.error && !showTCPNotice) return null
  return <section className="traffic-overview" aria-label={http ? 'Exchange overview' : 'TCP inspection summary'}>
    {http && <div className="traffic-overview__context">
      <OverviewDetail label="ENVIRONMENT" value={exchange.environment} />
      <OverviewDetail label="TARGET BINDING" value={trafficTargetBinding(exchange, targetBinding)} />
      <OverviewDetail label="STARTED" value={trafficStartedTime(exchange.startedAt)} />
      <OverviewDetail label="COMPLETED" value={duration(exchange.durationMs)} />
    </div>}
    {exchange.error && <div className="traffic-detail__error"><span>{exchange.protocol === 'http' ? 'REQUEST ERROR' : 'OPERATION ERROR'}</span><strong>{exchange.error}</strong></div>}
    {showTCPNotice && <section className="traffic-tcp-summary"><span>{tcp?.inspection?.toUpperCase() || 'TCP SESSION'}</span><strong>{tcp?.inspectionReason || 'Application protocol details are not available for this connection.'}</strong><small>{formatTrafficBytes(Math.max(0, exchange.requestBytes))} sent · {formatTrafficBytes(Math.max(0, exchange.responseBytes))} received</small></section>}
  </section>
}

function protocolMessageText(message: TrafficMessage) {
  const fields = (message.fields || []).map((field) => `${field.name}: ${field.value}`).join('\n')
  return [message.summary, fields, message.content].filter(Boolean).join('\n')
}

function displayedProtocolMessageSummary(message: TrafficMessage) {
  const summary = (message.summary || message.type).trim()
  const content = message.content?.trim()
  if (!content) return summary
  const normalizedSummary = summary.replace(/\s+/g, ' ').toLowerCase()
  const normalizedContent = content.replace(/\s+/g, ' ').toLowerCase()
  return normalizedSummary.includes(normalizedContent) ? '' : summary
}

const protocolMessageNoise = new Set([
  'bind-complete', 'close-complete', 'column-definition', 'column-end', 'definition-end', 'describe', 'empty-query',
  'execute', 'flush', 'no-data', 'parameter-description', 'parse-complete', 'portal-suspended', 'ready',
  'row-description', 'sync',
])

export function semanticTrafficMessage(exchange: TrafficExchange, direction: TrafficDirection) {
  const request = direction === 'request'
  const messages = (request ? exchange.tcp?.requestMessages : exchange.tcp?.responseMessages) || []
  const preferred = request
    ? messages.find((message) => message.contentType?.toLowerCase().startsWith('text/x-sql'))
      || messages.find((message) => message.content && message.encoding !== 'base64')
      || messages.find((message) => !protocolMessageNoise.has(message.type.toLowerCase()))
      || messages[0]
    : messages.find((message) => message.type.toLowerCase() === 'error')
      || messages.find((message) => message.content && message.encoding !== 'base64')
      || messages.find((message) => ['command-complete', 'ok', 'response', 'result-end'].includes(message.type.toLowerCase()))
      || [...messages].reverse().find((message) => !protocolMessageNoise.has(message.type.toLowerCase()))
      || messages[messages.length - 1]
  const label = request ? 'COMMAND' : 'RESULT'
  const fallback = request
    ? exchange.tcp?.operation || 'Operation'
    : exchange.error || (exchange.tcp?.outcome === 'one-way' ? 'No response' : exchange.tcp?.outcome || 'No result captured')
  const fields = (preferred?.fields || []).filter((field) => field.value.trim())
  return {
    label,
    title: preferred ? displayedProtocolMessageSummary(preferred) || (request ? exchange.tcp?.operation || preferred.type : preferred.summary || preferred.type) : fallback,
    type: preferred?.type || '',
    content: preferred?.encoding === 'base64' ? '' : preferred?.content || '',
    contentType: preferred?.contentType || '',
    binary: preferred?.encoding === 'base64',
    fields,
    truncated: Boolean(preferred?.truncated),
    contentBytes: Math.max(0, preferred?.contentBytes || 0),
    capturedBytes: Math.max(0, preferred?.capturedBytes || 0),
  }
}

function TCPSemanticMessage({ exchange, direction }: { exchange: TrafficExchange; direction: TrafficDirection }) {
  const message = semanticTrafficMessage(exchange, direction)
  const presentation = message.content ? trafficBodyPresentation(message.content, message.contentType) : { text: '', json: false }
  return <section className={`traffic-semantic-card traffic-semantic-card--${direction}`} aria-label={`${message.label.toLowerCase()} summary`}>
    <div className="traffic-semantic-card__header"><span>{message.label}</span><small>{message.contentType || message.type.toUpperCase() || 'not captured'}</small></div>
    <div className="traffic-semantic-card__body">
      <strong>{message.title}</strong>
      {presentation.text && <pre className={presentation.json ? 'traffic-json' : undefined}>{presentation.json ? highlightedJSON(presentation.text) : presentation.text}</pre>}
      {message.fields.length > 0 && <dl>{message.fields.map((field, index) => <div key={`${field.name}:${index}`}><dt>{field.name}</dt><dd>{field.value}</dd></div>)}</dl>}
      {message.binary && <p>Binary payload is available in Protocol messages.</p>}
      {!presentation.text && message.fields.length === 0 && !message.binary && <p>{direction === 'request' ? 'No command content was captured.' : exchange.tcp?.outcome === 'one-way' ? 'This command does not have a result.' : 'No result content was captured.'}</p>}
    </div>
    {message.truncated && <footer><span>CAPTURE TRUNCATED</span><span>{captureSummary(message.contentBytes, message.capturedBytes)}</span></footer>}
  </section>
}

type TraceTransactionNavigationItem = Extract<TraceNavigationItem, { kind: 'transaction' }>

function TCPCommandResultSummary({ exchanges, view }: {
  exchanges: TrafficExchange[]
  view: TrafficDirection | 'compare'
}) {
  const label = view === 'request' ? 'Command' : view === 'response' ? 'Result' : 'Command and result'
  return <section className={`traffic-command-results${view === 'compare' ? '' : ' traffic-command-results--single'}`} aria-label={label}>
    <ol>
      {exchanges.map((exchange, index) => <li className="traffic-command-result" key={exchange.sequence}>
        <header><span>{String(index + 1).padStart(2, '0')}</span><strong>{exchange.tcp?.operation || 'COMMAND'}</strong><small>{duration(exchange.durationMs)}</small></header>
        <div className="traffic-semantic-grid">
          {view !== 'response' && <TCPSemanticMessage exchange={exchange} direction="request" />}
          {view !== 'request' && <TCPSemanticMessage exchange={exchange} direction="response" />}
        </div>
      </li>)}
    </ol>
  </section>
}

function TCPProtocolMessage({ message, index }: { message: TrafficMessage; index: number }) {
  const presentation = message.content && message.encoding !== 'base64'
    ? trafficBodyPresentation(message.content, message.contentType || '')
    : { text: message.content || '', json: false }
  const summary = displayedProtocolMessageSummary(message)
  const fields = (message.fields || []).filter((field) => field.value.trim())
  return <article className="traffic-protocol-message" aria-label={`Message ${index + 1}: ${message.type}`}>
    <header>
      <span className="traffic-protocol-message__index" aria-hidden="true">{String(index + 1).padStart(2, '0')}</span>
      <div><span>{message.type.toUpperCase()}</span>{summary && <strong>{summary}</strong>}</div>
      <small><span>+{message.offsetMs}ms</span><span>{formatTrafficBytes(Math.max(0, message.wireBytes))}</span></small>
    </header>
    {fields.length > 0 && <dl aria-label="Message fields">{fields.map((field, fieldIndex) => <div key={`${field.name}:${fieldIndex}`}><dt>{field.name}</dt><dd>{field.value}</dd></div>)}</dl>}
    {presentation.text && <div className="traffic-protocol-message__content"><div>{message.contentType && <code>{message.contentType}</code>}{message.encoding === 'base64' && <span>BINARY · BASE64</span>}</div><pre className={presentation.json ? 'traffic-json' : undefined}>{presentation.json ? highlightedJSON(presentation.text) : presentation.text}</pre></div>}
    {message.truncated && <footer>PAYLOAD TRUNCATED · {formatTrafficBytes(message.capturedBytes || 0)} OF {formatTrafficBytes(message.contentBytes || 0)}</footer>}
  </article>
}

function TCPMessageInspector({ exchange, direction, compact = false }: { exchange: TrafficExchange; direction: TrafficDirection; compact?: boolean }) {
  const request = direction === 'request'
  const directionLabel = request ? 'command' : 'result'
  const messages = (request ? exchange.tcp?.requestMessages : exchange.tcp?.responseMessages) || []
  const messageCount = (request ? exchange.tcp?.requestMessageCount : exchange.tcp?.responseMessageCount) || messages.length
  const bytes = Math.max(0, request ? exchange.requestBytes : exchange.responseBytes)
  const truncated = Boolean(request ? exchange.tcp?.requestTruncated : exchange.tcp?.responseTruncated)
  const copy = () => navigator.clipboard.writeText(messages.map(protocolMessageText).join('\n\n')).catch(() => undefined)
  return <section className={`traffic-message-workbench traffic-message-workbench--${direction} traffic-protocol-workbench${compact ? ' traffic-message-workbench--compact' : ''}`} aria-label={`${directionLabel} protocol messages`}>
    <div className="traffic-message-workbench__summary"><code>{messageCount} {messageCount === 1 ? 'MESSAGE' : 'MESSAGES'}</code><div><span>{formatTrafficBytes(bytes)}</span><button className="traffic-copy-button" type="button" onClick={copy} aria-label={`Copy ${directionLabel} messages`} title={`Copy ${directionLabel} messages`} disabled={messages.length === 0}><CopyIcon /><span>COPY</span></button></div></div>
    <div className="traffic-protocol-messages">{messages.length > 0 ? messages.map((message, index) => <TCPProtocolMessage message={message} index={index} key={`${message.offsetMs}:${message.type}:${index}`} />) : <div className="traffic-payload__empty"><strong>No decoded {directionLabel} messages.</strong><small>{formatTrafficBytes(bytes)} transferred</small></div>}</div>
    {truncated && <footer><span>CAPTURE TRUNCATED</span><span>{captureSummary(bytes, request ? exchange.requestCapturedBytes || 0 : exchange.responseCapturedBytes || 0)}</span></footer>}
  </section>
}

function TCPProtocolDetails({ exchanges, direction, transaction, onDirection, onToggle }: {
  exchanges: TrafficExchange[]
  direction: TrafficDirection
  transaction?: TraceTransactionNavigationItem
  onDirection: (direction: TrafficDirection) => void
  onToggle?: (group: number) => void
}) {
  return <section className="traffic-tcp-details" aria-label="TCP details">
    {transaction && <header className="traffic-command-results__toolbar">
      <div><span>TRACE NAVIGATION</span><strong>{transaction.spans.length} decoded TCP {transaction.spans.length === 1 ? 'operation' : 'operations'}</strong><small>{transaction.expanded ? 'TCP operations are included in trace navigation.' : 'TCP operations remain hidden from trace navigation.'}</small></div>
      {onToggle && <button type="button" aria-expanded={transaction.expanded} onClick={() => onToggle(transaction.group)}>{transaction.expanded ? 'HIDE FROM TRACE' : 'INCLUDE IN TRACE'}</button>}
    </header>}
    <nav className="traffic-payload-tabs traffic-protocol-direction-tabs" role="tablist" aria-label="Protocol message direction">
      <button type="button" role="tab" aria-selected={direction === 'request'} className={direction === 'request' ? 'is-active' : ''} onClick={() => onDirection('request')}>COMMAND</button>
      <button type="button" role="tab" aria-selected={direction === 'response'} className={direction === 'response' ? 'is-active' : ''} onClick={() => onDirection('response')}>RESULT</button>
    </nav>
    <ol>
      {exchanges.map((exchange, index) => <li className="traffic-command-result" key={exchange.sequence}>
        <header><span>{String(index + 1).padStart(2, '0')}</span><strong>{exchange.tcp?.operation || 'COMMAND'}</strong><small>{duration(exchange.durationMs)}</small></header>
        <TCPMessageInspector exchange={exchange} direction={direction} />
      </li>)}
    </ol>
  </section>
}

function statusTone(exchange: TrafficExchange) {
  if (exchange.error || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete' || (exchange.status || 0) >= 500) return 'is-error'
  if ((exchange.status || 0) >= 400) return 'is-warning'
  return 'is-success'
}

export function TrafficDetail({ exchange, trace, traceNavigationItems, traceNavigationItem, traceNavigationPending = false, targetBinding, onTraceNavigate, onTraceTransactionToggle, onClose }: {
  exchange: TrafficExchange
  trace?: TrafficTrace | null
  traceNavigationItems?: TraceNavigationItem[]
  traceNavigationItem?: TraceNavigationItem
  traceNavigationPending?: boolean
  targetBinding?: ComponentBinding
  onTraceNavigate?: (item: TraceNavigationItem) => void
  onTraceTransactionToggle?: (group: number) => void
  onClose: () => void
}) {
  const transaction = traceNavigationItem?.kind === 'transaction' ? traceNavigationItem : undefined
  const transactionCommands = transaction ? traceTransactionCommandSpans(transaction.spans).map((span) => span.exchange) : []
  const detailExchange = transaction?.exchange || exchange
  const http = !transaction && detailExchange.protocol === 'http'
  const decodedTCP = !transaction && !http && detailExchange.tcp?.kind === 'operation'
  const semanticTCP = Boolean(transaction || decodedTCP)
  const tcpCommandExchanges = transaction ? transactionCommands : decodedTCP ? [detailExchange] : []
  const tcpProtocolExchanges = transaction ? transaction.spans.map((span) => span.exchange) : decodedTCP ? [detailExchange] : []
  const payloadExchange = http || decodedTCP
  const [maximized, setMaximized] = useState(false)
  const [view, setView] = useState<TrafficDetailView>(() => defaultTrafficDetailView(detailExchange))
  const [protocolDirection, setProtocolDirection] = useState<TrafficDirection>('request')
  const [requestView, setRequestView] = useState<TrafficPayloadView>(() => defaultTrafficPayloadView(detailExchange, 'request'))
  const [responseView, setResponseView] = useState<TrafficPayloadView>(() => defaultTrafficPayloadView(detailExchange, 'response'))

  useEffect(() => {
    setView((current) => current === 'compare' && (detailExchange.protocol === 'http' || semanticTCP) ? current : defaultTrafficDetailView(detailExchange))
    setProtocolDirection('request')
    setRequestView(defaultTrafficPayloadView(detailExchange, 'request'))
    setResponseView(defaultTrafficPayloadView(detailExchange, 'response'))
  }, [detailExchange.project, detailExchange.environment, detailExchange.sequence, semanticTCP, traceNavigationItem?.key])

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
    if ((http && payloadExchange) || semanticTCP) setView('compare')
  }

  const tcpStatus = detailExchange.tcp?.outcome === 'success' ? 'OK' : detailExchange.tcp?.outcome === 'one-way' ? 'SENT' : detailExchange.tcp?.outcome === 'incomplete' ? 'INCOMPLETE' : detailExchange.tcp?.outcome === 'error' ? 'ERROR' : 'SESSION'
  const status = detailExchange.error ? 'ERROR' : detailExchange.status ? String(detailExchange.status) : http ? 'OK' : tcpStatus
  const totalBytes = Math.max(0, detailExchange.requestBytes) + Math.max(0, detailExchange.responseBytes)
  const requestTarget = detailExchange.requestTarget || detailExchange.path || '/'
  const applicationProtocol = detailExchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
  const operation = detailExchange.tcp?.operation || 'SESSION'
  const commandCount = transaction ? transactionCommands.length : decodedTCP ? 1 : 0
  const commandLabel = `${commandCount} ${commandCount === 1 ? 'COMMAND' : 'COMMANDS'}`
  const headingEyebrow = transaction || decodedTCP
    ? `${applicationProtocol} COMMAND / RESULT`
    : `${http ? 'HTTP' : applicationProtocol} EXCHANGE #${detailExchange.sequence}`

  return <aside className={`traffic-detail${maximized ? ' traffic-detail--maximized' : ''}`} role="dialog" aria-label={`Traffic request and response ${detailExchange.sequence}`}>
    <header className="traffic-detail__header">
      <div className="traffic-detail__heading"><span className="eyebrow">{headingEyebrow}</span><h3><span>{http ? detailExchange.method || 'HTTP' : applicationProtocol}</span> <code>{transaction ? commandLabel : http ? requestTarget : operation}</code></h3><small><code>{detailExchange.source}</code><i>→</i><code>{detailExchange.target}</code></small></div>
      <div className="traffic-detail__outcome"><b className={statusTone(detailExchange)}>{status}</b><span><strong>{duration(detailExchange.durationMs)}</strong></span><span><strong>{formatTrafficBytes(totalBytes)}</strong></span></div>
      <div className="traffic-detail__actions"><DrawerSizeButton fullScreen={maximized} subject="traffic details" onToggle={toggleMaximized} /><button type="button" onClick={onClose} aria-label="Close traffic details" title="Close">×</button></div>
      <div className="traffic-detail__header-context">
        <TraceNavigator trace={trace} exchange={detailExchange} items={traceNavigationItems} itemKey={traceNavigationItem?.key} pending={traceNavigationPending} onNavigate={onTraceNavigate} />
        <InterventionBadges exchange={detailExchange} />
      </div>
    </header>

    <div className="traffic-detail__content">
      {(!maximized || !http) && <TrafficOverview exchange={detailExchange} targetBinding={targetBinding} />}
      {(http || semanticTCP) && <>
        <nav className="traffic-detail__tabs" role="tablist" aria-label="Exchange payload">
          {http ? <>
            <button type="button" role="tab" aria-selected={view === 'request'} className={view === 'request' ? 'is-active' : ''} onClick={() => setView('request')}>REQUEST</button>
            <button type="button" role="tab" aria-selected={view === 'response'} className={view === 'response' ? 'is-active' : ''} onClick={() => setView('response')}>RESPONSE</button>
            {maximized && <button className="traffic-detail__compare" type="button" role="tab" aria-selected={view === 'compare'} onClick={() => setView('compare')}><CompareIcon />COMPARE</button>}
          </> : <>
            <button type="button" role="tab" aria-selected={view === 'request'} className={view === 'request' ? 'is-active' : ''} onClick={() => setView('request')}>COMMAND</button>
            <button type="button" role="tab" aria-selected={view === 'response'} className={view === 'response' ? 'is-active' : ''} onClick={() => setView('response')}>RESULT</button>
            {maximized && <button className="traffic-detail__compare" type="button" role="tab" aria-selected={view === 'compare'} onClick={() => setView('compare')}><CompareIcon />COMPARE</button>}
            <button type="button" role="tab" aria-selected={view === 'summary'} className={view === 'summary' ? 'is-active' : ''} onClick={() => setView('summary')}>TCP DETAILS</button>
          </>}
        </nav>
        {semanticTCP && (view === 'request' || view === 'response' || view === 'compare') && <TCPCommandResultSummary exchanges={tcpCommandExchanges} view={view} />}
        {semanticTCP && view === 'summary' && <TCPProtocolDetails exchanges={tcpProtocolExchanges} direction={protocolDirection} transaction={transaction} onDirection={setProtocolDirection} onToggle={onTraceTransactionToggle} />}
        {http && view === 'request' && <div className="traffic-detail__message" role="tabpanel"><TrafficMessageInspector exchange={detailExchange} direction="request" view={requestView} onView={setRequestView} /></div>}
        {http && view === 'response' && <div className="traffic-detail__message" role="tabpanel"><TrafficMessageInspector exchange={detailExchange} direction="response" view={responseView} onView={setResponseView} /></div>}
        {http && view === 'compare' && <div className="traffic-detail__comparison" role="tabpanel"><TrafficMessageInspector compact exchange={detailExchange} direction="request" view={requestView} onView={setRequestView} /><TrafficMessageInspector compact exchange={detailExchange} direction="response" view={responseView} onView={setResponseView} /></div>}
      </>}
    </div>
  </aside>
}
