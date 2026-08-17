import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { DaemonStatus, Environment, Project } from '../types'
import { AppChrome, type EnvironmentView } from './Chrome'

const project = { name: 'billing' } as Project
const environment = { project: 'billing', name: 'local', status: 'healthy' } as Environment
const daemon = { state: 'ready', instanceId: 'instance', activeEnvironments: [], recoveryProblems: [] } as unknown as DaemonStatus

function renderChrome(activeEnvironment?: Environment, activeView: EnvironmentView = 'overview', settingsActive = false) {
  return renderToStaticMarkup(
    <AppChrome
      projects={[project]}
      environments={[environment]}
      activeProject={activeEnvironment ? project : undefined}
      activeEnvironment={activeEnvironment}
      activeView={activeView}
      settingsActive={settingsActive}
      commands={[]}
      daemon={daemon}
      onNavigate={() => undefined}
      onDaemonRefresh={async () => daemon}
      onDaemonRestart={async (instanceId) => ({ restarting: true, previousInstanceId: instanceId, handoff: true, activeEnvironments: [] })}
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
    expect(markup).toContain('daemon ready')
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
    expect(markup).toContain('<button class="is-active" aria-current="page"')
  })

  it('keeps settings globally available and marks the settings route', () => {
    const markup = renderChrome(undefined, 'overview', true)

    expect(markup).toContain('aria-label="Application"')
    expect(markup).toContain('class="is-active" aria-current="page"><svg')
    expect(markup).toContain('<svg class="settings-gear"')
    expect(markup).toContain('<span>Settings</span>')
    expect(markup).toContain('<nav class="crumbs" aria-label="Breadcrumb"><a href="/projects">projects</a><b>/</b><strong aria-current="page">settings</strong></nav>')
  })
})
