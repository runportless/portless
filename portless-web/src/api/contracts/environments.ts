import type { ComponentBinding, Connection, Service } from './topology'

export type EnvironmentStatus = 'starting' | 'recovering' | 'healthy' | 'degraded' | 'failed' | 'stopping' | 'stopped' | 'unknown'

export interface SourceBinding {
  name: string
  path: string
  status: string
  warnings?: string[]
  createdAt: string
  scannedAt: string
}

export interface ConfigurationIssue {
  code: string
  subject?: string
  message: string
  remediation?: string
}

export interface Environment {
  project: string
  name: string
  clonedFrom?: string
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

export interface EnvironmentList {
  environments: Environment[]
  total?: number
}

export interface EnvironmentMutation {
  environment: Environment
  warnings: string[]
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
  environment: string
  number: number
  type: string
  state: string
  actor: string
  startedAt: string
  completedAt?: string
  error?: string
  events: OperationEvent[]
}

export interface LogEntry {
  timestamp: string
  service: string
  stream: string
  generation: number
  message: string
}

export interface LogList {
  project?: string
  environment?: string
  service?: string
  entries: LogEntry[]
}

export interface TimelineEvent {
  project: string
  environment: string
  sequence: number
  timestamp: string
  actor: string
  type: string
  subject?: string
  severity: string
  summary: string
  details?: Record<string, unknown>
}

export interface TimelineList {
  timeline: TimelineEvent[]
}
