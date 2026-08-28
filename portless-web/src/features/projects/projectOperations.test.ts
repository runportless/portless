import { describe, expect, it } from 'vitest'
import { environmentCanStop, environmentKey, environmentRoute, projectRoute } from './projectOperations'

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
})
