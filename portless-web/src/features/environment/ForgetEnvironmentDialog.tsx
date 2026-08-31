import { useRef, type RefObject } from 'react'
import { ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import { FormDialog } from '../../components/overlays/FormDialog'
import type { Environment } from '../../api/contracts/environments'

export function ForgetEnvironmentDialog({ environment, busy, error, restoreFocusRef, onDismissError, onClose, onForget }: {
  environment: Environment
  busy: boolean
  error: ActionErrorDetails | null
  restoreFocusRef: RefObject<HTMLElement | null>
  onDismissError: () => void
  onClose: () => void
  onForget: () => Promise<void>
}) {
  const cancelButton = useRef<HTMLButtonElement>(null)
  const blocked = environment.status !== 'stopped'
  const selector = `${environment.project}/${environment.name}`

  return <FormDialog
    className="forget-environment-modal"
    role="alertdialog"
    titleID="forget-environment-title"
    descriptionID="forget-environment-description"
    closeLabel="Close forget environment"
    closeBlocked={busy}
    initialFocusRef={cancelButton}
    restoreFocusRef={restoreFocusRef}
    header={<div><div className="eyebrow">ENVIRONMENT</div><h2 id="forget-environment-title">Forget {selector}?</h2></div>}
    onClose={onClose}
  >
    <div className="environment-forget-content">
      <p id="forget-environment-description">This permanently removes the environment definition and its retained Portless state. Source files and checkouts on disk are not deleted.</p>
      <div className="environment-forget-impact">
        <div><span className="eyebrow">ENVIRONMENT</span><strong>{environment.name} · {environment.status}</strong></div>
        <div><span className="eyebrow">REMOVED STATE</span><strong>Timeline, traffic, mocks, recordings, faults, and provider bindings</strong></div>
        <div><span className="eyebrow">PRESERVED</span><strong>Source checkouts and managed data volumes</strong></div>
      </div>
      {blocked && <p className="source-modal-note source-modal-note--danger">Stop this environment before forgetting it.</p>}
    </div>
    {error && <ActionErrorNotice error={error} onDismiss={onDismissError} />}
    <footer><button ref={cancelButton} className="button button--quiet" type="button" disabled={busy} onClick={onClose}>CANCEL</button><button className="button button--danger" type="button" disabled={busy || blocked} onClick={() => void onForget()}>{busy ? 'FORGETTING…' : 'FORGET ENVIRONMENT'}</button></footer>
  </FormDialog>
}
