import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import type { Environment } from '../api/contracts/environments'
import type { Project } from '../api/contracts/projects'
import type { ControlPlaneHealth, DaemonDiagnostics, DaemonHandoffStatus, DaemonStatus } from '../api/contracts/system'
import { AppChrome, scrollCommandIntoView, type EnvironmentView, type SettingsView } from './Chrome'

const project = { name: 'billing' } as Project
const environment = { project: 'billing', name: 'local', status: 'healthy' } as Environment
const daemon = { state: 'ready', instanceId: 'instance', activeEnvironments: [], recoveryProblems: [] } as unknown as DaemonStatus
const handoff = { state: 'ready', verifiedAt: '2026-08-25T12:00:00Z', activeEnvironments: [], problems: [] } as DaemonHandoffStatus
const diagnostics = { inventory: { processes: 0, containers: 0, proxyListeners: 0, activeEnvironments: 0, problems: [] } } as unknown as DaemonDiagnostics
const controlPlaneHealth: ControlPlaneHealth = { api: { state: 'ready', latencyMs: 2 }, events: { state: 'idle', connections: 0, connected: 0 } }

function renderChrome(activeEnvironment?: Environment, activeView: EnvironmentView = 'overview', settingsActive = false, settingsView: SettingsView = 'appearance', live = true) {
  return renderToStaticMarkup(
    <AppChrome
      projects={[project]}
      environments={[environment]}
      activeProject={activeEnvironment ? project : undefined}
      activeEnvironment={activeEnvironment}
      activeView={activeView}
      settingsActive={settingsActive}
      settingsView={settingsView}
      commands={[]}
      daemon={daemon}
      diagnostics={diagnostics}
      controlPlaneHealth={controlPlaneHealth}
      live={live}
      onNavigate={() => undefined}
      onSettingsToggle={() => undefined}
      onDaemonRefresh={async () => daemon}
      onDaemonDiagnosticsRefresh={async () => diagnostics}
      onDaemonHandoffVerify={async () => handoff}
      onDaemonRestart={async (instanceId) => ({ restarting: true, restartId: 'restart', reason: 'browser', previousInstanceId: instanceId, targetBuildId: 'build', acceptedAt: '2026-08-25T12:00:00Z', deadlineAt: '2026-08-25T12:00:05Z', handoff: true, activeEnvironments: [] })}
      onDaemonReconnected={async () => undefined}
    >
      <div>content</div>
    </AppChrome>,
  )
}

describe('application navigation', () => {
  it('does not invent an environment scope from the first environment', () => {
    const markup = renderChrome()

    expect(markup).toContain('aria-label="Portless projects"')
    expect(markup).not.toContain('<small>local</small>')
    expect(markup).not.toContain('environment-chip')
    expect(markup).toContain('aria-label="Projects"')
    expect(markup).not.toContain('All projects')
    expect(markup).not.toContain('Workspace')
    expect(markup).not.toContain('Bindings')
    expect(markup).not.toContain('Topology')
    expect(markup).not.toContain('Traffic')
    expect(markup).not.toContain('Timeline')
    expect(markup).toContain('<nav class="crumbs" aria-label="Breadcrumb"><strong aria-current="page">projects</strong></nav>')
    expect(markup).not.toContain('<strong>all</strong>')
    expect(markup).toContain('<span>ready</span><small>DETAILS ›</small>')
    expect(markup).not.toContain('<span>daemon ready</span>')
    expect(markup).toContain('aria-label="daemon ready"')
    expect(markup).toContain('aria-label="Collapse navigation" aria-expanded="true"')
    expect(markup).toContain('aria-expanded="false"')
  })

  it('shows and selects views only for the active environment', () => {
    const markup = renderChrome(environment, 'topology')

    expect(markup).toContain('aria-label="billing/local views"')
    expect(markup).toContain('Bindings')
    expect(markup).toContain('Topology')
    expect(markup).toContain('Timeline')
    expect(markup).toContain('<a href="/projects">projects</a>')
    expect(markup).toContain('<a href="/projects/billing">billing</a>')
    expect(markup).toContain('<strong aria-current="page">local</strong>')
    expect(markup).toContain('<nav class="crumbs" aria-label="Breadcrumb"><a href="/projects">projects</a><b>/</b><a href="/projects/billing">billing</a><b>/</b><strong aria-current="page">local</strong></nav>')
    expect(markup).toContain('<button class="is-active" aria-label="Topology" aria-current="page"')
    expect(markup.indexOf('<span>Faults</span>')).toBeLessThan(markup.indexOf('<span>Bindings</span>'))
    expect(markup.indexOf('<span>Bindings</span>')).toBeLessThan(markup.indexOf('<span>Timeline</span>'))
  })

  it('keeps settings globally available and marks the settings route', () => {
    const markup = renderChrome(undefined, 'overview', true, 'mcp')

    expect(markup).toContain('aria-label="Application"')
    expect(markup).toContain('class="is-active" aria-label="Settings" aria-current="page"><svg')
    expect(markup).toContain('<svg class="settings-gear"')
    expect(markup).toContain('<span>Settings</span>')
    expect(markup).toContain('<nav class="crumbs" aria-label="Breadcrumb"><a href="/projects">projects</a><b>/</b><a href="/settings">settings</a><b>/</b><strong aria-current="page">mcp</strong></nav>')
  })

  it('marks only the reconnecting daemon text for animation', () => {
    const reconnecting = renderChrome(undefined, 'overview', false, 'appearance', false)
    const ready = renderChrome()

    expect(reconnecting).toContain('<span class="daemon-state--reconnecting">reconnecting</span>')
    expect(ready).toContain('<span>ready</span>')
    expect(ready).not.toContain('daemon-state--reconnecting')
  })

  it('restores a compact icon rail with accessible navigation labels', () => {
    vi.stubGlobal('window', {
      localStorage: { getItem: (key: string) => key === 'portless.sidebar-collapsed' ? 'true' : null },
      sessionStorage: { getItem: (key: string) => key === 'portless.expanded-projects' ? JSON.stringify(['billing']) : null },
    })

    try {
      const markup = renderChrome(environment, 'traffic')

      expect(markup).toContain('<div class="shell shell--sidebar-collapsed">')
      expect(markup).toContain('aria-label="Expand navigation" aria-expanded="false"')
      expect(markup).toContain('aria-label="billing project, 1 environment" title="billing"')
      expect(markup).toContain('aria-label="billing/local, healthy" aria-current="page" title="billing/local"')
      expect(markup).toContain('aria-label="Traffic" aria-current="page" title="Traffic"')
      expect(markup).toContain('aria-label="Settings" title="Settings"')
      expect(markup).toContain('aria-label="Daemon ready" aria-expanded="false" title="Daemon ready"')
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

describe('command palette', () => {
  it('scrolls the selected command to the nearest visible position', () => {
    const scrollIntoView = vi.fn()

    scrollCommandIntoView({ scrollIntoView })

    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' })
  })
})
