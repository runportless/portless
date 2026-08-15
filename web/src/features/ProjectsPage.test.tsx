import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment, Project } from '../types'
import { ProjectsPage } from './ProjectsPage'

const project = { name: 'store', sources: [{ name: 'store', services: ['checkout', 'inventory', 'orders'] }] } as Project
const environment = {
  project: 'store',
  name: 'local',
  revision: 1,
  status: 'healthy',
  services: [{
    name: 'checkout',
    kind: 'process',
    required: true,
    health: { kind: 'http', timeout: 5, interval: 10 },
    status: 'ready',
    generation: 1,
    endpoints: [],
    restartCount: 0,
    recentRequests: 0,
  }],
  sources: [{ name: 'store', path: '/Users/dev/workspace/store', status: 'ready', scannedAt: new Date().toISOString() }],
  bindings: [],
  connections: [],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
} satisfies Environment
const qaEnvironment = { ...environment, name: 'qa-local', revision: 2, sources: [{ ...environment.sources[0] }] } satisfies Environment

describe('projects page', () => {
  it('uses the concise Projects title without control-plane copy or runtime controls', () => {
    const markup = renderProjects()

    expect(markup).toContain('<h1>Projects</h1>')
    expect(markup).not.toContain('LOCAL CONTROL PLANE')
    expect(markup).not.toContain('Projects &amp; environments')
    expect(markup).not.toContain('CONTAINER RUNTIME')
  })

  it('keeps the project detail heading and environment listing', () => {
    const markup = renderProjects(project)

    expect(markup).toContain('<div class="eyebrow">PROJECT</div>')
    expect(markup).toContain('<h1>store</h1>')
    expect(markup).not.toContain('sources ·')
    expect(markup).toContain('<code>local</code>')
    expect(markup).toContain('<span>ENVIRONMENTS</span>')
    expect(markup).toContain('<small>1 environment</small>')
    expect(markup).toContain('aria-haspopup="dialog"')
    expect(markup).toContain('>CREATE ENVIRONMENT</button>')
    expect(markup).not.toContain('class="panel clone-panel"')
    expect(markup).not.toContain('class="create-environment-modal"')
    expect(markup).toContain('PROJECT SOURCES')
    expect(markup).toContain('Filesystem bindings')
    expect(markup).toContain('/Users/dev/workspace/store')
    expect(markup).toContain('checkout, inventory, orders')
    expect(markup).not.toContain('one logical application, many repositories')
    expect(markup).not.toContain('Each environment clones this topology')
    expect(markup.indexOf('ENVIRONMENTS')).toBeLessThan(markup.indexOf('PROJECT SOURCES'))
  })

  it('groups environment bindings beneath one logical project source', () => {
    const markup = renderProjects(project, [environment, qaEnvironment])

    expect(markup).toContain('<span>PROJECT SOURCES</span><small>1 source</small>')
    expect(markup).toContain('<small>local, qa-local</small>')
    expect(markup.match(/class="table-row project-source-row"/g)).toHaveLength(1)
  })

  it('keeps the empty state focused on setup instructions', () => {
    const markup = renderToStaticMarkup(<ProjectsPage projects={[]} environments={[]} onNavigate={() => undefined} onChanged={async () => undefined} />)

    expect(markup).toContain('No environments yet')
    expect(markup).toContain('Start one repository or assemble several.')
    expect(markup).not.toContain('repository—or')
    expect(markup).not.toContain('empty-environment__graphic')
  })
})

function renderProjects(selectedProject?: Project, renderedEnvironments = [environment]) {
  return renderToStaticMarkup(<ProjectsPage projects={[project]} environments={renderedEnvironments} selectedProject={selectedProject} onNavigate={() => undefined} onChanged={async () => undefined} />)
}
