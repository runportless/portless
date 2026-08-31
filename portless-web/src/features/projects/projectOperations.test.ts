import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import { environmentCanRestart, environmentCanStop, environmentKey, environmentRoute, projectRoute, runEnvironmentOperation } from './projectOperations'

afterEach(() => vi.unstubAllGlobals())

describe('project operations', () => {
  it('builds encoded public routes without ownership keys', () => {
    const environment = { project: 'Store Demo', name: 'QA / local' }

    expect(projectRoute(environment.project)).toBe('/projects/Store%20Demo')
    expect(environmentRoute(environment)).toBe('/environments/Store%20Demo/QA%20%2F%20local')
    expect(environmentKey(environment)).toBe('Store Demo/QA / local')
  })

  it('only offers stop for actionable environment states', () => {
    expect(environmentCanStop({ status: 'healthy' })).toBe(true)
    expect(environmentCanStop({ status: 'degraded' })).toBe(true)
    expect(environmentCanStop({ status: 'stopped' })).toBe(false)
    expect(environmentCanStop({ status: 'stopping' })).toBe(false)
    expect(environmentCanStop({ status: 'recovering' })).toBe(false)
  })

  it('only offers restart for stable running environment states', () => {
    for (const status of ['healthy', 'degraded', 'failed'] as const) expect(environmentCanRestart({ status })).toBe(true)
    for (const status of ['starting', 'recovering', 'stopping', 'stopped', 'unknown'] as const) expect(environmentCanRestart({ status })).toBe(false)
  })

  it('completes shutdown before starting a restarted environment', async () => {
    const requests: Array<{ path: string; method?: string; body?: string }> = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      requests.push({ path: String(input), method: init?.method, body: typeof init?.body === 'string' ? init.body : undefined })
      return new Response(JSON.stringify({ number: requests.length, state: 'succeeded' }), { headers: { 'Content-Type': 'application/json' } })
    }))

    await runEnvironmentOperation({ project: 'Store Demo', name: 'QA / local' } as Environment, 'restart')

    expect(requests.map((request) => `${request.method} ${request.path}`)).toEqual([
      'POST /api/v1/environments/Store%20Demo/QA%20%2F%20local/down',
      'POST /api/v1/environments/Store%20Demo/QA%20%2F%20local/up',
    ])
    expect(JSON.parse(requests[0].body || '')).toEqual({ removeVolumes: false })
    expect(requests[1].body).toBeUndefined()
  })
})
