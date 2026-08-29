import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { Service } from '../../../api/contracts/topology'
import { ServiceDrawer } from './ServiceDrawer'

describe('service drawer', () => {
  it('groups service details into dense, titled drawer panels', () => {
    const service = {
      name: 'checkout',
      kind: 'process',
      framework: 'nestjs',
      command: ['npm', 'run', 'start:dev'],
      required: true,
      health: { kind: 'tcp', timeout: 5, interval: 2 },
      debug: { adapter: 'node-inspector', launcher: 'nest-cli', command: ['npm', 'run', 'start:debug'] },
      launchMode: 'managed',
      status: 'ready',
      generation: 10,
      pid: 76723,
      upstreamPort: 53902,
      endpoints: [{ kind: 'public', protocol: 'http', host: 'checkout.local.store.localhost', port: 80, url: 'http://checkout.local.store.localhost' }],
      startedAt: '2026-08-28T12:00:00Z',
      restartCount: 0,
      recentRequests: 0,
    } as Service
    const environment = {
      project: 'store', name: 'local', revision: 1, status: 'healthy',
      createdAt: '2026-08-28T12:00:00Z', updatedAt: '2026-08-28T12:00:00Z',
      services: [service], connections: [], bindings: [{ service: 'checkout', provider: 'local' }],
    } as Environment

    const markup = renderToStaticMarkup(<ServiceDrawer environment={environment} service={service} onClose={() => undefined} onChanged={() => undefined} />)

    expect(markup).toContain('class="drawer service-drawer"')
    expect(markup).toContain('class="drawer-content service-drawer-content service-drawer-content--details"')
    expect(markup).toContain('>SERVICE IDENTITY<')
    expect(markup).toContain('class="detail-grid service-detail-grid"')
    expect(markup).toContain('class="service-detail--wide"')
    expect(markup).toContain('class="drawer-section service-endpoints"')
    expect(markup).toContain('class="drawer-section service-command"')
    expect(markup).toContain('class="drawer-section service-health"')
    expect(markup).toContain('class="service-health-summary"')
    expect(markup).toContain('http://checkout.local.store.localhost')
  })
})
