export type MockUnmatchedRequests = 'reject' | 'forward'
export interface MockVersion { createdAt: string; modifiedAt: string; parentCreatedAt: string }

export type MockScenarioActivationState = 'disabled' | 'enabled' | 'degraded'

export interface MockScenarioActivation {
  state: MockScenarioActivationState
  targetServices: string[]
  activeServices: string[]
  enabledAt?: string
}

export type MockQueryMatcher =
  | { match: 'exists' }
  | { match: 'equals' | 'regex'; value: string }

export interface MockRoute {
  name: string
  service: string
  method: string
  path: string
  query?: Record<string, MockQueryMatcher>
  status: number
  headers?: Record<string, string>
  body?: string
  delayMs?: number
  enabled: boolean
  createdAt?: string
  modifiedAt?: string
}

export interface MockScenario {
  unmatchedRequests: MockUnmatchedRequests
  version: MockVersion
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

export interface MockResponse {
  status: number
  headers?: Record<string, string>
  body?: string
  delayMs?: number
}

export type MockPreview = { service: string } & (
  | { outcome: 'mocked'; route: string; response: MockResponse }
  | { outcome: 'rejected'; response: MockResponse }
  | { outcome: 'forward'; destination: { provider: 'local' | 'remote'; url: string; classification?: string; writePolicy?: string } }
  | { outcome: 'blocked'; reason: { code: string; message: string } }
)

export interface MockRequest {
  service: string
  method: string
  path: string
  query?: Record<string, string[]>
  headers?: Record<string, string[]>
  body?: string
}

export interface PreviewMockRequest {
  request: MockRequest
  draft?: MockRoute
  originalRoute?: string
}
