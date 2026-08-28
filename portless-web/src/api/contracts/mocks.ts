export interface MockRoute {
  name: string
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

export interface MockProfile {
  project: string
  environment: string
  name: string
  service: string
  description?: string
  routes: MockRoute[]
  createdAt: string
  modifiedAt: string
}

export interface MockProfileList {
  mocks: MockProfile[]
}

export interface MockMutation {
  mock: MockProfile
  warnings: string[]
}

export interface MockPreview {
  matched: boolean
  route?: string
  status: number
  headers?: Record<string, string>
  body?: string
  delayMs?: number
}
