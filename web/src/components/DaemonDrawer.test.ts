import { describe, expect, it } from 'vitest'
import type { DaemonStatus, RuntimeStatus } from '../types'
import { daemonDiagnostics } from './DaemonDrawer'

describe('daemon diagnostics', () => {
  it('uses explicit version labels and includes safe handoff context', () => {
    const status: DaemonStatus = {
      state: 'ready', pid: 33083, startedAt: '2026-08-12T15:57:59-05:00',
      instanceId: 'f8ecffdf6d6f', buildId: '9f15670e7324', protocolVersion: '2', apiVersion: '3',
      handoffReady: true, recoveryProblems: [], activeEnvironments: ['golden-path/local'],
    }
    const runtime = { selected: 'docker', version: '29.4.0', state: 'ready', preference: 'auto', candidates: [] } as RuntimeStatus

    const output = daemonDiagnostics(status, runtime)

    expect(output).toContain('Protocol Version: 2')
    expect(output).toContain('API Version: 3')
    expect(output).toContain('Runtime: docker 29.4.0')
    expect(output).toContain('Runtime handoff: ready')
    expect(output).toContain('  golden-path/local')
  })
})
