import { useEffect, useMemo, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { relativeTime, StatusMark } from '../../../components/Status'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { experimentScopes, recordingScopeLabel } from '../../experimentScopes'

export function RecordingsPanel({ environment, recordings, refresh }: { environment: Environment; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('checkout-debug')
  const [scopeID, setScopeID] = useState('')
  const [capturePayloads, setCapturePayloads] = useState(false)
  const [maxPayloadBytes, setMaxPayloadBytes] = useState(65536)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  useEffect(() => {
    setScopeID('')
    setError(null)
  }, [environment.project, environment.name])

  const start = async () => {
    setError(null)
    const recordingName = name.trim()
    if (!recordingName) {
      setError(actionError("Recording wasn't started", 'Enter a recording name.'))
      return
    }
    try {
      await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody({ name: recordingName, source: selectedScope?.source || '', target: selectedScope?.target || '', capturePayloads, maxEvents: 10000, maxPayloadBytes }) })
      await refresh()
    } catch (value) {
      setError(actionError("Recording wasn't started", value))
    }
  }

  const stop = async (recording: Recording) => {
    setError(null)
    try {
      await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/stop`), { method: 'POST' })
      await refresh()
    } catch (value) {
      setError(actionError("Recording wasn't stopped", value))
    }
  }

  const remove = async (recording: Recording) => {
    setError(null)
    try {
      await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' })
      await refresh()
    } catch (value) {
      setError(actionError("Recording wasn't deleted", value))
    }
  }

  return <div className="experiment-layout">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-form"><div className="panel-title"><span>START RECORDING</span></div><label><span>NAME</span><input value={name} onChange={(event) => { setName(event.target.value); setError(null) }} /></label><label><span>TRAFFIC SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} onChange={(event) => { setScopeID(event.target.value); setError(null) }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label><label className="recording-body-toggle"><input type="checkbox" checked={capturePayloads} onChange={(event) => setCapturePayloads(event.target.checked)} /><span><strong>CAPTURE PAYLOADS</strong><small>Retains HTTP bodies and decoded database or messaging content.</small></span></label>{capturePayloads && <><label><span>MAXIMUM PAYLOAD SIZE</span><select value={maxPayloadBytes} onChange={(event) => setMaxPayloadBytes(Number(event.target.value))}><option value={16384}>16 KiB</option><option value={65536}>64 KiB</option><option value={262144}>256 KiB</option><option value={1048576}>1 MiB</option></select></label><div className="recording-body-warning"><strong>APPLICATION DATA</strong><span>Captured payloads are retained locally and can contain application data.</span></div></>}<button className="button button--primary" onClick={start}>● START RECORDING</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>RECORDINGS</span></div>{recordings.map((recording) => <div className="experiment-row" key={recording.name}><StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} /><div><strong>{recording.name}</strong><small>{recordingScopeLabel(recording)} · {recording.eventCount} events{recording.capturePayloads ? ' · payloads captured' : ''}</small></div><span>{relativeTime(recording.startedAt)} ago</span><div>{recording.status === 'active' ? <button onClick={() => stop(recording)}>STOP</button> : <><a href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}>EXPORT</a><button onClick={() => remove(recording)}>DELETE</button></>}</div></div>)}{recordings.length === 0 && <div className="empty-row">No recordings. Start one before reproducing a local issue.</div>}</section>
  </div>
}
