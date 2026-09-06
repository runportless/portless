import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficReplayWorkspace } from '../../../api/contracts/traffic_replay'
import { TrafficReplayResult } from './TrafficReplayResult'

it('renders replay JSON losslessly with the trace response header and representation tabs inside the pane', () => {
  const baseline: TrafficExchange = {
    project: 'store', environment: 'local', sequence: 1, source: 'external', target: 'checkout', background: false,
    startedAt: '2026-09-06T12:00:00Z', completedAt: '2026-09-06T12:00:00.012Z', requestBytes: 0, responseBytes: 52,
    protocol: 'http', status: 201, durationMs: 12, responseBody: '{"id":9007199254740993,"value":1.00,"value":2}', responseHeaders: { 'Content-Type': ['application/json'] },
  }
  const workspace: TrafficReplayWorkspace = { project: 'store', environment: 'local', number: 1, createdAt: '2026-09-06T12:00:00Z', daemonStartedAt: '2026-09-06T11:00:00Z', revision: 1, nextRunNumber: 1, baseline }
  const html = renderToStaticMarkup(<TrafficReplayResult workspace={workspace} outdated={false} />)
  expect(html).toContain('class="traffic-json__number">9007199254740993</span>')
  expect(html).toContain('class="traffic-json__number">1.00</span>')
  expect(html.match(/class="traffic-json__key">&quot;value&quot;/g)).toHaveLength(2)
  expect(html).toContain('>Body</button>')
  expect(html).toContain('>Headers</button>')
  expect(html).toContain('>Raw</button>')
  expect(html).toContain('<code>HTTP 201<span>Original · 12 ms</span></code>')
  expect(html).toContain('<span>application/json</span><span>52 B</span>')
  const heading = html.indexOf('traffic-message-workbench__summary')
  const tabs = html.indexOf('class="traffic-payload-tabs"')
  const body = html.indexOf('class="replay-response__body"')
  expect(heading).toBeGreaterThan(html.indexOf('class="replay-response"'))
  expect(tabs).toBeGreaterThan(heading)
  expect(body).toBeGreaterThan(tabs)
})
