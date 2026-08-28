import type { Environment } from '../../types'

export type EnvironmentTab = 'overview' | 'topology' | 'traffic' | 'mocks' | 'recordings' | 'faults' | 'bindings' | 'timeline'

export type EnvironmentNavigationOptions = {
  edge?: string
  protocol?: 'http' | 'tcp'
  profile?: string
}

export function environmentUIPath(environment: Pick<Environment, 'project' | 'name'>, tab: EnvironmentTab, options: EnvironmentNavigationOptions = {}) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  if (tab === 'overview') return base
  const query = new URLSearchParams({ tab })
  if (options.edge) query.set('edge', options.edge)
  if (options.protocol) query.set('protocol', options.protocol)
  if (options.profile) query.set('profile', options.profile)
  return `${base}?${query}`
}
