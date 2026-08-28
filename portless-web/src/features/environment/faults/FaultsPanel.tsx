import { useEffect, useMemo, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { StatusMark } from '../../../components/Status'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import { experimentScopes, preferredFaultScope } from '../../experimentScopes'

export function FaultsPanel({ environment, faults, refresh }: { environment: Environment; faults: FaultRule[]; refresh: () => Promise<void> }) {
  const [name, setName] = useState('slow-downstream')
  const scopes = useMemo(() => experimentScopes(environment), [environment])
  const initialScope = preferredFaultScope(environment, scopes)?.id || ''
  const [scopeID, setScopeID] = useState(initialScope)
  const [effect, setEffect] = useState<'latency' | 'status' | 'abort'>('latency')
  const [value, setValue] = useState('2000')
  const [expiryMinutes, setExpiryMinutes] = useState('')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const selectedScope = scopes.find((scope) => scope.id === scopeID)

  useEffect(() => {
    setScopeID(preferredFaultScope(environment, scopes)?.id || '')
    setError(null)
  }, [environment.project, environment.name]) // eslint-disable-line react-hooks/exhaustive-deps

  const create = async () => {
    setError(null)
    const faultName = name.trim()
    if (!faultName) {
      setError(actionError("Fault wasn't enabled", 'Enter a fault name.'))
      return
    }
    if (!selectedScope) {
      setError(actionError("Fault wasn't enabled", 'No configurable connection is available in this environment.'))
      return
    }
    const body = {
      name: faultName,
      source: selectedScope.source,
      target: selectedScope.target,
      probability: 1,
      latencyMs: effect === 'latency' ? Number(value) : 0,
      statusCode: effect === 'status' ? Number(value) : 0,
      abort: effect === 'abort',
      ...(expiryMinutes ? { expiresAt: new Date(Date.now() + Number(expiryMinutes) * 60_000).toISOString() } : {}),
    }
    try {
      await api(environmentPath(environment, '/faults'), { method: 'POST', ...jsonBody(body) })
      await refresh()
    } catch (reason) {
      setError(actionError("Fault wasn't enabled", reason))
    }
  }

  const changeRule = async (fault: FaultRule, action: 'enable' | 'disable') => {
    setError(null)
    try {
      await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}/${action}`), { method: 'POST' })
      await refresh()
    } catch (value) {
      setError(actionError(`Fault wasn't ${action}d`, value))
    }
  }

  const remove = async (fault: FaultRule) => {
    setError(null)
    try {
      await api(environmentPath(environment, `/faults/${encodeURIComponent(fault.name)}`), { method: 'DELETE' })
      await refresh()
    } catch (value) {
      setError(actionError("Fault wasn't deleted", value))
    }
  }

  const clear = async () => {
    setError(null)
    try {
      await api(environmentPath(environment, '/faults/disable-all'), { method: 'POST' })
      await refresh()
    } catch (value) {
      setError(actionError("Faults weren't disabled", value))
    }
  }

  const hasActiveFaults = faults.some((fault) => fault.enabled)
  return <div className="experiment-layout">
    {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
    <section className="panel experiment-form"><div className="panel-title"><span>INTRODUCE FAILURE</span></div><label><span>NAME</span><input value={name} onChange={(event) => { setName(event.target.value); setError(null) }} /></label><label><span>CONNECTION</span><select aria-label="Fault connection" value={scopeID} onChange={(event) => { setScopeID(event.target.value); setError(null) }}>{scopes.map((scope) => <option value={scope.id} key={scope.id}>{scope.label}</option>)}</select></label><div className="segmented">{(['latency', 'status', 'abort'] as const).map((item) => <button key={item} className={effect === item ? 'is-active' : ''} onClick={() => { setEffect(item); setValue(item === 'latency' ? '2000' : item === 'status' ? '503' : ''); setError(null) }}>{item}</button>)}</div>{effect !== 'abort' && <label><span>{effect === 'latency' ? 'MILLISECONDS' : 'HTTP STATUS'}</span><input type="number" value={value} onChange={(event) => { setValue(event.target.value); setError(null) }} /></label>}<label><span>AUTOMATIC DISABLE</span><select value={expiryMinutes} onChange={(event) => { setExpiryMinutes(event.target.value); setError(null) }}><option value="">Until manually disabled</option><option value="10">After 10 minutes</option><option value="30">After 30 minutes</option><option value="60">After 1 hour</option><option value="240">After 4 hours</option></select></label><button className="button button--warning" disabled={!selectedScope} onClick={create}>ENABLE FAULT</button></section>
    <section className="panel experiment-list"><div className="panel-title"><span>FAULT RULES</span><button disabled={!hasActiveFaults} onClick={clear}>DISABLE ALL</button></div>{faults.map((fault) => <div className={`experiment-row ${fault.enabled ? 'is-warning' : ''}`} key={fault.name}><StatusMark status={fault.enabled ? 'degraded' : 'stopped'} label={false} /><div><strong>{fault.name}</strong><small>{fault.scopeSummary}</small><small className="fault-lifetime">{faultLifetime(fault)}</small></div><span>{fault.matchCount} matches</span><div><button onClick={() => changeRule(fault, fault.enabled ? 'disable' : 'enable')}>{fault.enabled ? 'DISABLE' : 'ENABLE'}</button><button onClick={() => remove(fault)}>DELETE</button></div></div>)}{faults.length === 0 && <div className="empty-row">No fault rules have been created.</div>}</section>
  </div>
}

function faultLifetime(fault: FaultRule) {
  if (!fault.enabled) return 'disabled'
  if (!fault.expiresAt) return 'active until disabled'
  return `expires ${new Date(fault.expiresAt).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}`
}
