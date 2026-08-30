import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { createRecordingDefaults, formatRecordingDuration, RecordingHistoryRepeatButton, RecordingsPanel, startRecordingDurationTimer } from './RecordingsPanel'

const environment: Environment = {
  project: 'store',
  name: 'local',
  revision: 1,
  status: 'healthy',
  primaryService: 'checkout',
  createdAt: '',
  updatedAt: '',
  services: [
    { name: 'checkout', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 },
    { name: 'orders', kind: 'process', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, launchMode: 'managed', status: 'ready', generation: 1, endpoints: [], restartCount: 0, recentRequests: 0 },
  ],
  connections: [{ source: 'checkout', target: 'orders', protocol: 'http', required: true }],
}

const recording: Recording = {
  project: 'store',
  environment: 'local',
  name: 'checkout-debug',
  source: 'checkout',
  target: 'orders',
  capturePayloads: false,
  maxEvents: 10000,
  maxPayloadBytes: 65536,
  status: 'active',
  startedAt: '2026-08-28T12:00:00Z',
  eventCount: 3,
}

const completedRecording: Recording = {
  ...recording,
  name: 'completed-checkout',
  status: 'completed',
  completedAt: '2026-08-28T12:05:00Z',
}

afterEach(() => vi.restoreAllMocks())

describe('RecordingsPanel', () => {
  it('separates the active recording control from recording history', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[recording, completedRecording]} refresh={async () => undefined} />)
    const history = html.slice(html.indexOf('<table class="recording-history-table"'))

    expect(html).toContain('class="recordings-page"')
    expect(html).toContain('class="panel recording-control-panel"')
    expect(html).toContain('<div class="panel-title"><span>ACTIVE RECORDING</span></div>')
    expect(html).toContain('class="recording-active-control"')
    expect(html).not.toContain('recording-active-control__lead')
    expect(html).toContain('class="recording-active-control__pulse"')
    expect(html).not.toContain('status__mark')
    expect(html).toContain('>RECORDING</span>')
    expect(html).not.toContain('Stop this recording before starting another.')
    expect(html).toContain('STOP RECORDING')
    expect(html).toContain('<span>CAPTURED EVENTS</span><strong>3</strong>')
    expect(html).toContain('<span>DURATION</span>')
    expect(html).not.toContain('<span>STARTED</span>')
    expect(html).toContain('<span>HISTORY</span>')
    expect(html).not.toContain('<small>1 RECORDING</small>')
    expect(history).toContain('<th>Recording</th>')
    expect(history).toContain('<th>Created at</th>')
    expect(history).toContain('<th>Duration</th>')
    expect(history).toContain('<th aria-label="Actions"></th>')
    expect(history).not.toContain('<th>Actions</th>')
    expect(history).toContain('<td class="recording-history__duration">00:05:00</td>')
    expect(history).toContain('<time dateTime="2026-08-28T12:00:00Z"')
    expect(history).toContain('completed-checkout')
    expect(history).not.toContain('checkout-debug')
    expect(html).not.toContain('class="recording-control-form"')
    expect(html).not.toContain('class="form-modal"')
  })

  it('keeps recording controls inline when no recording is active', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[]} refresh={async () => undefined} />)

    expect(html).toContain('<span>NEW RECORDING</span>')
    expect(html).not.toContain('<small>READY</small>')
    expect(html).toContain('class="recording-control-form"')
    expect(html).toContain('class="recording-control-form__primary"')
    expect(html).toContain('class="recording-payload-field" role="group"')
    expect(html).toContain('>PAYLOADS</span>')
    expect(html).toContain('class="recording-payload-toggle"')
    expect(html).toContain('<span>SCOPE</span>')
    expect(html).toContain('<span>INCLUDE</span>')
    expect(html).toContain('aria-label="Maximum payload size" disabled=""')
    expect(html).toContain('aria-label="About payload capture" aria-describedby="recording-payload-help"')
    expect(html).toContain('id="recording-payload-help" role="tooltip"')
    expect(html).toContain('Captured payloads are retained locally and may contain application data.')
    expect(html).not.toContain('recording-control-options')
    expect(html).toContain('name="portless-recording-name"')
    expect(html).toContain('data-1p-ignore="true"')
    expect(html).not.toContain('One recording can be active at a time.')
    expect(html).toContain('● START RECORDING')
    expect(html).toContain('No recording history yet. Completed recordings will appear here.')
    expect(html).not.toContain('CREATE RECORDING')
    expect(html).not.toContain('class="form-modal"')
  })

  it('shows the live captured-event counter in the active control', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[{ ...recording, eventCount: 1 }]} refresh={async () => undefined} />)

    expect(html).toContain('<span>CAPTURED EVENTS</span><strong>1</strong>')
  })

  it('formats and advances the active recording duration once per second', () => {
    expect(formatRecordingDuration('2026-08-28T12:00:00Z', Date.parse('2026-08-28T13:02:03Z'))).toBe('01:02:03')

    vi.spyOn(Date, 'now').mockReturnValueOnce(5_000).mockReturnValueOnce(6_000)
    let tick: () => void = () => undefined
    const scheduler = {
      setInterval: vi.fn((callback: () => void, milliseconds: number) => { tick = callback; expect(milliseconds).toBe(1000); return 42 }),
      clearInterval: vi.fn(),
    }
    const updates: number[] = []
    const stop = startRecordingDurationTimer((now) => updates.push(now), scheduler)

    expect(updates).toEqual([5_000])
    tick()
    expect(updates).toEqual([5_000, 6_000])
    stop()
    expect(scheduler.clearInterval).toHaveBeenCalledWith(42)
  })

  it('presents deletion as a named row action before confirmation', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[completedRecording]} refresh={async () => undefined} />)

    expect(html).toContain('completed-checkout')
    expect(html).toContain('aria-label="Delete all 1 completed recording"')
    expect(html).toContain('>DELETE ALL</button>')
    expect(html).toContain('aria-label="Record completed-checkout again"')
    expect(html).toContain('>RECORD AGAIN</button>')
    expect(html).toContain('/recordings/completed-checkout/export')
    expect(html).toContain('>EXPORT</a>')
    expect(html).toContain('aria-label="Delete completed-checkout"')
    expect(html).toContain('>DELETE</button>')
    expect(html).not.toContain('Confirm delete completed-checkout')
  })

  it('requires confirmation before recording a history row again', () => {
    const html = renderToStaticMarkup(<RecordingHistoryRepeatButton recordingName="completed-checkout" confirming starting={false} disabled={false} onClick={() => undefined} />)

    expect(html).toContain('class="recording-history__repeat is-confirming"')
    expect(html).toContain('aria-label="Confirm record completed-checkout again"')
    expect(html).toContain('>CONFIRM</button>')
  })

  it('disables repeat actions while another recording is active', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[recording, completedRecording]} refresh={async () => undefined} />)

    expect(html).toMatch(/<button[^>]*disabled=""[^>]*aria-label="Record completed-checkout again"[^>]*>RECORD AGAIN<\/button>/)
  })

  it('paginates recording history after six recordings', () => {
    const recordings = Array.from({ length: 7 }, (_, index) => ({
      ...completedRecording,
      name: `recording-${String(index + 1).padStart(2, '0')}`,
    }))
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={recordings} refresh={async () => undefined} />)

    expect(html).toContain('>recording-01</strong>')
    expect(html).toContain('>recording-05</strong>')
    expect(html).toContain('>recording-06</strong>')
    expect(html).not.toContain('>recording-07</strong>')
    expect(html).toContain('aria-label="recordings pagination"')
    expect(html).toContain('<span>1–6 of 7</span>')
    expect(html).toContain('<small>1 / 2</small>')
  })

  it('generates a repeated recording with the prior capture settings and an available name', () => {
    const previous = { ...completedRecording, capturePayloads: true, maxEvents: 5000, maxPayloadBytes: 100000 }
    const existingRepeat = { ...previous, name: 'completed-checkout-2' }

    expect(createRecordingDefaults([previous, existingRepeat], previous)).toEqual({
      name: 'completed-checkout-3',
      source: 'checkout',
      target: 'orders',
      capturePayloads: true,
      maxEvents: 5000,
      maxPayloadBytes: 100000,
    })
  })
})
