import { useEffect, useMemo, useRef, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { FormDialog } from '../../../components/overlays/FormDialog'
import { StatusMark } from '../../../components/Status'
import { experimentScopes, preferredFaultScope } from '../../experimentScopes'

interface CreateFaultInput {
  name: string
  source: string
  target: string
  probability: number
  latencyMs: number
  statusCode: number
  abort: boolean
  expiresAt?: string
}

export function nextFaultName(faults: FaultRule[]) {
  const existing = new Set(faults.map((fault) => fault.name.toLowerCase()))
  const stem = 'slow-downstream'
  if (!existing.has(stem)) return stem
  let sequence = 2
  while (existing.has(`${stem}-${sequence}`)) sequence++
  return `${stem}-${sequence}`
}

export function FaultsPanel({ environment, faults, refresh }: { environment: Environment; faults: FaultRule[]; refresh: () => Promise<void> }) {
  const [createName, setCreateName] = useState<string | null>(null)
  const [busy, setBusy] = useState('')
  const [deleteName, setDeleteName] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const orderedFaults = [...faults.filter((fault) => fault.enabled), ...faults.filter((fault) => !fault.enabled)]
  const hasActiveFaults = orderedFaults.some((fault) => fault.enabled)

  useEffect(() => {
    setCreateName(null)
    setDeleteName('')
    setError(null)
  }, [environment.project, environment.name])

  const create = async (input: CreateFaultInput) => {
    setBusy('create')
    setError(null)
    try {
      await api(environmentPath(environment, '/faults'), { method: 'POST', ...jsonBody(input) })
      await refresh()
      setCreateName(null)
    } catch (value) {
      setError(actionError("Fault wasn't created", value))
    } finally {
      setBusy('')
    }
  }

  const changeRule = async (fault: FaultRule, action: 'enable' | 'disable') => {
    setBusy(`${action}:${fault.name}`)
    setDeleteName('')
    setError(null)
    try {
      await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}/${action}`), { method: 'POST' })
      await refresh()
    } catch (value) {
      setError(actionError(`Fault wasn't ${action}d`, value))
    } finally {
      setBusy('')
    }
  }

  const remove = async (fault: FaultRule) => {
    if (deleteName !== fault.name) {
      setDeleteName(fault.name)
      setError(null)
      return
    }
    setBusy(`delete:${fault.name}`)
    setError(null)
    try {
      await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}`), { method: 'DELETE' })
      await refresh()
      setDeleteName('')
    } catch (value) {
      setError(actionError("Fault wasn't deleted", value))
    } finally {
      setBusy('')
    }
  }

  const clear = async () => {
    setBusy('disable-all')
    setDeleteName('')
    setError(null)
    try {
      await api(environmentPath(environment, '/faults/disable-all'), { method: 'POST' })
      await refresh()
    } catch (value) {
      setError(actionError("Faults weren't disabled", value))
    } finally {
      setBusy('')
    }
  }

  return <div className="faults-page">
    {!createName && error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-list">
      <div className="panel-title faults-panel-title">
        <span>FAULTS</span>
        <div className="faults-title-actions">
          <button
            className="button button--primary button--small panel-create-button"
            type="button"
            disabled={!!busy}
            onClick={() => { setCreateName(nextFaultName(faults)); setDeleteName(''); setError(null) }}
          >CREATE FAULT</button>
        </div>
      </div>
      {orderedFaults.length > 0 && <div className="faults-bulk-actions">
        <button className="button button--quiet button--small" type="button" disabled={!!busy || !hasActiveFaults} onClick={() => void clear()}>{busy === 'disable-all' ? 'DISABLING…' : 'DISABLE ALL'}</button>
      </div>}
      <div className="fault-table-scroll">
        <table className="fault-table">
          <thead><tr><th>State</th><th>Fault</th><th>Connection / effect</th><th>Lifetime</th><th>Matches</th><th>Actions</th></tr></thead>
          <tbody>
            {orderedFaults.map((fault) => <tr key={fault.name}>
              <td><div className={`fault-table__state${fault.enabled ? ' is-active' : ''}`}><StatusMark status={fault.enabled ? 'degraded' : 'stopped'} label={false} /><span>{fault.enabled ? 'active' : 'disabled'}</span></div></td>
              <td><strong>{fault.name}</strong></td>
              <td className="fault-table__scope" title={fault.scopeSummary}>{fault.scopeSummary}</td>
              <td className="fault-table__lifetime">{faultLifetime(fault)}</td>
              <td>{fault.matchCount}</td>
              <td><div className="table-row-actions">
                <button type="button" disabled={!!busy} onClick={() => void changeRule(fault, fault.enabled ? 'disable' : 'enable')}>
                  {busy === `${fault.enabled ? 'disable' : 'enable'}:${fault.name}` ? fault.enabled ? 'DISABLING…' : 'ENABLING…' : fault.enabled ? 'DISABLE' : 'ENABLE'}
                </button>
                <button
                  className={deleteName === fault.name ? 'is-confirming' : ''}
                  type="button"
                  disabled={!!busy}
                  aria-label={deleteName === fault.name ? `Confirm delete ${fault.name}` : `Delete ${fault.name}`}
                  onClick={() => void remove(fault)}
                >{busy === `delete:${fault.name}` ? 'DELETING…' : deleteName === fault.name ? 'CONFIRM' : 'DELETE'}</button>
              </div></td>
            </tr>)}
            {faults.length === 0 && <tr><td className="fault-table__empty" colSpan={6}>No faults. Create one to simulate latency, errors, or dropped connections.</td></tr>}
          </tbody>
        </table>
      </div>
    </section>

    {createName && <CreateFaultModal
      environment={environment}
      initialName={createName}
      busy={busy === 'create'}
      error={error}
      onDismissError={() => setError(null)}
      onClose={() => { setCreateName(null); setError(null) }}
      onCreate={create}
    />}
  </div>
}

export function CreateFaultModal({ environment, initialName, busy, error, onDismissError, onClose, onCreate }: {
  environment: Environment
  initialName: string
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onCreate: (input: CreateFaultInput) => Promise<void>
}) {
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const [name, setName] = useState(initialName)
  const [scopeID, setScopeID] = useState(preferredFaultScope(environment, scopes)?.id || '')
  const [effect, setEffect] = useState<'latency' | 'status' | 'abort'>('latency')
  const [value, setValue] = useState('2000')
  const [expiryMinutes, setExpiryMinutes] = useState('')
  const nameInput = useRef<HTMLInputElement>(null)
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  const chooseEffect = (next: typeof effect) => {
    setEffect(next)
    setValue(next === 'latency' ? '2000' : next === 'status' ? '503' : '')
    onDismissError()
  }

  return <FormDialog
    className="fault-form-modal"
    titleID="create-fault-title"
    descriptionID="create-fault-description"
    closeLabel="Close create fault"
    closeBlocked={busy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">FAILURE INJECTION</div><h2 id="create-fault-title">Create Fault</h2></div>}
    onClose={onClose}
  >
    <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => {
      event.preventDefault()
      if (busy || !name.trim() || !selectedScope) return
      void onCreate({
        name: name.trim(),
        source: selectedScope.source,
        target: selectedScope.target,
        probability: 1,
        latencyMs: effect === 'latency' ? Number(value) : 0,
        statusCode: effect === 'status' ? Number(value) : 0,
        abort: effect === 'abort',
        ...(expiryMinutes ? { expiresAt: new Date(Date.now() + Number(expiryMinutes) * 60_000).toISOString() } : {}),
      })
    }}>
      <p id="create-fault-description">Introduce a controlled failure while testing this environment.</p>
      <div className="form-modal__fields">
        <label><span>NAME</span><input ref={nameInput} name="portless-fault-name" required autoComplete="off" spellCheck="false" value={name} disabled={busy} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => { setName(event.target.value); onDismissError() }} /></label>
        <label><span>CONNECTION</span><select aria-label="Fault connection" value={scopeID} disabled={busy} onChange={(event) => { setScopeID(event.target.value); onDismissError() }}>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label>
        <fieldset className="fault-effect provider-field--wide">
          <legend>EFFECT</legend>
          <div className="segmented">{(['latency', 'status', 'abort'] as const).map((item) => <button type="button" key={item} className={effect === item ? 'is-active' : ''} aria-pressed={effect === item} disabled={busy} onClick={() => chooseEffect(item)}>{item}</button>)}</div>
        </fieldset>
        {effect !== 'abort' && <label><span>{effect === 'latency' ? 'MILLISECONDS' : 'HTTP STATUS'}</span><input type="number" required value={value} disabled={busy} onChange={(event) => { setValue(event.target.value); onDismissError() }} /></label>}
        <label className={effect === 'abort' ? 'provider-field--wide' : ''}><span>AUTOMATIC DISABLE</span><select value={expiryMinutes} disabled={busy} onChange={(event) => { setExpiryMinutes(event.target.value); onDismissError() }}><option value="">Until manually disabled</option><option value="10">After 10 minutes</option><option value="30">After 30 minutes</option><option value="60">After 1 hour</option><option value="240">After 4 hours</option></select></label>
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={busy || !name.trim() || !selectedScope}>{busy ? 'CREATING…' : 'CREATE FAULT'}</button></footer>
    </form>
  </FormDialog>
}

function faultLifetime(fault: FaultRule) {
  if (!fault.enabled) return 'disabled'
  if (!fault.expiresAt) return 'until disabled'
  return `expires ${new Date(fault.expiresAt).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}`
}
