import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { ControlPlaneHealth, DaemonDiagnostics, DaemonHandoffStatus, DaemonStatus, RelayStatus, RuntimeStatus } from '../api/contracts/system'
import { DaemonDrawer, daemonDiagnostics, daemonTabIndexForKey } from './DaemonDrawer'

const runtime: RuntimeStatus = {
  selected: 'docker', version: '29.4.0', state: 'ready', preference: 'auto',
  candidates: [
    { name: 'docker', state: 'ready', version: '29.4.0' },
    { name: 'podman', state: 'missing', reason: 'Podman is not installed.' },
  ],
}

const relay = {
  platform: 'launchd', service: 'dev.portless.relay', installed: true, running: true, healthy: true,
  httpHealthy: true, helperCurrent: true, helperBuildId: 'build-current', currentBuildId: 'build-current',
  dnsHealthy: true, resolverPresent: true, resolverHealthy: true, endpointPoolReady: true, endpointPoolManaged: true,
  dnsListenAddress: '127.77.0.1:1053', targetSocket: '/private/ingress.sock', dnsTargetSocket: '/private/dns.sock',
} as RelayStatus

const diagnostics: DaemonDiagnostics = {
  collectedAt: '2026-08-25T12:00:02Z',
  inventory: { processes: 2, containers: 1, proxyListeners: 3, activeEnvironments: 1, problems: [] },
  recovery: { result: 'healthy', completedAt: '2026-08-25T11:59:58Z', durationMs: 42, recovered: 1, problems: [] },
  build: {
    version: '0.9.0', distribution: 'source', commit: '1234567890abcdef1234567890abcdef12345678',
    runningBuildId: 'build-current', onDiskBuildId: 'build-current', current: true,
  },
  lastRestart: {
    restartId: 'restart-browser', reason: 'browser', previousInstanceId: 'previous-instance', instanceId: 'current-instance',
    targetBuildId: 'build-current', acceptedAt: '2026-08-25T11:59:59Z', deadlineAt: '2026-08-25T12:00:04Z',
    readyAt: '2026-08-25T12:00:00Z', durationMs: 731, withinSla: true,
  },
}

const controlPlaneHealth: ControlPlaneHealth = {
  api: { state: 'ready', latencyMs: 3, checkedAt: '2026-08-25T12:00:01Z' },
  events: { state: 'connected', connections: 2, connected: 2, lastConnectedAt: '2026-08-25T12:00:01Z' },
}

describe('daemon diagnostics', () => {
  it('uses explicit version labels and includes safe handoff context', () => {
    const status: DaemonStatus = {
      state: 'ready', pid: 33083, startedAt: '2026-08-12T15:57:59-05:00',
      instanceId: 'f8ecffdf6d6f', buildId: '9f15670e7324', protocolVersion: '3.0.0', apiVersion: '8.0.0',
      recoveryProblems: [], activeEnvironments: ['store/local'],
    }
    const handoff: DaemonHandoffStatus = {
      state: 'ready', verifiedAt: '2026-08-25T12:00:00Z', problems: [], activeEnvironments: ['store/local'],
    }
    const output = daemonDiagnostics(status, runtime, relay, handoff, diagnostics)

    expect(output).toContain('Protocol Version: 3.0.0')
    expect(output).toContain('API Version: 8.0.0')
    expect(output).toContain('Runtime: docker 29.4.0')
    expect(output).toContain('Runtime preference: auto')
    expect(output).toContain('podman: missing — Podman is not installed.')
    expect(output).toContain('Runtime handoff: ready')
    expect(output).toContain('Handoff verified: 2026-08-25T12:00:00Z')
    expect(output).toContain('  store/local')
    expect(output).toContain('DNS resolver: ready (localhost, portless.test)')
    expect(output).toContain('Relay service: dev.portless.relay (launchd, running)')
    expect(output).toContain('Relay helper: Matches daemon build (Build build-curren)')
    expect(output).toContain('Last restart: restart-browser')
    expect(output).toContain('Last restart duration: 731 ms')
    expect(output).toContain('Last restart SLA: met')
  })

  it('labels an unverified residual endpoint pool as degraded', () => {
    const status: DaemonStatus = {
      state: 'ready', pid: 33083, startedAt: '2026-08-25T12:00:00Z', instanceId: 'instance', buildId: 'build',
      protocolVersion: '4.0.0', apiVersion: '12.6.0', recoveryProblems: [], activeEnvironments: [],
    }
    const residualRelay: RelayStatus = {
      ...relay, installed: false, running: false, healthy: false, endpointPoolResidual: true,
      endpointPoolDetail: '1 reserved Portless address configured', problem: 'ownership receipt is missing',
    }
    const output = daemonDiagnostics(status, runtime, residualRelay)
    expect(output).toContain('TCP endpoint pool: residual; ownership unverified')
    expect(output).toContain('Relay helper: Residual aliases (Ownership receipt unavailable)')
  })

  it('opens on a status summary with ordered operational sections and accessible tabs', () => {
    const status: DaemonStatus = {
      state: 'ready', pid: 33083, startedAt: '2026-08-25T12:00:00Z', instanceId: '1234567890abcdef', buildId: 'build',
      protocolVersion: '4.0.0', apiVersion: '12.2.0', recoveryProblems: [], activeEnvironments: ['store/local'],
    }
    const handoff: DaemonHandoffStatus = { state: 'ready', verifiedAt: '2026-08-25T12:00:01Z', problems: [], activeEnvironments: ['store/local'] }
    const markup = renderToStaticMarkup(createElement(DaemonDrawer, {
      status, diagnostics, controlPlaneHealth, runtime, relay, live: true,
      onClose: () => undefined,
      onRefresh: async () => status,
      onRefreshDiagnostics: async () => diagnostics,
      onVerifyHandoff: async () => handoff,
      onRestart: async (instanceId: string) => ({ restarting: true, restartId: 'restart', reason: 'browser', previousInstanceId: instanceId, targetBuildId: 'build', acceptedAt: '2026-08-25T12:00:00Z', deadlineAt: '2026-08-25T12:00:05Z', handoff: true, activeEnvironments: [] }),
      onReconnected: async () => undefined,
    }))

    expect(markup).toContain('role="tablist" aria-label="Daemon details"')
    expect(markup).toContain('role="tab" aria-selected="true" tabindex="0">STATUS</button>')
    expect(markup).toContain('role="tab" aria-selected="false" tabindex="-1">RUNTIME</button>')
    expect(markup).toContain('role="tab" aria-selected="false" tabindex="-1">STORAGE</button>')
    expect(markup).toContain('role="tab" aria-selected="false" tabindex="-1">LOGS</button>')
    expect(markup).toContain('role="tabpanel" aria-labelledby="daemon-tab-status" tabindex="0"')
    expect(markup).toContain('<span>INSTANCE</span><strong title="1234567890abcdef">1234567890ab</strong><small>1 active environment</small>')
    expect(markup.indexOf('BUILD PROVENANCE')).toBeLessThan(markup.indexOf('CONTROL-PLANE HEALTH'))
    expect(markup.indexOf('CONTROL-PLANE HEALTH')).toBeLessThan(markup.indexOf('LAST RESTART'))
    expect(markup.indexOf('LAST RESTART')).toBeLessThan(markup.indexOf('RECOVERY STATUS'))
    expect(markup).toContain('<span>VERSION</span><strong>0.9.0</strong>')
    expect(markup).toContain('<span>API ROUND TRIP</span>')
    expect(markup).toContain('<strong>3 ms</strong>')
    expect(markup).toContain('<span>EVENT STREAMS</span>')
    expect(markup).toContain('<strong>2 connected</strong>')
    expect(markup).toContain('<span>RESULT</span><strong>Healthy</strong>')
    expect(markup).toContain('<span>DURATION</span><strong>731 ms</strong>')
    expect(markup).toContain('<span>5 SECOND SLA</span><strong title="restart-browser">Met</strong>')
    expect(markup).not.toContain('RUNTIME ENGINE')
  })

  it('moves between tabs only for horizontal tab-list keys', () => {
    expect(daemonTabIndexForKey(0, 'ArrowRight')).toBe(1)
    expect(daemonTabIndexForKey(0, 'ArrowLeft')).toBe(3)
    expect(daemonTabIndexForKey(2, 'Home')).toBe(0)
    expect(daemonTabIndexForKey(1, 'End')).toBe(3)
    expect(daemonTabIndexForKey(1, 'ArrowDown')).toBeNull()
    expect(daemonTabIndexForKey(1, 'ArrowUp')).toBeNull()
  })
})
