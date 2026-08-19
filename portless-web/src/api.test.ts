import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, APIError } from './api'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('API response handling', () => {
  it('turns the relay HTML fallback into a concise daemon error', async () => {
    const fallback = '<!doctype html><html><body><style>several kilobytes of fallback markup</style></body></html>'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(fallback, {
      status: 503,
      headers: { 'Content-Type': 'text/html; charset=utf-8', 'Retry-After': '2' },
    })))

    let caught: unknown
    try {
      await api('/environments/store/local/traffic/traces')
    } catch (error) {
      caught = error
    }
    expect(caught).toBeInstanceOf(APIError)
    expect(caught).toMatchObject({
      status: 503,
      code: 'DAEMON_UNAVAILABLE',
      message: 'Portless is reconnecting to the local daemon. Try again in a moment.',
    })
    expect((caught as Error).message).not.toContain('<html')
    expect((caught as Error).message).not.toContain('<style>')
  })

  it('rejects successful HTML responses instead of returning markup as API data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html><body>fallback</body></html>', {
      status: 200,
      headers: { 'Content-Type': 'text/html' },
    })))

    await expect(api('/traffic')).rejects.toMatchObject({
      status: 200,
      code: 'UNEXPECTED_API_RESPONSE',
      message: 'Portless received an unexpected HTML response (HTTP 200).',
    })
  })

  it('normalizes connection failures while the daemon socket is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('fetch failed with low-level socket details')))

    await expect(api('/traffic')).rejects.toMatchObject({
      status: 0,
      code: 'DAEMON_UNAVAILABLE',
      message: 'Portless is reconnecting to the local daemon. Try again in a moment.',
    })
  })

  it('preserves structured API errors from the daemon', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: 'RESOURCE_NOT_FOUND', message: 'traffic exchange was not found' },
    }), { status: 404, headers: { 'Content-Type': 'application/json' } })))

    await expect(api('/traffic/exchanges/42')).rejects.toMatchObject({
      status: 404,
      code: 'RESOURCE_NOT_FOUND',
      message: 'traffic exchange was not found',
    })
  })
})
