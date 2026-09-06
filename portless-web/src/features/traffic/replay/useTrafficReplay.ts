import { useEffect, useMemo, useSyncExternalStore } from 'react'
import { api, APIError, environmentPath, jsonBody } from '../../../api'
import type { Environment } from '../../../api/contracts/environments'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficReplayIdentity, TrafficReplayWorkspace } from '../../../api/contracts/traffic_replay'
import { actionError, type ActionErrorDetails } from '../../../components/ActionError'
import { clearReplayCredentials, replayDraftFingerprint, replayRequestDraft, serializeReplayDraft, type ReplayRequestDraft } from './trafficReplayDraft'

export interface TrafficReplayState {
  workspace: TrafficReplayWorkspace | null
  draft: ReplayRequestDraft | null
  visible: boolean
  busy: boolean
  confirming: boolean
  awaitingReceipt: boolean
  replacement: TrafficExchange | null
  error: ActionErrorDetails | null
  resultFingerprint: string
  expired: boolean
}

const initialState = (): TrafficReplayState => ({ workspace: null, draft: null, visible: false, busy: false, confirming: false, awaitingReceipt: false, replacement: null, error: null, resultFingerprint: '', expired: false })

export function replayIdentity(workspace: TrafficReplayWorkspace): TrafficReplayIdentity {
  return { createdAt: workspace.createdAt, daemonStartedAt: workspace.daemonStartedAt }
}

export function replayWorkspaceQuery(workspace: TrafficReplayWorkspace, result = false) {
  const query = new URLSearchParams({ expectedCreatedAt: workspace.createdAt, expectedDaemonStartedAt: workspace.daemonStartedAt })
  if (result) query.set('include', 'result')
  return `?${query}`
}

type ReplayAPI = <T>(path: string, options?: RequestInit) => Promise<T>

const idleTimeout = 60 * 60 * 1000
const activityInterval = 30_000

export function createTrafficReplaySession(environment: Pick<Environment, 'project' | 'name'>, request: ReplayAPI = api) {
  let state = initialState()
  const listeners = new Set<() => void>()
  let epoch = 0
  let active = true
  let timer: ReturnType<typeof setTimeout> | undefined
  let idleTimer: ReturnType<typeof setTimeout> | undefined
  let activityTimer: ReturnType<typeof setTimeout> | undefined
  let lastActivity = 0
  let lastActivitySent = 0
  let syncingActivity = false
  let submittedFingerprint = ''
  let originalDraft: ReplayRequestDraft | null = null
  let expectedRun = 0
  let pollDelay = 500
  const root = environmentPath(environment, '/traffic/replays')
  const set = (update: Partial<TrafficReplayState>) => {
    state = { ...state, ...update }
    for (const listener of listeners) listener()
  }
  const call = async <T,>(path: string, options?: RequestInit): Promise<T> => {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 12_000)
    try { return await request<T>(path, { ...options, signal: controller.signal }) }
    finally { clearTimeout(timeout) }
  }
  const valid = (attempt: number) => active && epoch === attempt
  const pending = () => state.busy || state.awaitingReceipt || state.workspace?.run?.state === 'running'
  const clearDraftCredentials = () => {
    const draft = state.draft
    if (draft && originalDraft) originalDraft = clearReplayCredentials(originalDraft, draft)
    return draft && clearReplayCredentials(draft)
  }
  const merge = (workspace: TrafficReplayWorkspace) => {
    const previous = state.workspace
    if (previous && (previous.createdAt !== workspace.createdAt || previous.daemonStartedAt !== workspace.daemonStartedAt || previous.number !== workspace.number)) throw new APIError(409, { code: 'REPLAY_WORKSPACE_CHANGED', message: 'The replay workspace changed. A previous request may have reached the application. Open a new replay to continue.' })
    return { ...previous, ...workspace, baseline: workspace.baseline || previous?.baseline, draft: workspace.draft || previous?.draft, result: workspace.result || previous?.result }
  }
  const releaseWorkspace = (workspace: TrafficReplayWorkspace) => call(`${root}/${workspace.number}`, { method: 'DELETE', keepalive: true, ...jsonBody(replayIdentity(workspace)) })
  const cleanup = (release = true) => {
    ++epoch
    clearTimeout(timer)
    clearTimeout(idleTimer)
    clearTimeout(activityTimer)
    activityTimer = undefined
    syncingActivity = false
    // Let bounded requests finish so a late creation response can be released.
    const workspace = state.workspace
    if (release && workspace) void releaseWorkspace(workspace).catch(() => undefined)
    originalDraft = null
    submittedFingerprint = ''
    expectedRun = 0
    pollDelay = 500
  }
  const close = () => { cleanup(); set(initialState()) }
  const scheduleIdleCleanup = () => {
    clearTimeout(idleTimer)
    idleTimer = setTimeout(close, Math.max(0, lastActivity + idleTimeout - Date.now()))
  }
  const scheduleActivity = () => {
    if (activityTimer !== undefined || syncingActivity) return
    activityTimer = setTimeout(() => { activityTimer = undefined; void syncActivity() }, Math.max(0, lastActivitySent + activityInterval - Date.now()))
  }
  const syncActivity = async () => {
    const workspace = state.workspace
    if (!workspace || !active || !state.visible || state.expired) return
    const attempt = epoch
    const observed = lastActivity
    lastActivitySent = Date.now()
    syncingActivity = true
    let retry = false
    try {
      await call(`${root}/${workspace.number}/activity`, { method: 'POST', ...jsonBody(replayIdentity(workspace)) })
    } catch (reason) {
      if (!valid(attempt)) return
      if (reason instanceof APIError && [404, 409, 410].includes(reason.status)) {
        set({ expired: true, confirming: false, draft: clearDraftCredentials(), error: actionError('Replay is no longer available', new Error('Reopen Replay to start a new session. An admitted request may still have reached the application.')) })
      } else retry = true
    } finally {
      if (valid(attempt)) syncingActivity = false
    }
    if (valid(attempt) && !state.expired && (retry || lastActivity > observed)) scheduleActivity()
  }
  const activity = () => {
    if (!active || !state.visible || !state.workspace || state.expired) return false
    if (Date.now() - lastActivity >= idleTimeout) { close(); return false }
    lastActivity = Date.now()
    scheduleIdleCleanup()
    scheduleActivity()
    return true
  }
  const schedulePoll = () => {
    clearTimeout(timer)
    timer = setTimeout(() => { void poll() }, pollDelay)
  }
  const poll = async () => {
    const current = state.workspace
    if (!current || !active) return
    const attempt = epoch
    try {
      const metadata = await call<TrafficReplayWorkspace>(`${root}/${current.number}${replayWorkspaceQuery(current)}`)
      if (!valid(attempt)) return
      const workspace = merge(metadata)
      // A failed POST may not have been admitted. A GET never resubmits it.
      if (expectedRun && metadata.run?.number !== expectedRun && metadata.nextRunNumber === expectedRun) {
        set({ workspace, awaitingReceipt: false, busy: false, error: actionError('Replay was not admitted', new Error('The daemon has no receipt for this attempt. Review the request before choosing Send replay again.')) })
        return
      }
      set({ workspace, awaitingReceipt: false, error: null })
      pollDelay = 500
      if (workspace.run?.state === 'running') { schedulePoll(); return }
      const full = await call<TrafficReplayWorkspace>(`${root}/${current.number}${replayWorkspaceQuery(current, true)}`)
      if (!valid(attempt)) return
      if (!full.baseline) {
        set({ workspace: full, draft: null, busy: false, expired: true, error: actionError('Replay contents were cleared', new Error('The retained baseline and result are no longer available. An admitted request may still have reached the application.')) })
        return
      }
      set({ workspace: merge(full), busy: false, resultFingerprint: submittedFingerprint, error: null })
    } catch (reason) {
      if (!valid(attempt)) return
      const gone = reason instanceof APIError && [404, 409, 410].includes(reason.status)
      set({ busy: false, ...(gone ? { expired: true, awaitingReceipt: false, workspace: state.workspace && { ...state.workspace, run: state.workspace.run ? { ...state.workspace.run, state: 'interrupted', outcome: 'unknown' } : undefined } } : { awaitingReceipt: true }), error: actionError(gone ? 'Replay is no longer available' : 'Waiting for the replay receipt', gone ? new Error('The replay session was closed, became inactive, or the daemon changed. If sending had begun, the request may have reached the application. Nothing will be sent automatically.') : reason) })
      if (!gone) { pollDelay = Math.min(5000, pollDelay * 2); schedulePoll() }
    }
  }
  const openNew = async (exchange: TrafficExchange) => {
    const attempt = ++epoch
    originalDraft = null
    set({ ...initialState(), visible: true, busy: true })
    try {
      const workspace = await call<TrafficReplayWorkspace>(root, { method: 'POST', ...jsonBody({ sequence: exchange.sequence, startedAt: exchange.startedAt }) })
      if (!valid(attempt)) { void releaseWorkspace(workspace).catch(() => undefined); return }
      if (!workspace.baseline || !workspace.draft) throw new Error('The daemon did not return the captured request.')
      originalDraft = replayRequestDraft(workspace.draft)
      set({ workspace, draft: originalDraft, busy: false })
      lastActivity = lastActivitySent = Date.now()
      scheduleIdleCleanup()
    } catch (reason) {
      if (valid(attempt)) set({ busy: false, error: actionError("Replay couldn't open", reason) })
    }
  }
  const sendPrepared = async (confirmed: boolean) => {
    const workspace = state.workspace
    if (!workspace || !state.draft || pending() || !activity()) return
    const attempt = epoch
    submittedFingerprint = replayDraftFingerprint(state.draft)
    expectedRun = workspace.nextRunNumber
    set({ busy: true, confirming: false, error: null })
    try {
      const accepted = await call<TrafficReplayWorkspace>(`${root}/${workspace.number}/runs`, { method: 'POST', ...jsonBody({ ...replayIdentity(workspace), revision: workspace.revision, runNumber: expectedRun, confirmRemoteWrite: confirmed }) })
      if (!valid(attempt)) return
      set({ workspace: merge(accepted), busy: false, awaitingReceipt: true })
      schedulePoll()
    } catch (reason) {
      if (!valid(attempt)) return
      // Validation/admission responses are definitive; disconnection/timeouts are not.
      if (reason instanceof APIError && reason.status >= 400 && reason.status < 500) {
        set({ busy: false, error: actionError("Replay wasn't sent", reason) })
      } else {
        set({ busy: false, awaitingReceipt: true, error: actionError('Checking whether replay was admitted', new Error('The send response was interrupted. Portless is checking the receipt without sending again.')) })
        schedulePoll()
      }
    }
  }
  const session = {
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    snapshot: () => state,
    connect() { active = true },
    async open(exchange: TrafficExchange) {
      const baseline = state.workspace?.baseline
      if (state.workspace && (pending() || (baseline?.sequence === exchange.sequence && baseline.startedAt === exchange.startedAt && !state.expired))) { activity(); return }
      if (state.workspace) { set({ replacement: exchange, visible: true }); return }
      await openNew(exchange)
    },
    async replace() {
      const attempt = epoch
      const exchange = state.replacement
      const workspace = state.workspace
      if (!exchange || pending()) return
      set({ busy: true, confirming: false, error: null })
      try {
        if (workspace) {
          try { await releaseWorkspace(workspace) }
          catch (reason) { if (!(reason instanceof APIError && [404, 410].includes(reason.status))) throw reason }
        }
        if (!valid(attempt)) return
        cleanup(false)
        await openNew(exchange)
      } catch (reason) { if (valid(attempt)) set({ busy: false, error: actionError("The previous replay couldn't be released", reason) }) }
    },
    keep() { set({ replacement: null }) },
    activity,
    close,
    change(draft: ReplayRequestDraft) { if (!pending() && !state.confirming && activity()) set({ draft, error: null }) },
    destination(name: string) { if (state.draft && !pending() && !state.confirming && activity()) set({ draft: { ...clearDraftCredentials()!, environment: name }, error: null }) },
    reset() { if (originalDraft && !pending() && activity()) {
      clearDraftCredentials()
      set({ draft: clearReplayCredentials(originalDraft), error: null, confirming: false })
    } },
    async send() {
      const workspace = state.workspace
      if (!workspace?.baseline || !state.draft || pending() || state.confirming || !activity()) return
      const attempt = epoch
      try {
        const draft = serializeReplayDraft(state.draft, workspace.baseline)
        set({ busy: true, error: null })
        const prepared = await call<TrafficReplayWorkspace>(`${root}/${workspace.number}/draft`, { method: 'PUT', ...jsonBody({ ...replayIdentity(workspace), revision: workspace.revision, draft }) })
        if (!valid(attempt)) return
        if (originalDraft) originalDraft = clearReplayCredentials(originalDraft, replayRequestDraft(draft))
        set({ workspace: merge(prepared), busy: false })
        if (prepared.destination?.requiresConfirmation) { set({ confirming: true }); return }
        await sendPrepared(false)
      } catch (reason) { if (valid(attempt)) set({ busy: false, error: actionError("Replay wasn't prepared", reason) }) }
    },
    confirm: () => sendPrepared(true),
    cancelConfirmation() { activity(); set({ confirming: false }) },
    dismissError() { set({ error: null }) },
    clear: close,
    dispose() {
      active = false
      cleanup()
      state = initialState()
    },
  }
  return session
}

export function useTrafficReplay(environment: Pick<Environment, 'project' | 'name'>) {
  const { project, name } = environment
  const session = useMemo(() => createTrafficReplaySession({ project, name }), [project, name])
  useEffect(() => {
    session.connect()
    window.addEventListener('pagehide', session.close)
    return () => { window.removeEventListener('pagehide', session.close); session.dispose() }
  }, [session])
  const state = useSyncExternalStore(session.subscribe, session.snapshot, session.snapshot)
  return { ...session, state }
}
