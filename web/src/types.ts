export type EnvironmentStatus = 'starting' | 'recovering' | 'development' | 'healthy' | 'degraded' | 'failed' | 'stopping' | 'stopped' | 'unknown'
export type ServiceStatus = 'planned' | 'starting' | 'recovering' | 'ready' | 'unhealthy' | 'exited' | 'failed' | 'stopping' | 'stopped' | 'unknown'
export type ProviderKind = 'local' | 'container' | 'remote'
export type LaunchMode = 'managed' | 'debug'
export type DebugAdapter = 'node-inspector' | 'jdwp'
export type RemoteClassification = 'development' | 'qa' | 'staging' | 'unknown'
export type WritePolicy = 'read-only' | 'read-write'

export interface RuntimeCandidate {
  name: 'docker' | 'podman'
  state: 'ready' | 'missing' | 'failed'
  version?: string
  reason?: string
}

export interface RuntimeStatus {
  preference: 'auto' | 'docker' | 'podman'
  selected?: 'docker' | 'podman'
  state: 'ready' | 'missing' | 'failed'
  version?: string
  reason?: string
  candidates: RuntimeCandidate[]
}

export interface DaemonStatus {
  state: string
  pid: number
  startedAt: string
  instanceId: string
  buildId: string
  protocolVersion: string
  apiVersion: string
  handoffReady: boolean
  recoveryProblems: string[]
  activeEnvironments: string[]
}

export interface DaemonRestart {
  restarting: boolean
  previousInstanceId: string
  handoff: boolean
  activeEnvironments: string[]
}

export interface RelayStatus {
  platform: string
  service: string
  installed: boolean
  running: boolean
  healthy: boolean
  httpHealthy: boolean
  dnsHealthy: boolean
  resolverPresent: boolean
  resolverHealthy: boolean
  endpointPoolReady: boolean
  endpointPoolDetail?: string
  targetSocket?: string
  dnsTargetSocket?: string
  dnsListenAddress: string
  resolverPath?: string
  healthError?: string
  dnsHealthError?: string
  resolverHealthError?: string
  problem?: string
}

export interface Evidence { file: string; explanation: string; confidence: string }
export interface HealthCheck { kind: string; path?: string; timeout: number; interval: number }

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

export type Protocol = 'http' | 'tcp'
export type EndpointKind = 'public' | 'connection'
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

export interface Connection {
  source: string
  target: string
  protocol: Protocol
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

export interface ProjectSource { name: string; services?: string[] }

export interface EnvironmentSummary {
  project: string
  name: string
  revision: number
  status: EnvironmentStatus
  reason?: string
  serviceCount: number
  readyCount: number
  remoteCount: number
  updatedAt: string
  dashboardUrl?: string
}

export interface Project {
  name: string
  revision: number
  primaryService?: string
  createdAt: string
  updatedAt: string
  dashboardUrl?: string
  sources?: ProjectSource[]
  services?: ServiceDefinition[]
  connections?: Connection[]
  environments?: EnvironmentSummary[]
}

export interface SourceBinding {
  name: string
  path: string
  status: string
  warnings?: string[]
  scannedAt: string
}

export interface RemoteTarget {
  url: string
  classification: RemoteClassification
  writePolicy: WritePolicy
  healthPath?: string
}

export interface ComponentBinding {
  service: string
  provider: ProviderKind
  source?: string
  remote?: RemoteTarget
}

export interface ConfigurationIssue { code: string; subject?: string; message: string; remediation?: string }

export interface Environment {
  project: string
  name: string
  revision: number
  status: EnvironmentStatus
  reason?: string
  primaryService?: string
  createdAt: string
  updatedAt: string
  dashboardUrl?: string
  sources?: SourceBinding[]
  bindings?: ComponentBinding[]
  services: Service[]
  connections: Connection[]
  issues?: ConfigurationIssue[]
}

export interface OperationEvent { sequence: number; timestamp: string; type: string; subject?: string; message: string; payload?: Record<string, unknown> }
export interface Operation { project: string; environment: string; number: number; type: string; state: string; actor: string; startedAt: string; completedAt?: string; error?: string; events: OperationEvent[] }

export interface LogEntry { timestamp: string; service: string; stream: string; generation: number; message: string }

export interface TrafficEvent {
  project: string
  environment: string
  sequence: number
  protocol: string
  source: string
  target: string
  targetProvider?: ProviderKind
  remoteClassification?: RemoteClassification
  startedAt: string
  completedAt: string
  method?: string
  host?: string
  path?: string
  status?: number
  durationMs: number
  requestBytes: number
  responseBytes: number
  fault?: string
  recording?: string
  error?: string
  requestHeaders?: Record<string, string>
  responseHeaders?: Record<string, string>
  requestBody?: string
  responseBody?: string
  requestBodyTruncated?: boolean
  responseBodyTruncated?: boolean
}

export interface TrafficActivity {
  project: string
  environment: string
  protocol: string
  source: string
  target: string
  observedAt: string
  phase: 'open' | 'data' | 'heartbeat' | 'close'
  activeConnections: number
  requestBytes?: number
  responseBytes?: number
  fault?: string
}

export interface Recording { project: string; environment: string; name: string; source?: string; target?: string; captureBodies: boolean; maxEvents: number; maxBodyBytes: number; status: string; startedAt: string; completedAt?: string; expiresAt?: string; eventCount: number }
export interface FaultRule { project: string; environment: string; name: string; source: string; target: string; method?: string; path?: string; probability: number; latencyMs?: number; jitterMs?: number; statusCode?: number; abort?: boolean; enabled: boolean; createdAt: string; expiresAt?: string; matchCount: number; revision: number; scopeSummary: string }
export interface TimelineEvent { project: string; environment: string; sequence: number; timestamp: string; actor: string; type: string; subject?: string; severity: string; summary: string; details?: Record<string, unknown> }

export interface APIErrorShape {
  code: string
  message: string
  subject?: Record<string, unknown>
  details?: Record<string, unknown>
  remediation?: Array<{ label: string; command?: string; url?: string }>
}
