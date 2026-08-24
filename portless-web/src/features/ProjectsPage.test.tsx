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
    launchMode: 'managed',
    generation: 1,
    endpoints: [],
    restartCount: 0,
    recentRequests: 0,
  }],
  sources: [{ name: 'store', path: '/Users/dev/workspace/store', status: 'ready', createdAt: new Date().toISOString(), scannedAt: new Date().toISOString() }],
  bindings: [],
  connections: [],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
} satisfies Environment
const qaEnvironment = { ...environment, name: 'qa-local', revision: 2, sources: [{ ...environment.sources[0] }] } satisfies Environment
const stoppedEnvironment = { ...qaEnvironment, name: 'demo', status: 'stopped' } satisfies Environment

describe('projects page', () => {
  it('uses the concise Projects title without control-plane copy or runtime controls', () => {
    const markup = renderProjects()

    expect(markup).toContain('<h1>Projects</h1>')
    expect(markup).not.toContain('LOCAL CONTROL PLANE')
    expect(markup).not.toContain('Projects &amp; environments')
    expect(markup).not.toContain('CONTAINER RUNTIME')
  })

  it('lists each project once instead of rendering one row per environment', () => {
    const markup = renderProjects(undefined, [environment, qaEnvironment])

    expect(markup).toContain('<div class="panel-title"><span>PROJECTS</span></div>')
    expect(markup.match(/class="table-row project-index-row"/g)).toHaveLength(1)
    expect(markup).not.toContain('class="table-row environment-row"')
    expect(markup).toContain('title="local, qa-local"')
    expect(markup).toContain('<span>Last updated</span>')
    expect(markup).not.toContain('<span>Why</span>')
    expect(markup).toContain('<time dateTime=')
  })

  it('does not degrade a project because an intentionally stopped environment exists', () => {
    const markup = renderProjects(undefined, [environment, stoppedEnvironment])

    expect(markup).toContain('<span class="status status--success" title="healthy">')
    expect(markup).not.toContain('title="degraded"')
  })

  it('offers to start a stopped environment from its project row', () => {
    const markup = renderProjects(project, [stoppedEnvironment])

    expect(markup).toContain('aria-label="Start demo"')
    expect(markup).toContain('>START</button>')
    expect(markup).not.toContain('aria-label="Stop demo"')
  })

  it('keeps the project detail heading and environment listing', () => {
    const markup = renderProjects(project)

    expect(markup).toContain('<div class="eyebrow">PROJECT</div>')
    expect(markup).toContain('<h1>store</h1>')
    expect(markup).not.toContain('sources ·')
    expect(markup).toContain('<span>Status</span><span>Environment</span><span>Ready</span><span>Remote</span><span>Modified</span><span>Why</span>')
    expect(markup).not.toContain('<span>Project</span>')
    expect(markup).toContain('<strong title="local">local</strong>')
    expect(markup).toContain('<span class="status status--success" title="healthy"><span class="status__mark" aria-hidden="true">●</span><span>healthy</span></span>')
    expect(markup).toContain('<span>ENVIRONMENTS</span>')
    expect(markup).toContain('aria-label="Stop all store environments"')
    expect(markup).toContain('>STOP ALL</button>')
    expect(markup).toContain('<span>Modified</span>')
    expect(markup).toContain('<span>Why</span></div><span aria-hidden="true"></span>')
    expect(markup).toContain('aria-label="Stop local"')
    expect(markup).not.toContain('<span>Age</span>')
    expect(markup).toContain('<time dateTime=')
    expect(markup).not.toContain('<small>1 environment</small>')
    expect(markup).toContain('aria-haspopup="dialog"')
    expect(markup).toContain('>CREATE ENVIRONMENT</button>')
    expect(markup).not.toContain('class="panel clone-panel"')
    expect(markup).not.toContain('class="create-environment-modal"')
    expect(markup).toContain('<span>SOURCES</span>')
    expect(markup).toContain('>ADD SOURCE</button>')
    expect(markup).toContain('<span>Name</span><span>Path</span><span>Services</span><span aria-hidden="true"></span>')
    expect(markup).not.toContain('<span>Actions</span>')
    expect(markup).toContain('/Users/dev/workspace/store')
    expect(markup).toContain('checkout, inventory, orders')
    expect(markup).toContain('<div class="checkout-source"><span class="status status--success" title="ready"><span class="status__mark" aria-hidden="true">●</span></span><strong>store</strong></div>')
    expect(markup).not.toContain('one logical application, many repositories')
    expect(markup).not.toContain('Each environment clones this topology')
    expect(markup.indexOf('<span>ENVIRONMENTS</span>')).toBeLessThan(markup.indexOf('<span>SOURCES</span>'))
  })

  it('deduplicates checkout paths without repeating environment names in source rows', () => {
    const markup = renderProjects(project, [environment, qaEnvironment])

    expect(markup).toContain('<span>SOURCES</span><button')
    expect(markup).not.toContain('<small>local, qa-local</small>')
    expect(markup.match(/<code class="truncate" title="\/Users\/dev\/workspace\/store">/g)).toHaveLength(1)
    expect(markup).toContain('>DELETE</button>')
    expect(markup.match(/class="table-row project-source-row"/g)).toHaveLength(1)
  })

  it('paginates project environments and sources independently at ten rows', () => {
    const environments = Array.from({ length: 11 }, (_, index) => ({
      ...environment,
      name: `environment-${String(index + 1).padStart(2, '0')}`,
      revision: index + 1,
      sources: [{ ...environment.sources[0] }],
    } satisfies Environment))
    const sources = Array.from({ length: 11 }, (_, index) => ({
      name: `source-${String(index + 1).padStart(2, '0')}`,
      services: [`service-${String(index + 1).padStart(2, '0')}`],
    }))
    const paginatedProject = { ...project, sources } as Project

    const markup = renderProjects(paginatedProject, environments)

    expect(markup).toContain('aria-label="environments pagination"')
    expect(markup).toContain('aria-label="sources pagination"')
    expect(markup.match(/class="table-row environment-row"/g)).toHaveLength(10)
    expect(markup.match(/class="table-row project-source-row"/g)).toHaveLength(10)
    expect(markup).toContain('1–10 of 11')
    expect(markup).toContain('<strong title="environment-10">environment-10</strong>')
    expect(markup).not.toContain('<strong title="environment-11">environment-11</strong>')
    expect(markup).toContain('<strong>source-10</strong>')
    expect(markup).not.toContain('<strong>source-11</strong>')
  })

  it('distinguishes an environment that needs source configuration from one using remote providers', () => {
    const inventoryProject = { ...project, sources: [{ name: 'inventory', services: ['inventory'] }] } as Project
    const local = { ...environment, sources: [{ ...environment.sources[0], name: 'inventory', path: '/Users/dev/workspace/inventory' }] } satisfies Environment
    const needsConfiguration = {
      ...qaEnvironment,
      sources: [],
      issues: [{ code: 'MISSING_BINDING', subject: 'inventory', message: 'component has no provider binding' }],
    } satisfies Environment
    const remote = { ...qaEnvironment, name: 'remote', sources: [], issues: [] } satisfies Environment

    const markup = renderProjects(inventoryProject, [local, needsConfiguration, remote])

    expect(markup).toContain('configuration required')
    expect(markup).toContain('not bound locally')
    expect(markup).not.toContain('<small>qa-local</small>')
    expect(markup).not.toContain('<small>remote</small>')
    expect(markup).toContain('component has no provider binding')
  })

  it('keeps the empty project state focused on setup instructions', () => {
    const markup = renderToStaticMarkup(<ProjectsPage projects={[]} environments={[]} onNavigate={() => undefined} onChanged={async () => undefined} />)

    expect(markup).toContain('No projects yet')
    expect(markup).toContain('Start one repository or assemble several.')
    expect(markup).not.toContain('repository—or')
    expect(markup).not.toContain('empty-environment__graphic')
  })
})

function renderProjects(selectedProject?: Project, renderedEnvironments: Environment[] = [environment]) {
  return renderToStaticMarkup(<ProjectsPage projects={[project]} environments={renderedEnvironments} selectedProject={selectedProject} onNavigate={() => undefined} onChanged={async () => undefined} />)
}
