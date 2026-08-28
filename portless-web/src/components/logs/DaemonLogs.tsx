import { api } from '../../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../ActionError'
import type { DaemonLogSnapshot } from '../../api/contracts/system'
import { LogViewer, type LogViewerLabels } from './LogViewer'
import { useLogTail } from './useLogTail'

const daemonLogLabels: LogViewerLabels = {
  controls: 'Daemon log controls',
  raw: 'Open raw daemon logs in new tab',
  pause: 'Pause daemon log tail',
  resume: 'Resume daemon log tail',
  active: 'Daemon log tail active',
  output: 'Daemon logs',
  loading: 'Loading daemon logs…',
  empty: 'No daemon logs have been written yet.',
}

export function DaemonLogs({ instanceId }: { instanceId?: string }) {
  const logs = useLogTail<DaemonLogSnapshot, ActionErrorDetails>({
    identity: instanceId || '',
    initialValue: emptyDaemonLogSnapshot,
    load: (signal) => api<DaemonLogSnapshot>('/daemon/logs', { signal }),
    mapError: (error) => actionError("Daemon logs couldn't be loaded", error),
    equal: daemonLogSnapshotsEqual,
  })

  return <>
    {logs.error && <div className="log-view__error"><ActionErrorNotice error={logs.error} onDismiss={logs.dismissError} /></div>}
    <DaemonLogView snapshot={logs.value} loaded={logs.loaded} tailing={logs.tailing} scrollRevision={logs.scrollRevision} onTail={logs.toggleTailing} />
  </>
}

export function DaemonLogView({ snapshot, loaded, tailing, scrollRevision = 0, onTail }: {
  snapshot: DaemonLogSnapshot
  loaded: boolean
  tailing: boolean
  scrollRevision?: number
  onTail: () => void
}) {
  const meta = snapshot.truncated ? <div className="log-view__meta"><span>Latest 256 KiB · older output omitted</span></div> : undefined
  return <LogViewer
    className="daemon-log-view"
    outputClassName="log-view__output--raw"
    labels={daemonLogLabels}
    loaded={loaded}
    empty={snapshot.content === ''}
    tailing={tailing}
    scrollRevision={scrollRevision}
    rawText={snapshot.content}
    rawDisabled={!loaded}
    toolbarMeta={meta}
    onTail={onTail}
  >
    {snapshot.content}
  </LogViewer>
}

function emptyDaemonLogSnapshot(): DaemonLogSnapshot {
  return { content: '', truncated: false }
}

function daemonLogSnapshotsEqual(current: DaemonLogSnapshot, next: DaemonLogSnapshot) {
  return current.content === next.content && current.truncated === next.truncated
}
