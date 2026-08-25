import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Environment, TrafficExchange, TrafficTrace } from '../../types'
import { loadTrafficSnapshot } from './trafficSnapshot'

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => { resolve = complete })
  return { promise, resolve }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('traffic snapshots', () => {
  it('loads traces after the exchange cutoff so reconciliation cannot apply an older trace snapshot', async () => {
    const exchangeResponse = deferred<Response>()
    const exchange = { sequence: 42 } as TrafficExchange
    const trace = { number: 42, lastSequence: 42 } as TrafficTrace
    const calls: string[] = []
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url.includes('/traffic/exchanges')) return exchangeResponse.promise
      if (url.includes('/traffic/traces')) return Promise.resolve(jsonResponse({ traces: [trace] }))
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    const pending = loadTrafficSnapshot({ project: 'store', name: 'local' } as Environment, 'checkout:orders')

    expect(calls).toEqual(['/api/v1/environments/store/local/traffic/exchanges?protocol=all&limit=1000'])
    exchangeResponse.resolve(jsonResponse({ exchanges: [exchange] }))
    await vi.waitFor(() => expect(calls).toHaveLength(2))

    await expect(pending).resolves.toEqual({ exchanges: [exchange], traces: [trace], throughSequence: 42 })
    expect(calls[1]).toBe('/api/v1/environments/store/local/traffic/traces?background=include&limit=1000&edge=checkout%3Aorders')
  })
})
