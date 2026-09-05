import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import type { Service } from '../../../api/contracts/topology'
import { mockScenarioFor } from '../service/servicePresentation'
import { TopologyCanvas, topologyServicePreviewDetails } from './TopologyCanvas'
import { buildTopology } from './topologyModel'

const service: Service = {
  name: 'inventory', kind: 'process', framework: 'spring-boot', required: true, launchMode: 'managed',
  health: { kind: 'http', timeout: 5, interval: 2 }, status: 'ready', generation: 1, restartCount: 0, recentRequests: 0,
  endpoints: [{ kind: 'public', protocol: 'http', host: 'inventory.local.store.localhost', port: 80, url: 'http://inventory.local.store.localhost' }],
}
const environment: Environment = {
  project: 'store', name: 'local', revision: 1, status: 'healthy', createdAt: '', updatedAt: '',
  primaryService: 'inventory', services: [service], connections: [],
  bindings: [{ service: 'inventory', provider: 'mock', mock: { scenario: 'sold-out' } }],
}
const render = (value: Environment) => renderToStaticMarkup(<TopologyCanvas environment={value} faults={[]} paused centerRequest={0} onService={() => undefined} onEdge={() => undefined} />)

describe('topology mock indicators', () => {
  it('adds a compact mock badge while retaining service identity, endpoint, and readiness', () => {
    const markup = render(environment)
    expect(markup).toContain('class="topology-node__mock">MOCK</span>')
    expect(markup).toContain('class="topology-node__framework">spring-boot</span>')
    expect(markup).toContain('<strong>inventory</strong><small>inventory.local.store.localhost</small>')
    expect(markup).toContain('class="status status--success" title="ready"')
  })

  it('only marks the service currently bound to a mock scenario', () => {
    const local = { ...environment, bindings: [{ service: 'inventory', provider: 'local' as const }] }
    expect(render(local)).not.toContain('topology-node__mock')
    expect(render({ ...environment, bindings: [] })).not.toContain('topology-node__mock')
    expect(render({ ...environment, bindings: [{ service: 'orders', provider: 'mock', mock: { scenario: 'sold-out' } }] })).not.toContain('topology-node__mock')
    expect(mockScenarioFor({ bindings: [{ service: 'inventory', provider: 'local', mock: { scenario: 'sold-out' } }] }, 'inventory')).toBeUndefined()
  })

  it('keeps the mock provider separate from an unhealthy service status', () => {
    const markup = render({ ...environment, services: [{ ...service, status: 'unhealthy' }] })
    expect(markup).toContain('class="topology-node__mock">MOCK</span>')
    expect(markup).toContain('class="status status--warning" title="unhealthy"')
  })

  it('includes the bound scenario in preview details and removes it after restoration', () => {
    const edges = buildTopology(environment).edges
    expect(topologyServicePreviewDetails(environment, service, edges, [])).toMatchObject({
      type: 'spring-boot', mode: 'mock', mockScenario: 'sold-out', endpoint: 'http://inventory.local.store.localhost',
    })
    const restored = { ...environment, bindings: [{ service: 'inventory', provider: 'local' as const }] }
    expect(topologyServicePreviewDetails(restored, service, edges, [])).toMatchObject({ mode: 'managed', mockScenario: undefined })
  })

  it('does not present healthy mock metadata as an error, but preserves readiness failures and faults', () => {
    const edges = buildTopology(environment).edges
    const ready = { ...service, reason: 'mock scenario sold-out' }
    const faults: FaultRule[] = [{
      project: 'store', environment: 'local', name: 'inventory-delay', source: 'checkout', target: 'inventory', enabled: true,
      probability: 1, latencyMs: 100, enabledAt: '', createdAt: '', matchCount: 0, revision: 1, scopeSummary: 'checkout → inventory',
    }]
    expect(topologyServicePreviewDetails(environment, ready, edges, faults)).toMatchObject({
      mockScenario: 'sold-out', reason: undefined, fault: 'inventory-delay on checkout → inventory',
    })
    expect(topologyServicePreviewDetails(environment, { ...ready, status: 'unhealthy', reason: 'mock provider unavailable' }, edges, [])).toMatchObject({
      mockScenario: 'sold-out', reason: 'mock provider unavailable',
    })
  })
})
