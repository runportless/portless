import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { DaemonLogSnapshot } from '../types'
import { daemonLogBlob, DaemonLogs, DaemonLogView } from './DaemonLogs'

describe('daemon logs', () => {
  it('starts with bounded live tailing and a raw-log action', () => {
    const markup = renderToStaticMarkup(<DaemonLogs instanceId="daemon-one" />)

    expect(markup).toContain('aria-label="Open raw daemon logs in new tab"')
    expect(markup).toContain('aria-label="Pause daemon log tail"')
    expect(markup).toContain('aria-pressed="true"')
    expect(markup).toContain('aria-label="Daemon log tail active"')
    expect(markup).toContain('Loading daemon logs…')
    expect(markup).not.toContain('DAEMON.LOG')
    expect(markup).not.toContain('Retained daemon output')
  })

  it('renders raw content and explains when older output was omitted', () => {
    const snapshot: DaemonLogSnapshot = { content: 'time=now level=INFO msg="ready <locally>"\n', truncated: true }
    const markup = renderToStaticMarkup(<DaemonLogView snapshot={snapshot} loaded tailing={false} onRaw={() => undefined} onTail={() => undefined} />)

    expect(markup).toContain('aria-label="Daemon logs"')
    expect(markup).toContain('Latest 256 KiB · older output omitted')
    expect(markup).not.toContain('DAEMON.LOG')
    expect(markup).toContain('ready &lt;locally&gt;')
    expect(markup).toContain('aria-label="Resume daemon log tail"')
    expect(markup).not.toContain('aria-label="Daemon log tail active"')
  })

  it('creates an exact plain-text raw snapshot', async () => {
    const content = 'first line\nsecond line\n'
    const blob = daemonLogBlob(content)

    expect(blob.type).toBe('text/plain;charset=utf-8')
    expect(await blob.text()).toBe(content)
  })
})
