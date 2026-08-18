import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { environmentSessionKey, LoadingScreen, parseRoute } from './App'

describe('application routes', () => {
  it('recognizes the top-level settings page without inventing project scope', () => {
    expect(parseRoute('/settings')).toEqual({ settings: true, settingsTab: 'appearance', tab: 'overview' })
    expect(parseRoute('/projects/billing')).toEqual({ project: 'billing', settings: false, settingsTab: 'appearance', tab: 'overview' })
  })

  it('routes directly to a scoped MCP settings view', () => {
    expect(parseRoute('/settings?tab=mcp&env=store%2Flocal')).toEqual({
      settings: true,
      settingsTab: 'mcp',
      settingsEnvironment: 'store/local',
      tab: 'overview',
    })
    expect(parseRoute('/settings?tab=not-a-setting')).toEqual({ settings: true, settingsTab: 'appearance', tab: 'overview' })
    expect(parseRoute('/environments/store/local?tab=traffic')).toEqual({
      project: 'store',
      environment: 'local',
      settings: false,
      settingsTab: 'appearance',
      tab: 'traffic',
    })
  })

  it('replaces environment view state when the daemon instance changes', () => {
    const environment = { project: 'billing', name: 'local' }
    expect(environmentSessionKey(environment, null)).toBe('billing/local@daemon-pending')
    expect(environmentSessionKey(environment, { instanceId: 'daemon-a' })).toBe('billing/local@daemon-a')
    expect(environmentSessionKey(environment, { instanceId: 'daemon-b' })).toBe('billing/local@daemon-b')
  })

  it('renders a clear, accessible connecting state', () => {
    const markup = renderToStaticMarkup(createElement(LoadingScreen))
    expect(markup).toContain('class="splash__content" role="status" aria-live="polite"')
    expect(markup).toContain('class="splash__spinner" aria-hidden="true"')
    expect(markup).toContain('Connecting to the local control plane…')
  })
})
