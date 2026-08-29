import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { Recording } from '../../../api/contracts/experiments'
import { RecordingsPanel } from './RecordingsPanel'

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

describe('RecordingsPanel', () => {
  it('makes the active recording primary and explains why another cannot be created', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[recording]} refresh={async () => undefined} />)

    expect(html).toContain('class="recordings-page"')
    expect(html).toContain('class="panel experiment-list"')
    expect(html).toContain('CREATE RECORDING')
    expect(html).toContain('id="recording-create-unavailable"')
    expect(html).toContain('Stop checkout-debug before creating another recording.')
    expect(html).toContain('aria-describedby="recording-create-unavailable"')
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*aria-describedby="recording-create-unavailable"[^>]*>CREATE RECORDING<\/button>/)
    expect(html).toContain('class="experiment-row recording-row recording-row--active"')
    expect(html).toContain('>RECORDING</span>')
    expect(html).not.toContain('RECORDING NOW')
    expect(html).toContain('STOP RECORDING')
    expect(html).toContain('checkout-debug')
    expect(html).toContain('checkout → orders · 3 events')
    expect(html).not.toContain('class="panel experiment-form"')
    expect(html).not.toContain('class="form-modal"')
  })

  it('keeps an actionable empty list', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[]} refresh={async () => undefined} />)

    expect(html).toContain('No recordings. Create one before reproducing a local issue.')
    expect(html).toContain('CREATE RECORDING')
    expect(html).not.toContain('recording-create-unavailable')
    expect(html).not.toMatch(/<button[^>]*disabled=""[^>]*>CREATE RECORDING<\/button>/)
  })

  it('uses a singular event label for the first captured exchange', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[{ ...recording, eventCount: 1 }]} refresh={async () => undefined} />)

    expect(html).toContain('checkout → orders · 1 event')
    expect(html).not.toContain('1 events')
  })

  it('presents deletion as a named row action before confirmation', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[completedRecording]} refresh={async () => undefined} />)

    expect(html).toContain('completed-checkout')
    expect(html).toContain('aria-label="Delete completed-checkout"')
    expect(html).toContain('>DELETE</button>')
    expect(html).not.toContain('Confirm delete completed-checkout')
  })
})
