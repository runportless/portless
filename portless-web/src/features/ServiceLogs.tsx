import { useEffect, useRef, useState } from 'react'
import { api, environmentPath } from '../api'
import { actionError, ActionErrorNotice, type ActionErrorDetails } from '../components/ActionError'
import type { Environment, LogEntry } from '../types'

const serviceLogLimit = 500
const serviceLogPollMilliseconds = 1_000
const rawLogURLLifetimeMilliseconds = 60_000

export function ServiceLogs({ environment, service }: { environment: Pick<Environment, 'project' | 'name'>; service: string }) {
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [loaded, setLoaded] = useState(false)
  const [tailing, setTailing] = useState(true)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const viewport = useRef<HTMLPreElement>(null)
  const path = `${environmentPath(environment, '/logs')}?service=${encodeURIComponent(service)}&limit=${serviceLogLimit}`

  useEffect(() => {
    if (!tailing) return
    let active = true
    let nextPoll: number | undefined
    const controller = new AbortController()
    const load = async () => {
      try {
        const result = await api<{ entries: LogEntry[] }>(path, { signal: controller.signal })
        if (!active) return
        setEntries(result.entries)
        setLoaded(true)
        setError(null)
      } catch (value) {
        if (!active || controller.signal.aborted) return
        setLoaded(true)
        setError(actionError(`Logs for ${service} couldn't be loaded`, value))
      } finally {
        if (active) nextPoll = window.setTimeout(() => void load(), serviceLogPollMilliseconds)
      }
    }
    void load()
    return () => {
      active = false
      controller.abort()
      window.clearTimeout(nextPoll)
    }
  }, [path, service, tailing])

  useEffect(() => {
    if (!tailing || !viewport.current) return
    viewport.current.scrollTop = viewport.current.scrollHeight
  }, [entries, loaded, tailing])

  const openRawLogs = () => {
    const url = window.URL.createObjectURL(rawLogBlob(entries))
    window.open(url, '_blank', 'noopener,noreferrer')
    window.setTimeout(() => window.URL.revokeObjectURL(url), rawLogURLLifetimeMilliseconds)
  }

  return <>
    {error && <div className="log-view__error"><ActionErrorNotice error={error} onDismiss={() => setError(null)} /></div>}
    <ServiceLogView
      entries={entries}
      loaded={loaded}
      tailing={tailing}
      service={service}
      viewportRef={viewport}
      onRaw={openRawLogs}
      onTail={() => setTailing((value) => !value)}
    />
  </>
}

export function ServiceLogView({ entries, loaded, tailing, service, viewportRef, onRaw, onTail }: {
  entries: LogEntry[]
  loaded: boolean
  tailing: boolean
  service: string
  viewportRef?: React.RefObject<HTMLPreElement | null>
  onRaw: () => void
  onTail: () => void
}) {
  const tailAction = tailing ? 'Pause live tail' : 'Resume live tail'
  return <div className="log-view">
    <div className="log-view__toolbar">
      <div className="log-view__controls" role="group" aria-label="Log view controls">
        <button className="log-view__control" type="button" aria-label="Open raw logs in new tab" title="Open raw logs in new tab" onClick={onRaw}>RAW ↗</button>
        <button className="log-view__control log-view__control--tail" type="button" aria-label={tailAction} title={tailAction} aria-pressed={tailing} onClick={onTail}><TailIcon active={tailing} />{tailing ? 'TAILING' : 'TAIL'}</button>
      </div>
    </div>
    <div className="log-view__viewport">
      <pre ref={viewportRef} className="log-view__output" aria-label={`${service} logs`}>
        {!loaded ? <span className="log-view__empty">Loading logs…</span>
          : entries.length === 0 ? <span className="log-view__empty">No logs captured for this service.</span>
            : entries.map((entry, index) => <span className={`log-line${entry.stream === 'stderr' ? ' log-line--stderr' : ''}`} key={`${entry.timestamp}-${entry.stream}-${entry.generation}-${index}`}><time dateTime={entry.timestamp}>{formatLogTimestamp(entry.timestamp)}</time><b className="log-line__stream">{entry.stream}</b><span className="log-line__message">{entry.message}</span></span>)}
      </pre>
      {tailing && <span className="log-view__tail-spinner" role="status" aria-label="Live tail active" />}
    </div>
  </div>
}

export function rawLogText(entries: LogEntry[]) {
  return entries.map((entry) => entry.message).join('\n')
}

export function rawLogBlob(entries: LogEntry[]) {
  return new Blob([rawLogText(entries)], { type: 'text/plain;charset=utf-8' })
}

function formatLogTimestamp(timestamp: string) {
  const value = new Date(timestamp)
  return Number.isNaN(value.getTime()) ? timestamp : value.toLocaleTimeString()
}

function TailIcon({ active }: { active: boolean }) {
  return active
    ? <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg>
    : <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 1 9 5 2 9Z" /></svg>
}
