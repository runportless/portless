import type { ReactNode } from 'react'

export function CopyIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="3" width="8" height="9" /><path d="M10 12v2H2V5h3" /></svg>
}

export function CompareIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="3" width="5" height="10" /><rect x="9" y="3" width="5" height="10" /></svg>
}

export function formatTrafficBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

export function trafficBodySummary(bytes: number, capturedBytes: number, direction: 'request' | 'response') {
  if (bytes <= 0) return `No ${direction} body`
  if (capturedBytes <= 0) return `Body content was not captured · ${formatTrafficBytes(bytes)} transferred`
  return `${formatTrafficBytes(capturedBytes)} captured from ${formatTrafficBytes(bytes)} transferred`
}

export function captureSummary(bytes: number, capturedBytes: number) {
  if (bytes <= 0) return '0 B transferred'
  if (capturedBytes <= 0) return `${formatTrafficBytes(bytes)} transferred`
  if (capturedBytes < bytes) return `${formatTrafficBytes(capturedBytes)} of ${formatTrafficBytes(bytes)} captured`
  return `${formatTrafficBytes(bytes)} captured`
}

export function trafficBodyPresentation(body: string, contentType = '') {
  const trimmed = body.trim()
  const mediaType = contentType.split(';', 1)[0].trim().toLowerCase()
  const declaredJSON = mediaType === 'application/json' || mediaType === 'text/json' || mediaType.endsWith('+json')
  if (declaredJSON || trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try { return { text: JSON.stringify(JSON.parse(body), null, 2), json: true } } catch { /* Preserve malformed or streaming JSON as received. */ }
  }
  return { text: body, json: false }
}

const jsonTokenPattern = /("(?:\\.|[^"\\])*")(?=\s*:)|("(?:\\.|[^"\\])*")|(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false)\b|\b(null)\b/g

export function highlightedJSON(value: string) {
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

export function TrafficTextContent({ content, contentType = '', json = false }: { content: string; contentType?: string; json?: boolean }) {
  const presentation = json ? { text: content, json: true } : trafficBodyPresentation(content, contentType)
  return <pre className={presentation.json ? 'traffic-json' : undefined}>{presentation.json ? highlightedJSON(presentation.text) : presentation.text}</pre>
}
