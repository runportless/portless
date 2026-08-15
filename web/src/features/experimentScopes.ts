import type { Environment, Protocol, Recording } from '../types'

export interface ExperimentScope {
  id: string
  source: string
  target: string
  protocol: Protocol
  label: string
}

export function experimentScopes(environment: Environment): ExperimentScope[] {
  const scopes: ExperimentScope[] = []
  const seen = new Set<string>()
  const add = (source: string, target: string, protocol: ExperimentScope['protocol'], binding?: string) => {
    const id = experimentScopeID(source, target)
    if (!source || !target || seen.has(id)) return
    seen.add(id)
    scopes.push({ id, source, target, protocol, label: `${source} → ${target} · ${protocolLabel(protocol, binding)}` })
  }

  const primary = environment.services.find((service) => service.name === environment.primaryService)
  if (primary?.kind === 'process') add('external', primary.name, 'http')
  for (const connection of environment.connections || []) add(connection.source, connection.target, connection.protocol, connection.binding)
  return scopes
}

export function experimentScopeID(source: string, target: string) { return `${source}:${target}` }

export function preferredFaultScope(environment: Environment, scopes = experimentScopes(environment)) {
  return scopes.find((scope) => scope.source === environment.primaryService) || scopes[0]
}

export function recordingScopeLabel(recording: Pick<Recording, 'source' | 'target'>) {
  if (!recording.source && !recording.target) return 'all traffic'
  if (!recording.source) return `any source → ${recording.target}`
  if (!recording.target) return `${recording.source} → any target`
  return `${recording.source} → ${recording.target}`
}

function protocolLabel(protocol: ExperimentScope['protocol'], binding?: string) {
	if (binding) return binding.replaceAll('-', ' ').toUpperCase()
  return protocol.toUpperCase()
}
