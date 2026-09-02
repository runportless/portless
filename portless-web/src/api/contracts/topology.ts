export type ServiceStatus = 'planned' | 'starting' | 'recovering' | 'ready' | 'unhealthy' | 'exited' | 'failed' | 'stopping' | 'stopped' | 'unknown'
export type ProviderKind = 'local' | 'container' | 'remote' | 'mock'
export type LaunchMode = 'managed' | 'debug'
export type DebugAdapter = 'node-inspector' | 'jdwp'
export type RemoteClassification = 'development' | 'qa' | 'staging' | 'unknown'
export type WritePolicy = 'read-only' | 'read-write'
export type Protocol = 'http' | 'tcp'
export type ApplicationProtocol = 'postgresql' | 'redis' | 'mysql' | 'nats'
export type EndpointKind = 'public' | 'connection'

export interface Evidence {
  file: string
  explanation: string
  confidence: string
}

export interface HealthCheck {
  kind: string
  path?: string
  timeout: number
  interval: number
}

export interface ServiceDefinition {
  name: string
  kind: 'process' | 'resource'
  framework?: string
  resource?: { type: string; version: string }
  command?: string[]
  workingDirectory?: string
  serviceDirectory?: string
  debug?: { adapter: DebugAdapter; launcher: 'node-direct' | 'nest-cli' | 'spring-gradle' | 'spring-maven'; command: string[] }
  portEnvironment?: string
  port?: number
  environment?: Record<string, string>
  required: boolean
  health: HealthCheck
  evidence?: Evidence[]
}

export interface Endpoint {
  kind: EndpointKind
  protocol: Protocol
  host: string
  port: number
  url: string
  address?: string
}

export interface Service extends ServiceDefinition {
  launchMode: LaunchMode
  debugger?: { adapter: DebugAdapter; host: string; port: number; state: 'starting' | 'listening' | 'stopped' | string }
  status: ServiceStatus
  reason?: string
  generation: number
  pid?: number
  upstreamPort?: number
  endpoints: Endpoint[]
  startedAt?: string
  restartCount: number
  recentRequests: number
  p95Millis?: number
}

export interface ConfigurationValue {
  key: string
  value: string
  classification: string
  source: string
}

export interface ServiceConfiguration {
  service: string
  command: string[]
  workingDirectory?: string
  portEnvironment?: string
  environment: ConfigurationValue[]
  health: HealthCheck
}

export interface Connection {
  source: string
  target: string
  protocol: Protocol
  applicationProtocol?: ApplicationProtocol
  binding?: string
  environment?: string
  required: boolean
}

export interface EffectiveConnection extends Connection {
  targetProvider: ProviderKind
  targetStatus: ServiceStatus
  endpoint?: Endpoint
  runtimeTarget?: string
  injectedEnvironment?: Record<string, string>
}

export interface RemoteTarget {
  url: string
  classification: RemoteClassification
  writePolicy: WritePolicy
  healthPath?: string
}

export interface MockTarget {
	scenario: string
}

export interface ComponentBinding {
  service: string
  provider: ProviderKind
  source?: string
  remote?: RemoteTarget
  mock?: MockTarget
  modifiedAt?: string
}
