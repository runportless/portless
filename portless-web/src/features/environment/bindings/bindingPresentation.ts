import type { Environment, SourceBinding } from '../../../api/contracts/environments'
import type { Project, ProjectSource } from '../../../api/contracts/projects'
import type { ComponentBinding, ProviderKind, Service } from '../../../api/contracts/topology'

export type EnvironmentCheckoutRow = {
  source: ProjectSource
  checkout?: SourceBinding
  usedBy: string[]
  required: boolean
}

export function defaultProviderBinding(project: Project | undefined, environment: Environment, service: Service): ComponentBinding | undefined {
  if (service.kind === 'resource') return { service: service.name, provider: 'container' }
  const owner = project?.sources?.find((source) => source.services?.some((name) => name.toLowerCase() === service.name.toLowerCase()))
  if (!owner || !environment.sources?.some((source) => source.name.toLowerCase() === owner.name.toLowerCase())) return undefined
  return { service: service.name, provider: 'local', source: owner.name }
}

export function providerBindingMatches(binding: ComponentBinding, expected: ComponentBinding) {
  if (binding.provider !== expected.provider) return false
  if (binding.provider === 'local') return binding.source?.toLowerCase() === expected.source?.toLowerCase()
  if (binding.provider === 'mock') return binding.mock?.profile.toLowerCase() === expected.mock?.profile.toLowerCase()
  return binding.provider === 'container'
}

export function providerDisplayName(provider: ProviderKind) {
  if (provider === 'local') return 'Checkout'
  if (provider === 'container') return 'Container'
  if (provider === 'mock') return 'Mock'
  return 'Remote'
}

export function environmentCheckoutRows(project: Project | undefined, environment: Environment): EnvironmentCheckoutRow[] {
  const declared = project?.sources?.length
    ? project.sources
    : (environment.sources || []).map((source) => ({ name: source.name, services: [] }))
  return declared.map((source) => {
    const checkout = environment.sources?.find((item) => item.name.toLowerCase() === source.name.toLowerCase())
    const usedBy = (environment.bindings || [])
      .filter((binding) => binding.provider === 'local' && binding.source?.toLowerCase() === source.name.toLowerCase())
      .map((binding) => binding.service)
      .sort()
    const owned = new Set((source.services || []).map((service) => service.toLowerCase()))
    const required = usedBy.length > 0 || (environment.issues || []).some((issue) =>
      (issue.code === 'MISSING_BINDING' || issue.code === 'MISSING_SOURCE') && !!issue.subject && owned.has(issue.subject.toLowerCase()),
    )
    return { source, checkout, usedBy, required }
  })
}

export function formatBindingTimestamp(value: string) {
  return new Date(value).toLocaleString([], { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}
