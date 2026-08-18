export type MCPScope =
  | { kind: 'environment'; environment: string }
  | { kind: 'workspace'; directory: string }
  | { kind: 'all' }

export interface MCPCapabilities {
  lifecycle: boolean
  trafficControl: boolean
  sensitiveTraffic: boolean
}

export interface MCPConfiguration {
  serverName: string
  executable: string
  scope: MCPScope
  capabilities: MCPCapabilities
}

export interface MCPClientServerConfiguration {
  command: string
  args: string[]
  cwd?: string
}

export interface MCPClientConfiguration {
  mcpServers: Record<string, MCPClientServerConfiguration>
}

export const defaultMCPCapabilities: MCPCapabilities = {
  lifecycle: false,
  trafficControl: false,
  sensitiveTraffic: false,
}

export function buildMCPArguments(configuration: Pick<MCPConfiguration, 'scope' | 'capabilities'>): string[] {
  const args: string[] = []
  if (configuration.scope.kind === 'environment') args.push('--env', configuration.scope.environment.trim())
  args.push('mcp', 'serve')
  if (configuration.scope.kind === 'all') args.push('--all-environments')
  if (configuration.capabilities.lifecycle) args.push('--allow-lifecycle')
  if (configuration.capabilities.trafficControl) args.push('--allow-traffic-control')
  if (configuration.capabilities.sensitiveTraffic) args.push('--allow-sensitive-traffic')
  return args
}

export function buildMCPClientConfiguration(configuration: MCPConfiguration): MCPClientConfiguration {
  const server: MCPClientServerConfiguration = {
    command: configuration.executable.trim(),
    args: buildMCPArguments(configuration),
  }
  if (configuration.scope.kind === 'workspace') server.cwd = configuration.scope.directory.trim()
  return { mcpServers: { [configuration.serverName.trim()]: server } }
}

export function serializeMCPClientConfiguration(configuration: MCPConfiguration): string {
  return JSON.stringify(buildMCPClientConfiguration(configuration), null, 2)
}

export function buildMCPCommand(configuration: MCPConfiguration): string {
  const command = [configuration.executable.trim(), ...buildMCPArguments(configuration)].map(shellQuote).join(' ')
  if (configuration.scope.kind !== 'workspace') return command
  return `cd ${shellQuote(configuration.scope.directory.trim())} && ${command}`
}

export function mcpToolCount(capabilities: MCPCapabilities): number {
  return 15 + (capabilities.lifecycle ? 3 : 0) + (capabilities.trafficControl ? 5 : 0) + (capabilities.sensitiveTraffic ? 1 : 0)
}

export function mcpAccessLabel(capabilities: MCPCapabilities): 'READ ONLY' | 'OPERATOR' | 'SENSITIVE' {
  if (capabilities.sensitiveTraffic) return 'SENSITIVE'
  if (capabilities.lifecycle || capabilities.trafficControl) return 'OPERATOR'
  return 'READ ONLY'
}

export function suggestedMCPServerName(scope: MCPScope): string {
  const suffix = scope.kind === 'environment' ? scope.environment : scope.kind === 'all' ? 'all' : 'workspace'
  const normalized = suffix.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  return normalized ? `portless-${normalized}` : 'portless'
}

export function validMCPConfiguration(configuration: MCPConfiguration): boolean {
  if (!configuration.serverName.trim() || !configuration.executable.trim()) return false
  if (configuration.scope.kind === 'environment') return configuration.scope.environment.trim() !== ''
  if (configuration.scope.kind === 'workspace') return configuration.scope.directory.trim() !== ''
  return true
}

function shellQuote(value: string): string {
  if (/^[A-Za-z0-9_./:@+,-]+$/.test(value)) return value
  return `'${value.replaceAll("'", `'"'"'`)}'`
}
