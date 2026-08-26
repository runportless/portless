import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from './ActionError'
import type { DaemonLogSnapshot } from '../types'

const daemonLogPollMilliseconds = 1_000
const rawLogURLLifetimeMilliseconds = 60_000

export function DaemonLogs({ instanceId }: { instanceId?: string }) {
  const [snapshot, setSnapshot] = useState<DaemonLogSnapshot>({ content: '', truncated: false })
  const [loaded, setLoaded] = useState(false)
  const [tailing, setTailing] = useState(true)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const viewport = useRef<HTMLPreElement>(null)

  useEffect(() => {
    if (!tailing) return
    let active = true
    let nextPoll: number | undefined
    const controller = new AbortController()
    const load = async () => {
      try {
        const result = await api<DaemonLogSnapshot>('/daemon/logs', { signal: controller.signal })
        if (!active) return
        setSnapshot((current) => current.content === result.content && current.truncated === result.truncated ? current : result)
        setLoaded(true)
        setError(null)
      } catch (value) {
        if (!active || controller.signal.aborted) return
        setLoaded(true)
        setError(actionError("Daemon logs couldn't be loaded", value))
      } finally {
        if (active) nextPoll = window.setTimeout(() => void load(), daemonLogPollMilliseconds)
      }
    }
    void load()
    return () => {
      active = false
      controller.abort()
      window.clearTimeout(nextPoll)
    }
  }, [instanceId, tailing])

  useEffect(() => {
    if (!tailing || !viewport.current) return
    viewport.current.scrollTop = viewport.current.scrollHeight
  }, [snapshot, loaded, tailing])

  const openRawLogs = () => {
    const url = window.URL.createObjectURL(daemonLogBlob(snapshot.content))
    window.open(url, '_blank', 'noopener,noreferrer')
    window.setTimeout(() => window.URL.revokeObjectURL(url), rawLogURLLifetimeMilliseconds)
  }

  return <>
    {error && <div className="log-view__error"><ActionErrorNotice error={error} onDismiss={() => setError(null)} /></div>}
    <DaemonLogView
      snapshot={snapshot}
      loaded={loaded}
      tailing={tailing}
      viewportRef={viewport}
      onRaw={openRawLogs}
      onTail={() => setTailing((value) => !value)}
    />
  </>
}

export function DaemonLogView({ snapshot, loaded, tailing, viewportRef, onRaw, onTail }: {
  snapshot: DaemonLogSnapshot
  loaded: boolean
  tailing: boolean
  viewportRef?: React.RefObject<HTMLPreElement | null>
  onRaw: () => void
  onTail: () => void
}) {
  const tailAction = tailing ? 'Pause daemon log tail' : 'Resume daemon log tail'
  return <div className="log-view daemon-log-view">
    <div className="log-view__toolbar">
      <div className="log-view__meta"><strong>DAEMON.LOG</strong><span>{snapshot.truncated ? 'Latest 256 KiB · older output omitted' : 'Retained daemon output'}</span></div>
      <div className="log-view__controls" role="group" aria-label="Daemon log controls">
        <button className="log-view__control" type="button" aria-label="Open raw daemon logs in new tab" title="Open raw daemon logs in new tab" disabled={!loaded} onClick={onRaw}>RAW ↗</button>
        <button className="log-view__control log-view__control--tail" type="button" aria-label={tailAction} title={tailAction} aria-pressed={tailing} onClick={onTail}><TailIcon active={tailing} />{tailing ? 'TAILING' : 'TAIL'}</button>
      </div>
    </div>
    <div className="log-view__viewport">
      <pre ref={viewportRef} className="log-view__output log-view__output--raw" aria-label="Daemon logs">
        {!loaded ? <span className="log-view__empty">Loading daemon logs…</span>
          : snapshot.content === '' ? <span className="log-view__empty">No daemon logs have been written yet.</span>
            : snapshot.content}
      </pre>
      {tailing && <span className="log-view__tail-spinner" role="status" aria-label="Daemon log tail active" />}
    </div>
  </div>
}

export function daemonLogBlob(content: string) {
  return new Blob([content], { type: 'text/plain;charset=utf-8' })
}

function TailIcon({ active }: { active: boolean }) {
  return active
    ? <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg>
    : <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 1 9 5 2 9Z" /></svg>
}
