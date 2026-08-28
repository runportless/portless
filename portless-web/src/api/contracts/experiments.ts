export interface Recording {
  project: string
  environment: string
  name: string
  source?: string
  target?: string
  capturePayloads: boolean
  maxEvents: number
  maxPayloadBytes: number
  status: string
  startedAt: string
  completedAt?: string
  expiresAt?: string
  eventCount: number
}

export interface RecordingList {
  recordings: Recording[]
}

export interface FaultRule {
  project: string
  environment: string
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

export interface FaultList {
  faults: FaultRule[]
}
