import { useEffect, useMemo, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import { RowActionsMenu } from '../../../components/RowActionsMenu'
import { SortableTableHeader, type TableSort } from '../../../components/SortableTableHeader'
import { relativeTime } from '../../../components/Status'
import { experimentScopeID, experimentScopes, recordingScopeLabel } from '../../experimentScopes'

const recordingHistoryPageSize = 6
type RecordingHistorySortField = 'name' | 'events' | 'createdAt' | 'duration' | 'completed'
const defaultRecordingHistorySort: TableSort<RecordingHistorySortField> = { key: 'createdAt', direction: 'desc' }

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
  const [busy, setBusy] = useState('')
  const [deleteName, setDeleteName] = useState('')
  const [menuRecording, setMenuRecording] = useState('')
  const [deleteAllConfirm, setDeleteAllConfirm] = useState(false)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [historyPage, setHistoryPage] = useState(0)
  const [historySort, setHistorySort] = useState<TableSort<RecordingHistorySortField>>(defaultRecordingHistorySort)
  const activeRecording = recordings.find((recording) => recording.status === 'active')
  const historyRecordings = useMemo(() => sortRecordingHistory(recordings.filter((recording) => recording.status !== 'active'), historySort), [recordings, historySort])
  const historyPagination = paginateItems(historyRecordings, historyPage, recordingHistoryPageSize)
  const controlDefaults = createRecordingDefaults(recordings)

  useEffect(() => {
    setDeleteName('')
    setMenuRecording('')
    setDeleteAllConfirm(false)
    setError(null)
    setHistoryPage(0)
    setHistorySort(defaultRecordingHistorySort)
  }, [environment.project, environment.name])

  const start = async (input: CreateRecordingInput, action = 'create') => {
    setBusy(action)
    setDeleteName('')
    setMenuRecording('')
    setDeleteAllConfirm(false)
    setError(null)
    try {
      await api(environmentPath(environment, '/recordings'), { method: 'POST', ...jsonBody(input) })
      await refresh()
    } catch (value) {
      setError(actionError("Recording wasn't started", value))
    } finally {
      setBusy('')
    }
  }

  const stop = async (recording: Recording) => {
    setBusy(`stop:${recording.name}`)
    setDeleteName('')
    setMenuRecording('')
    setDeleteAllConfirm(false)
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
    setDeleteAllConfirm(false)
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
      setMenuRecording('')
    } catch (value) {
      setError(actionError("Recording wasn't deleted", value))
    } finally {
      setBusy('')
    }
  }

  const removeAll = async () => {
    if (busy || historyRecordings.length === 0) return
    if (!deleteAllConfirm) {
      setDeleteAllConfirm(true)
      setDeleteName('')
      setMenuRecording('')
      setError(null)
      return
    }
    setBusy('delete-all')
    setDeleteAllConfirm(false)
    setDeleteName('')
    setMenuRecording('')
    setError(null)
    try {
      for (const recording of historyRecordings) {
        await api(environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}`), { method: 'DELETE' })
      }
      await refresh()
      setHistoryPage(0)
    } catch (value) {
      await refresh().catch(() => undefined)
      setHistoryPage(0)
      setError(actionError("Recording history wasn't fully deleted", value))
    } finally {
      setBusy('')
    }
  }

  const recordAgain = async (recording: Recording) => {
    await start(createRecordingDefaults(recordings, recording), `repeat:${recording.name}`)
  }

  const clearRowConfirmations = () => {
    setDeleteName('')
    setMenuRecording('')
    setDeleteAllConfirm(false)
  }

  return <div className="recordings-page">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel recording-control-panel">
      <div className="panel-title"><span>{activeRecording ? 'ACTIVE RECORDING' : 'NEW RECORDING'}</span></div>
      {activeRecording
        ? <ActiveRecordingControl recording={activeRecording} busy={busy === `stop:${activeRecording.name}`} onStop={() => void stop(activeRecording)} />
        : <RecordingControlForm
            key={`${environment.project}/${environment.name}`}
            environment={environment}
            defaults={controlDefaults}
            busy={!!busy}
            onDismissError={() => setError(null)}
            onCreate={(input) => start(input)}
          />}
    </section>

    <section className="panel recording-history-panel">
      <div className="panel-title recording-history-title">
        <span>HISTORY</span>
        <button
          className={`recording-history-delete-all${deleteAllConfirm ? ' is-confirming' : ''}`}
          type="button"
          disabled={!!busy || historyRecordings.length === 0}
          aria-label={deleteAllConfirm
            ? `Confirm delete all ${historyRecordings.length} completed recording${historyRecordings.length === 1 ? '' : 's'}`
            : historyRecordings.length === 0
              ? 'Delete all completed recordings'
              : `Delete all ${historyRecordings.length} completed recording${historyRecordings.length === 1 ? '' : 's'}`}
          onClick={() => void removeAll()}
        >{busy === 'delete-all' ? 'DELETING…' : deleteAllConfirm ? 'CONFIRM' : 'DELETE ALL'}</button>
      </div>
      <div className="recording-history-scroll">
        <table className="recording-history-table">
          <thead><tr className={`sortable-header-row${historySort.key === defaultRecordingHistorySort.key && historySort.direction === defaultRecordingHistorySort.direction ? ' is-default-sort' : ''}`}>
            <SortableTableHeader label="Recording" sortKey="name" sort={historySort} itemCount={historyRecordings.length} onSort={(sort) => { setHistorySort(sort); setHistoryPage(0); clearRowConfirmations() }} />
            <SortableTableHeader label="Events" sortKey="events" sort={historySort} itemCount={historyRecordings.length} onSort={(sort) => { setHistorySort(sort); setHistoryPage(0); clearRowConfirmations() }} />
            <SortableTableHeader label="Created at" sortKey="createdAt" sort={historySort} itemCount={historyRecordings.length} onSort={(sort) => { setHistorySort(sort); setHistoryPage(0); clearRowConfirmations() }} />
            <SortableTableHeader label="Duration" sortKey="duration" sort={historySort} itemCount={historyRecordings.length} onSort={(sort) => { setHistorySort(sort); setHistoryPage(0); clearRowConfirmations() }} />
            <SortableTableHeader label="Completed" sortKey="completed" sort={historySort} itemCount={historyRecordings.length} onSort={(sort) => { setHistorySort(sort); setHistoryPage(0); clearRowConfirmations() }} />
            <th aria-label="Actions" />
          </tr></thead>
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
              <td className="recording-history__duration">{recording.completedAt ? formatRecordingDuration(recording.startedAt, new Date(recording.completedAt).getTime()) : '—'}</td>
              <td className="recording-history__time"><time dateTime={recording.completedAt || recording.startedAt}>{relativeTime(recording.completedAt || recording.startedAt)} ago</time></td>
              <td><div className="table-row-actions recording-history__actions">
                <RecordingHistoryRepeatButton
                  recordingName={recording.name}
                  starting={busy === `repeat:${recording.name}`}
                  disabled={!!busy || !!activeRecording}
                  onClick={() => void recordAgain(recording)}
                />
                <RowActionsMenu
                  label={`Recording actions for ${recording.name}`}
                  menuLabel={`${recording.name} recording actions`}
                  open={menuRecording === recording.name}
                  disabled={!!busy}
                  onOpenChange={(open) => {
                    setMenuRecording(open ? recording.name : '')
                    if (!open || menuRecording !== recording.name) setDeleteName('')
                  }}
                >
                  <a
                    href={`/api/v1${environmentPath(environment, `/recordings/${encodeURIComponent(recording.name)}/export`)}`}
                    role="menuitem"
                    aria-label={`Export ${recording.name}`}
                    onClick={clearRowConfirmations}
                  >EXPORT</a>
                  <button className={`is-danger${deleteName === recording.name ? ' is-confirming' : ''}`} type="button" role="menuitem" disabled={!!busy} aria-label={deleteName === recording.name ? `Confirm delete ${recording.name}` : `Delete ${recording.name}`} onClick={() => void remove(recording)}>{busy === `delete:${recording.name}` ? 'DELETING…' : deleteName === recording.name ? 'CONFIRM' : 'DELETE'}</button>
                </RowActionsMenu>
              </div></td>
            </tr>)}
            {historyRecordings.length === 0 && <tr><td className="recording-history__empty" colSpan={6}>No recording history yet. Completed recordings will appear here.</td></tr>}
          </tbody>
        </table>
      </div>
      <PanelPagination label="recordings" pagination={historyPagination} onPage={(page) => { setHistoryPage(page); clearRowConfirmations() }} />
    </section>
  </div>
}

export function RecordingHistoryRepeatButton({ recordingName, starting, disabled, onClick }: {
  recordingName: string
  starting: boolean
  disabled: boolean
  onClick: () => void
}) {
  return <button
    className="recording-history__repeat"
    type="button"
    disabled={disabled}
    aria-label={`Repeat recording ${recordingName}`}
    onClick={onClick}
  ><RepeatRecordingIcon /><span>{starting ? 'STARTING…' : 'REPEAT'}</span></button>
}

function RepeatRecordingIcon() {
  return <svg className="recording-history__repeat-icon" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
    <path d="M13 6.5A5.5 5.5 0 1 0 13 10" />
    <path d="M10.5 3.5H13v3" />
  </svg>
}

function formatRecordingTimestamp(value: string) {
  return new Date(value).toLocaleString([], { dateStyle: 'short', timeStyle: 'short' })
}

export function formatRecordingDuration(startedAt: string, now = Date.now()) {
  const started = new Date(startedAt).getTime()
  const elapsedSeconds = Number.isFinite(started) && Number.isFinite(now) ? Math.max(0, Math.floor((now - started) / 1000)) : 0
  const hours = Math.floor(elapsedSeconds / 3600)
  const minutes = Math.floor(elapsedSeconds % 3600 / 60)
  const seconds = elapsedSeconds % 60
  return [hours, minutes, seconds].map((value) => String(value).padStart(2, '0')).join(':')
}

export function sortRecordingHistory(recordings: Recording[], sort: TableSort<RecordingHistorySortField>) {
  const direction = sort.direction === 'asc' ? 1 : -1
  return [...recordings].sort((left, right) => {
    const nameOrder = compareRecordingText(left.name, right.name)
    let order = 0

    switch (sort.key) {
      case 'name':
        order = nameOrder
        break
      case 'events':
        order = left.eventCount - right.eventCount
        break
      case 'createdAt':
        order = recordingTimestampValue(left.startedAt) - recordingTimestampValue(right.startedAt)
        break
      case 'duration':
        order = recordingDurationValue(left) - recordingDurationValue(right)
        break
      case 'completed':
        order = recordingTimestampValue(left.completedAt) - recordingTimestampValue(right.completedAt)
        break
    }

    return direction * order || nameOrder
  })
}

function compareRecordingText(left: string, right: string) {
  return left.localeCompare(right, undefined, { sensitivity: 'base', numeric: true })
}

function recordingTimestampValue(value?: string) {
  const timestamp = value ? Date.parse(value) : 0
  return Number.isNaN(timestamp) ? 0 : timestamp
}

function recordingDurationValue(recording: Recording) {
  return Math.max(0, recordingTimestampValue(recording.completedAt) - recordingTimestampValue(recording.startedAt))
}

type RecordingDurationScheduler = {
  setInterval: (callback: () => void, milliseconds: number) => number
  clearInterval: (timer: number) => void
}

export function startRecordingDurationTimer(onTick: (now: number) => void, scheduler: RecordingDurationScheduler = window) {
  const tick = () => onTick(Date.now())
  tick()
  const timer = scheduler.setInterval(tick, 1000)
  return () => scheduler.clearInterval(timer)
}

function ActiveRecordingControl({ recording, busy, onStop }: { recording: Recording; busy: boolean; onStop: () => void }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => startRecordingDurationTimer(setNow), [recording.startedAt])

  return <div className="recording-active-control">
    <div className="recording-active-control__identity">
      <span className="recording-active-control__pulse" aria-hidden="true" />
      <div><div><strong>{recording.name}</strong><span className="recording-active-control__badge">RECORDING</span></div></div>
    </div>
    <div className="recording-active-control__details">
      <div><span>TRAFFIC SCOPE</span><strong>{recordingScopeLabel(recording)}</strong></div>
      <div><span>CAPTURED EVENTS</span><strong>{recording.eventCount.toLocaleString()}</strong></div>
      <div><span>DURATION</span><strong>{formatRecordingDuration(recording.startedAt, now)}</strong></div>
      <div><span>PAYLOADS</span><strong>{recording.capturePayloads ? 'Captured' : 'Not captured'}</strong></div>
    </div>
    <button className="button button--danger" type="button" disabled={busy} onClick={onStop}>{busy ? 'STOPPING…' : 'STOP RECORDING'}</button>
  </div>
}

function RecordingControlForm({ environment, defaults, busy, onDismissError, onCreate }: {
  environment: Environment
  defaults: CreateRecordingInput
  busy: boolean
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
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  useEffect(() => {
    if (dirty) return
    setName(defaults.name)
    setScopeID(defaults.source || defaults.target ? experimentScopeID(defaults.source, defaults.target) : '')
    setCapturePayloads(defaults.capturePayloads)
    setMaxPayloadBytes(defaults.maxPayloadBytes)
  }, [defaults.capturePayloads, defaults.maxPayloadBytes, defaults.name, defaults.source, defaults.target, dirty])

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
      <div className="recording-control-form__primary">
        <label><span>NAME</span><input name="portless-recording-name" required autoComplete="off" spellCheck="false" value={name} disabled={busy} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => { setName(event.target.value); change() }} /></label>
        <label><span>SCOPE</span><select aria-label="Recording traffic scope" value={scopeID} disabled={busy} onChange={(event) => { setScopeID(event.target.value); change() }}><option value="">All traffic</option>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label>
        <div className={`recording-payload-field${capturePayloads ? ' is-active' : ''}`} role="group" aria-labelledby="recording-payload-label">
          <span id="recording-payload-label">PAYLOADS</span>
          <div className="recording-payload-field__control">
            <label className="recording-payload-toggle"><input type="checkbox" checked={capturePayloads} disabled={busy} onChange={(event) => { setCapturePayloads(event.target.checked); change() }} /><span>INCLUDE</span></label>
            <select aria-label="Maximum payload size" value={maxPayloadBytes} disabled={busy || !capturePayloads} onChange={(event) => { setMaxPayloadBytes(Number(event.target.value)); change() }}>{![16384, 65536, 262144, 1048576].includes(maxPayloadBytes) && <option value={maxPayloadBytes}>{maxPayloadBytes.toLocaleString()} bytes</option>}<option value={16384}>16 KiB</option><option value={65536}>64 KiB</option><option value={262144}>256 KiB</option><option value={1048576}>1 MiB</option></select>
            <span className="recording-payload-help">
              <button type="button" aria-label="About payload capture" aria-describedby="recording-payload-help">i</button>
              <span className="recording-payload-tooltip" id="recording-payload-help" role="tooltip">Captured payloads are retained locally and may contain application data.</span>
            </span>
          </div>
        </div>
        <button className="button button--primary recording-control-form__start" type="submit" disabled={busy || !name.trim()}>{busy ? 'STARTING…' : '● START RECORDING'}</button>
      </div>
    </form>
}
