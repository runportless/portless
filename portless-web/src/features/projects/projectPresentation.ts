import type { Environment, EnvironmentStatus } from '../../api/contracts/environments'
import type { Project, ProjectSource } from '../../api/contracts/projects'

export type ProjectOverview = {
  project: Project
  status: EnvironmentStatus
  environmentNames: string
  sourceCount: number
  serviceCount: number
  updatedAt?: string
}

export type ProjectSourceRow = ProjectSource & {
  checkouts: Array<{ path: string; status: string }>
  unbound: Array<{ name: string; configurationRequired: boolean }>
}

export function statusCounts(items: Array<{ status: string }>) {
  return items.reduce<Record<string, number>>((result, item) => {
    result[item.status] = (result[item.status] ?? 0) + 1
    return result
  }, {})
}

export function projectOverview(project: Project, environments: Environment[]): ProjectOverview {
  const owned = environments.filter((environment) => environment.project === project.name)
  const services = new Set([
    ...(project.services ?? []).map((service) => service.name),
    ...(project.sources ?? []).flatMap((source) => source.services ?? []),
    ...owned.flatMap((environment) => environment.services.map((service) => service.name)),
  ])
  return {
    project,
    status: aggregateProjectStatus(owned),
    environmentNames: owned.map((environment) => environment.name).sort().join(', '),
    sourceCount: project.sources?.length ?? 0,
    serviceCount: services.size,
    updatedAt: newestTimestamp([project.updatedAt, ...owned.map((environment) => environment.updatedAt)]),
  }
}

export function aggregateProjectStatus(environments: Environment[]): EnvironmentStatus {
  if (environments.length === 0) return 'unknown'
  const active = environments.filter((environment) => environment.status !== 'stopped')
  if (active.length === 0) return 'stopped'
  const states = new Set(active.map((environment) => environment.status))
  const priority: EnvironmentStatus[] = ['failed', 'degraded', 'recovering', 'starting', 'stopping', 'unknown']
  const urgent = priority.find((status) => states.has(status))
  if (urgent) return urgent
  if (active.every((environment) => environment.status === 'healthy')) return 'healthy'
  return 'degraded'
}

export function formatTimestamp(value: string) {
  return new Date(value).toLocaleString([], { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

export function projectSourceRows(project: Project, environments: Environment[]): ProjectSourceRow[] {
  return project.sources?.map((source) => ({
    ...source,
    checkouts: sourceCheckouts(environments, source.name),
    unbound: sourceUnboundEnvironments(environments, source.name, source.services ?? []),
  })) ?? []
}

export function projectSourceStatus(source: Pick<ProjectSourceRow, 'checkouts' | 'unbound'>) {
  if (source.checkouts.some((checkout) => ['failed', 'exited', 'unreachable'].includes(checkout.status))) return 'failed'
  if (source.unbound.some((environment) => environment.configurationRequired) || source.checkouts.some((checkout) => checkout.status !== 'ready')) return 'degraded'
  return source.checkouts.length > 0 ? 'ready' : 'unknown'
}

function newestTimestamp(values: Array<string | undefined>) {
  return values.reduce<string | undefined>((latest, value) => {
    if (!value || !Number.isFinite(new Date(value).getTime())) return latest
    if (!latest || new Date(value).getTime() > new Date(latest).getTime()) return value
    return latest
  }, undefined)
}

function sourceCheckouts(environments: Environment[], sourceName: string) {
  const grouped = new Map<string, { path: string; statuses: string[] }>()
  for (const environment of environments) {
    for (const source of environment.sources ?? []) {
      if (source.name !== sourceName) continue
      const checkout = grouped.get(source.path) ?? { path: source.path, statuses: [] }
      checkout.statuses.push(source.status)
      grouped.set(source.path, checkout)
    }
  }
  return [...grouped.values()].map((checkout) => ({
    path: checkout.path,
    status: checkout.statuses.every((status) => status === checkout.statuses[0]) ? checkout.statuses[0] : 'unknown',
  }))
}

function sourceUnboundEnvironments(environments: Environment[], sourceName: string, services: string[]) {
  const serviceNames = new Set(services.map((service) => service.toLowerCase()))
  return environments.flatMap((environment) => {
    if ((environment.sources ?? []).some((source) => source.name.toLowerCase() === sourceName.toLowerCase())) return []
    const configurationRequired = (environment.issues ?? []).some((issue) =>
      (issue.code === 'MISSING_BINDING' || issue.code === 'MISSING_SOURCE') && Boolean(issue.subject) && serviceNames.has(issue.subject!.toLowerCase()),
    )
    return [{ name: environment.name, configurationRequired }]
  })
}
