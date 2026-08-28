import type { DaemonRestart } from './api/contracts/system'

export const DAEMON_RESTART_SLA_MS = 5_000

export function daemonRestartDeadline(receipt: DaemonRestart, initiatedAt: number) {
  const localDeadline = initiatedAt + DAEMON_RESTART_SLA_MS
  const serverDeadline = Date.parse(receipt.deadlineAt)
  if (!Number.isFinite(serverDeadline)) return localDeadline
  return Math.min(Math.max(serverDeadline, initiatedAt), localDeadline)
}

export function daemonRestartPollDelay(attempt: number) {
  return attempt < 5 ? 100 : 250
}
