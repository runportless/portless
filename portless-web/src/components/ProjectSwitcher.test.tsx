import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../api/contracts/environments'
import type { Project } from '../api/contracts/projects'
import { ProjectSwitcher } from './ProjectSwitcher'

const projects = ['golf', 'bravo', 'echo', 'alpha', 'foxtrot', 'delta', 'charlie'].map((name) => ({ name }) as Project)
const recentNames = ['alpha', 'bravo', 'golf', 'charlie', 'echo']
const recentProjects = recentNames.map((name) => projects.find((project) => project.name === name)!)
const environment = (project: string, status: Environment['status'], name = 'local'): Environment => ({
  project, name, status, revision: 1, createdAt: '2026-09-05T00:00:00Z', updatedAt: '2026-09-05T00:00:00Z', services: [], connections: [],
})

function renderSwitcher(environments: Environment[], current?: string, activeEnvironment?: Environment, recent = recentProjects) {
  return renderToStaticMarkup(<ProjectSwitcher projects={projects} recentProjects={recent} environments={environments} project={projects.find((project) => project.name === current)} activeEnvironment={activeEnvironment} onClose={() => undefined} onManage={() => undefined} onSwitch={() => undefined} onOpenEnvironment={() => undefined} />)
}

function optionRows(markup: string, kind: 'project' | 'environment' = 'project') {
  return [...markup.matchAll(/<button[^>]*role="option"[^>]*>.*?<\/button>/g)].map(([row]) => row).filter((row) => row.includes(`data-kind="${kind}"`))
}

function names(markup: string, kind: 'project' | 'environment' = 'project') {
  return optionRows(markup, kind).map((row) => row.match(/<strong[^>]*>(.*?)<\/strong>/)![1])
}

describe('project switcher', () => {
  it('shows recent projects in their visit order, including running projects', () => {
    const markup = renderSwitcher([environment('bravo', 'healthy')], 'bravo')

    expect(names(markup)).toEqual(recentNames)
    expect(optionRows(markup)).toHaveLength(5)
    expect(markup).toContain('>Recent projects</div>')
    expect(markup).not.toContain('>All projects</div>')
    expect(optionRows(markup)[0]).toContain('<em>UNKNOWN</em>')
    expect(projects[0].name).toBe('golf')
  })

  it.each<Environment['status']>(['healthy', 'stopped', 'failed', 'degraded', 'starting', 'stopping', 'recovering', 'unknown'])('marks the current project while preserving its %s state', (status) => {
    const markup = renderSwitcher([environment('bravo', status)], 'bravo')
    const current = optionRows(markup).find((row) => row.includes('data-current="true"'))!

    expect(current).toContain('>bravo</strong>')
    expect(current).toContain('>Current</span>')
    expect(current).toContain(`<em>${status.toUpperCase()}</em>`)
    expect(markup).not.toContain('>ACTIVE<')
    expect(optionRows(markup).filter((row) => row.includes('>Current</span>'))).toHaveLength(1)
  })

  it('renders Current separately from recent ordering and runtime status', () => {
    const environments = [environment('alpha', 'stopped'), environment('bravo', 'healthy')]
    const before = renderSwitcher(environments, 'alpha')
    const after = renderSwitcher(environments, 'bravo')

    expect(names(after)).toEqual(names(before))
    expect(optionRows(before)[0]).toContain('>Current</span>')
    expect(optionRows(after)[0]).not.toContain('>Current</span>')
    expect(optionRows(after)[0]).toContain('<em>STOPPED</em>')
    expect(optionRows(after)[1]).toContain('>Current</span>')
    expect(optionRows(after)[1]).toContain('<em>HEALTHY</em>')
  })

  it('keeps the current project in place while its runtime starts, stops, or fails', () => {
    const expected = names(renderSwitcher([], 'alpha'))
    for (const status of ['starting', 'healthy', 'stopping', 'stopped', 'failed'] as const) {
      const markup = renderSwitcher([environment('bravo', 'healthy'), environment('alpha', status)], 'alpha')

      expect(names(markup)).toEqual(expected)
      expect(optionRows(markup)[0]).toContain('>Current</span>')
      expect(optionRows(markup)[0]).toContain(`<em>${status.toUpperCase()}</em>`)
      expect(optionRows(markup)[1]).toContain('<em>HEALTHY</em>')
    }
  })

  it('puts individual running environments first, sorted by project and environment', () => {
    const environments = [environment('bravo', 'healthy', 'qa'), environment('alpha', 'stopped'), environment('alpha', 'degraded', 'dev'), environment('bravo', 'healthy')]
    const markup = renderSwitcher(environments, 'bravo')

    expect(names(markup, 'environment')).toEqual(['alpha/dev', 'bravo/local', 'bravo/qa'])
    expect(optionRows(markup, 'environment')[0]).toContain('<em>DEGRADED</em>')
    expect(markup.indexOf('>Running environments</div>')).toBeLessThan(markup.indexOf('>Recent projects</div>'))
    expect(markup.indexOf('>bravo/qa</strong>')).toBeLessThan(markup.indexOf('>Recent projects</div>'))
    expect(names(markup)).toEqual(recentNames)
    expect(environments[0].name).toBe('qa')
  })

  it.each<Environment['status']>(['healthy', 'failed', 'degraded', 'starting', 'stopping', 'recovering', 'unknown'])('keeps a %s environment accessible with its actual state', (status) => {
    const markup = renderSwitcher([environment('bravo', status)], 'bravo')

    expect(names(markup, 'environment')).toEqual(['bravo/local'])
    expect(optionRows(markup, 'environment')[0]).toContain(`<em>${status.toUpperCase()}</em>`)
  })

  it('distinguishes the current environment from another environment with the same name', () => {
    const current = environment('bravo', 'healthy')
    const markup = renderSwitcher([environment('alpha', 'healthy'), current, environment('bravo', 'healthy', 'qa')], 'bravo', current)
    const running = optionRows(markup, 'environment')

    expect(running[0]).not.toContain('>Current</span>')
    expect(running[1]).toContain('>Current</span>')
    expect(running[2]).not.toContain('>Current</span>')
    expect(optionRows(markup)[1]).toContain('>Current</span>')
    expect([...running, ...optionRows(markup)].filter((row) => row.includes('aria-selected="true"'))).toHaveLength(1)
    expect(running[0]).toContain('aria-selected="true"')
  })

  it('keeps stopped projects available when no environments are running', () => {
    const markup = renderSwitcher([environment('alpha', 'stopped')], 'alpha')

    expect(optionRows(markup, 'environment')).toHaveLength(0)
    expect(markup).toContain('No running environments.')
    expect(optionRows(markup)).toHaveLength(5)
    expect(optionRows(markup)[0]).toContain('>Current</span>')
    expect(optionRows(markup)[0]).toContain('<em>STOPPED</em>')
  })

  it('shows running environments and Manage projects before any project has been visited', () => {
    const markup = renderSwitcher([environment('delta', 'healthy')], undefined, undefined, [])

    expect(names(markup, 'environment')).toEqual(['delta/local'])
    expect(optionRows(markup)).toHaveLength(0)
    expect(markup).toContain('No recent projects.')
    expect(markup).toContain('>Manage projects ')
  })
})
