import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { Project } from '../../api/contracts/projects'
import { aggregateProjectStatus, projectOverview, projectSourceRows, projectSourceStatus } from './projectPresentation'

const baseEnvironment = {
  project: 'store',
  name: 'local',
  revision: 1,
  status: 'healthy',
  services: [{
    name: 'checkout',
    kind: 'process',
    required: true,
    health: { kind: 'http', timeout: 5, interval: 10 },
    launchMode: 'managed',
    status: 'ready',
    generation: 1,
    endpoints: [],
    restartCount: 0,
    recentRequests: 0,
  }],
  sources: [{ name: 'checkout', path: '/workspace/checkout', status: 'ready', createdAt: '2026-08-27T19:00:00Z', scannedAt: '2026-08-27T20:00:00Z' }],
  bindings: [],
  connections: [],
  createdAt: '2026-08-27T19:00:00Z',
  updatedAt: '2026-08-27T20:00:00Z',
} as Environment

describe('project presentation', () => {
  it('summarizes only a project\'s environments and ignores stopped environments for health', () => {
    const project = { name: 'store', updatedAt: '2026-08-27T19:00:00Z', sources: [{ name: 'checkout', services: ['checkout'] }] } as Project
    const overview = projectOverview(project, [
      baseEnvironment,
      { ...baseEnvironment, name: 'demo', status: 'stopped' },
      { ...baseEnvironment, project: 'other', name: 'failed', status: 'failed' },
    ])

    expect(overview.status).toBe('healthy')
    expect(overview.environmentNames).toBe('demo, local')
    expect(overview.serviceCount).toBe(1)
    expect(overview.updatedAt).toBe(baseEnvironment.updatedAt)
  })

  it('uses the most urgent active environment status', () => {
    expect(aggregateProjectStatus([{ ...baseEnvironment, status: 'degraded' }, { ...baseEnvironment, name: 'recovering', status: 'recovering' }])).toBe('degraded')
    expect(aggregateProjectStatus([{ ...baseEnvironment, status: 'stopped' }])).toBe('stopped')
    expect(aggregateProjectStatus([])).toBe('unknown')
  })

  it('deduplicates checkouts and marks missing source bindings independently', () => {
    const project = { name: 'store', sources: [{ name: 'checkout', services: ['checkout'] }] } as Project
    const rows = projectSourceRows(project, [
      baseEnvironment,
      { ...baseEnvironment, name: 'qa', sources: [{ ...baseEnvironment.sources![0] }] },
      { ...baseEnvironment, name: 'remote', sources: [], issues: [] },
      { ...baseEnvironment, name: 'missing', sources: [], issues: [{ code: 'MISSING_BINDING', subject: 'checkout', message: 'missing' }] },
    ])

    expect(rows[0].checkouts).toEqual([{ path: '/workspace/checkout', status: 'ready' }])
    expect(rows[0].unbound).toEqual([
      { name: 'remote', configurationRequired: false },
      { name: 'missing', configurationRequired: true },
    ])
    expect(projectSourceStatus(rows[0])).toBe('degraded')
  })
})
