import type { ApplicationProtocol, ProviderKind, RemoteClassification } from './topology'

export type TrafficRequestKind = 'navigation' | 'subresource' | 'fetch' | 'service' | 'unknown'
export type TrafficTraceContextSource = 'generated' | 'portless' | 'w3c' | 'b3' | 'datadog'
export type TrafficCorrelation = 'exact' | 'inferred' | 'partial' | 'ambiguous'
export type TrafficInspection = 'decoded' | 'opaque' | 'encrypted' | 'unsupported' | 'malformed' | 'limited'
export type TrafficTCPOutcome = 'success' | 'error' | 'one-way' | 'incomplete'
export type TrafficMessageEncoding = 'utf8' | 'base64'

export interface TrafficMessageField {
  name: string
  value: string
  encoding?: TrafficMessageEncoding
}

export interface TrafficMessage {
  type: string
  offsetMs: number
  summary?: string
  wireBytes: number
  contentBytes?: number
  capturedBytes?: number
  truncated?: boolean
  content?: string
  contentType?: string
  encoding?: TrafficMessageEncoding
  fields?: TrafficMessageField[]
}

export interface TrafficTCPExchange {
  kind: 'operation' | 'session'
  applicationProtocol?: ApplicationProtocol
  operation?: string
  inspection: TrafficInspection
  inspectionReason?: string
  outcome?: TrafficTCPOutcome
  requestMessageCount?: number
  responseMessageCount?: number
  requestMessages?: TrafficMessage[]
  responseMessages?: TrafficMessage[]
  requestTruncated?: boolean
  responseTruncated?: boolean
}

export interface TrafficExchange {
  project: string
  environment: string
  sequence: number
  protocol: string
  source: string
  target: string
  background: boolean
  targetProvider?: ProviderKind
  remoteClassification?: RemoteClassification
  startedAt: string
  completedAt: string
  method?: string
  host?: string
  path?: string
  requestTarget?: string
  requestKind?: TrafficRequestKind
  status?: number
  durationMs: number
  requestBytes: number
  responseBytes: number
  requestCapturedBytes?: number
  responseCapturedBytes?: number
  fault?: string
  recording?: string
  mockProfile?: string
  mockRoute?: string
  error?: string
  traceId?: string
  spanId?: string
  parentSpanId?: string
  traceContextSource?: TrafficTraceContextSource
  requestHeaders?: Record<string, string[]>
  responseHeaders?: Record<string, string[]>
  requestBody?: string
  responseBody?: string
  requestBodyTruncated?: boolean
  responseBodyTruncated?: boolean
  tcp?: TrafficTCPExchange
}

export interface TrafficExchangeList {
  exchanges: TrafficExchange[]
}

export interface TrafficTraceSpan {
  exchange: TrafficExchange
  parentSequence?: number
  depth: number
  startOffsetMs: number
  correlation: TrafficCorrelation
  transactionGroup?: number
}

export interface TrafficTrace {
  project: string
  environment: string
  number: number
  lastSequence: number
  traceId?: string
  rootSequence?: number
  protocol: string
  startedAt: string
  completedAt: string
  durationMs: number
  method?: string
  requestTarget?: string
  source: string
  target: string
  status?: number
  error: boolean
  faulted: boolean
  background: boolean
  provisional: boolean
  spanCount: number
  correlation: TrafficCorrelation
  spans?: TrafficTraceSpan[]
}

export interface TrafficTraceList {
  traces: TrafficTrace[]
}

export interface TrafficActivity {
  project: string
  environment: string
  protocol: string
  applicationProtocol?: ApplicationProtocol
  source: string
  target: string
  observedAt: string
  phase: 'open' | 'data' | 'heartbeat' | 'close'
  activeConnections: number
  requestBytes?: number
  responseBytes?: number
  fault?: string
}

export interface TrafficClearResponse {
  cleared: number
  throughSequence: number
}
