import { api, environmentPath } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../ActionError'
import type { Environment, LogEntry } from '../../types'
import { LogViewer, type LogViewerLabels } from './LogViewer'
import { useLogTail } from './useLogTail'

const serviceLogLimit = 500
const serviceLogLabels: LogViewerLabels = {
  controls: 'Log view controls',
  raw: 'Open raw logs in new tab',
  pause: 'Pause live tail',
  resume: 'Resume live tail',
  active: 'Live tail active',
  output: '',
  loading: 'Loading logs…',
  empty: 'No logs captured for this service.',
}

export function ServiceLogs({ environment, service }: { environment: Pick<Environment, 'project' | 'name'>; service: string }) {
  const path = `${environmentPath(environment, '/logs')}?service=${encodeURIComponent(service)}&limit=${serviceLogLimit}`
  const logs = useLogTail<LogEntry[], ActionErrorDetails>({
    identity: `${environment.project}/${environment.name}/${service}`,
    initialValue: () => [],
    load: (signal) => api<{ entries: LogEntry[] }>(path, { signal }).then((result) => result.entries),
    mapError: (error) => actionError(`Logs for ${service} couldn't be loaded`, error),
  })

  return <>
    {logs.error && <div className="log-view__error"><ActionErrorNotice error={logs.error} onDismiss={logs.dismissError} /></div>}
    <ServiceLogView
      entries={logs.value}
      loaded={logs.loaded}
      tailing={logs.tailing}
      scrollRevision={logs.scrollRevision}
      service={service}
      onTail={logs.toggleTailing}
    />
  </>
}

export function ServiceLogView({ entries, loaded, tailing, scrollRevision = 0, service, onTail }: {
  entries: LogEntry[]
  loaded: boolean
  tailing: boolean
  scrollRevision?: number
  service: string
  onTail: () => void
}) {
  return <LogViewer
    labels={{ ...serviceLogLabels, output: `${service} logs` }}
    loaded={loaded}
    empty={entries.length === 0}
    tailing={tailing}
    scrollRevision={scrollRevision}
    rawText={serviceLogText(entries)}
    onTail={onTail}
  >
    {entries.map((entry, index) => <span className={`log-line${entry.stream === 'stderr' ? ' log-line--stderr' : ''}`} key={`${entry.timestamp}-${entry.stream}-${entry.generation}-${index}`}><time dateTime={entry.timestamp}>{formatLogTimestamp(entry.timestamp)}</time><b className="log-line__stream">{entry.stream}</b><span className="log-line__message">{entry.message}</span></span>)}
  </LogViewer>
}

export function serviceLogText(entries: LogEntry[]) {
  return entries.map((entry) => entry.message).join('\n')
}

function formatLogTimestamp(timestamp: string) {
  const value = new Date(timestamp)
  return Number.isNaN(value.getTime()) ? timestamp : value.toLocaleTimeString()
}
