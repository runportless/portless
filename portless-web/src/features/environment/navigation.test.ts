import { describe, expect, it } from 'vitest'
import { environmentUIPath } from './navigation'

describe('environment navigation', () => {
  it('gives mock scenarios and their route editors shareable routes', () => {
    const environment = { project: 'store', name: 'local' }
    expect(environmentUIPath(environment, 'mocks', { scenario: 'sold-out' })).toBe('/environments/store/local?tab=mocks&scenario=sold-out')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'route', scenario: 'sold-out' })).toBe('/environments/store/local?tab=mocks&scenario=sold-out&workspace=route')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'route', scenario: 'sold-out', route: 'lookup' })).toBe('/environments/store/local?tab=mocks&scenario=sold-out&workspace=route&route=lookup')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'route', route: 'ignored' })).toBe('/environments/store/local?tab=mocks')
  })
})
