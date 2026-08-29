import { useEffect, useMemo, useRef, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import { relativeTime } from '../../../components/Status'
import { experimentScopeID, experimentScopes, recordingScopeLabel } from '../../experimentScopes'

const recordingHistoryPageSize = 5

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
  const [repeatFrom, setRepeatFrom] = useState<Recording>()
  const [controlRequest, setControlRequest] = useState(0)
  const [busy, setBusy] = useState('')
  const [deleteName, setDeleteName] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [historyPage, setHistoryPage] = useState(0)
  const activeRecording = recordings.find((recording) => recording.status === 'active')
  const historyRecordings = recordings.filter((recording) => recording.status !== 'active')
  const historyPagination = paginateItems(historyRecordings, historyPage, recordingHistoryPageSize)
  const controlDefaults = createRecordingDefaults(recordings, repeatFrom)

  useEffect(() => {
    setRepeatFrom(undefined)
    setControlRequest(0)
    setDeleteName('')
    setError(null)
    setHistoryPage(0)
  }, [environment.project, environment.name])

  const start = async (input: CreateRecordingInput) => {
    setBusy('create')
    setDeleteName('')
    setError(null)
    try {
      await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody(input) })
      await refresh()
      setRepeatFrom(undefined)
    } catch (value) {
      setError(actionError("Recording wasn't started", value))
    } finally {
      setBusy('')
    }
  }

  const stop = async (recording: Recording) => {
    setBusy(`stop:${recording.name}`)
    setDeleteName('')
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

  const prepareRepeat = (recording: Recording) => {
    setRepeatFrom(recording)
    setControlRequest((value) => value + 1)
    setDeleteName('')
    setError(null)
  }

  return <div className="recordings-page">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel recording-control-panel">
      <div className="panel-title"><span>RECORDING CONTROL</span><small>{activeRecording ? 'CAPTURE IN PROGRESS' : 'READY'}</small></div>
      {activeRecording
        ? <ActiveRecordingControl recording={activeRecording} busy={busy === `stop:${activeRecording.name}`} onStop={() => void stop(activeRecording)} />
        : <RecordingControlForm
            key={`${environment.project}/${environment.name}:${controlRequest}`}
            environment={environment}
            defaults={controlDefaults}
            busy={busy === 'create'}
            focusNameRequest={controlRequest}
            onDismissError={() => setError(null)}
            onCreate={start}
          />}
    </section>

    <section className="panel recording-history-panel">
      <div className="panel-title recording-history-title">
        <span>HISTORY</span>
      </div>
      <div className="recording-history-scroll">
        <table className="recording-history-table">
          <thead><tr><th>Recording</th><th>Events</th><th>Created at</th><th>Completed</th><th>Actions</th></tr></thead>
          <tbody>
            {historyPagination.items.map((recording) => <tr key={recording.name}>
              <td>
                <strong>{recording.name}</strong>
                <small className="recording-history__details" title={`${recordingScopeLabel(recording)}; payloads ${recording.capturePayloads ? 'captured' : 'not captured'}`}>
                  {recordingScopeLabel(recording)} · Payloads {recording.capturePayloads ? 'captured' : 'not captured'}
                </small>
              </td>
              <td>{recording.eventCount.toLocaleString()}</td>
              <td className="recording-history__created"><time dateTime={recording.startedAt} title={new Date(recording.startedAt).toLocaleString()}>{formatRecordingTimestamp(recording.startedAt)}</time></td>
              <td className="recording-history__time"><time dateTime={recording.completedAt || recording.startedAt}>{relativeTime(recording.completedAt || recording.startedAt)} ago</time></td>
              <td><div className="table-row-actions recording-history__actions">
                <button
                  type="button"
                  disabled={!!busy || !!activeRecording}
                  aria-label={`Record ${recording.name} again`}
                  onClick={() => prepareRepeat(recording)}
                >RECORD AGAIN</button>
                <a
                  className="recording-history__export"
                  href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}
                  onClick={() => setDeleteName('')}
                >EXPORT</a>
                <button
                  className={deleteName === recording.name ? 'is-confirming' : ''}
                  type="button"
                  disabled={!!busy}
                  aria-label={deleteName === recording.name ? `Confirm delete ${recording.name}` : `Delete ${recording.name}`}
                  onClick={() => void remove(recording)}
                >{busy === `delete:${recording.name}` ? 'DELETING…' : deleteName === recording.name ? 'CONFIRM' : 'DELETE'}</button>
              </div></td>
            </tr>)}
            {historyRecordings.length === 0 && <tr><td className="recording-history__empty" colSpan={5}>No recording history yet. Completed recordings will appear here.</td></tr>}
          </tbody>
        </table>
      </div>
      <PanelPagination label="recordings" pagination={historyPagination} onPage={(page) => { setHistoryPage(page); setDeleteName('') }} />
    </section>
  </div>
}

function formatRecordingTimestamp(value: string) {
  return new Date(value).toLocaleString([], { dateStyle: 'short', timeStyle: 'short' })
}

function ActiveRecordingControl({ recording, busy, onStop }: { recording: Recording; busy: boolean; onStop: () => void }) {
  return <div className="recording-active-control">
    <div className="recording-active-control__lead">
      <div className="recording-active-control__identity">
        <span className="recording-active-control__pulse" aria-hidden="true" />
        <div><div><strong>{recording.name}</strong><span className="recording-active-control__badge">RECORDING</span></div></div>
      </div>
      <button className="button button--danger" type="button" disabled={busy} onClick={onStop}>{busy ? 'STOPPING…' : 'STOP RECORDING'}</button>
    </div>
    <div className="recording-active-control__details">
      <div><span>TRAFFIC SCOPE</span><strong>{recordingScopeLabel(recording)}</strong></div>
      <div><span>CAPTURED EVENTS</span><strong>{recording.eventCount.toLocaleString()}</strong></div>
      <div><span>STARTED</span><strong>{relativeTime(recording.startedAt)} ago</strong></div>
      <div><span>PAYLOADS</span><strong>{recording.capturePayloads ? 'Captured' : 'Not captured'}</strong></div>
    </div>
  </div>
}

function RecordingControlForm({ environment, defaults, busy, focusNameRequest, onDismissError, onCreate }: {
  environment: Environment
  defaults: CreateRecordingInput
  busy: boolean
  focusNameRequest: number
  onDismissError: () => void
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
  const [dirty, setDirty] = useState(false)
  const nameInput = useRef<HTMLInputElement>(null)
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  useEffect(() => {
    if (dirty) return
    setName(defaults.name)
    setScopeID(defaults.source || defaults.target ? experimentScopeID(defaults.source, defaults.target) : '')
    setCapturePayloads(defaults.capturePayloads)
    setMaxPayloadBytes(defaults.maxPayloadBytes)
  }, [defaults.capturePayloads, defaults.maxPayloadBytes, defaults.name, defaults.source, defaults.target, dirty])

  useEffect(() => {
    if (!focusNameRequest) return
    nameInput.current?.focus()
    nameInput.current?.select()
  }, [focusNameRequest])

  const change = () => {
    setDirty(true)
    onDismissError()
  }

  return <form className="recording-control-form" autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => {
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
      <p>Capture traffic while you reproduce an issue.</p>
      <div className="recording-control__fields">
        <label><span>NAME</span><input ref={nameInput} name="portless-recording-name" required autoComplete="off" spellCheck="false" value={name} disabled={busy} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => { setName(event.target.value); change() }} /></label>
        <label><span>TRAFFIC SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} disabled={busy} onChange={(event) => { setScopeID(event.target.value); change() }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label>
        <label className="recording-body-toggle provider-field--wide"><input type="checkbox" checked={capturePayloads} disabled={busy} onChange={(event) => { setCapturePayloads(event.target.checked); change() }} /><span><strong>CAPTURE PAYLOADS</strong><small>Retains HTTP bodies and decoded database or messaging content.</small></span></label>
        {capturePayloads && <>
          <label><span>MAXIMUM PAYLOAD SIZE</span><select value={maxPayloadBytes} disabled={busy} onChange={(event) => { setMaxPayloadBytes(Number(event.target.value)); change() }}>{![16384, 65536, 262144, 1048576].includes(maxPayloadBytes) && <option value={maxPayloadBytes}>{maxPayloadBytes.toLocaleString()} bytes</option>}<option value={16384}>16 KiB</option><option value={65536}>64 KiB</option><option value={262144}>256 KiB</option><option value={1048576}>1 MiB</option></select></label>
          <div className="recording-body-warning"><strong>APPLICATION DATA</strong><span>Captured payloads are retained locally and can contain application data.</span></div>
        </>}
      </div>
      <footer><button className="button button--primary" type="submit" disabled={busy || !name.trim()}>{busy ? 'STARTING…' : '● START RECORDING'}</button></footer>
    </form>
}
