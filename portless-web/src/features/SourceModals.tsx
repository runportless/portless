import { useEffect, useRef, useState, type RefObject } from 'react'
import { api, jsonBody } from '../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import { FormDialog } from '../components/overlays/FormDialog'
import type { Environment, SourceBinding } from '../api/contracts/environments'
import type { Project, ProjectSource } from '../api/contracts/projects'
import type { DirectorySelection } from '../api/contracts/system'

async function chooseSourceDirectory(initialPath: string) {
  return api<DirectorySelection | undefined>('/system/directories/select', {
    method: 'POST', ...jsonBody({ initialPath: initialPath.trim() }),
  })
}

export function AddProjectSourceModal({ project, environments, busy, error, onDismissError, onClose, onAdd }: {
  project: Project
  environments: Environment[]
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onAdd: (name: string, path: string, environment: string) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [path, setPath] = useState('')
  const [environment, setEnvironment] = useState(environments[0]?.name || '')
  const [choosingPath, setChoosingPath] = useState(false)
  const [pickerError, setPickerError] = useState<ActionErrorDetails | null>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const pathInput = useRef<HTMLInputElement>(null)
  const activeEnvironments = environments.filter((item) => item.status !== 'stopped')
  const modalBusy = busy || choosingPath
  const blocked = activeEnvironments.length > 0 || !environment
  const dismissErrors = () => { setPickerError(null); onDismissError() }
  const browse = async () => {
    setChoosingPath(true)
    dismissErrors()
    try {
      const selection = await chooseSourceDirectory(path)
      if (selection?.path) setPath(selection.path)
    } catch (value) {
      setPickerError(actionError('Could not choose a source directory', value))
    } finally {
      setChoosingPath(false)
      requestAnimationFrame(() => pathInput.current?.focus())
    }
  }
  const visibleError = pickerError || error
  return <FormDialog
    className="add-source-modal"
    titleID="add-source-title"
    descriptionID="add-source-description"
    closeLabel="Close add source"
    closeBlocked={modalBusy}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">PROJECT SOURCE</div><h2 id="add-source-title">Add source</h2></div>}
    onClose={onClose}
  >
      <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); if (!modalBusy && !blocked) void onAdd(name.trim(), path.trim(), environment) }}>
        <p id="add-source-description">Discover this repository and add its services to {project.name}. The checkout path will only be configured for the selected environment.</p>
        <div className="form-modal__fields">
          <label><span>NAME</span><input ref={nameInput} name="portless-project-source-name" value={name} placeholder="inventory" required autoComplete="off" spellCheck="false" disabled={modalBusy} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => { setName(event.target.value); dismissErrors() }} /></label>
          <label><span>INITIAL ENVIRONMENT</span><select aria-label="Initial environment" value={environment} disabled={modalBusy} onChange={(event) => { setEnvironment(event.target.value); dismissErrors() }}>{environments.map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
          <div className="source-path-field">
            <label htmlFor="portless-add-source-path">INITIAL CHECKOUT PATH</label>
            <div className="source-path-control">
              <input ref={pathInput} id="portless-add-source-path" name="portless-project-source-path" value={path} placeholder="/Users/you/workspace/inventory" required autoComplete="off" spellCheck="false" disabled={modalBusy} onChange={(event) => { setPath(event.target.value); dismissErrors() }} />
              <button className="button button--quiet source-path-browse" type="button" disabled={modalBusy} onClick={() => void browse()}>{choosingPath ? 'CHOOSING…' : 'BROWSE…'}</button>
            </div>
          </div>
          {activeEnvironments.length > 0 && <aside className="source-modal-warning" role="note"><span className="source-modal-warning__mark" aria-hidden="true">!</span><div><strong>STOP REQUIRED</strong><p>Stop every project environment first: {activeEnvironments.map((item) => item.name).join(', ')}.</p></div></aside>}
        </div>
        {visibleError && <ActionErrorNotice error={visibleError} onDismiss={dismissErrors} />}
        <footer><button className="button button--quiet" type="button" disabled={modalBusy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={modalBusy || blocked || !name.trim() || !path.trim()}>{busy ? 'ADDING…' : 'ADD SOURCE'}</button></footer>
      </form>
  </FormDialog>
}

export function ConfigureCheckoutModal({ environment, source, checkout, busy, error, onDismissError, onClose, onSave }: {
  environment: Environment
  source: ProjectSource
  checkout?: SourceBinding
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onSave: (path: string) => Promise<void>
}) {
  const [path, setPath] = useState(checkout?.path || '')
  const [choosingPath, setChoosingPath] = useState(false)
  const [pickerError, setPickerError] = useState<ActionErrorDetails | null>(null)
  const pathInput = useRef<HTMLInputElement>(null)
  const modalBusy = busy || choosingPath
  const dismissErrors = () => { setPickerError(null); onDismissError() }
  const browse = async () => {
    setChoosingPath(true)
    dismissErrors()
    try {
      const selection = await chooseSourceDirectory(path)
      if (selection?.path) setPath(selection.path)
    } catch (value) {
      setPickerError(actionError('Could not choose a source directory', value))
    } finally {
      setChoosingPath(false)
      requestAnimationFrame(() => pathInput.current?.focus())
    }
  }
  useEffect(() => {
    if (!checkout) return
    const frame = window.requestAnimationFrame(() => pathInput.current?.select())
    return () => window.cancelAnimationFrame(frame)
  }, [checkout])
  const stopped = environment.status === 'stopped'
  const visibleError = pickerError || error
  const editing = !!checkout
  return <FormDialog
    className="edit-source-modal"
    titleID="edit-checkout-title"
    descriptionID="edit-checkout-description"
    closeLabel="Close checkout configuration"
    closeBlocked={modalBusy}
    initialFocusRef={pathInput}
    header={<div><div className="eyebrow">ENVIRONMENT CHECKOUT</div><h2 id="edit-checkout-title">{editing ? 'Edit' : 'Configure'} {source.name}</h2></div>}
    onClose={onClose}
  >
      <form autoComplete="off" onSubmit={(event) => { event.preventDefault(); if (!modalBusy && stopped) void onSave(path.trim()) }}>
        <p id="edit-checkout-description">Choose the directory {environment.project}/{environment.name} should use for {source.name}. The project source and every other environment stay unchanged.</p>
        <div className="form-modal__fields source-path-fields">
          <div className="source-path-field">
            <label htmlFor="portless-edit-source-path">CHECKOUT PATH</label>
            <div className="source-path-control">
              <input ref={pathInput} id="portless-edit-source-path" name="portless-source-checkout-path" value={path} placeholder="/Users/you/workspace/inventory" required autoComplete="off" spellCheck="false" disabled={modalBusy} onChange={(event) => { setPath(event.target.value); dismissErrors() }} />
              <button className="button button--quiet source-path-browse" type="button" disabled={modalBusy} onClick={() => void browse()}>{choosingPath ? 'CHOOSING…' : 'BROWSE…'}</button>
            </div>
          </div>
          {!stopped && <aside className="source-modal-warning" role="note"><span className="source-modal-warning__mark" aria-hidden="true">!</span><div><strong>STOP REQUIRED</strong><p>Stop this environment before changing a source checkout.</p></div></aside>}
        </div>
        {visibleError && <ActionErrorNotice error={visibleError} onDismiss={dismissErrors} />}
        <footer><button className="button button--quiet" type="button" disabled={modalBusy} onClick={onClose}>CANCEL</button><button className="button button--primary" type="submit" disabled={modalBusy || !stopped || !path.trim() || path.trim() === checkout?.path}>{busy ? 'SAVING…' : editing ? 'SAVE CHANGES' : 'SAVE CHECKOUT'}</button></footer>
      </form>
  </FormDialog>
}

export function DeleteProjectSourceModal({ project, source, environments, busy, error, restoreFocusRef, onDismissError, onClose, onDelete }: {
  project: Project
  source: ProjectSource
  environments: Environment[]
  busy: boolean
  error: ActionErrorDetails | null
  restoreFocusRef?: RefObject<HTMLElement | null>
  onDismissError: () => void
  onClose: () => void
  onDelete: () => Promise<void>
}) {
  const cancelButton = useRef<HTMLButtonElement>(null)
  const ownedServices = source.services || []
  const ownedServiceNames = new Set(ownedServices.map((name) => name.toLowerCase()))
  const affectedConnections = project.connections?.filter((connection) => ownedServiceNames.has(connection.source.toLowerCase()) || ownedServiceNames.has(connection.target.toLowerCase())) || []
  const activeEnvironments = environments.filter((item) => item.status !== 'stopped')
  const lastSource = (project.sources?.length || 0) <= 1
  const blocked = lastSource || activeEnvironments.length > 0
  return <FormDialog
    className="delete-source-modal"
    role="alertdialog"
    titleID="delete-source-title"
    descriptionID="delete-source-description"
    closeLabel="Close delete source"
    closeBlocked={busy}
    initialFocusRef={cancelButton}
    restoreFocusRef={restoreFocusRef}
    header={<div><div className="eyebrow">PROJECT TOPOLOGY</div><h2 id="delete-source-title">Delete {source.name}?</h2></div>}
    onClose={onClose}
  >
      <div className="source-delete-content">
        <p id="delete-source-description">This removes the source from every environment in {project.name}. Services owned by it and resources used only by those services are also removed.</p>
        <div className="source-delete-impact"><div><span className="eyebrow">SERVICES REMOVED</span><strong>{ownedServices.length ? ownedServices.join(', ') : 'No services were discovered for this source'}</strong></div>{affectedConnections.length > 0 && <div><span className="eyebrow">CONNECTIONS REMOVED</span><strong>{affectedConnections.map((connection) => `${connection.source} → ${connection.target}`).join(', ')}</strong></div>}</div>
        {lastSource && <p className="source-modal-note source-modal-note--danger">A project must retain at least one source.</p>}
        {activeEnvironments.length > 0 && <p className="source-modal-note source-modal-note--danger">Stop every environment first: {activeEnvironments.map((item) => item.name).join(', ')}.</p>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button ref={cancelButton} className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--danger" type="button" disabled={busy || blocked} onClick={() => void onDelete()}>{busy ? 'DELETING…' : 'DELETE SOURCE'}</button></footer>
  </FormDialog>
}

export function RemoveCheckoutModal({ environment, source, usedBy, busy, error, onDismissError, onClose, onRemove }: {
  environment: Environment
  source: ProjectSource
  usedBy: string[]
  busy: boolean
  error: ActionErrorDetails | null
  onDismissError: () => void
  onClose: () => void
  onRemove: () => Promise<void>
}) {
  const cancelButton = useRef<HTMLButtonElement>(null)
  const stopped = environment.status === 'stopped'
  const blocked = !stopped || usedBy.length > 0
  return <FormDialog
    className="delete-source-modal"
    role="alertdialog"
    titleID="remove-checkout-title"
    descriptionID="remove-checkout-description"
    closeLabel="Close remove checkout"
    closeBlocked={busy}
    initialFocusRef={cancelButton}
    header={<div><div className="eyebrow">ENVIRONMENT CHECKOUT</div><h2 id="remove-checkout-title">Remove {source.name} checkout?</h2></div>}
    onClose={onClose}
  >
      <div className="source-delete-content">
        <p id="remove-checkout-description">Remove the local checkout from {environment.project}/{environment.name}. The project source, its services, and every other environment stay unchanged.</p>
        {usedBy.length > 0 && <p className="source-modal-note source-modal-note--danger">Switch these services away from the Checkout provider first: {usedBy.join(', ')}.</p>}
        {!stopped && <p className="source-modal-note source-modal-note--danger">Stop this environment before removing its checkout.</p>}
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
      <footer><button ref={cancelButton} className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--danger" type="button" disabled={busy || blocked} onClick={() => void onRemove()}>{busy ? 'REMOVING…' : 'REMOVE CHECKOUT'}</button></footer>
  </FormDialog>
}
