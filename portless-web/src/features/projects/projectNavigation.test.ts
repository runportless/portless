import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { emptyProjectNavigationPreferences, formatProjectLastOpened, initialProjectDestination, projectDestination, pruneProjectNavigationPreferences, recentProjectLimit, recentProjects, recordProjectVisit, removeProjectNavigationPreferences, setProjectHidden, sidebarProjectFor } from './projectNavigation'

const projects = ['alpha', 'bravo', 'charlie', 'delta', 'echo', 'foxtrot', 'golf'].map((name) => ({ name }) as Project)
const environment = (project: string, name: string, status: Environment['status']) => ({ project, name, status }) as Environment

describe('project navigation preferences', () => {
  it('records project and environment visits without making the active project daemon-global', () => {
    const openedAt = '2026-08-30T12:00:00Z'
    const visited = recordProjectVisit(emptyProjectNavigationPreferences(), 'alpha', 'local', openedAt)

    expect(visited).toEqual({
      lastActiveProject: 'alpha',
      lastEnvironmentByProject: { alpha: 'local' },
      lastOpenedByProject: { alpha: openedAt },
      hiddenProjects: [],
    })
    expect(sidebarProjectFor(projects, undefined, visited)?.name).toBe('alpha')
    expect(sidebarProjectFor(projects, projects[1], visited)?.name).toBe('bravo')
  })

  it('keeps hidden projects durable while pruning preferences for forgotten projects', () => {
    let preferences = recordProjectVisit(emptyProjectNavigationPreferences(), 'alpha', 'local')
    preferences = recordProjectVisit(preferences, 'bravo', 'demo')
    preferences = setProjectHidden(preferences, 'alpha', true)

    expect(preferences.hiddenProjects).toEqual(['alpha'])
    expect(removeProjectNavigationPreferences(preferences, 'bravo')).toEqual({
      lastEnvironmentByProject: { alpha: 'local' },
      lastOpenedByProject: { alpha: expect.any(String) },
      hiddenProjects: ['alpha'],
    })
    expect(pruneProjectNavigationPreferences(preferences, [projects[1]])).toEqual({
      lastActiveProject: 'bravo',
      lastEnvironmentByProject: { bravo: 'demo' },
      lastOpenedByProject: { bravo: expect.any(String) },
      hiddenProjects: [],
    })
  })

  it('shows the five most recently opened projects while excluding projects hidden from recent', () => {
    const now = new Date('2026-08-30T12:00:00Z').getTime()
    let preferences = emptyProjectNavigationPreferences()
    projects.forEach((project, index) => {
      preferences = recordProjectVisit(preferences, project.name, undefined, new Date(now - index * 60_000).toISOString())
    })
    preferences = setProjectHidden(preferences, 'bravo', true)

    const recent = recentProjects(projects, preferences)

    expect(recent).toHaveLength(recentProjectLimit)
    expect(recent.map((project) => project.name)).toEqual(['alpha', 'charlie', 'delta', 'echo', 'foxtrot'])
  })

  it('moves a reopened project to the front and drops the least recent of the five', () => {
    let preferences = emptyProjectNavigationPreferences()
    projects.forEach((project, index) => {
      preferences = recordProjectVisit(preferences, project.name, undefined, `2026-08-${30 - index}T12:00:00Z`)
    })
    expect(recentProjects(projects, preferences).map((project) => project.name)).toEqual(['alpha', 'bravo', 'charlie', 'delta', 'echo'])

    preferences = recordProjectVisit(preferences, 'golf', 'local', '2026-09-05T12:00:00Z')

    expect(recentProjects(projects, preferences).map((project) => project.name)).toEqual(['golf', 'alpha', 'bravo', 'charlie', 'delta'])
  })

  it('retains older visits and omits unvisited or invalid entries without filling the list with unrelated projects', () => {
    const preferences = { ...emptyProjectNavigationPreferences(), lastOpenedByProject: { alpha: '2025-01-01T12:00:00Z', bravo: 'invalid', charlie: '2025-01-01T12:00:00Z', forgotten: '2026-09-05T12:00:00Z' } }

    expect(recentProjects([...projects].reverse(), preferences).map((project) => project.name)).toEqual(['alpha', 'charlie'])
    expect(recentProjects(projects, emptyProjectNavigationPreferences())).toEqual([])
  })

  it('selects the remembered environment, then a running environment, then the first environment', () => {
    const project = projects[0]
    const environments = [environment('alpha', 'zeta', 'stopped'), environment('alpha', 'local', 'healthy'), environment('alpha', 'demo', 'stopped')]

    expect(projectDestination(project, environments, { ...emptyProjectNavigationPreferences(), lastEnvironmentByProject: { alpha: 'zeta' } })).toBe('/environments/alpha/zeta')
    expect(projectDestination(project, environments, emptyProjectNavigationPreferences())).toBe('/environments/alpha/local')
    expect(projectDestination(project, environments.map((item) => ({ ...item, status: 'stopped' })), emptyProjectNavigationPreferences())).toBe('/environments/alpha/demo')
  })

  it('resolves the root only when a remembered, sole running, or sole known project is unambiguous', () => {
    const environments = [environment('bravo', 'local', 'healthy')]

    expect(initialProjectDestination(projects.slice(0, 2), environments, emptyProjectNavigationPreferences())).toBe('/environments/bravo/local')
    expect(initialProjectDestination(projects.slice(0, 2), [], emptyProjectNavigationPreferences())).toBe('/projects')
    expect(initialProjectDestination([projects[0]], [], emptyProjectNavigationPreferences())).toBe('/projects/alpha')
  })

  it('formats last-opened timestamps for compact registry rows', () => {
    const now = new Date('2026-08-30T12:00:00Z').getTime()

    expect(formatProjectLastOpened(undefined, now)).toBe('never')
    expect(formatProjectLastOpened('2026-08-30T11:42:00Z', now)).toBe('18m ago')
    expect(formatProjectLastOpened('2026-08-26T12:00:00Z', now)).toBe('4d ago')
  })
})
