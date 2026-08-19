import type { APIErrorShape, Environment } from './types'

export class APIError extends Error {
  status: number
  code: string
  details?: Record<string, unknown>
  remediation?: APIErrorShape['remediation']

  constructor(status: number, value: APIErrorShape) {
    super(value.message)
    this.name = 'APIError'
    this.status = status
    this.code = value.code
    this.details = value.details
    this.remediation = value.remediation
  }
}

let csrf = ''
const daemonUnavailable: APIErrorShape = {
  code: 'DAEMON_UNAVAILABLE',
  message: 'Portless is reconnecting to the local daemon. Try again in a moment.',
}

export function setCSRF(value: string) {
  csrf = value
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method ?? 'GET').toUpperCase()
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrf) headers.set('X-Portless-CSRF', csrf)
  let response: Response
  try {
    response = await fetch(`/api/v1${path}`, { ...options, headers, credentials: 'same-origin' })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new APIError(0, daemonUnavailable)
  }
  if (response.status === 204) return undefined as T
  const contentType = response.headers.get('content-type') ?? ''
  let body: string
  try {
    body = await response.text()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new APIError(response.status, daemonUnavailable)
  }
  const jsonResponse = contentType.toLowerCase().includes('json')
  let value: unknown = body
  if (jsonResponse) {
    try {
      value = body ? JSON.parse(body) : undefined
    } catch {
      throw new APIError(response.status, responseError(response, body, contentType))
    }
  }
  if (!response.ok) {
    throw new APIError(response.status, apiErrorShape(value) || responseError(response, body, contentType))
  }
  if (!jsonResponse) throw new APIError(response.status, responseError(response, body, contentType))
  return value as T
}

function apiErrorShape(value: unknown): APIErrorShape | undefined {
  if (!value || typeof value !== 'object') return undefined
  const error = (value as { error?: unknown }).error
  if (!error || typeof error !== 'object') return undefined
  const candidate = error as Partial<APIErrorShape>
  if (typeof candidate.code !== 'string' || typeof candidate.message !== 'string') return undefined
  return candidate as APIErrorShape
}

function responseError(response: Response, body: string, contentType: string): APIErrorShape {
  if ([502, 503, 504].includes(response.status)) return daemonUnavailable
  const html = contentType.toLowerCase().includes('text/html') || /^\s*(?:<!doctype\s+html|<html\b)/i.test(body)
  return {
    code: 'UNEXPECTED_API_RESPONSE',
    message: `Portless received an unexpected ${html ? 'HTML' : 'non-JSON'} response (HTTP ${response.status}).`,
  }
}

export function jsonBody(value: unknown): Pick<RequestInit, 'body' | 'headers'> {
  return { body: JSON.stringify(value), headers: { 'Content-Type': 'application/json' } }
}

export function projectPath(project: string, suffix = '') {
  return `/projects/${encodeURIComponent(project)}${suffix}`
}

export function environmentPath(environment: Pick<Environment, 'project' | 'name'>, suffix = '') {
  return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}${suffix}`
}

export function connectEvents(environment: Pick<Environment, 'project' | 'name'>, topics: string[], onEvent: (type: string, value: unknown) => void) {
  const query = topics.map((topic) => `topic=${encodeURIComponent(topic)}`).join('&')
  const source = new EventSource(`/api/v1${environmentPath(environment, '/stream')}?${query}`)
  for (const topic of topics) {
    source.addEventListener(topic, (event) => {
      try {
        onEvent(topic, JSON.parse((event as MessageEvent).data))
      } catch {
        // A malformed live event is ignored; the next snapshot reconciles state.
      }
    })
  }
  return () => source.close()
}
