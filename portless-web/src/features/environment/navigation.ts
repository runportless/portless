import type { Environment } from '../../api/contracts/environments'

export type EnvironmentTab = 'overview' | 'topology' | 'traffic' | 'mocks' | 'recordings' | 'faults' | 'bindings' | 'timeline'

export type EnvironmentNavigationOptions = {
  edge?: string
  protocol?: 'http' | 'tcp'
  profile?: string
  workspace?: 'create' | 'route'
  route?: string
}

export function environmentUIPath(environment: Pick<Environment, 'project' | 'name'>, tab: EnvironmentTab, options: EnvironmentNavigationOptions = {}) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  if (tab === 'overview') return base
  const query = new URLSearchParams({ tab })
  if (options.edge) query.set('edge', options.edge)
  if (options.protocol) query.set('protocol', options.protocol)
  if (tab === 'mocks' && options.workspace === 'create') query.set('workspace', 'create')
  else if (tab === 'mocks') {
    if (options.workspace === 'route') query.set('workspace', 'route')
    if (options.profile) query.set('profile', options.profile)
    if (options.workspace === 'route' && options.profile && options.route) query.set('route', options.route)
  }
  return `${base}?${query}`
}
