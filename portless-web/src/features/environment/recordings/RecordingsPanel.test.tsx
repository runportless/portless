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

describe('RecordingsPanel', () => {
  it('renders recordings as the page workspace and defers creation to a dialog action', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[recording]} refresh={async () => undefined} />)

    expect(html).toContain('class="recordings-page"')
    expect(html).toContain('class="panel experiment-list"')
    expect(html).toContain('CREATE RECORDING')
    expect(html).toContain('checkout-debug')
    expect(html).toContain('checkout → orders · 3 events')
    expect(html).not.toContain('class="panel experiment-form"')
    expect(html).not.toContain('class="form-modal"')
  })

  it('keeps an actionable empty list', () => {
    const html = renderToStaticMarkup(<RecordingsPanel environment={environment} recordings={[]} refresh={async () => undefined} />)

    expect(html).toContain('No recordings. Create one before reproducing a local issue.')
    expect(html).toContain('CREATE RECORDING')
  })
})
