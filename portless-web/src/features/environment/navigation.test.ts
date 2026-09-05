import { describe, expect, it } from 'vitest'
import { environmentUIPath, environmentViews, parseEnvironmentView } from './navigation'

describe('environment navigation', () => {
  it('keeps one ordered set of views and preserves their public URLs', () => {
    expect(environmentViews.map((view) => view.name)).toEqual(['overview', 'topology', 'traffic', 'mocks', 'recordings', 'faults', 'bindings', 'timeline'])
    for (const { name } of environmentViews) {
      expect(parseEnvironmentView(name)).toBe(name)
      expect(environmentUIPath({ project: 'Store Demo', name: 'QA / local' }, name)).toBe(`/environments/Store%20Demo/QA%20%2F%20local${name === 'overview' ? '' : `?tab=${name}`}`)
    }
    expect(parseEnvironmentView(null)).toBe('overview')
    expect(parseEnvironmentView('invalid')).toBe('overview')
  })

  it('retains source-aware traffic filters', () => {
    expect(environmentUIPath({ project: 'store', name: 'local' }, 'traffic', { edge: 'checkout:orders', protocol: 'http' })).toBe('/environments/store/local?tab=traffic&edge=checkout%3Aorders&protocol=http')
  })

  it('gives the scenario split view shareable route selections', () => {
    const environment = { project: 'store', name: 'local' }
    expect(environmentUIPath(environment, 'mocks', { scenario: 'sold-out' })).toBe('/environments/store/local?tab=mocks&scenario=sold-out')
    expect(environmentUIPath(environment, 'mocks', { createRoute: true, scenario: 'sold-out' })).toBe('/environments/store/local?tab=mocks&scenario=sold-out&create=route')
    expect(environmentUIPath(environment, 'mocks', { scenario: 'sold-out', route: 'lookup' })).toBe('/environments/store/local?tab=mocks&scenario=sold-out&route=lookup')
    expect(environmentUIPath(environment, 'mocks', { createRoute: true, route: 'ignored' })).toBe('/environments/store/local?tab=mocks')
  })
})
