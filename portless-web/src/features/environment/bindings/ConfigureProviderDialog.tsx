import { useRef, useState } from 'react'
import { api, environmentPath } from '../../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../../components/ActionError'
import { FormDialog } from '../../../components/overlays/FormDialog'
import type { Environment, Operation } from '../../../api/contracts/environments'
import type { Project } from '../../../api/contracts/projects'
import type { ComponentBinding, ProviderKind, RemoteClassification, WritePolicy } from '../../../api/contracts/topology'
import { waitForEnvironmentOperation } from '../operationPolling'
import { bindingFor } from '../service/servicePresentation'
import { defaultProviderBinding, providerBindingMatches, providerDisplayName } from './bindingPresentation'

export function ConfigureProviderDialog({ environment, project, initialService, onChanged, onClose }: {
  environment: Environment
  project?: Project
  initialService?: string
  onChanged: () => void | Promise<void>
  onClose: () => void
}) {
  const initialServiceName = initialService || environment.services[0]?.name || ''
  const initialTarget = environment.services.find((item) => item.name === initialServiceName)
  const initialBinding = bindingFor(environment, initialServiceName)
  const [service, setService] = useState(initialServiceName)
  const [provider, setProvider] = useState<ProviderKind>(initialBinding?.provider || (initialTarget?.kind === 'resource' ? 'container' : 'local'))
  const [source, setSource] = useState(initialBinding?.source || environment.sources?.[0]?.name || '')
  const [remoteURL, setRemoteURL] = useState(initialBinding?.remote?.url || '')
  const [classification, setClassification] = useState<RemoteClassification>(initialBinding?.remote?.classification || 'qa')
  const [writePolicy, setWritePolicy] = useState<WritePolicy>(initialBinding?.remote?.writePolicy || 'read-only')
  const [healthPath, setHealthPath] = useState(initialBinding?.remote?.healthPath || '/health')
  const [busyAction, setBusyAction] = useState<'save' | 'reset' | ''>('')
  const [saveError, setSaveError] = useState<ActionErrorDetails | null>(null)
  const serviceSelect = useRef<HTMLSelectElement>(null)
  const providerSelect = useRef<HTMLSelectElement>(null)
  const serviceLocked = !!initialService
  const selected = environment.services.find((item) => item.name === service)
	const currentBinding = bindingFor(environment, service)
	const scenarioOwned = currentBinding?.provider === 'mock'
  const defaultBinding = selected ? defaultProviderBinding(project, environment, selected) : undefined
  const resetAvailable = !!currentBinding && !!defaultBinding && !providerBindingMatches(currentBinding, defaultBinding)
  const busy = busyAction !== ''
  const transitionBlocked = ['starting', 'stopping', 'recovering', 'unknown'].includes(environment.status)
  const providerUnchanged = !!currentBinding && currentBinding.provider === provider && (
    provider === 'container' ||
    (provider === 'local' && currentBinding.source?.toLowerCase() === source.toLowerCase()) ||
		(provider === 'remote' && currentBinding.remote?.url === remoteURL && currentBinding.remote?.classification === classification && currentBinding.remote?.writePolicy === writePolicy && (currentBinding.remote?.healthPath || '') === healthPath)
  )

  const initializeProviderForm = (serviceName: string) => {
    const target = environment.services.find((item) => item.name === serviceName)
    const current = bindingFor(environment, serviceName)
    setService(serviceName)
    setProvider(current?.provider || (target?.kind === 'resource' ? 'container' : 'local'))
    setSource(current?.source || environment.sources?.[0]?.name || '')
    setRemoteURL(current?.remote?.url || '')
    setClassification(current?.remote?.classification || 'qa')
    setWritePolicy(current?.remote?.writePolicy || 'read-only')
    setHealthPath(current?.remote?.healthPath || '/health')
  }

  const bind = async () => {
    setBusyAction('save')
    setSaveError(null)
    try {
      const binding: ComponentBinding = { service, provider }
      if (provider === 'local') binding.source = source
      if (provider === 'remote') binding.remote = { url: remoteURL, classification, writePolicy, healthPath }
      const operation = await api<Operation>(environmentPath(environment, `/bindings/${encodeURIComponent(service)}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
        body: JSON.stringify(binding),
      })
      const completed = await waitForEnvironmentOperation(environment, operation)
      if (completed.state !== 'succeeded') throw new Error(completed.error || `Provider change ${completed.state}`)
      await onChanged()
      onClose()
    } catch (reason) {
      setSaveError(actionError("Provider wasn't updated", reason))
    } finally {
      setBusyAction('')
    }
  }

  const reset = async () => {
    if (!defaultBinding) return
    setBusyAction('reset')
    setSaveError(null)
    try {
      const operation = await api<Operation>(environmentPath(environment, `/bindings/${encodeURIComponent(service)}`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
        body: JSON.stringify(defaultBinding),
      })
      const completed = await waitForEnvironmentOperation(environment, operation)
      if (completed.state !== 'succeeded') throw new Error(completed.error || `Provider reset ${completed.state}`)
      await onChanged()
      onClose()
    } catch (reason) {
      setSaveError(actionError("Provider wasn't reset", reason))
    } finally {
      setBusyAction('')
    }
  }

  return <FormDialog
    className="configure-provider-modal"
    titleID="configure-provider-title"
    descriptionID="configure-provider-description"
    closeLabel="Close configure provider"
    closeBlocked={busy}
    initialFocusRef={serviceLocked ? providerSelect : serviceSelect}
    header={<div><div className="eyebrow">PROVIDER BINDING</div><h2 id="configure-provider-title">Configure Provider</h2></div>}
    onClose={onClose}
  >
	<form onSubmit={(event) => { event.preventDefault(); if (!scenarioOwned) void bind() }}>
      <p id="configure-provider-description">Choose how Portless should run or route this service in this environment.</p>
      <div className="form-modal__fields configure-provider-form__fields">
        {serviceLocked ? <div className="provider-service-value"><span>SERVICE</span><strong>{service}</strong></div> : <label><span>SERVICE</span><select ref={serviceSelect} aria-label="Service" value={service} disabled={busy} onChange={(event) => { initializeProviderForm(event.target.value); setSaveError(null) }}>{environment.services.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>}
		{scenarioOwned ? <div className="provider-service-value"><span>PROVIDER</span><strong>Mock scenario</strong></div> : <label><span>PROVIDER</span><select ref={providerSelect} aria-label="Provider" value={provider} disabled={busy} onChange={(event) => { setProvider(event.target.value as ProviderKind); setSaveError(null) }}>{selected?.kind === 'process' && <option value="local">{providerDisplayName('local')}</option>}{selected?.kind === 'resource' && <option value="container">{providerDisplayName('container')}</option>}{selected?.kind === 'process' && <option value="remote">{providerDisplayName('remote')}</option>}</select></label>}
        {provider === 'local' && (environment.sources?.length ? <label className="provider-field--wide"><span>SOURCE CHECKOUT</span><select aria-label="Source checkout" value={source} disabled={busy} onChange={(event) => { setSource(event.target.value); setSaveError(null) }}>{environment.sources.map((item) => <option key={item.name}>{item.name}</option>)}</select></label> : <ProviderInfoCard kind="checkout" title="CHECKOUT REQUIRED" description="Configure a checkout below before using the Checkout provider for this service." />)}
        {provider === 'remote' && <>
          <label className="provider-field--wide"><span>REMOTE URL</span><input aria-label="Remote URL" type="url" placeholder="https://payments.qa.example.com" value={remoteURL} disabled={busy} onChange={(event) => { setRemoteURL(event.target.value); setSaveError(null) }} /></label>
          <label><span>CLASSIFICATION</span><select aria-label="Classification" value={classification} disabled={busy} onChange={(event) => { setClassification(event.target.value as RemoteClassification); setSaveError(null) }}><option value="development">development</option><option value="qa">qa</option><option value="staging">staging</option><option value="unknown">unknown</option></select></label>
          <label><span>WRITE POLICY</span><select aria-label="Write policy" value={writePolicy} disabled={busy} onChange={(event) => { setWritePolicy(event.target.value as WritePolicy); setSaveError(null) }}><option value="read-only">read-only</option><option value="read-write">read-write</option></select></label>
          <label className="provider-field--wide"><span>HEALTH PATH</span><input aria-label="Health path" value={healthPath} disabled={busy} onChange={(event) => { setHealthPath(event.target.value); setSaveError(null) }} placeholder="/health" /></label>
          <ProviderInfoCard kind="remote" title="REMOTE BOUNDARY" description="Traffic still passes through Portless, so recordings and faults remain available. A read-only binding blocks POST, PUT, PATCH, and DELETE before they leave this machine." />
        </>}
		{scenarioOwned && <ProviderInfoCard kind="scenario" title={`SCENARIO / ${currentBinding.mock?.scenario || 'UNKNOWN'}`} description="This provider is controlled by a mock scenario. Disable that scenario from Mocks before configuring this service directly." />}
        {transitionBlocked && <small className="provider-stop-note provider-field--wide">Wait for the environment to finish {environment.status} before changing a provider.</small>}
      </div>
      {saveError && <ActionErrorNotice error={saveError} onDismiss={() => setSaveError(null)} />}
	  <footer>{!scenarioOwned && resetAvailable && <button className="button button--quiet provider-reset-button" type="button" disabled={busy || transitionBlocked} onClick={() => void reset()}>{busyAction === 'reset' ? 'RESETTING…' : 'RESET TO DEFAULT'}</button>}<button className="button button--quiet" type="button" disabled={busy} onClick={onClose}>{scenarioOwned ? 'CLOSE' : 'CANCEL'}</button>{!scenarioOwned && <button className={provider === 'remote' ? 'button button--warning' : 'button button--primary'} type="submit" disabled={busy || transitionBlocked || providerUnchanged || !service || (provider === 'remote' && !remoteURL) || (provider === 'local' && !source)}>{busyAction === 'save' ? 'SWITCHING…' : environment.status === 'stopped' ? 'SAVE CHANGES' : 'SWITCH PROVIDER'}</button>}</footer>
    </form>
  </FormDialog>
}

function ProviderInfoCard({ kind, title, description }: { kind: 'checkout' | 'remote' | 'scenario'; title: string; description: string }) {
  return <aside className={`provider-info-card provider-info-card--${kind} provider-field--wide`} role="note">
    <span className="provider-info-card__icon" aria-hidden="true">
		{kind === 'remote' ? <svg viewBox="0 0 24 24"><path d="M5 18h7a3 3 0 0 0 3-3v-3" /><path d="M11 6h7v7" /><path d="m10 14 8-8" /></svg> : kind === 'scenario' ? <svg viewBox="0 0 24 24"><path d="M5 8h14v10H5z" /><path d="m9 11-2 2 2 2" /><path d="m15 11 2 2-2 2" /></svg> : <svg viewBox="0 0 24 24"><path d="M4 7h6l2 2h8v10H4z" /><path d="M4 7v12" /></svg>}
    </span>
    <div><strong>{title}</strong><p>{description}</p></div>
  </aside>
}
