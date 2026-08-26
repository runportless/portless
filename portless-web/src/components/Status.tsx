import type { EnvironmentStatus, ServiceStatus } from '../types'

type StatusValue = EnvironmentStatus | ServiceStatus | string

export function StatusMark({ status, label = true }: { status: StatusValue; label?: boolean }) {
  const tone = statusTone(status)
  return (
    <span className={`status status--${tone}`} title={status.replaceAll('_', ' ')}>
      <span className="status__mark" aria-hidden="true">{tone === 'warning' ? '▲' : tone === 'danger' ? '■' : tone === 'unknown' ? '○' : '●'}</span>
      {label && <span>{status.replaceAll('_', ' ')}</span>}
    </span>
  )
}

export function statusTone(status: StatusValue) {
  if (['healthy', 'ready', 'active', 'succeeded'].includes(status)) return 'success'
  if (['degraded', 'unhealthy', 'starting', 'checking', 'recovering', 'restarting', 'stopping'].includes(status)) return 'warning'
  if (['failed', 'exited', 'unreachable'].includes(status)) return 'danger'
  if (['unknown', 'missing'].includes(status)) return 'unknown'
  return 'muted'
}

export function relativeTime(input?: string) {
  if (!input) return '—'
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(input).getTime()) / 1000))
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`
  return `${Math.floor(seconds / 86400)}d`
}

export function duration(milliseconds = 0) {
  if (milliseconds < 1000) return `${milliseconds}ms`
  return `${(milliseconds / 1000).toFixed(2)}s`
}

export function StatePanel({ title, value, tone, detail }: { title: string; value: string | number; tone?: string; detail?: string }) {
  return (
    <div className={`state-panel ${tone ? `state-panel--${tone}` : ''}`}>
      <div className="eyebrow">{title}</div>
      <div className="state-panel__value">{value}</div>
      {detail && <div className="state-panel__detail">{detail}</div>}
    </div>
  )
}
