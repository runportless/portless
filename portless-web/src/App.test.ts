import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { environmentSessionKey, LoadingScreen, pageTitle, parseRoute, settingsToggleDestination } from './App'

describe('application routes', () => {
  it('recognizes the top-level settings page without inventing project scope', () => {
    expect(parseRoute('/settings')).toEqual({ settings: true, settingsTab: 'appearance', view: 'overview' })
    expect(parseRoute('/projects/billing')).toEqual({ project: 'billing', settings: false, settingsTab: 'appearance', view: 'overview' })
  })

  it('routes directly to a scoped MCP settings view', () => {
    expect(parseRoute('/settings?tab=mcp&env=store%2Flocal')).toEqual({
      settings: true,
      settingsTab: 'mcp',
      settingsEnvironment: 'store/local',
      view: 'overview',
    })
    expect(parseRoute('/settings?tab=not-a-setting')).toEqual({ settings: true, settingsTab: 'appearance', view: 'overview' })
    expect(parseRoute('/environments/store/local?tab=traffic')).toEqual({
      project: 'store',
      environment: 'local',
      settings: false,
      settingsTab: 'appearance',
      view: 'traffic',
    })
    expect(parseRoute('/environments/store/local?tab=mocks&scenario=sold-out')).toEqual({
      project: 'store',
      environment: 'local',
      settings: false,
      settingsTab: 'appearance',
      mockScenario: 'sold-out',
      view: 'mocks',
    })
    expect(parseRoute('/environments/store/local?tab=traffic&scenario=ignored')).not.toHaveProperty('mockScenario')
    expect(parseRoute('/environments/store/local?tab=mocks&scenario=sold-out&create=route')).toEqual({
      project: 'store',
      environment: 'local',
      settings: false,
      settingsTab: 'appearance',
      mockScenario: 'sold-out',
      mockCreateRoute: true,
      view: 'mocks',
    })
    expect(parseRoute('/environments/store/local?tab=mocks&scenario=sold-out&route=lookup')).toEqual({
      project: 'store',
      environment: 'local',
      settings: false,
      settingsTab: 'appearance',
      mockScenario: 'sold-out',
      mockRoute: 'lookup',
      view: 'mocks',
    })
    const orphanedRoute = parseRoute('/environments/store/local?tab=mocks&create=route&route=ignored')
    expect(orphanedRoute).not.toHaveProperty('mockCreateRoute')
    expect(orphanedRoute).not.toHaveProperty('mockRoute')
    expect(parseRoute('/environments/store/local?tab=traffic&scenario=sold-out&create=route')).not.toHaveProperty('mockCreateRoute')
    expect(parseRoute('/environments/store/local?tab=mocks&scenario=sold-out&route=new')).toHaveProperty('mockRoute', 'new')
  })

  it('toggles settings back to the exact previous route', () => {
    const environmentRoute = '/environments/store/local?tab=traffic&edge=checkout%3Aorders'

    expect(settingsToggleDestination(environmentRoute)).toBe('/settings')
    expect(settingsToggleDestination('/settings?tab=mcp', environmentRoute)).toBe(environmentRoute)
    expect(settingsToggleDestination('/settings', '/settings?tab=runtime')).toBe('/projects')
  })

  it('replaces environment view state when the daemon instance changes', () => {
    const environment = { project: 'billing', name: 'local' }
    expect(environmentSessionKey(environment, null)).toBe('billing/local@daemon-pending')
    expect(environmentSessionKey(environment, { instanceId: 'daemon-a' })).toBe('billing/local@daemon-a')
    expect(environmentSessionKey(environment, { instanceId: 'daemon-b' })).toBe('billing/local@daemon-b')
  })

  it('includes the current project in the browser title', () => {
    expect(pageTitle('Store Checkout')).toBe('Portless | Store Checkout')
    expect(pageTitle()).toBe('Portless')
  })

  it('renders a clear, accessible connecting state', () => {
    const markup = renderToStaticMarkup(createElement(LoadingScreen))
    expect(markup).toContain('class="splash__content" role="status" aria-live="polite"')
    expect(markup).toContain('class="splash__spinner" aria-hidden="true"')
    expect(markup).toContain('Connecting to the local control plane…')
  })
})
