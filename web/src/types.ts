export type ProjectStatus = 'starting' | 'healthy' | 'degraded' | 'failed' | 'stopping' | 'stopped' | 'unknown'
export type ServiceStatus = 'planned' | 'starting' | 'ready' | 'unhealthy' | 'exited' | 'failed' | 'stopping' | 'stopped' | 'unknown'

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
  kind: 'process' | 'container'
  framework?: string
  template?: string
  version?: string
  command?: string[]
  workingDirectory?: string
  portEnvironment?: string
  environment?: Record<string, string>
  required: boolean
  health: HealthCheck
  evidence?: Evidence[]
}

export interface Service extends ServiceDefinition {
  status: ServiceStatus
  reason?: string
  generation: number
  pid?: number
  upstreamPort?: number
  ingressUrl?: string
  startedAt?: string
  restartCount: number
  recentRequests: number
  p95Millis?: number
}

export interface Connection {
  source: string
  target: string
  protocol: 'http' | 'tcp' | 'postgres' | 'redis'
  environment?: string
  required: boolean
}

export interface Project {
  name: string
  path: string
  revision: number
  status: ProjectStatus
  reason?: string
  primaryService?: string
  createdAt: string
  updatedAt: string
  dashboardUrl?: string
  services: Service[]
  connections: Connection[]
}

export interface OperationEvent {
  sequence: number
  timestamp: string
  type: string
  subject?: string
  message: string
  payload?: Record<string, unknown>
}

export interface Operation {
  project: string
  number: number
  type: string
  state: string
  actor: string
  startedAt: string
  completedAt?: string
  error?: string
  events: OperationEvent[]
}

export interface TrafficEvent {
  project: string
  sequence: number
  protocol: string
  source: string
  target: string
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
  headers?: Record<string, string>
}

export interface Recording {
  project: string
  name: string
  source?: string
  target?: string
  captureBodies: boolean
  maxEvents: number
  maxBodyBytes: number
  status: string
  startedAt: string
  completedAt?: string
  expiresAt?: string
  eventCount: number
}

export interface FaultRule {
  project: string
  name: string
  source: string
  target: string
  method?: string
  path?: string
  probability: number
  latencyMs?: number
  jitterMs?: number
  statusCode?: number
  abort?: boolean
  enabled: boolean
  createdAt: string
  expiresAt?: string
  matchCount: number
  revision: number
  scopeSummary: string
}

export interface TimelineEvent {
  project: string
  sequence: number
  timestamp: string
  actor: string
  type: string
  subject?: string
  severity: string
  summary: string
  details?: Record<string, unknown>
}

export interface APIErrorShape {
  code: string
  message: string
  subject?: Record<string, unknown>
  details?: Record<string, unknown>
  remediation?: Array<{ label: string; command?: string; url?: string }>
}
