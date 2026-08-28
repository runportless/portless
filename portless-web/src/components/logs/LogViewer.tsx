import { useEffect, useRef, type ReactNode } from 'react'
import { openRawLog } from './logDownload'

export type LogViewerLabels = {
  controls: string
  raw: string
  pause: string
  resume: string
  active: string
  output: string
  loading: string
  empty: string
}

export function LogViewer({ className = '', outputClassName = '', labels, loaded, empty, tailing, scrollRevision = 0, rawText, rawDisabled = false, toolbarMeta, children, onTail }: {
  className?: string
  outputClassName?: string
  labels: LogViewerLabels
  loaded: boolean
  empty: boolean
  tailing: boolean
  scrollRevision?: number
  rawText: string
  rawDisabled?: boolean
  toolbarMeta?: ReactNode
  children: ReactNode
  onTail: () => void
}) {
  const viewport = useRef<HTMLPreElement>(null)
  useEffect(() => {
    if (!tailing || !viewport.current) return
    viewport.current.scrollTop = viewport.current.scrollHeight
  }, [loaded, scrollRevision, tailing])

  const tailAction = tailing ? labels.pause : labels.resume
  const rootClass = ['log-view', className].filter(Boolean).join(' ')
  const outputClass = ['log-view__output', outputClassName].filter(Boolean).join(' ')
  return <div className={rootClass}>
    <div className="log-view__toolbar">
      {toolbarMeta}
      <div className="log-view__controls" role="group" aria-label={labels.controls}>
        <button className="log-view__control" type="button" aria-label={labels.raw} title={labels.raw} disabled={rawDisabled} onClick={() => openRawLog(rawText)}>RAW ↗</button>
        <button className="log-view__control log-view__control--tail" type="button" aria-label={tailAction} title={tailAction} aria-pressed={tailing} onClick={onTail}><TailIcon active={tailing} />{tailing ? 'TAILING' : 'TAIL'}</button>
      </div>
    </div>
    <div className="log-view__viewport">
      <pre ref={viewport} className={outputClass} aria-label={labels.output}>
        {!loaded ? <span className="log-view__empty">{labels.loading}</span>
          : empty ? <span className="log-view__empty">{labels.empty}</span>
            : children}
      </pre>
      {tailing && <span className="log-view__tail-spinner" role="status" aria-label={labels.active} />}
    </div>
  </div>
}

function TailIcon({ active }: { active: boolean }) {
  return active
    ? <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="3" height="8" /><rect x="6" y="1" width="3" height="8" /></svg>
    : <svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 1 9 5 2 9Z" /></svg>
}
