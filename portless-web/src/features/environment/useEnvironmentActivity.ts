import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, connectEvents, environmentPath } from '../../api'
import type { Environment, FaultRule, Recording, TimelineEvent } from '../../types'

const environmentActivityTopics = ['environment.state', 'service.state', 'recording.state', 'fault.state', 'operation.state']

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
      api<{ timeline: TimelineEvent[] }>(`${base}/timeline?limit=1000`),
      api<{ recordings: Recording[] }>(`${base}/recordings`),
      api<{ faults: FaultRule[] }>(`${base}/faults`),
    ])
    setTimeline(timelineResult.timeline)
    setRecordings(recordingResult.recordings)
    setFaults(faultResult.faults)
    setError('')
  }, [source])

  useEffect(() => {
    refresh().catch((value) => setError(value instanceof Error ? value.message : String(value)))
    return connectEvents(source, environmentActivityTopics, () => {
      onChangedRef.current()
      refresh().catch(() => undefined)
    })
  }, [refresh, source])

  return { timeline, recordings, faults, error, dismissError: () => setError(''), refresh }
}
