import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment, RuntimeStatus } from '../types'
import { SettingsPage, settingsPath, type SettingsTab } from './SettingsPage'

const runtime: RuntimeStatus = {
  preference: 'auto',
  selected: 'docker',
  state: 'ready',
  version: '29.4.0',
  candidates: [
    { name: 'podman', state: 'missing', reason: 'Podman is not installed.' },
    { name: 'docker', state: 'ready', version: '29.4.0' },
  ],
}

const environment = {
  project: 'store',
  name: 'local',
  status: 'healthy',
  sources: [{ name: 'store', path: '/Users/dev/store', status: 'ready', createdAt: '2026-08-18T12:00:00Z', scannedAt: '2026-08-18T12:00:00Z' }],
  services: [],
  connections: [],
} as unknown as Environment

describe('settings page', () => {
  it('offers system, light, and dark browser themes', () => {
    const markup = renderSettings('appearance')

    expect(markup).toContain('<h1>Settings</h1>')
    expect(markup).toContain('role="tablist" aria-label="Settings sections"')
    expect(markup).toContain('role="radiogroup" aria-label="Theme"')
    expect(markup).toContain('role="radio" aria-checked="true" class="theme-choice is-selected"')
    expect(markup).toContain('theme-preview--system')
    expect(markup).toContain('theme-preview--light')
    expect(markup).toContain('theme-preview--dark')
    expect(markup).toContain('light theme active')
  })

  it('contains the container runtime configuration on its own tab', () => {
    const markup = renderSettings('runtime')

    expect(markup).toContain('aria-selected="true" aria-controls="settings-panel-runtime"')
    expect(markup).toContain('CONTAINER RUNTIME')
    expect(markup).toContain('preference: auto')
    expect(markup).toContain('USE DOCKER')
    expect(markup).toContain('USE PODMAN')
    expect(markup).toContain('USE AUTOMATIC SELECTION')
    expect(markup).not.toContain('role="radiogroup" aria-label="Theme"')
  })

  it('generates a read-only, environment-scoped MCP configuration by default', () => {
    const markup = renderSettings('mcp')

    expect(markup).toContain('CONFIGURE CLIENT')
    expect(markup).toContain('<div class="eyebrow">ACCESS</div>')
    expect(markup).toContain('Choose the environments and capabilities available to your MCP client.')
    expect(markup).toContain('portless-store-local')
    expect(markup).toContain('&quot;--env&quot;')
    expect(markup).toContain('&quot;store/local&quot;')
    expect(markup).toContain('READ ONLY · 15 TOOLS')
    expect(markup).not.toContain('--allow-lifecycle')
    expect(markup).not.toContain('--allow-traffic-control')
    expect(markup).not.toContain('--allow-sensitive-traffic')
    expect(markup).not.toContain('START MCP')
    expect(markup).not.toContain('STOP MCP')
  })

  it('creates stable settings URLs', () => {
    expect(settingsPath('appearance')).toBe('/settings')
    expect(settingsPath('runtime')).toBe('/settings?tab=runtime')
    expect(settingsPath('mcp', 'store/local')).toBe('/settings?tab=mcp&env=store%2Flocal')
  })
})

function renderSettings(tab: SettingsTab) {
  return renderToStaticMarkup(<SettingsPage
    tab={tab}
    preference="light"
    resolvedTheme="light"
    runtime={runtime}
    environments={[environment]}
    onNavigate={() => undefined}
    onPreferenceChange={() => undefined}
    onRuntimeChange={async () => undefined}
    onRuntimeStart={async () => undefined}
  />)
}
