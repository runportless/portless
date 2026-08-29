import { useEffect, useMemo, useRef, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { FormDialog } from '../../../components/overlays/FormDialog'
import { relativeTime, StatusMark } from '../../../components/Status'
import { experimentScopeID, experimentScopes, recordingScopeLabel } from '../../experimentScopes'

export interface CreateRecordingInput {
  name: string
  source: string
  target: string
  capturePayloads: boolean
  maxEvents: number
  maxPayloadBytes: number
}

export function createRecordingDefaults(recordings: Recording[], previous?: Recording): CreateRecordingInput {
  const existingNames = new Set(recordings.map((recording) => recording.name.toLowerCase()))
  const originalName = previous?.name || 'checkout-debug'
  const numberedName = originalName.match(/^(.*)-(\d+)$/)
  const stem = numberedName?.[1] || originalName
  let sequence = numberedName ? Number(numberedName[2]) + 1 : 2
  let name = originalName
  while (existingNames.has(name.toLowerCase())) {
    const suffix = `-${sequence}`
    const availableStem = stem.slice(0, 64 - suffix.length).replace(/[._-]+$/, '')
    name = `${availableStem}${suffix}`
    sequence++
  }
  return {
    name,
    source: previous?.source || '',
    target: previous?.target || '',
    capturePayloads: previous?.capturePayloads ?? false,
    maxEvents: previous?.maxEvents ?? 10000,
    maxPayloadBytes: previous?.maxPayloadBytes ?? 65536,
  }
}

export function RecordingsPanel({ environment, recordings, refresh }: { environment: Environment; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [createDefaults, setCreateDefaults] = useState<CreateRecordingInput | null>(null)
  const [busy, setBusy] = useState('')
  const [deleteName, setDeleteName] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const activeRecording = recordings.find((recording) => recording.status === 'active')
  const orderedRecordings = activeRecording
    ? [activeRecording, ...recordings.filter((recording) => recording !== activeRecording)]
    : recordings

  useEffect(() => {
    setCreateDefaults(null)
    setDeleteName('')
    setError(null)
  }, [environment.project, environment.name])

  useEffect(() => {
    if (activeRecording) setCreateDefaults(null)
  }, [activeRecording])

  const start = async (input: CreateRecordingInput) => {
    setBusy('create')
    setError(null)
    try {
      await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody(input) })
      await refresh()
      setCreateDefaults(null)
    } catch (value) {
      setError(actionError("Recording wasn't started", value))
    } finally {
      setBusy('')
    }
  }

  const stop = async (recording: Recording) => {
    setBusy(`stop:${recording.name}`)
    setError(null)
    try {
      await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/stop`), { method: 'POST' })
      await refresh()
    } catch (value) {
      setError(actionError("Recording wasn't stopped", value))
    } finally {
      setBusy('')
    }
  }

  const remove = async (recording: Recording) => {
    if (deleteName !== recording.name) {
      setDeleteName(recording.name)
      setError(null)
      return
    }
    setBusy(`delete:${recording.name}`)
    setError(null)
    try {
      await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' })
      await refresh()
      setDeleteName('')
    } catch (value) {
      setError(actionError("Recording wasn't deleted", value))
    } finally {
      setBusy('')
    }
  }

  return <div className="recordings-page">
    {!createDefaults && error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-list">
      <div className="panel-title recordings-panel-title">
        <span>RECORDINGS</span>
        <div className="recordings-title-actions">
          {activeRecording && <small id="recording-create-unavailable">Stop {activeRecording.name} before creating another recording.</small>}
          <button
            className="button button--primary button--small panel-create-button"
            type="button"
            disabled={!!busy || !!activeRecording}
            aria-describedby={activeRecording ? 'recording-create-unavailable' : undefined}
            onClick={() => { setCreateDefaults(createRecordingDefaults(recordings)); setDeleteName(''); setError(null) }}
          >CREATE RECORDING</button>
        </div>
      </div>
      {orderedRecordings.map((recording) => <div className={`experiment-row recording-row${recording.status === 'active' ? ' recording-row--active' : ''}`} key={recording.name}>
        <StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} />
        <div>
          <div className="recording-row__name">
            <strong>{recording.name}</strong>
            {recording.status === 'active' && <span>RECORDING</span>}
          </div>
          <small>{recordingScopeLabel(recording)} · {recording.eventCount} {recording.eventCount === 1 ? 'event' : 'events'}{recording.capturePayloads ? ' · payloads captured' : ''}</small>
        </div>
        <span>{relativeTime(recording.startedAt)} ago</span>
        <div>
          {recording.status === 'active'
            ? <button type="button" disabled={!!busy} onClick={() => { setDeleteName(''); void stop(recording) }}>{busy === `stop:${recording.name}` ? 'STOPPING…' : 'STOP RECORDING'}</button>
            : <>
                <button
                  type="button"
                  disabled={!!busy || !!activeRecording}
                  aria-label={`Record ${recording.name} again`}
                  aria-describedby={activeRecording ? 'recording-create-unavailable' : undefined}
                  onClick={() => { setCreateDefaults(createRecordingDefaults(recordings, recording)); setDeleteName(''); setError(null) }}
                >RECORD AGAIN</button>
                <a href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`} onClick={() => setDeleteName('')}>EXPORT</a>
                <button
                  className={deleteName === recording.name ? 'is-confirming' : ''}
                  type="button"
                  disabled={!!busy}
                  aria-label={deleteName === recording.name ? `Confirm delete ${recording.name}` : `Delete ${recording.name}`}
                  onClick={() => void remove(recording)}
                >{busy === `delete:${recording.name}` ? 'DELETING…' : deleteName === recording.name ? 'CONFIRM' : 'DELETE'}</button>
              </>}
        </div>
      </div>)}
      {recordings.length === 0 && <div className="empty-row">No recordings. Create one before reproducing a local issue.</div>}
    </section>

    {createDefaults && !activeRecording && <CreateRecordingModal
      environment={environment}
      defaults={createDefaults}
      busy={busy === 'create'}
      error={error}
      onDismissError={() => setError(null)}
      onClose={() => { setCreateDefaults(null); setError(null) }}
      onCreate={start}
    />}
  </div>
}

function CreateRecordingModal({ environment, defaults, busy, error, onDismissError, onClose, onCreate }: {
  environment: Environment
  defaults: CreateRecordingInput
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onCreate: (input: CreateRecordingInput) => Promise<void>
}) {
  const scopes = useMemo(() => {
    const available = experimentScopes(environment)
    if (!defaults.source && !defaults.target) return available
    const id = experimentScopeID(defaults.source, defaults.target)
    if (available.some((scope) => scope.id === id)) return available
    return [{ id, source: defaults.source, target: defaults.target, label: recordingScopeLabel(defaults) }, ...available]
  }, [defaults, environment])
  const [name, setName] = useState(defaults.name)
  const [scopeID, setScopeID] = useState(defaults.source || defaults.target ? experimentScopeID(defaults.source, defaults.target) : '')
  const [capturePayloads, setCapturePayloads] = useState(defaults.capturePayloads)
  const [maxPayloadBytes, setMaxPayloadBytes] = useState(defaults.maxPayloadBytes)
  const nameInput = useRef<HTMLInputElement>(null)
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  return <FormDialog
    titleID="create-recording-title"
    descriptionID="create-recording-description"
    closeLabel="Close create recording"
    closeBlocked={busy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">TRAFFIC RECORDING</div><h2 id="create-recording-title">Create Recording</h2></div>}
    onClose={onClose}
  >
    <form onSubmit={(event) => {
      event.preventDefault()
      if (busy || !name.trim()) return
      void onCreate({
        name: name.trim(),
        source: selectedScope?.source || '',
        target: selectedScope?.target || '',
        capturePayloads,
        maxEvents: defaults.maxEvents,
        maxPayloadBytes,
      })
    }}>
      <p id="create-recording-description">Capture traffic while you reproduce an issue.</p>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} name="recording-name" required autoComplete="off" value={name} disabled={busy} onChange={(event) => { setName(event.target.value); onDismissError() }} /></label>
        <label><span>TRAFFIC SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} disabled={busy} onChange={(event) => { setScopeID(event.target.value); onDismissError() }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label>
        <label className="recording-body-toggle provider-field--wide"><input type="checkbox" checked={capturePayloads} disabled={busy} onChange={(event) => { setCapturePayloads(event.target.checked); onDismissError() }} /><span><strong>CAPTURE PAYLOADS</strong><small>Retains HTTP bodies and decoded database or messaging content.</small></span></label>
        {capturePayloads && <>
          <label className="provider-field--wide"><span>MAXIMUM PAYLOAD SIZE</span><select value={maxPayloadBytes} disabled={busy} onChange={(event) => setMaxPayloadBytes(Number(event.target.value))}>{![16384, 65536, 262144, 1048576].includes(maxPayloadBytes) && <option value={maxPayloadBytes}>{maxPayloadBytes.toLocaleString()} bytes</option>}<option value={16384}>16 KiB</option><option value={65536}>64 KiB</option><option value={262144}>256 KiB</option><option value={1048576}>1 MiB</option></select></label>
          <div className="recording-body-warning provider-field--wide"><strong>APPLICATION DATA</strong><span>Captured payloads are retained locally and can contain application data.</span></div>
        </>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !name.trim()}>{busy ? 'STARTING…' : '● START RECORDING'}</button></footer>
    </form>
  </FormDialog>
}
