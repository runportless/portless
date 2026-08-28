import type { ComponentBinding, Environment, Protocol, Service } from '../../../types'

export type ServiceEndpoint = { label: string; value: string; detail: string; href?: string }

export function bindingFor(environment: Pick<Environment, 'bindings'>, service: string) {
  return environment.bindings?.find((binding) => binding.service === service)
}

export function publicEndpoint(service: Service, protocol?: Protocol) {
  return (service.endpoints || []).find((endpoint) => endpoint.kind === 'public' && (!protocol || endpoint.protocol === protocol))
}

export function overviewServiceEndpoint(environment: Pick<Environment, 'bindings'>, service: Service) {
  const binding = bindingFor(environment, service.name)
  return publicEndpoint(service)?.url || binding?.remote?.url || ''
}

export function displayLaunchMode(environment: Pick<Environment, 'bindings'>, service: Service) {
  const provider = bindingFor(environment, service.name)?.provider
  if (provider === 'mock') return 'mock'
  if (service.kind === 'resource' && provider === 'container') return 'container'
  if (service.kind !== 'process' || provider !== 'local') return '—'
  return service.launchMode || 'managed'
}

export function serviceEndpoints(service: Service, binding?: ComponentBinding): ServiceEndpoint[] {
  const endpoints: ServiceEndpoint[] = []
  const seen = new Set<string>()
  const add = (endpoint: ServiceEndpoint) => {
    if (!endpoint.value || seen.has(endpoint.value)) return
    seen.add(endpoint.value)
    endpoints.push(endpoint)
  }

  for (const endpoint of service.endpoints || []) {
    if (endpoint.kind !== 'public') continue
    add({
      label: endpoint.protocol === 'http' ? 'CLEAN URL' : 'PUBLIC ENDPOINT',
      value: endpoint.url,
      detail: endpoint.protocol === 'http' ? 'Browser and host access through Portless' : `Stable ${endpoint.protocol} endpoint through Portless`,
      ...(isWebURL(endpoint.url) ? { href: endpoint.url } : {}),
    })
  }
  if (binding?.remote?.url) {
    add({
      label: 'REMOTE PROVIDER',
      value: binding.remote.url,
      detail: `${binding.remote.classification} · ${binding.remote.writePolicy}`,
      ...(isWebURL(binding.remote.url) ? { href: binding.remote.url } : {}),
    })
  }
  return endpoints
}

function isWebURL(value: string) {
  return /^https?:\/\//.test(value)
}
