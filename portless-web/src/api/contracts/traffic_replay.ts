import type { TrafficExchange } from './traffic'

export interface TrafficReplayIdentity {
  createdAt: string
  daemonStartedAt: string
}

export interface PrepareTrafficReplayRequest {
  sequence: number
  startedAt: string
}

export interface TrafficReplayDraft {
  environment: string
  method: string
  requestTarget: string
  headers: Record<string, string[]>
  bodyMode: 'captured' | 'replacement' | 'empty'
  body: string
  omittedHeaders?: string[]
}

export interface UpdateTrafficReplayDraftRequest extends TrafficReplayIdentity {
  revision: number
  draft: TrafficReplayDraft
}

export interface RunTrafficReplayRequest extends TrafficReplayIdentity {
  revision: number
  runNumber: number
  confirmRemoteWrite: boolean
}

export interface TrafficReplayDestination {
  environment: string
  provider: string
  mockScenario?: string
  mockRoute?: string
  classification?: string
  writePolicy?: string
  url: string
  requiresConfirmation: boolean
}

export interface TrafficReplayLimitation {
  field: string
  code: string
  message: string
}

export interface TrafficReplayRun {
  number: number
  revision: number
  state: 'running' | 'completed' | 'failed' | 'interrupted'
  outcome: 'not-sent' | 'response-received' | 'unknown'
  startedAt: string
  completedAt?: string
  deadline: string
  error?: string
}

export interface TrafficReplayChange {
  path: string
  kind: string
  before?: string
  after?: string
}

export interface TrafficComparisonSection {
  state: 'equal' | 'different' | 'partial' | 'unavailable'
  format?: string
  reason?: string
  changes?: TrafficReplayChange[]
}

export interface TrafficResponseComparison {
  state: string
  originalStatus: number
  replayStatus: number
  statusChanged: boolean
  durationDeltaMs: number
  headers: TrafficComparisonSection
  body: TrafficComparisonSection
}

export interface TrafficReplayResult {
  runNumber: number
  request: TrafficReplayDraft
  destination: TrafficReplayDestination
  exchange?: TrafficExchange
  comparison: TrafficResponseComparison
  limitations?: TrafficReplayLimitation[]
}

export interface TrafficReplayWorkspace extends TrafficReplayIdentity {
  project: string
  environment: string
  number: number
  preparedUntil?: string
  revision: number
  nextRunNumber: number
  baseline?: TrafficExchange
  draft?: TrafficReplayDraft
  destination?: TrafficReplayDestination
  limitations?: TrafficReplayLimitation[]
  run?: TrafficReplayRun
  receipts?: TrafficReplayRun[]
  result?: TrafficReplayResult
}
