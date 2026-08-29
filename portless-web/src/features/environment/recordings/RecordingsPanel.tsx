import { useEffect, useMemo, useRef, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { FormDialog } from '../../../components/overlays/FormDialog'
import { relativeTime, StatusMark } from '../../../components/Status'
import { experimentScopes, recordingScopeLabel } from '../../experimentScopes'

interface CreateRecordingInput {
  name: string
  source: string
  target: string
  capturePayloads: boolean
  maxEvents: number
  maxPayloadBytes: number
}

export function RecordingsPanel({ environment, recordings, refresh }: { environment: Environment; recordings: Recording[]; refresh: () => Promise<void> }) {
  const [createOpen, setCreateOpen] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)

  useEffect(() => {
    setCreateOpen(false)
    setError(null)
  }, [environment.project, environment.name])

  const start = async (input: CreateRecordingInput) => {
    setBusy('create')
    setError(null)
    try {
      await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody(input) })
      await refresh()
      setCreateOpen(false)
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
    setBusy(`delete:${recording.name}`)
    setError(null)
    try {
      await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' })
      await refresh()
    } catch (value) {
      setError(actionError("Recording wasn't deleted", value))
    } finally {
      setBusy('')
    }
  }

  return <div className="recordings-page">
    {!createOpen && error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-list">
      <div className="panel-title">
        <span>RECORDINGS</span>
        <button className="button button--primary button--small panel-create-button" type="button" disabled={!!busy} onClick={() => { setCreateOpen(true); setError(null) }}>CREATE RECORDING</button>
      </div>
      {recordings.map((recording) => <div className="experiment-row" key={recording.name}>
        <StatusMark status={recording.status === 'active' ? 'active' : 'stopped'} label={false} />
        <div>
          <strong>{recording.name}</strong>
          <small>{recordingScopeLabel(recording)} · {recording.eventCount} events{recording.capturePayloads ? ' · payloads captured' : ''}</small>
        </div>
        <span>{relativeTime(recording.startedAt)} ago</span>
        <div>
          {recording.status === 'active'
            ? <button type="button" disabled={!!busy} onClick={() => void stop(recording)}>{busy === `stop:${recording.name}` ? 'STOPPING…' : 'STOP'}</button>
            : <>
                <a href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}>EXPORT</a>
                <button type="button" disabled={!!busy} onClick={() => void remove(recording)}>{busy === `delete:${recording.name}` ? 'DELETING…' : 'DELETE'}</button>
              </>}
        </div>
      </div>)}
      {recordings.length === 0 && <div className="empty-row">No recordings. Create one before reproducing a local issue.</div>}
    </section>

    {createOpen && <CreateRecordingModal
      environment={environment}
      busy={busy === 'create'}
      error={error}
      onDismissError={() => setError(null)}
      onClose={() => { setCreateOpen(false); setError(null) }}
      onCreate={start}
    />}
  </div>
}

function CreateRecordingModal({ environment, busy, error, onDismissError, onClose, onCreate }: {
  environment: Environment
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onCreate: (input: CreateRecordingInput) => Promise<void>
}) {
  const [name, setName] = useState('checkout-debug')
  const [scopeID, setScopeID] = useState('')
  const [capturePayloads, setCapturePayloads] = useState(false)
  const [maxPayloadBytes, setMaxPayloadBytes] = useState(65536)
  const nameInput = useRef<HTMLInputElement>(null)
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  return <FormDialog
    titleID="create-recording-title"
    descriptionID="create-recording-description"
    closeLabel="Close create recording"
    closeBlocked={busy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">TRAFFIC RECORDING</div><h2 id="create-recording-title">Create recording</h2></div>}
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
        maxEvents: 10000,
        maxPayloadBytes,
      })
    }}>
      <p id="create-recording-description">Capture traffic while you reproduce an issue. This recording retains up to 10,000 events in this environment.</p>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} name="recording-name" required autoComplete="off" value={name} disabled={busy} onChange={(event) => { setName(event.target.value); onDismissError() }} /></label>
        <label><span>TRAFFIC SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} disabled={busy} onChange={(event) => { setScopeID(event.target.value); onDismissError() }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label>
        <label className="recording-body-toggle provider-field--wide"><input type="checkbox" checked={capturePayloads} disabled={busy} onChange={(event) => { setCapturePayloads(event.target.checked); onDismissError() }} /><span><strong>CAPTURE PAYLOADS</strong><small>Retains HTTP bodies and decoded database or messaging content.</small></span></label>
        {capturePayloads && <>
          <label className="provider-field--wide"><span>MAXIMUM PAYLOAD SIZE</span><select value={maxPayloadBytes} disabled={busy} onChange={(event) => setMaxPayloadBytes(Number(event.target.value))}><option value={16384}>16 KiB</option><option value={65536}>64 KiB</option><option value={262144}>256 KiB</option><option value={1048576}>1 MiB</option></select></label>
          <div className="recording-body-warning provider-field--wide"><strong>APPLICATION DATA</strong><span>Captured payloads are retained locally and can contain application data.</span></div>
        </>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !name.trim()}>{busy ? 'STARTING…' : '● START RECORDING'}</button></footer>
    </form>
  </FormDialog>
}
