import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficReplayDraft, TrafficReplayWorkspace } from '../../../api/contracts/traffic_replay'
import { TrafficReplayEditor } from './TrafficReplayEditor'
import { replayDestinationReason, replayRequestDraft } from './trafficReplayDraft'
import { createTrafficReplaySession } from './useTrafficReplay'

const environment: Environment = {
  project: 'store', name: 'local', status: 'healthy', revision: 1, createdAt: '', updatedAt: '', connections: [],
  services: [{ name: 'checkout', kind: 'process', launchMode: 'managed', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, status: 'ready', generation: 1, restartCount: 0, recentRequests: 0, endpoints: [{ kind: 'public', protocol: 'http', url: 'http://checkout.local.store.localhost', host: 'checkout.local.store.localhost', port: 80 }], mock: { scenario: 'partial', unmatchedRequests: 'forward', state: 'enabled' } }],
  bindings: [{ service: 'checkout', provider: 'remote', remote: { url: 'https://checkout.qa.test', classification: 'qa', writePolicy: 'read-only' } }],
}
const baseline: TrafficExchange = { project: 'store', environment: 'local', sequence: 1, source: 'external', target: 'checkout', background: false, protocol: 'http', method: 'GET', startedAt: '', completedAt: '', requestBytes: 0, responseBytes: 0, durationMs: 0, status: 200 }

it('shows the prepared mock destination instead of the retained remote provider', () => {
  const draft: TrafficReplayDraft = { environment: 'local', method: 'GET', requestTarget: '/mock', headers: {}, bodyMode: 'empty', body: '' }
  const workspace: TrafficReplayWorkspace = { project: 'store', environment: 'local', number: 1, createdAt: '', daemonStartedAt: '', revision: 1, nextRunNumber: 1, baseline, draft, destination: { environment: 'local', provider: 'mock', mockScenario: 'partial', mockRoute: 'fixed', url: 'http://checkout.local.store.localhost', requiresConfirmation: false } }
  const session = createTrafficReplaySession(environment)
  const replay = { ...session, state: { ...session.snapshot(), workspace, draft: replayRequestDraft(draft) } }
  const html = renderToStaticMarkup(<TrafficReplayEditor replay={replay} environments={[environment]} onClose={() => undefined} />)
  expect(html).toContain('<strong>mock</strong>')
  expect(html).toContain('Mock response: partial / fixed')
  const edited = { ...replay, state: { ...replay.state, draft: { ...replay.state.draft, requestTarget: '/real' } } }
  const changed = renderToStaticMarkup(<TrafficReplayEditor replay={edited} environments={[environment]} onClose={() => undefined} />)
  expect(changed).not.toContain('Mock response: partial / fixed')
  expect(changed).toContain('remote · qa · read-only')
  session.dispose()
})

it('permits preparing a surviving partial mock after a crash but respects explicit stop', () => {
  const service = environment.services[0]
  expect(replayDestinationReason({ ...environment, services: [{ ...service, status: 'failed' }] }, baseline)).toBe('')
  expect(replayDestinationReason({ ...environment, services: [{ ...service, status: 'stopped' }] }, baseline)).toBe('Target service stopped')
  expect(replayDestinationReason({ ...environment, services: [{ ...service, status: 'failed', mock: undefined }] }, baseline)).toBe('Target service failed')
})
