import type { Environment } from '../../api/contracts/environments'

export const environmentViews = [
  { name: 'overview', label: 'Overview', command: 'Open overview' },
  { name: 'topology', label: 'Topology', command: 'Open topology' },
  { name: 'traffic', label: 'Traffic', command: 'Inspect live traffic' },
  { name: 'mocks', label: 'Mocks', command: 'Manage mock responses' },
  { name: 'recordings', label: 'Recordings', command: 'Start a recording' },
  { name: 'faults', label: 'Faults', command: 'Introduce a fault' },
  { name: 'bindings', label: 'Bindings', command: 'Configure providers' },
  { name: 'timeline', label: 'Timeline', command: 'Open timeline' },
] as const

export type EnvironmentView = typeof environmentViews[number]['name']

export function parseEnvironmentView(value: string | null): EnvironmentView {
  return environmentViews.find((view) => view.name === value)?.name ?? 'overview'
}

export function environmentViewLabel(value: EnvironmentView) {
  return environmentViews.find((view) => view.name === value)!.label
}

export type EnvironmentNavigationOptions = {
  edge?: string
  protocol?: 'http' | 'tcp'
  scenario?: string
  createRoute?: boolean
  route?: string
}

export function environmentUIPath(environment: Pick<Environment, 'project' | 'name'>, view: EnvironmentView = 'overview', options: EnvironmentNavigationOptions = {}) {
  const base = `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
  if (view === 'overview') return base
  const query = new URLSearchParams({ tab: view })
  if (options.edge) query.set('edge', options.edge)
  if (options.protocol) query.set('protocol', options.protocol)
  if (view === 'mocks') {
    if (options.scenario) {
      query.set('scenario', options.scenario)
      if (options.route) query.set('route', options.route)
      else if (options.createRoute) query.set('create', 'route')
    }
  }
  return `${base}?${query}`
}
