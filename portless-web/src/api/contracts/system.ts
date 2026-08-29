export interface Session {
  actor: string
  browser: boolean
  csrf: string
}

export interface DirectorySelection {
  path: string
}

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
  recoveryProblems: string[]
  activeEnvironments: string[]
}

export interface DaemonManagedInventory {
  processes: number
  containers: number
  proxyListeners: number
  activeEnvironments: number
  problems: string[]
}

export interface DaemonRecoveryStatus {
  result: 'not-run' | 'healthy' | 'degraded' | 'failed'
  completedAt?: string
  durationMs: number
  recovered: number
  problems: string[]
}

export interface DaemonBuildProvenance {
  version: string
  distribution: string
  commit: string
  runningBuildId: string
  onDiskBuildId?: string
  current: boolean
  problem?: string
}

export interface DaemonStorageStatus {
  databaseBytes: number
  recordingCount: number
  recordedEventCount: number
  recordedBytes: number
  liveTrafficExchanges: number
  liveTrafficBytes: number
  serviceLogBytes: number
  daemonLogBytes: number
  trafficExchangeLimitPerEnvironment: number
  trafficPayloadLimitPerEnvironment: number
  recordingDefaultEventLimit: number
  recordingMaximumEventLimit: number
  recordingDefaultPayloadLimit: number
  recordingMaximumPayloadLimit: number
  serviceLogGenerationLimit: number
  serviceLogStreamLimitBytes: number
  trafficPrunedAt?: string
  serviceLogsPrunedAt?: string
  problems: string[]
}

export interface DaemonDiagnostics {
  collectedAt: string
  inventory: DaemonManagedInventory
  recovery: DaemonRecoveryStatus
  build: DaemonBuildProvenance
  lastRestart?: DaemonRestartStatus
  storage?: DaemonStorageStatus
}

export interface ControlPlaneHealth {
  api: {
    state: 'ready' | 'unreachable'
    latencyMs?: number
    checkedAt?: string
  }
  events: {
    state: 'connected' | 'reconnecting' | 'idle'
    connections: number
    connected: number
    lastConnectedAt?: string
  }
}

export interface DaemonLogSnapshot {
  content: string
  truncated: boolean
}

export interface DaemonHandoffStatus {
  state: 'ready' | 'blocked'
  verifiedAt: string
  problems: string[]
  activeEnvironments: string[]
}

export interface DaemonRestart {
  restarting: boolean
  restartId: string
  reason: string
  previousInstanceId: string
  targetBuildId: string
  acceptedAt: string
  deadlineAt: string
  handoff: boolean
  activeEnvironments: string[]
}

export interface DaemonRestartStatus {
  restartId: string
  reason: string
  previousInstanceId: string
  instanceId: string
  targetBuildId: string
  acceptedAt: string
  deadlineAt: string
  readyAt: string
  durationMs: number
  withinSla: boolean
}

export interface RelayStatus {
  platform: string
  service: string
  installed: boolean
  running: boolean
  healthy: boolean
  httpHealthy: boolean
  helperVerified: boolean
  helperCompatible: boolean
  helperBuildId?: string
  helperVersion?: string
  requiredHelperVersion: string
  helperError?: string
  configurationError?: string
  dnsHealthy: boolean
  resolverPresent: boolean
  resolverHealthy: boolean
  endpointPoolReady: boolean
  endpointPoolManaged: boolean
  endpointPoolResidual?: boolean
  endpointPoolDetail?: string
  endpointPoolError?: string
  targetSocket?: string
  dnsTargetSocket?: string
  dnsListenAddress: string
  resolverPath?: string
  localhostResolverPath?: string
  healthError?: string
  dnsHealthError?: string
  resolverHealthError?: string
  problem?: string
}
