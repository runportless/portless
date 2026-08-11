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

export function setCSRF(value: string) {
  csrf = value
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method ?? 'GET').toUpperCase()
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrf) headers.set('X-Portless-CSRF', csrf)
  const response = await fetch(`/api/v1${path}`, { ...options, headers, credentials: 'same-origin' })
  if (response.status === 204) return undefined as T
  const contentType = response.headers.get('content-type') ?? ''
  const value = contentType.includes('application/json') ? await response.json() : await response.text()
  if (!response.ok) {
    const error = typeof value === 'object' && value?.error ? value.error as APIErrorShape : { code: 'REQUEST_FAILED', message: String(value) }
    throw new APIError(response.status, error)
  }
  return value as T
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
