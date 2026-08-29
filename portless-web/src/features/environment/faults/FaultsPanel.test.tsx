import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import { CreateFaultModal, FaultsPanel, nextFaultName } from './FaultsPanel'

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

const fault = (name: string, enabled: boolean, matchCount = 0): FaultRule => ({
  project: 'store', environment: 'local', name, source: 'checkout', target: 'orders', probability: 1,
  latencyMs: 2000, enabled, createdAt: '2026-08-29T12:00:00Z', matchCount, revision: 1,
  scopeSummary: 'checkout → orders · latency 2000ms',
})

describe('FaultsPanel', () => {
  it('makes the fault list the page and opens creation from a primary action', () => {
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[]} refresh={async () => undefined} />)

    expect(html).toContain('class="faults-page"')
    expect(html).toContain('<span>FAULTS</span>')
    expect(html).toMatch(/<button[^>]*class="button button--primary button--small panel-create-button"[^>]*>CREATE FAULT<\/button>/)
    expect(html).toContain('class="fault-table"')
    expect(html).toContain('<th>State</th><th>Fault</th><th>Connection / effect</th><th>Lifetime</th><th>Matches</th><th>Actions</th>')
    expect(html).toContain('No faults. Create one to simulate latency, errors, or dropped connections.')
    expect(html).not.toContain('faults-bulk-actions')
    expect(html).not.toContain('DISABLE ALL')
    expect(html).not.toContain('INTRODUCE FAILURE')
    expect(html).not.toContain('class="panel experiment-form"')
    expect(html).not.toContain('class="form-modal')
  })

  it('puts active rules first and distinguishes reusable disabled rules', () => {
    const disabled = fault('old-latency', false)
    const active = fault('current-latency', true, 1)
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[disabled, active]} refresh={async () => undefined} />)

    expect(html.indexOf('current-latency')).toBeLessThan(html.indexOf('old-latency'))
    expect(html).toContain('class="fault-table__state is-active"')
    expect(html).toContain('>active</span>')
    expect(html).toContain('>disabled</span>')
    expect(html).toContain('>until disabled</td>')
    expect(html).toContain('<td>1</td>')
    expect(html).not.toContain('fault-row--active')
    expect(html).toContain('class="faults-bulk-actions"')
    expect(html.indexOf('CREATE FAULT')).toBeLessThan(html.indexOf('DISABLE ALL'))
    expect(html).toContain('>DISABLE</button>')
    expect(html).toContain('>ENABLE</button>')
  })

  it('shows the bulk action for declared rules and disables it when none are active', () => {
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[fault('old-latency', false)]} refresh={async () => undefined} />)

    expect(html).toMatch(/class="faults-bulk-actions"><button[^>]*disabled=""[^>]*>DISABLE ALL<\/button>/)
  })

  it('requires a named second action before deleting a rule', () => {
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[fault('slow-orders', false)]} refresh={async () => undefined} />)

    expect(html).toContain('aria-label="Delete slow-orders"')
    expect(html).toContain('>DELETE</button>')
    expect(html).not.toContain('Confirm delete slow-orders')
  })

  it('suggests an available default fault name', () => {
    expect(nextFaultName([fault('slow-downstream', false), fault('slow-downstream-2', false)])).toBe('slow-downstream-3')
  })

  it('keeps password managers away from the fault name field', () => {
    const html = renderToStaticMarkup(<CreateFaultModal
      environment={environment}
      initialName="slow-downstream"
      busy={false}
      error={null}
      onDismissError={() => undefined}
      onClose={() => undefined}
      onCreate={async () => undefined}
    />)

    expect(html).toContain('<form autoComplete="off" data-1p-ignore="true"')
    expect(html).toMatch(/<input(?=[^>]*name="portless-fault-name")(?=[^>]*data-1p-ignore="true")[^>]*>/)
  })
})
