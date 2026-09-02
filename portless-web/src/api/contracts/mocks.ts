export type MockScenarioActivationState = 'disabled' | 'enabled' | 'degraded'

export interface MockScenarioActivation {
  state: MockScenarioActivationState
  targetServices: string[]
  activeServices: string[]
  enabledAt?: string
}

export interface MockRoute {
  name: string
  service: string
  method: string
  path: string
  query?: Record<string, string>
  status: number
  headers?: Record<string, string>
  body?: string
  delayMs?: number
  enabled: boolean
  createdAt?: string
  modifiedAt?: string
}

export interface MockScenario {
  project: string
  environment: string
  name: string
  description?: string
  routes: MockRoute[]
  activation: MockScenarioActivation
  createdAt: string
  modifiedAt: string
}

export interface MockScenarioList {
  scenarios: MockScenario[]
}

export interface MockScenarioMutation {
  scenario: MockScenario
  warnings: string[]
}

export interface MockPreview {
  service: string
  matched: boolean
  route?: string
  status: number
  headers?: Record<string, string>
  body?: string
  delayMs?: number
}
