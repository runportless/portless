import { describe, expect, it } from 'vitest'
import { environmentUIPath } from './navigation'

describe('environment navigation', () => {
  it('gives the mock creation workspace a shareable route', () => {
    const environment = { project: 'store', name: 'local' }
    expect(environmentUIPath(environment, 'mocks', { workspace: 'create' })).toBe('/environments/store/local?tab=mocks&workspace=create')
    expect(environmentUIPath(environment, 'mocks', { profile: 'sold-out' })).toBe('/environments/store/local?tab=mocks&profile=sold-out')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'create', profile: 'ignored' })).toBe('/environments/store/local?tab=mocks&workspace=create')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'route', profile: 'sold-out' })).toBe('/environments/store/local?tab=mocks&workspace=route&profile=sold-out')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'route', profile: 'sold-out', route: 'lookup' })).toBe('/environments/store/local?tab=mocks&workspace=route&profile=sold-out&route=lookup')
    expect(environmentUIPath(environment, 'mocks', { workspace: 'route', route: 'ignored' })).toBe('/environments/store/local?tab=mocks&workspace=route')
  })
})
