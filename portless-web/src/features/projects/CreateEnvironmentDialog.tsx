import { useRef, useState } from 'react'
import { api, jsonBody } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { FormDialog } from '../../components/overlays/FormDialog'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { environmentRoute } from './projectOperations'

export function CreateEnvironmentDialog({ project, environments, initialCloneFrom, onClose, onNavigate, onChanged }: {
  project: Project
  environments: Environment[]
  initialCloneFrom?: string
  onClose: () => void
  onNavigate: (path: string) => void
  onChanged: () => Promise<void>
}) {
  const [name, setName] = useState('')
  const [cloneFrom, setCloneFrom] = useState(() => environments.some((environment) => environment.name === initialCloneFrom)
    ? initialCloneFrom ?? ''
    : environments.some((environment) => environment.name === 'local') ? 'local' : environments[0]?.name ?? '')
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [creating, setCreating] = useState(false)
  const nameInput = useRef<HTMLInputElement>(null)
  const close = () => {
    if (creating) return
    setError(null)
    onClose()
  }
  const create = async () => {
    const trimmedName = name.trim()
    if (!trimmedName) {
      setError(actionError("Environment wasn't created", new Error('Enter an environment name.')))
      nameInput.current?.focus()
      return
    }
    if (!cloneFrom) {
      setError(actionError("Environment wasn't created", new Error('Choose an environment to clone.')))
      return
    }
    setError(null)
    setCreating(true)
    try {
      await api('/environments', { method: 'POST', ...jsonBody({ project: project.name, name: trimmedName, from: cloneFrom }) })
      await onChanged()
      onClose()
      onNavigate(environmentRoute({ project: project.name, name: trimmedName }))
    } catch (reason) {
      setError(actionError("Environment wasn't created", reason))
    } finally {
      setCreating(false)
    }
  }
  return <FormDialog
    className="create-environment-modal"
    titleID="create-environment-title"
    descriptionID="create-environment-description"
    closeLabel="Close create environment"
    closeBlocked={creating}
    initialFocusRef={nameInput}
    header={<div><div className="eyebrow">NEW ENVIRONMENT</div><h2 id="create-environment-title">Create Environment</h2></div>}
    onClose={close}
  >
    <form autoComplete="off" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onSubmit={(event) => { event.preventDefault(); void create() }}>
      <p id="create-environment-description">Clone providers and source bindings, then customize the result.</p>
      <div className="form-modal__fields create-environment-form__fields">
        <label><span>NAME</span><input ref={nameInput} name="portless-environment-name" value={name} placeholder="qa-local" required autoComplete="off" spellCheck="false" disabled={creating} data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-protonpass-ignore="true" data-keeper-ignore="true" data-form-type="other" onChange={(event) => setName(event.target.value)} /></label>
        <label><span>CLONE FROM</span><select value={cloneFrom} disabled={creating} onChange={(event) => setCloneFrom(event.target.value)}>{environments.map((environment) => <option key={environment.name}>{environment.name}</option>)}</select></label>
      </div>
      {error && <ActionErrorNotice error={error} onDismiss={() => setError(null)} />}
      <footer><button className="button button--quiet" type="button" disabled={creating} onClick={close}>CANCEL</button><button className="button button--primary" type="submit" disabled={creating || !name.trim() || !cloneFrom}>{creating ? 'CREATING…' : 'CREATE ENVIRONMENT'}</button></footer>
    </form>
  </FormDialog>
}
