import { useEffect, useState } from 'react'
import type { Environment } from '../../api/contracts/environments'
import { ActionErrorNotice, type ActionErrorDetails } from '../../components/ActionError'
import type { EnvironmentActions } from './useEnvironmentActions'
import type { EnvironmentActivity } from './useEnvironmentActivity'

type NoticeSource = { key: string; error: ActionErrorDetails; dismiss?: () => void }
type Notice = { error: ActionErrorDetails; sources: NoticeSource[] }

export function EnvironmentNotices({ environment, actions, activity }: {
  environment: Environment
  actions: Pick<EnvironmentActions, 'error' | 'dismissError' | 'trackingInterrupted' | 'resumeTracking'>
  activity: Pick<EnvironmentActivity, 'error' | 'dismissError'>
}) {
  const [dismissed, setDismissed] = useState<string[]>([])
  const sources: NoticeSource[] = []
  const addSource = (source: string, error: ActionErrorDetails, dismiss?: () => void) => {
    const message = error.message.trim()
    const key = JSON.stringify([environment.project, environment.name, source, message, error.code])
    sources.push({ key, error: { ...error, message }, dismiss })
  }

  // Prefer action details while merging their matching persisted failure reason.
  if (actions.error) addSource('action', actions.error, actions.dismissError)
  if (activity.error) addSource('activity', activity.error, activity.dismissError)
  for (const issue of environment.issues ?? []) {
    addSource(`configuration:${issue.subject ?? ''}`, { title: 'Configuration needs attention', message: issue.message, code: issue.code,
      ...(issue.remediation ? { remediation: [{ label: issue.remediation }] } : {}),
    })
  }
  const reason = environment.reason?.trim()
  // Routine lifecycle progress and healthy/stopped status stay in the header.
  if (['failed', 'degraded', 'unknown'].includes(environment.status) && reason && reason !== environment.status && reason !== 'not running') {
    const title = environment.status === 'failed' ? 'Environment failed' : environment.status === 'unknown' ? 'Environment status unknown' : 'Environment needs attention'
    addSource(`environment:${environment.status}`, { title, message: reason })
  }

  const noticeKeys = JSON.stringify(sources.map(({ key }) => key))
  useEffect(() => {
    const currentKeys = new Set<string>(JSON.parse(noticeKeys))
    setDismissed((current) => {
      const retained = current.filter((key) => currentKeys.has(key))
      return retained.length === current.length ? current : retained
    })
  }, [noticeKeys])

  const notices = new Map<string, Notice>()
  for (const source of sources.filter(({ key }) => !dismissed.includes(key))) {
    const { error } = source
    const matching = [...notices].find(([, notice]) => notice.error.message === error.message && (!notice.error.code || !error.code || notice.error.code === error.code))
    if (matching) {
      const [key, previous] = matching
      const remediation = [...(previous.error.remediation ?? []), ...(error.remediation ?? [])]
      notices.set(key, {
        error: { ...previous.error, code: previous.error.code || error.code,
          remediation: [...new Map(remediation.map((item) => [JSON.stringify(item), item])).values()],
        },
        sources: [...previous.sources, source],
      })
    } else {
      notices.set(source.key, { error, sources: [source] })
    }
  }
  if (!notices.size && !actions.trackingInterrupted) return null

  return <div className="environment-notices">
    {[...notices].map(([key, notice]) => <ActionErrorNotice key={key} error={notice.error} onDismiss={() => {
      // Dismiss every matching source so the saved reason cannot replace the action error.
      setDismissed((current) => [...new Set([...current, ...notice.sources.map((source) => source.key)])])
      notice.sources.forEach((source) => source.dismiss?.())
    }} />)}
    {actions.trackingInterrupted && <button className="button" onClick={actions.resumeTracking}>RESUME TRACKING</button>}
  </div>
}
