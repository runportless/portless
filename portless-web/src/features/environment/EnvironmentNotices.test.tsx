import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import { EnvironmentNotices } from './EnvironmentNotices'

const environment: Environment = { project: 'store', name: 'qa-local', status: 'failed', revision: 1, createdAt: '', updatedAt: '', services: [], connections: [] }
const actions = { error: null, dismissError: () => undefined, trackingInterrupted: false, resumeTracking: () => undefined }
const activity = { error: null, dismissError: () => undefined }

describe('environment error notices', () => {
  it.each(['failed', 'degraded', 'unknown'] as const)('uses the shared red notice for a persisted %s reason', (status) => {
    const markup = renderToStaticMarkup(<EnvironmentNotices environment={{ ...environment, status, reason: 'Orders runtime could not be verified.' }} actions={actions} activity={activity} />)
    expect(markup).toContain('class="action-error" role="alert"')
    expect(markup).toContain('Orders runtime could not be verified.')
    expect(markup).toContain('aria-label="Dismiss error"')
    expect(markup).not.toContain('environment-status-reason')
    expect(markup).not.toContain('class="alert')
  })

  it('merges the matching action, activity, configuration, and persisted failure without losing remediation', () => {
    const message = 'The checkout is already running in store/local.'
    const markup = renderToStaticMarkup(<EnvironmentNotices environment={{ ...environment, reason: ` ${message} `,
      issues: [{ code: 'SOURCE_IN_USE', message, remediation: 'Bind a separate worktree.' }],
    }} actions={{ ...actions, error: { title: 'Environment startup failed', message,
      remediation: [{ label: 'Open Bindings', url: '/environments/store/qa-local?tab=bindings' }],
    } }} activity={{ ...activity, error: { title: 'Environment activity failed', message, remediation: [{ label: 'Bind a separate worktree.' }] } }} />)
    expect(markup.match(/role="alert"/g)).toHaveLength(1)
    expect(markup.split(message)).toHaveLength(2)
    expect(markup).toContain('Environment startup failed')
    expect(markup).toContain('SOURCE_IN_USE')
    expect(markup).toContain('href="/environments/store/qa-local?tab=bindings"')
    expect(markup.split('Bind a separate worktree.')).toHaveLength(2)
  })

  it('keeps unrelated failures and operation tracking available', () => {
    const markup = renderToStaticMarkup(<EnvironmentNotices environment={{ ...environment, reason: 'Orders runtime could not be verified.' }}
      actions={{ ...actions, trackingInterrupted: true, error: { title: 'Startup progress could not be confirmed', message: 'The operation request timed out.', code: 'TIMEOUT' } }}
      activity={{ ...activity, error: { title: 'Activity unavailable', message: 'The timeline request failed.' } }} />)
    expect(markup.match(/role="alert"/g)).toHaveLength(3)
    expect(markup).toContain('TIMEOUT')
    expect(markup).toContain('The timeline request failed.')
    expect(markup).toContain('Orders runtime could not be verified.')
    expect(markup).toContain('RESUME TRACKING')
  })

  it('does not collapse distinct error codes that share a message', () => {
    const message = 'The request failed.'
    const markup = renderToStaticMarkup(<EnvironmentNotices environment={environment}
      actions={{ ...actions, error: { title: 'Start failed', message, code: 'START_FAILED' } }}
      activity={{ ...activity, error: { title: 'Activity unavailable', message, code: 'ACTIVITY_UNAVAILABLE' } }} />)
    expect(markup.match(/role="alert"/g)).toHaveLength(2)
    expect(markup).toContain('START_FAILED')
    expect(markup).toContain('ACTIVITY_UNAVAILABLE')
  })

  it.each(['healthy', 'stopped', 'starting', 'recovering', 'stopping'] as const)('does not turn routine %s status into an error', (status) => {
    const markup = renderToStaticMarkup(<EnvironmentNotices environment={{ ...environment, status, reason: 'Routine environment status' }} actions={actions} activity={activity} />)
    expect(markup).toBe('')
  })

  it('keeps configuration errors red and preserves each issue during a transition', () => {
    const markup = renderToStaticMarkup(<EnvironmentNotices environment={{ ...environment, status: 'recovering', reason: 'services are being recovered', issues: [
      { code: 'MISSING_SOURCE', message: 'Orders checkout is missing.', remediation: 'Attach the orders checkout in Bindings.' },
      { code: 'INVALID_PROVIDER', message: 'Inventory provider is invalid.', remediation: 'Choose an inventory provider.' },
    ] }} actions={actions} activity={activity} />)
    expect(markup.match(/class="action-error" role="alert"/g)).toHaveLength(2)
    expect(markup).toContain('Configuration needs attention')
    expect(markup).toContain('MISSING_SOURCE')
    expect(markup).toContain('INVALID_PROVIDER')
    expect(markup).toContain('Attach the orders checkout in Bindings.')
    expect(markup).toContain('Choose an inventory provider.')
    expect(markup).not.toContain('services are being recovered')
  })
})
