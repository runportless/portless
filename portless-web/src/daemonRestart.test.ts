import { describe, expect, it } from 'vitest'
import { DAEMON_RESTART_SLA_MS, daemonRestartDeadline, daemonRestartPollDelay } from './daemonRestart'
import type { DaemonRestart } from './types'

const receipt = (deadlineAt: string): DaemonRestart => ({
  restarting: true,
  restartId: 'restart-1',
  reason: 'browser',
  previousInstanceId: 'instance-1',
  targetBuildId: 'build-2',
  acceptedAt: '2026-08-26T12:00:00Z',
  deadlineAt,
  handoff: true,
  activeEnvironments: ['store/local'],
})

describe('daemon restart SLA', () => {
  it('uses the shared server deadline without exceeding five seconds locally', () => {
    const initiatedAt = Date.parse('2026-08-26T12:00:00Z')
    expect(daemonRestartDeadline(receipt('2026-08-26T12:00:04Z'), initiatedAt)).toBe(initiatedAt + 4_000)
    expect(daemonRestartDeadline(receipt('2026-08-26T12:00:30Z'), initiatedAt)).toBe(initiatedAt + DAEMON_RESTART_SLA_MS)
    expect(daemonRestartDeadline(receipt('2026-08-26T11:59:59Z'), initiatedAt)).toBe(initiatedAt)
    expect(daemonRestartDeadline(receipt('invalid'), initiatedAt)).toBe(initiatedAt + DAEMON_RESTART_SLA_MS)
  })

  it('polls aggressively during the expected short outage', () => {
    expect(Array.from({ length: 7 }, (_, index) => daemonRestartPollDelay(index))).toEqual([100, 100, 100, 100, 100, 250, 250])
  })
})
