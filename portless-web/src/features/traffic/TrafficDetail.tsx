import { useEffect, useState } from 'react'
import { duration } from '../../components/Status'
import type { TrafficExchange } from '../../types'

function Detail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>
}

export function formattedTrafficHeaders(headers: Record<string, string[]> | undefined, host?: string) {
  const values = Object.entries(headers || {}).filter(([name]) => !host || name.toLowerCase() !== 'host')
  if (host) values.push(['Host', [host]])
  return values.length
    ? values.sort(([left], [right]) => left.localeCompare(right)).flatMap(([name, entries]) => entries.map((value) => `${name}: ${value}`)).join('\n')
    : 'No headers captured'
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024*1024) return `${(value/1024).toFixed(value < 10*1024 ? 1 : 0)} KB`
  return `${(value/(1024*1024)).toFixed(1)} MB`
}

function trafficBodySummary(bytes: number, capturedBytes: number, direction: 'request' | 'response') {
  if (bytes <= 0) return `No ${direction} body`
  if (capturedBytes <= 0) return `Body content was not captured · ${formatBytes(bytes)} transferred`
  return `${formatBytes(capturedBytes)} captured from ${formatBytes(bytes)} transferred`
}

function formatTrafficBody(body: string) {
  const trimmed = body.trim()
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { return JSON.stringify(JSON.parse(body), null, 2) } catch { /* Preserve malformed or streaming JSON as received. */ }
  }
  return body
}

function TrafficMessage({ exchange, direction }: { exchange: TrafficExchange; direction: 'request' | 'response' }) {
  const request = direction === 'request'
  const bytes = request ? exchange.requestBytes : exchange.responseBytes
  const capturedBytes = request ? exchange.requestCapturedBytes || 0 : exchange.responseCapturedBytes || 0
  const body = request ? exchange.requestBody : exchange.responseBody
  const truncated = request ? exchange.requestBodyTruncated : exchange.responseBodyTruncated
  const startLine = request
    ? `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
    : exchange.status ? `HTTP ${exchange.status}` : exchange.error ? 'HTTP ERROR' : 'HTTP response'
  const headers = formattedTrafficHeaders(request ? exchange.requestHeaders : exchange.responseHeaders, request ? exchange.host : undefined)
  const copy = () => navigator.clipboard.writeText(`${startLine}\n${headers}${body ? `\n\n${body}` : ''}`).catch(() => undefined)
  return <section className={`traffic-message traffic-message--${direction}`}>
    <div className="traffic-message__title"><span>{direction.toUpperCase()}</span><div><small>{formatBytes(Math.max(0, bytes))}</small><button type="button" onClick={copy}>COPY</button></div></div>
    <div className="traffic-message__line"><code>{startLine}</code></div>
    <div className="traffic-message__headers"><span>HEADERS</span><pre>{headers}</pre></div>
    <div className="traffic-message__body"><span>BODY{truncated ? ' · TRUNCATED' : ''}</span>{body ? <><pre>{formatTrafficBody(body)}</pre>{truncated && <small>Showing the first {formatBytes(capturedBytes)} of the {direction} body.</small>}</> : <strong>{trafficBodySummary(bytes, capturedBytes, direction)}</strong>}</div>
  </section>
}

export function TrafficDetail({ exchange, onClose }: { exchange: TrafficExchange; onClose: () => void }) {
  const [maximized, setMaximized] = useState(false)
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => { if (event.key === 'Escape') maximized ? setMaximized(false) : onClose() }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [maximized, onClose])
  return <aside className={`traffic-detail${maximized ? ' traffic-detail--maximized' : ''}`} role="dialog" aria-label={`Traffic request and response ${exchange.sequence}`}>
    <header><div><span className="eyebrow">{exchange.protocol.toUpperCase()} EXCHANGE #{exchange.sequence}</span><h3>{exchange.method || exchange.protocol.toUpperCase()} {exchange.requestTarget || exchange.path || `${exchange.source} → ${exchange.target}`}</h3></div><div className="traffic-detail__actions"><button type="button" onClick={() => setMaximized((value) => !value)} aria-label={maximized ? 'Restore traffic details' : 'Maximize traffic details'} title={maximized ? 'Restore' : 'Maximize'}>{maximized ? '↙' : '↗'}</button><button type="button" onClick={onClose} aria-label="Close traffic details" title="Close">×</button></div></header>
    <div className="detail-grid"><Detail label="EDGE" value={`${exchange.source} → ${exchange.target}`} /><Detail label="STATUS" value={exchange.error ? 'error' : String(exchange.status || 'ok')} /><Detail label="DURATION" value={duration(exchange.durationMs)} /><Detail label="PROVIDER" value={exchange.targetProvider || '—'} /><Detail label="FAULT" value={exchange.fault || 'none'} /><Detail label="RECORDING" value={exchange.recording || 'none'} /></div>
    {exchange.traceId && <div className="traffic-detail__trace"><span>TRACE CONTEXT</span><code>{exchange.traceId}</code>{exchange.spanId && <small>span {exchange.spanId}{exchange.parentSpanId ? ` · parent ${exchange.parentSpanId}` : ''}</small>}</div>}
    {exchange.error && <div className="traffic-detail__error"><span>REQUEST ERROR</span><strong>{exchange.error}</strong></div>}
    {exchange.protocol === 'http'
      ? <div className="traffic-exchange"><TrafficMessage exchange={exchange} direction="request" /><TrafficMessage exchange={exchange} direction="response" /></div>
      : <div className="traffic-detail__notice"><span>TCP SESSION</span><strong>Payload content is not captured.</strong><small>{formatBytes(Math.max(0, exchange.requestBytes))} sent · {formatBytes(Math.max(0, exchange.responseBytes))} received</small></div>}
  </aside>
}
