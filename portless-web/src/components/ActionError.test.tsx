import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { APIError } from '../api'
import { actionError, ActionErrorNotice } from './ActionError'

describe('action error notice', () => {
  it('preserves structured API details in a compact alert', () => {
    const error = actionError("Recording wasn't started", new APIError(409, {
      code: 'CONNECTION_NOT_ACCEPTED',
      message: 'The selected connection is not present in the accepted project model.',
      remediation: [{ label: 'Review bindings', url: '/environments/billing/local/bindings' }],
    }))
    const markup = renderToStaticMarkup(<ActionErrorNotice error={error} onDismiss={() => undefined} />)

    expect(markup).toContain('class="action-error" role="alert"')
    expect(markup).toContain("Recording wasn&#x27;t started")
    expect(markup).toContain('CONNECTION_NOT_ACCEPTED')
    expect(markup).toContain('The selected connection is not present in the accepted project model.')
    expect(markup).toContain('href="/environments/billing/local/bindings"')
    expect(markup).toContain('aria-label="Dismiss error"')
  })

  it('turns ordinary failures into user-facing details', () => {
    expect(actionError("Fault wasn't enabled", new Error('Choose a connection.'))).toEqual({
      title: "Fault wasn't enabled",
      message: 'Choose a connection.',
    })
  })
})
