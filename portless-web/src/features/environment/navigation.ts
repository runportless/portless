import type { Environment } from '../../api/contracts/environments'

export type EnvironmentTab = 'overview' | 'topology' | 'traffic' | 'mocks' | 'recordings' | 'faults' | 'bindings' | 'timeline'

export type EnvironmentNavigationOptions = {
  edge?: string
  protocol?: 'http' | 'tcp'
  scenario?: string
  workspace?: 'route'
  route?: string
}

export function environmentUIPath(environment: Pick<Environment, 'project' | 'name'>, tab: EnvironmentTab, options: EnvironmentNavigationOptions = {}) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  if (tab === 'overview') return base
  const query = new URLSearchParams({ tab })
  if (options.edge) query.set('edge', options.edge)
  if (options.protocol) query.set('protocol', options.protocol)
  if (tab === 'mocks') {
	if (options.scenario) query.set('scenario', options.scenario)
	if (options.workspace === 'route' && options.scenario) {
      query.set('workspace', 'route')
      if (options.route) query.set('route', options.route)
    }
  }
  return `${base}?${query}`
}
