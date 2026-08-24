import { describe, expect, it } from 'vitest'
import type { DaemonStatus, RelayStatus, RuntimeStatus } from '../types'
import { daemonDiagnostics } from './DaemonDrawer'

describe('daemon diagnostics', () => {
  it('uses explicit version labels and includes safe handoff context', () => {
    const status: DaemonStatus = {
      state: 'ready', pid: 33083, startedAt: '2026-08-12T15:57:59-05:00',
      instanceId: 'f8ecffdf6d6f', buildId: '9f15670e7324', protocolVersion: '3.0.0', apiVersion: '8.0.0',
      handoffReady: true, recoveryProblems: [], activeEnvironments: ['store/local'],
    }
    const runtime = { selected: 'docker', version: '29.4.0', state: 'ready', preference: 'auto', candidates: [] } as RuntimeStatus
    const relay = {
      platform: 'launchd', service: 'dev.portless.relay', installed: true, running: true, healthy: true,
      httpHealthy: true, helperCurrent: true, dnsHealthy: true, resolverPresent: true, resolverHealthy: true,
      endpointPoolReady: true, dnsListenAddress: '127.77.0.1:1053',
    } as RelayStatus

    const output = daemonDiagnostics(status, runtime, relay)

    expect(output).toContain('Protocol Version: 3.0.0')
    expect(output).toContain('API Version: 8.0.0')
    expect(output).toContain('Runtime: docker 29.4.0')
    expect(output).toContain('Runtime handoff: ready')
    expect(output).toContain('  store/local')
    expect(output).toContain('DNS resolver: ready (localhost, portless.test)')
  })
})
