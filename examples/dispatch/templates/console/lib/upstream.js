const propagationNames = ['traceparent', 'tracestate']

export function requiredURL(name) {
  const value = process.env[name]?.trim()
  if (!value) throw new Error(`${name} is required`)
  return value.replace(/\/$/, '')
}

export function propagationHeaders(incoming) {
  const result = new Headers()
  for (const name of propagationNames) {
    const value = incoming.get(name)
    if (value) result.set(name, value)
  }
  return result
}

export async function readJSON(url, incoming) {
  const response = await fetch(url, {
    headers: propagationHeaders(incoming),
    cache: 'no-store',
    signal: AbortSignal.timeout(5_000),
  })
  const value = await response.json().catch(() => ({ error: { code: 'INVALID_RESPONSE', message: 'Dependency returned invalid JSON' } }))
  if (!response.ok) throw responseError(response.status, value)
  return value
}

export async function proxyJSON(request, url, init = {}) {
  const headers = propagationHeaders(request.headers)
  headers.set('accept', 'application/json')
  if (init.body !== undefined) headers.set('content-type', 'application/json')
  let response
  try {
    response = await fetch(url, {
      ...init,
      headers,
      cache: 'no-store',
      signal: AbortSignal.timeout(5_000),
    })
  } catch (error) {
    return Response.json({ error: { code: 'UPSTREAM_UNAVAILABLE', message: String(error) } }, { status: 502 })
  }
  const body = await response.text()
  return new Response(body, {
    status: response.status,
    headers: {
      'content-type': response.headers.get('content-type') || 'application/json',
      'X-Dispatch-Service': 'console',
      ...(response.headers.get('X-Portless-Remote-Policy') ? { 'X-Portless-Remote-Policy': response.headers.get('X-Portless-Remote-Policy') } : {}),
    },
  })
}

function responseError(status, value) {
  const detail = value?.error
  const error = new Error(detail?.message || `Dependency returned HTTP ${status}`)
  error.code = detail?.code || 'DEPENDENCY_FAILED'
  error.status = status
  return error
}

