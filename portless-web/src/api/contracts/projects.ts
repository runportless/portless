import type { Environment, EnvironmentStatus } from './environments'
import type { Connection, ServiceDefinition } from './topology'

export interface ProjectSource {
  name: string
  services?: string[]
}

export interface EnvironmentSummary {
  project: string
  name: string
  clonedFrom?: string
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

export interface ProjectList {
  projects: Project[]
  total: number
}

export interface ProjectMutation {
  project: Project
  environment: Environment
  warnings: string[]
}

export interface ProjectSourceMutation extends ProjectMutation {
  configurationRequired: string[]
}
