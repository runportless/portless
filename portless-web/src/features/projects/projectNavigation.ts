import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { environmentRoute, projectRoute } from './projectOperations'

export const projectNavigationStorageKey = 'portless.project-navigation.v1'
export const recentProjectLimit = 5
export const recentProjectMaximumAgeMs = 30 * 24 * 60 * 60 * 1_000

export interface ProjectNavigationPreferences {
  lastActiveProject?: string
  lastEnvironmentByProject: Record<string, string>
  lastOpenedByProject: Record<string, string>
  hiddenProjects: string[]
}

export function emptyProjectNavigationPreferences(): ProjectNavigationPreferences {
  return { lastEnvironmentByProject: {}, lastOpenedByProject: {}, hiddenProjects: [] }
}

export function readProjectNavigationPreferences(): ProjectNavigationPreferences {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(projectNavigationStorageKey) || 'null') as unknown
    if (!parsed || typeof parsed !== 'object') return emptyProjectNavigationPreferences()
    const value = parsed as Partial<ProjectNavigationPreferences>
    return {
      ...(typeof value.lastActiveProject === 'string' && value.lastActiveProject ? { lastActiveProject: value.lastActiveProject } : {}),
      lastEnvironmentByProject: stringRecord(value.lastEnvironmentByProject),
      lastOpenedByProject: stringRecord(value.lastOpenedByProject),
      hiddenProjects: Array.isArray(value.hiddenProjects) ? value.hiddenProjects.filter((item): item is string => typeof item === 'string' && !!item) : [],
    }
  } catch {
    return emptyProjectNavigationPreferences()
  }
}

export function writeProjectNavigationPreferences(preferences: ProjectNavigationPreferences) {
  try { window.localStorage.setItem(projectNavigationStorageKey, JSON.stringify(preferences)) }
  catch { /* Project navigation remains available for this page when storage is unavailable. */ }
}

export function recordProjectVisit(preferences: ProjectNavigationPreferences, project: string, environment?: string, openedAt = new Date().toISOString()): ProjectNavigationPreferences {
  return {
    ...preferences,
    lastActiveProject: project,
    lastOpenedByProject: { ...preferences.lastOpenedByProject, [project]: openedAt },
    lastEnvironmentByProject: environment ? { ...preferences.lastEnvironmentByProject, [project]: environment } : preferences.lastEnvironmentByProject,
  }
}

export function setProjectHidden(preferences: ProjectNavigationPreferences, project: string, hidden: boolean): ProjectNavigationPreferences {
  const projects = new Set(preferences.hiddenProjects)
  if (hidden) projects.add(project)
  else projects.delete(project)
  return { ...preferences, hiddenProjects: [...projects] }
}

export function removeProjectNavigationPreferences(preferences: ProjectNavigationPreferences, project: string): ProjectNavigationPreferences {
  const lastEnvironmentByProject = { ...preferences.lastEnvironmentByProject }
  const lastOpenedByProject = { ...preferences.lastOpenedByProject }
  delete lastEnvironmentByProject[project]
  delete lastOpenedByProject[project]
  return {
    ...(preferences.lastActiveProject && preferences.lastActiveProject !== project ? { lastActiveProject: preferences.lastActiveProject } : {}),
    lastEnvironmentByProject,
    lastOpenedByProject,
    hiddenProjects: preferences.hiddenProjects.filter((item) => item !== project),
  }
}

export function pruneProjectNavigationPreferences(preferences: ProjectNavigationPreferences, projects: Project[]): ProjectNavigationPreferences {
  const names = new Set(projects.map((project) => project.name))
  const lastEnvironmentByProject = filterRecord(preferences.lastEnvironmentByProject, names)
  const lastOpenedByProject = filterRecord(preferences.lastOpenedByProject, names)
  return {
    ...(preferences.lastActiveProject && names.has(preferences.lastActiveProject) ? { lastActiveProject: preferences.lastActiveProject } : {}),
    lastEnvironmentByProject,
    lastOpenedByProject,
    hiddenProjects: preferences.hiddenProjects.filter((item) => names.has(item)),
  }
}

export function projectIsRunning(project: Pick<Project, 'name'>, environments: Environment[]) {
  return environments.some((environment) => environment.project === project.name && environment.status !== 'stopped')
}

export function runningProjects(projects: Project[], environments: Environment[]) {
  return projects.filter((project) => projectIsRunning(project, environments))
}

export function recentProjects(projects: Project[], environments: Environment[], preferences: ProjectNavigationPreferences, now = Date.now()) {
  const hidden = new Set(preferences.hiddenProjects)
  const cutoff = now - recentProjectMaximumAgeMs
  return projects
    .filter((project) => !projectIsRunning(project, environments) && !hidden.has(project.name))
    .map((project) => ({ project, openedAt: timestamp(preferences.lastOpenedByProject[project.name]) }))
    .filter((item) => item.openedAt >= cutoff)
    .sort((left, right) => right.openedAt - left.openedAt || left.project.name.localeCompare(right.project.name))
    .slice(0, recentProjectLimit)
    .map((item) => item.project)
}

export function projectDestination(project: Project, environments: Environment[], preferences: ProjectNavigationPreferences) {
  const owned = environments.filter((environment) => environment.project === project.name)
  const remembered = preferences.lastEnvironmentByProject[project.name]
  const rememberedEnvironment = remembered ? owned.find((environment) => environment.name === remembered) : undefined
  if (rememberedEnvironment) return environmentRoute(rememberedEnvironment)
  const firstRunning = [...owned].filter((environment) => environment.status !== 'stopped').sort(compareEnvironmentNames)[0]
  if (firstRunning) return environmentRoute(firstRunning)
  const firstEnvironment = [...owned].sort(compareEnvironmentNames)[0]
  return firstEnvironment ? environmentRoute(firstEnvironment) : projectRoute(project.name)
}

export function initialProjectDestination(projects: Project[], environments: Environment[], preferences: ProjectNavigationPreferences) {
  const remembered = preferences.lastActiveProject ? projects.find((project) => project.name === preferences.lastActiveProject) : undefined
  if (remembered) return projectDestination(remembered, environments, preferences)
  const active = runningProjects(projects, environments)
  if (active.length === 1) return projectDestination(active[0], environments, preferences)
  if (projects.length === 1) return projectDestination(projects[0], environments, preferences)
  return '/projects'
}

export function sidebarProjectFor(projects: Project[], routeProject: Project | undefined, preferences: ProjectNavigationPreferences) {
  if (routeProject) return routeProject
  const remembered = preferences.lastActiveProject ? projects.find((project) => project.name === preferences.lastActiveProject) : undefined
  if (remembered) return remembered
  return projects.length === 1 ? projects[0] : undefined
}

export function formatProjectLastOpened(value?: string, now = Date.now()) {
  const openedAt = timestamp(value)
  if (!openedAt) return 'never'
  const elapsed = Math.max(0, now - openedAt)
  const minutes = Math.floor(elapsed / 60_000)
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  return new Date(openedAt).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' })
}

function compareEnvironmentNames(left: Environment, right: Environment) {
  return left.name.localeCompare(right.name)
}

function timestamp(value?: string) {
  if (!value) return 0
  const result = new Date(value).getTime()
  return Number.isFinite(result) ? result : 0
}

function stringRecord(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
}

function filterRecord(values: Record<string, string>, names: Set<string>) {
  return Object.fromEntries(Object.entries(values).filter(([name]) => names.has(name)))
}
