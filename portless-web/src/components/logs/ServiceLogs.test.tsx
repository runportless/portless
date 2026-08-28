import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { LogEntry } from '../../types'
import { logBlob } from './logDownload'
import { serviceLogText, ServiceLogs, ServiceLogView } from './ServiceLogs'

const entries: LogEntry[] = [
  { timestamp: '2026-08-20T16:30:00.000Z', service: 'checkout', stream: 'stdout', generation: 3, message: 'ready on an allocated port' },
  { timestamp: '2026-08-20T16:30:01.000Z', service: 'checkout', stream: 'stderr', generation: 3, message: 'retrying <private> dependency' },
]

describe('service logs', () => {
  it('starts with live tailing enabled and a raw-log action', () => {
    const markup = renderToStaticMarkup(<ServiceLogs environment={{ project: 'billing', name: 'local' }} service="checkout" />)

    expect(markup).toContain('aria-label="Open raw logs in new tab"')
    expect(markup).toContain('aria-label="Pause live tail"')
    expect(markup).toContain('aria-pressed="true"')
    expect(markup).toContain('aria-label="Live tail active"')
    expect(markup).toContain('Loading logs…')
  })

  it('renders structured lines with time, stream, and message context', () => {
    const markup = renderToStaticMarkup(<ServiceLogView entries={entries} loaded tailing service="checkout" onTail={() => undefined} />)

    expect(markup).not.toContain('class="log-view__meta"')
    expect(markup).not.toContain('last 2 lines')
    expect(markup).not.toContain('timestamp · stream · message')
    expect(markup).toContain('dateTime="2026-08-20T16:30:00.000Z"')
    expect(markup).toContain('>stdout<')
    expect(markup).toContain('class="log-line log-line--stderr"')
    expect(markup).toContain('ready on an allocated port')
  })

  it('creates plain-text raw output and exposes paused tail controls', async () => {
    const markup = renderToStaticMarkup(<ServiceLogView entries={entries} loaded tailing={false} service="checkout" onTail={() => undefined} />)
    const rawText = serviceLogText(entries)
    const blob = logBlob(rawText)

    expect(rawText).toBe('ready on an allocated port\nretrying <private> dependency')
    expect(blob.type).toBe('text/plain;charset=utf-8')
    expect(await blob.text()).toBe(rawText)
    expect(markup).toContain('aria-label="Open raw logs in new tab"')
    expect(markup).toContain('aria-label="Resume live tail"')
    expect(markup).not.toContain('aria-label="Live tail active"')
  })
})
