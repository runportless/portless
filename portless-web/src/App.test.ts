import { describe, expect, it } from 'vitest'
import { environmentSessionKey, parseRoute } from './App'

describe('application routes', () => {
  it('recognizes the top-level settings page without inventing project scope', () => {
    expect(parseRoute('/settings')).toEqual({ settings: true, tab: 'overview' })
    expect(parseRoute('/projects/billing')).toEqual({ project: 'billing', settings: false, tab: 'overview' })
  })

  it('replaces environment view state when the daemon instance changes', () => {
    const environment = { project: 'billing', name: 'local' }
    expect(environmentSessionKey(environment, null)).toBe('billing/local@daemon-pending')
    expect(environmentSessionKey(environment, { instanceId: 'daemon-a' })).toBe('billing/local@daemon-a')
    expect(environmentSessionKey(environment, { instanceId: 'daemon-b' })).toBe('billing/local@daemon-b')
  })
})
