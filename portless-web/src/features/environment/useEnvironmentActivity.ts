import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath } from '../../api'
import type { Environment, TimelineEvent, TimelineList } from '../../api/contracts/environments'
import type { FaultList, FaultRule, Recording, RecordingList } from '../../api/contracts/experiments'
import type { TrafficExchange } from '../../api/contracts/traffic'

const environmentActivityTopics = ['environment.state', 'service.state', 'recording.state', 'fault.state', 'operation.state', 'traffic.exchange']

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

export function useEnvironmentActivity(environment: Pick<Environment, 'project' | 'name'>, onChanged: () => void) {
  const [timeline, setTimeline] = useState<TimelineEvent[]>([])
  const [recordings, setRecordings] = useState<Recording[]>([])
  const [faults, setFaults] = useState<FaultRule[]>([])
  const [error, setError] = useState('')
  const onChangedRef = useRef(onChanged)
  onChangedRef.current = onChanged
  const identity = `${environment.project}/${environment.name}`
  const source = useMemo(() => ({ project: environment.project, name: environment.name }), [identity]) // eslint-disable-line react-hooks/exhaustive-deps

  const refresh = useCallback(async () => {
    const base = environmentPath(source)
    const [timelineResult, recordingResult, faultResult] = await Promise.all([
      api<TimelineList>(`${base}/timeline?limit=1000`),
      api<RecordingList>(`${base}/recordings`),
      api<FaultList>(`${base}/faults`),
    ])
    setTimeline(timelineResult.timeline)
    setRecordings(recordingResult.recordings)
    setFaults(faultResult.faults)
    setError('')
  }, [source])

  const refreshRecordings = useCallback(async () => {
    const result = await api<RecordingList>(`${environmentPath(source)}/recordings`)
    setRecordings(result.recordings)
  }, [source])

  useEffect(() => {
    let recordingRefreshTimer: number | undefined
    refresh().catch((value) => setError(value instanceof Error ? value.message : String(value)))
    const disconnect = connectEvents(source, environmentActivityTopics, (type, value) => {
      if (type === 'traffic.exchange') {
        const exchange = value as TrafficExchange
        if (!exchange.recording) return
        setRecordings((current) => advanceRecordingCount(current, exchange))
        if (recordingRefreshTimer === undefined) {
          recordingRefreshTimer = window.setTimeout(() => {
            recordingRefreshTimer = undefined
            refreshRecordings().catch(() => undefined)
          }, 500)
        }
        return
      }
      onChangedRef.current()
      refresh().catch(() => undefined)
    })
    return () => {
      disconnect()
      if (recordingRefreshTimer !== undefined) window.clearTimeout(recordingRefreshTimer)
    }
  }, [refresh, refreshRecordings, source])

  return { timeline, recordings, faults, error, dismissError: () => setError(''), refresh }
}
