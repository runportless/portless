import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment, Project } from '../types'
import { AppChrome } from './Chrome'

const project = { name: 'billing' } as Project
const environment = { project: 'billing', name: 'local', status: 'healthy' } as Environment

function renderChrome(activeEnvironment?: Environment, activeView: 'overview' | 'traffic' = 'overview') {
  return renderToStaticMarkup(
    <AppChrome
      projects={[project]}
      environments={[environment]}
      activeProject={activeEnvironment ? project : undefined}
      activeEnvironment={activeEnvironment}
      activeView={activeView}
      commands={[]}
      onNavigate={() => undefined}
    >
      <div>content</div>
    </AppChrome>,
  )
}

describe('application navigation', () => {
  it('does not invent an environment scope from the first environment', () => {
    const markup = renderChrome()

    expect(markup).toContain('All projects')
    expect(markup).not.toContain('Providers')
    expect(markup).not.toContain('Traffic')
    expect(markup).not.toContain('Timeline')
  })

  it('shows and selects views only for the active environment', () => {
    const markup = renderChrome(environment, 'traffic')

    expect(markup).toContain('aria-label="billing/local views"')
    expect(markup).toContain('Providers')
    expect(markup).toContain('Timeline')
    expect(markup).toContain('<button class="is-active" aria-current="page"')
  })
})
