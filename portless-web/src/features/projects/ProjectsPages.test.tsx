import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { CreateEnvironmentDialog } from './CreateEnvironmentDialog'
import { ProjectOverviewPage } from './ProjectOverviewPage'
import { ForgetProjectDialog, ProjectsIndexPage, sortProjectRegistryRows } from './ProjectsIndexPage'
import { emptyProjectNavigationPreferences, type ProjectNavigationPreferences } from './projectNavigation'

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

describe('projects index page', () => {
  it('uses a focused project registry without initializing project detail UI', () => {
    const markup = renderProjectIndex()

    expect(markup).toContain('<h1>Projects</h1>')
    expect(markup).toContain('<div class="eyebrow">Workspace</div>')
    expect(markup).not.toContain('The sidebar shows one project at a time. Use this page to find, switch, or forget projects.')
    expect(markup).toContain('aria-label="Project registry controls"')
    expect(markup).toContain('placeholder="Search"')
    expect(markup).toContain('<span>all</span><strong>1</strong>')
    expect(markup).not.toContain('LOCAL CONTROL PLANE')
    expect(markup).not.toContain('Projects &amp; environments')
    expect(markup).not.toContain('CONTAINER RUNTIME')
    expect(markup).not.toContain('CREATE ENVIRONMENT')
    expect(markup).not.toContain('ADD SOURCE')
  })

  it('lists each project once instead of rendering one row per environment', () => {
    const markup = renderProjectIndex([environment, qaEnvironment])

    expect(markup).toContain('class="panel projects-table project-registry-table" aria-label="Projects"')
    expect(markup).toContain('table-row project-registry-row project-registry-row--interactive')
    expect(markup.match(/class="project-registry-row__project"/g)).toHaveLength(1)
    expect(markup).not.toContain('class="table-row environment-row"')
    expect(markup).toContain('<span>Last opened</span>')
    expect(markup).toContain('aria-label="Project actions for store"')
    expect(markup).toContain('<time>never</time>')
  })

  it('sorts projects by most recently opened by default and exposes every column sort', () => {
    const alpha = { name: 'alpha' } as Project
    const middle = { name: 'middle' } as Project
    const zulu = { name: 'zulu' } as Project
    const navigation = {
      ...emptyProjectNavigationPreferences(),
      lastOpenedByProject: {
        middle: '2026-08-20T12:00:00Z',
        zulu: '2026-08-29T12:00:00Z',
      },
    }

    const markup = renderProjectIndex([], navigation, [alpha, middle, zulu])

    expect(markup).toContain('project-registry-row sortable-header-row is-default-sort')
    expect(markup).toContain('aria-sort="descending"><span>Last opened</span><button class="sortable-column-sort-control" type="button" aria-label="Sort Last opened ascending"')
    expect(markup).toContain('aria-label="Sort Project ascending"')
    expect(markup).toContain('aria-label="Sort Runtime ascending"')
    expect(markup).toContain('aria-label="Sort Environments ascending"')
    expect(markup.indexOf('<strong>zulu</strong>')).toBeLessThan(markup.indexOf('<strong>middle</strong>'))
    expect(markup.indexOf('<strong>middle</strong>')).toBeLessThan(markup.indexOf('<strong>alpha</strong>'))
  })

  it('sorts project rows by each registry column with project name as the stable tie-breaker', () => {
    const rows = [
      registryRow('bravo', 'healthy', 1, '2026-08-20T12:00:00Z'),
      registryRow('alpha', 'stopped', 3, undefined),
      { ...registryRow('charlie', 'failed', 2, '2026-08-29T12:00:00Z'), focused: true },
    ]

    expect(sortProjectRegistryRows(rows, { key: 'project', direction: 'asc' }).map((row) => row.project.name)).toEqual(['alpha', 'bravo', 'charlie'])
    expect(sortProjectRegistryRows(rows, { key: 'runtime', direction: 'asc' }).map((row) => row.project.name)).toEqual(['charlie', 'bravo', 'alpha'])
    expect(sortProjectRegistryRows(rows, { key: 'environments', direction: 'desc' }).map((row) => row.project.name)).toEqual(['alpha', 'charlie', 'bravo'])
    expect(sortProjectRegistryRows(rows, { key: 'lastOpened', direction: 'desc' }).map((row) => row.project.name)).toEqual(['charlie', 'bravo', 'alpha'])
  })

  it('paginates the project registry after ten rows', () => {
    const projects = Array.from({ length: 11 }, (_, index) => ({
      name: `project-${String(index + 1).padStart(2, '0')}`,
    } as Project))
    const navigation = {
      ...emptyProjectNavigationPreferences(),
      lastOpenedByProject: Object.fromEntries(projects.map((item, index) => [item.name, new Date(Date.UTC(2026, 7, index + 1)).toISOString()])),
    }

    const markup = renderProjectIndex([], navigation, projects)

    expect(markup).toContain('aria-label="projects pagination"')
    expect(markup).toContain('1–10 of 11')
    expect(markup.match(/class="project-registry-row__project"/g)).toHaveLength(10)
    expect(markup).toContain('<strong>project-11</strong>')
    expect(markup).not.toContain('<strong>project-01</strong>')
    expect(markup).toContain('aria-label="Previous projects page" disabled=""')
    expect(markup).toContain('aria-label="Next projects page"')
  })

  it('does not degrade a project because an intentionally stopped environment exists', () => {
    const markup = renderProjectIndex([environment, stoppedEnvironment])

    expect(markup).toContain('<span class="status status--success" title="healthy">')
    expect(markup).not.toContain('title="degraded"')
  })

  it('keeps the empty state focused on setup instructions', () => {
    const markup = renderToStaticMarkup(<ProjectsIndexPage projects={[]} environments={[]} navigation={emptyProjectNavigationPreferences()} onOpenProject={() => undefined} onConfigureProject={() => undefined} onProjectHiddenChange={() => undefined} onForgetProject={async () => undefined} />)

    expect(markup).toContain('No projects yet')
    expect(markup).toContain('Start one repository or assemble several.')
    expect(markup).not.toContain('repository—or')
    expect(markup).not.toContain('empty-environment__graphic')
  })

  it('marks projects hidden from recents without removing them from the registry', () => {
    const markup = renderProjectIndex([environment], { ...emptyProjectNavigationPreferences(), hiddenProjects: ['store'] })

    expect(markup).toContain('<small>HIDDEN</small>')
    expect(markup).toContain('<span>hidden</span><strong>1</strong>')
    expect(markup).toContain('<strong>store</strong>')
  })

  it('previews retained state and blocks forgetting a running project', () => {
    const markup = renderToStaticMarkup(<ForgetProjectDialog project={project} environments={[environment]} busy={false} error={null} onDismissError={() => undefined} onClose={() => undefined} onForget={async () => undefined} />)

    expect(markup).toContain('role="alertdialog"')
    expect(markup).toContain('<h2 id="forget-project-title">Forget store?</h2>')
    expect(markup).toContain('Source files and checkouts on disk are not deleted.')
    expect(markup).toContain('local · healthy')
    expect(markup).toContain('Timelines, traffic, mocks, recordings, faults, and provider bindings')
    expect(markup).toContain('Stop every environment first: local.')
    expect(markup).toMatch(/<button class="button button--danger" type="button" disabled="">FORGET PROJECT<\/button>/)
  })
})

describe('project overview page', () => {
  it('exposes stopped environment actions through a row menu', () => {
    const markup = renderProjectOverview([stoppedEnvironment])

    expect(markup).toContain('class="environment-row-shell environment-row-shell--interactive"')
    expect(markup).toContain('aria-label="Environment actions for store/demo"')
    expect(markup).toContain('aria-haspopup="menu"')
    expect(markup).not.toContain('aria-label="Start demo"')
  })

  it('keeps the project detail heading and environment listing', () => {
    const markup = renderProjectOverview()

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
    expect(markup).toContain('aria-label="Environment actions for store/local"')
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
    expect(markup).toContain('aria-label="Source actions for store"')
    expect(markup.match(/class="project-table-row-actions__trigger"/g)).toHaveLength(2)
    expect(markup).not.toContain('one logical application, many repositories')
    expect(markup).not.toContain('Each environment clones this topology')
    expect(markup.indexOf('<span>ENVIRONMENTS</span>')).toBeLessThan(markup.indexOf('<span>SOURCES</span>'))
  })

  it('deduplicates checkout paths without repeating environment names in source rows', () => {
    const markup = renderProjectOverview([environment, qaEnvironment])

    expect(markup).toContain('<span>SOURCES</span><button')
    expect(markup).not.toContain('<small>local, qa-local</small>')
    expect(markup.match(/<code class="truncate" title="\/Users\/dev\/workspace\/store">/g)).toHaveLength(1)
    expect(markup).toContain('aria-label="Source actions for store"')
    expect(markup).not.toContain('>DELETE</button>')
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

    const markup = renderProjectOverview(environments, paginatedProject)

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

    const markup = renderProjectOverview([local, needsConfiguration, remote], inventoryProject)

    expect(markup).toContain('configuration required')
    expect(markup).toContain('not bound locally')
    expect(markup).not.toContain('<small>qa-local</small>')
    expect(markup).not.toContain('<small>remote</small>')
    expect(markup).toContain('component has no provider binding')
  })

  it('keeps environment creation in its own project-scoped dialog', () => {
    const markup = renderToStaticMarkup(<CreateEnvironmentDialog project={project} environments={[qaEnvironment, environment]} onClose={() => undefined} onNavigate={() => undefined} onChanged={async () => undefined} />)
    const contextualMarkup = renderToStaticMarkup(<CreateEnvironmentDialog project={project} environments={[qaEnvironment, environment]} initialCloneFrom="qa-local" onClose={() => undefined} onNavigate={() => undefined} onChanged={async () => undefined} />)

    expect(markup).toContain('role="dialog"')
    expect(markup).toContain('<h2 id="create-environment-title">Create Environment</h2>')
    expect(markup).toContain('name="portless-environment-name"')
    expect(markup).toContain('<option>qa-local</option><option selected="">local</option>')
    expect(contextualMarkup).toContain('<option selected="">qa-local</option><option>local</option>')
    expect(markup).toContain('>CREATE ENVIRONMENT</button>')
  })
})

function renderProjectIndex(renderedEnvironments: Environment[] = [environment], navigation: ProjectNavigationPreferences = emptyProjectNavigationPreferences(), projects: Project[] = [project]) {
  return renderToStaticMarkup(<ProjectsIndexPage projects={projects} environments={renderedEnvironments} navigation={navigation} onOpenProject={() => undefined} onConfigureProject={() => undefined} onProjectHiddenChange={() => undefined} onForgetProject={async () => undefined} />)
}

function registryRow(name: string, status: Environment['status'], environmentCount: number, openedAt?: string) {
  return { project: { name } as Project, status, environmentNames: '', sourceCount: 0, serviceCount: 0, running: status !== 'stopped', openedAt, environmentCount, focused: false }
}

function renderProjectOverview(renderedEnvironments: Environment[] = [environment], renderedProject: Project = project) {
  return renderToStaticMarkup(<ProjectOverviewPage project={renderedProject} environments={renderedEnvironments} onNavigate={() => undefined} onChanged={async () => undefined} />)
}
