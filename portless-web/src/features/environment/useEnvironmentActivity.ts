import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath } from '../../api'
import type { Environment, Operation, TimelineEvent, TimelineList } from '../../api/contracts/environments'
import type { FaultList, FaultRule, Recording, RecordingList } from '../../api/contracts/experiments'
import type { TrafficExchange } from '../../api/contracts/traffic'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'

const environmentActivityTopics = ['environment.state', 'service.state', 'recording.state', 'fault.state', 'operation.state', 'traffic.exchange']

export interface EnvironmentActivity {
  timeline: TimelineEvent[]
  recordings: Recording[]
  faults: FaultRule[]
  latestOperation?: Operation
  error: ActionErrorDetails | null
  loading: boolean
  refresh: () => Promise<void>
  dismissError: () => void
}

type ActivitySnapshot = Omit<EnvironmentActivity, 'refresh' | 'dismissError'>
type ActivitySession = { identity: string; controller: AbortController; revision: number; recordingRevision: number }
const emptyActivity: ActivitySnapshot = { timeline: [], recordings: [], faults: [], error: null, loading: true }

export function boundMockScenarios(environment: Pick<Environment, 'bindings'>) {
  return [...new Set((environment.bindings || []).flatMap((binding) =>
    binding.provider === 'mock' && binding.mock?.scenario ? [binding.mock.scenario] : [],
  ))].sort((left, right) => left.localeCompare(right))
}

export function advanceRecordingCount(recordings: Recording[], exchange: Pick<TrafficExchange, 'recording'>) {
  if (!exchange.recording) return recordings
  const recordingName = exchange.recording.toLocaleLowerCase()
  let changed = false
  const next = recordings.map((recording) => {
    if (recording.status !== 'active' || recording.name.toLocaleLowerCase() !== recordingName || recording.eventCount >= recording.maxEvents) return recording
    changed = true
    return { ...recording, eventCount: recording.eventCount + 1 }
  })
  return changed ? next : recordings
}

export function useEnvironmentActivity(environment: Pick<Environment, 'project' | 'name'> | undefined, identity: string, onChanged: () => void): EnvironmentActivity {
  const [state, setState] = useState<ActivitySnapshot & { identity: string }>({ ...emptyActivity, identity })
  const sessionRef = useRef<ActivitySession | null>(null)
  const identityRef = useRef(identity)
  identityRef.current = identity
  const onChangedRef = useRef(onChanged)
  onChangedRef.current = onChanged
  const project = environment?.project
  const name = environment?.name
  const source = useMemo(() => project && name ? { project, name } : undefined, [project, name])

  const refresh = useCallback(async () => {
    const session = sessionRef.current
    if (!source || !session || session.identity !== identity || session.controller.signal.aborted) return
    const revision = ++session.revision
    const recordingRevision = ++session.recordingRevision
    const base = environmentPath(source)
    const signal = AbortSignal.any([session.controller.signal, AbortSignal.timeout(10_000)])
    const [timelineResult, recordingResult, faultResult] = await Promise.all([
      api<TimelineList>(`${base}/timeline?limit=1000`, { signal }),
      api<RecordingList>(`${base}/recordings`, { signal }),
      api<FaultList>(`${base}/faults`, { signal }),
    ])
    if (sessionRef.current !== session || identityRef.current !== identity || revision !== session.revision || signal.aborted) return
    setState((current) => ({ ...current, identity, timeline: timelineResult.timeline, recordings: recordingRevision === session.recordingRevision ? recordingResult.recordings : current.recordings, faults: faultResult.faults, error: null, loading: false }))
  }, [identity, source])

  useEffect(() => {
    if (!source) return
    const session: ActivitySession = { identity, controller: new AbortController(), revision: 0, recordingRevision: 0 }
    sessionRef.current = session
    setState({ ...emptyActivity, identity })
    const current = () => sessionRef.current === session && identityRef.current === identity && !session.controller.signal.aborted
    const reportError = (reason: unknown) => {
      if (current()) setState((value) => ({ ...value, loading: false, error: actionError("Environment activity couldn't be loaded", reason) }))
    }
    const reload = () => { void refresh().catch(reportError) }
    const refreshRecordings = async () => {
      const revision = ++session.recordingRevision
      const signal = AbortSignal.any([session.controller.signal, AbortSignal.timeout(10_000)])
      const result = await api<RecordingList>(`${environmentPath(source)}/recordings`, { signal })
      if (current() && revision === session.recordingRevision) setState((value) => ({ ...value, recordings: result.recordings }))
    }
    let recordingRefreshTimer: number | undefined
    let connected = false
    reload()
    const disconnect = connectEvents(source, environmentActivityTopics, (type, value) => {
      if (!current()) return
      if (type === 'traffic.exchange') {
        const exchange = value as TrafficExchange
        if (!exchange.recording) return
        setState((snapshot) => ({ ...snapshot, recordings: advanceRecordingCount(snapshot.recordings, exchange) }))
        if (recordingRefreshTimer === undefined) {
          recordingRefreshTimer = window.setTimeout(() => {
            recordingRefreshTimer = undefined
            void refreshRecordings().catch(reportError)
          }, 500)
        }
        return
      }
      if (type === 'operation.state') setState((snapshot) => ({ ...snapshot, latestOperation: value as Operation }))
      onChangedRef.current()
      reload()
    }, () => {
      if (!current()) return
      if (connected) { onChangedRef.current(); reload() }
      connected = true
    })
    return () => {
      session.controller.abort()
      if (sessionRef.current === session) sessionRef.current = null
      disconnect()
      if (recordingRefreshTimer !== undefined) window.clearTimeout(recordingRefreshTimer)
    }
  }, [identity, refresh, source])

  const active = state.identity === identity && source ? state : emptyActivity
  return { ...active, refresh, dismissError: () => setState((current) => ({ ...current, error: null })) }
}
