import type { Environment } from '../../../api/contracts/environments'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficReplayDraft } from '../../../api/contracts/traffic_replay'
import { isWebSocketHandshake } from '../trafficProtocol'

export const replayMethods = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'] as const

const maxReplayBodyBytes = 25 * 1024 * 1024

export interface ReplayHeaderRow {
  id: number
  name: string
  value: string
  omitted: boolean
  sensitive: boolean
}

export interface ReplayRequestDraft {
  environment: string
  method: string
  requestTarget: string
  headers: ReplayHeaderRow[]
  bodyMode: TrafficReplayDraft['bodyMode']
  body: string
}

export function sensitiveReplayHeader(name: string) {
  return /authorization|cookie|token|secret|credential|password|api[-_]?key/i.test(name)
}

function hasStreamingReplayHeaders(headers: Record<string, string[]> | undefined) {
  return Object.entries(headers || {}).some(([name, values]) => {
    if (!['content-type', 'accept'].includes(name.toLowerCase())) return false
    return values.some((value) => (name.toLowerCase() === 'accept' ? value.split(',') : [value]).some((part) => {
      const mediaType = part.split(';', 1)[0].trim().toLowerCase()
      return mediaType === 'text/event-stream' || mediaType.startsWith('application/grpc')
    }))
  })
}

export function replayEntryReason(exchange: TrafficExchange) {
  if (exchange.protocol !== 'http' || isWebSocketHandshake(exchange)) return 'Only ordinary HTTP requests can be replayed.'
  if (!replayMethods.some((method) => method === exchange.method)) return 'This HTTP method is not supported for replay.'
  if (hasStreamingReplayHeaders(exchange.requestHeaders) || hasStreamingReplayHeaders(exchange.responseHeaders)) return 'gRPC and event streams are not supported for replay.'
  return ''
}

export function capturedReplayBodyAvailable(exchange: TrafficExchange) {
  const capture = exchange.requestCapture
  return capture?.state === 'empty' || (capture?.state === 'complete' && capture.exact && (!capture.encoding || capture.encoding === 'identity'))
}

export function replayRequestDraft(value: TrafficReplayDraft): ReplayRequestDraft {
  let id = 0
  const omitted = new Set((value.omittedHeaders || []).map((name) => name.toLowerCase()))
  const headers: ReplayHeaderRow[] = Object.entries(value.headers || {}).flatMap(([name, values]) => values.map((content) => ({
    id: ++id, name, value: content.includes('[REDACTED]') ? '' : content,
    omitted: omitted.has(name.toLowerCase()), sensitive: sensitiveReplayHeader(name) || content.includes('[REDACTED]'),
  })))
  for (const name of value.omittedHeaders || []) {
    if (!headers.some((header) => header.name.toLowerCase() === name.toLowerCase())) headers.push({ id: ++id, name, value: '', omitted: true, sensitive: sensitiveReplayHeader(name) })
  }
  return { environment: value.environment, method: value.method, requestTarget: value.requestTarget, bodyMode: value.bodyMode, body: value.body, headers }
}

export function clearReplayCredentials(draft: ReplayRequestDraft, credentialSource = draft): ReplayRequestDraft {
  const credentials = new Set<string>()
  for (const row of [...draft.headers, ...credentialSource.headers]) {
    if (!row.sensitive && !sensitiveReplayHeader(row.name)) continue
    if (!row.value || row.value === '[REDACTED]') continue
    credentials.add(row.value)
    if (row.name.toLowerCase() === 'authorization') {
      const separator = row.value.indexOf(' ')
      if (separator >= 0 && row.value.slice(separator + 1)) credentials.add(row.value.slice(separator + 1))
    }
    if (row.name.toLowerCase() === 'cookie') {
      for (const part of row.value.split(';')) {
        const separator = part.indexOf('=')
        const value = separator >= 0 ? part.slice(separator + 1).trim() : ''
        if (value) credentials.add(value)
      }
    }
  }
  // These values live only for this cleanup; retain no separate credential list.
  const ordered = [...credentials].sort((left, right) => right.length - left.length)
  const redact = (value: string) => ordered.reduce((current, credential) => current.split(credential).join('[REDACTED]'), value)
  return { ...draft, requestTarget: redact(draft.requestTarget), body: redact(draft.body), headers: draft.headers.map((row) => row.sensitive || sensitiveReplayHeader(row.name) ? { ...row, value: '', sensitive: true } : { ...row, value: redact(row.value) }) }
}

export function serializeReplayDraft(draft: ReplayRequestDraft, baseline: TrafficExchange): TrafficReplayDraft {
  if (!replayMethods.some((method) => method === draft.method)) throw new Error('Choose a supported HTTP method.')
  if (!draft.requestTarget.startsWith('/') || draft.requestTarget.startsWith('//') || draft.requestTarget.includes('#') || [...draft.requestTarget].some((character) => character.charCodeAt(0) <= 32 || character.charCodeAt(0) === 127) || /%(?![a-f\d]{2})/i.test(draft.requestTarget)) throw new Error('Enter an escaped path and query beginning with a single /. URLs, fragments and unescaped spaces are not supported.')
  if (new TextEncoder().encode(draft.requestTarget).length > 8192) throw new Error('The path and query must fit within 8 KiB.')
  if (draft.bodyMode === 'captured' && !capturedReplayBodyAvailable(baseline)) throw new Error('The complete request body is unavailable. Provide replacement text or explicitly choose an empty body.')
  const body = draft.bodyMode === 'empty' ? '' : draft.bodyMode === 'captured' ? baseline.requestBody || '' : draft.body
  if (body.length > maxReplayBodyBytes || new TextEncoder().encode(body).length > maxReplayBodyBytes) throw new Error('The request body is too large. Reduce its size and try again.')
  const headers: Record<string, string[]> = Object.create(null)
  const names = new Map<string, string>()
  const omittedHeaders: string[] = []
  let bytes = 0
  if (draft.headers.length > 128) throw new Error('Use at most 128 request header rows.')
  for (const row of draft.headers) {
    const name = row.name.trim()
    if (!name && !row.value) continue
    if (!/^[!#$%&'*+.^_`|~\da-z-]+$/i.test(name)) throw new Error('Each header needs a valid name.')
    if (row.omitted) { if (!omittedHeaders.some((value) => value.toLowerCase() === name.toLowerCase())) omittedHeaders.push(name); continue }
    if ([...row.value].some((character) => [0, 10, 13].includes(character.charCodeAt(0)))) throw new Error('Header values cannot contain line breaks or null characters.')
    if (row.value.includes('[REDACTED]') || ((row.sensitive || sensitiveReplayHeader(name)) && !row.value)) throw new Error(`Provide ${name} or explicitly omit that header.`)
    const canonical = names.get(name.toLowerCase()) || name
    names.set(name.toLowerCase(), canonical)
    headers[canonical] = [...(headers[canonical] || []), row.value]
    bytes += new TextEncoder().encode(name + row.value).length
  }
  if (bytes > 32768) throw new Error('Request headers must fit within 32 KiB.')
  if (omittedHeaders.some((name) => names.has(name.toLowerCase()))) throw new Error('Omit all rows of a header together, or provide all its values.')
  if (hasStreamingReplayHeaders(headers)) throw new Error('gRPC and event streams are not supported for replay. Use ordinary HTTP request headers.')
  return { environment: draft.environment, method: draft.method, requestTarget: draft.requestTarget, headers, bodyMode: draft.bodyMode, body, omittedHeaders }
}

export function replayDestinationReason(environment: Environment, baseline: TrafficExchange) {
  if (environment.project !== baseline.project) return 'Different project'
  if (['stopped', 'stopping', 'starting', 'recovering', 'unknown'].includes(environment.status)) return `Environment ${environment.status}`
  const target = environment.services.find((service) => service.name === baseline.target)
  if (!target) return 'Target service is missing'
  if (target.status !== 'ready') return `Target service ${target.status}`
  if (baseline.source === 'external') return target.endpoints?.some((endpoint) => endpoint.kind === 'public' && endpoint.protocol === 'http') ? '' : 'No public HTTP endpoint'
  return environment.connections.some((edge) => edge.source === baseline.source && edge.target === baseline.target && edge.protocol === 'http') ? '' : 'HTTP dependency edge is missing'
}

export function replayDraftFingerprint(draft: ReplayRequestDraft) {
  // This is only a display-staleness marker, never an admission or security key.
  // Keep no second plaintext copy of entered credentials after closing the editor.
  const value = JSON.stringify(draft)
  let left = 2166136261
  let right = 5381
  for (let index = 0; index < value.length; index++) {
    left = Math.imul(left ^ value.charCodeAt(index), 16777619)
    right = Math.imul(right, 33) ^ value.charCodeAt(index)
  }
  return `${value.length}:${(left >>> 0).toString(16)}:${(right >>> 0).toString(16)}`
}
