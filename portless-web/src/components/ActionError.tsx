import { APIError } from '../api'
import type { APIErrorShape } from '../api/contracts/errors'

export interface ActionErrorDetails {
  title: string
  message: string
  code?: string
  remediation?: APIErrorShape['remediation']
}

export function actionError(title: string, value: unknown): ActionErrorDetails {
  if (value instanceof APIError) {
    return { title, message: value.message, code: value.code, remediation: value.remediation }
  }
  if (value instanceof Error) return { title, message: value.message }
  return { title, message: String(value || 'The request could not be completed.') }
}

export function ActionErrorNotice({ error, onDismiss }: { error: ActionErrorDetails; onDismiss: () => void }) {
  return <section className="action-error" role="alert">
    <span className="action-error__mark" aria-hidden="true">!</span>
    <div className="action-error__body">
      <div className="action-error__heading">
        <strong>{error.title}</strong>
        {error.code && <code>{error.code}</code>}
      </div>
      <p>{error.message}</p>
      {!!error.remediation?.length && <ul className="action-error__remediation">
        {error.remediation.map((item, index) => <li key={`${item.label}-${index}`}>
          {item.url ? <a href={item.url}>{item.label}</a> : <span>{item.label}</span>}
          {item.command && <code>{item.command}</code>}
        </li>)}
      </ul>}
    </div>
    <button className="action-error__dismiss" type="button" aria-label="Dismiss error" title="Dismiss" onClick={onDismiss}>×</button>
  </section>
}
