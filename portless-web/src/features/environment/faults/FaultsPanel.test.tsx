import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../../api/contracts/environments'
import type { FaultRule } from '../../../api/contracts/experiments'
import { httpErrorStatusGroups } from '../../httpStatuses'
import { CreateFaultModal, FaultHTTPStatusField, FaultsPanel, nextFaultName, sortFaults } from './FaultsPanel'

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

const fault = (name: string, enabled: boolean, matchCount = 0, overrides: Partial<FaultRule> = {}): FaultRule => ({
  project: 'store', environment: 'local', name, source: 'checkout', target: 'orders', probability: 1,
  latencyMs: 2000, enabled, enabledAt: '2026-08-29T13:30:00Z', createdAt: '2026-08-29T12:00:00Z', matchCount, revision: 1,
  scopeSummary: 'checkout → orders · latency 2000ms',
  ...overrides,
})

describe('FaultsPanel', () => {
  it('makes the fault list the page and opens creation from a primary action', () => {
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[]} refresh={async () => undefined} />)

    expect(html).toContain('class="faults-page"')
    expect(html).toContain('<span>FAULTS</span>')
    expect(html).toMatch(/<button[^>]*class="button button--primary button--small panel-create-button"[^>]*>CREATE FAULT<\/button>/)
    expect(html).toContain('class="fault-table"')
    expect(html).toContain('<tr class="sortable-header-row is-default-sort">')
    expect(html).toContain('aria-sort="ascending"><span>State</span>')
    expect(html).toContain('aria-sort="none"><span>Name</span>')
    for (const label of ['Connection', 'Fault', 'Matches', 'Lifetime', 'Enabled at', 'Created at']) {
      expect(html).toContain(`aria-sort="none"><span>${label}</span>`)
    }
    expect(html.match(/class="sortable-table-header/g)).toHaveLength(8)
    expect(html).not.toContain('sortable-column-sort-control')
    expect(html).toContain('<th aria-label="Actions"></th>')
    expect(html).not.toContain('Sort Actions')
    expect(html).toContain('colSpan="9"')
    expect(html).toContain('No faults. Create one to simulate latency, errors, or dropped connections.')
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
    expect(html).toContain('class="fault-table__connection" title="checkout → orders">checkout → orders</td><td class="fault-table__effect">Latency · 2,000 ms</td>')
    expect(html).toContain('class="fault-table__enabled"><time dateTime="2026-08-29T13:30:00Z"')
    expect(html).toContain('class="fault-table__enabled">—</td>')
    expect(html).toContain('class="fault-table__created"><time dateTime="2026-08-29T12:00:00Z"')
    expect(html).not.toContain('checkout → orders · latency 2000ms')
    expect(html).toContain('>until disabled</td>')
    expect(html).toContain('<td>1</td>')
    expect(html).not.toContain('fault-row--active')
    expect(html).toContain('class="faults-bulk-actions"')
    expect(html).toContain('class="faults-disable-all-link"')
    expect(html.indexOf('CREATE FAULT')).toBeLessThan(html.indexOf('DISABLE ALL'))
    expect(html).toContain('>DISABLE</button>')
    expect(html).toContain('>ENABLE</button>')
    expect(html.match(/class="sortable-column-sort-control"/g)).toHaveLength(8)
  })

  it('shows the subheader action for declared rules and disables it when none are active', () => {
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[fault('old-latency', false)]} refresh={async () => undefined} />)

    expect(html).toMatch(/class="faults-bulk-actions"><button[^>]*class="faults-disable-all-link"[^>]*disabled=""[^>]*>DISABLE ALL<\/button>/)
  })

  it('keeps fault lifecycle visible and moves deletion into a named row menu', () => {
    const html = renderToStaticMarkup(<FaultsPanel environment={environment} faults={[fault('slow-orders', false)]} refresh={async () => undefined} />)

    expect(html).toContain('>ENABLE</button>')
    expect(html).toContain('aria-label="Fault actions for slow-orders"')
    expect(html).toContain('aria-haspopup="menu"')
    expect(html).not.toContain('aria-label="Delete slow-orders"')
    expect(html).not.toContain('>DELETE</button>')
    expect(html).not.toContain('Confirm delete slow-orders')
  })

  it('suggests an available default fault name', () => {
    expect(nextFaultName([fault('slow-downstream', false), fault('slow-downstream-2', false)])).toBe('slow-downstream-3')
  })

  it('sorts rules by every data column in either direction', () => {
    const faults = [
      fault('disabled', false, 7, {
        source: 'zeta', target: 'orders', latencyMs: 0, statusCode: 503,
        createdAt: '2026-08-29T14:00:00Z',
      }),
      fault('expiring', true, 2, {
        source: 'alpha', target: 'inventory', latencyMs: 0, abort: true,
        enabledAt: '2026-08-29T12:00:00Z', createdAt: '2026-08-29T11:00:00Z', expiresAt: '2026-08-29T15:00:00Z',
      }),
      fault('ongoing', true, 11, {
        source: 'alpha', target: 'payments', latencyMs: 100,
        enabledAt: '2026-08-29T13:00:00Z', createdAt: '2026-08-29T13:00:00Z',
      }),
    ]
    const names = (key: Parameters<typeof sortFaults>[1]['key'], direction: 'asc' | 'desc') => sortFaults(faults, { key, direction }).map((item) => item.name)

    expect(names('state', 'asc')).toEqual(['expiring', 'ongoing', 'disabled'])
    expect(names('state', 'desc')).toEqual(['disabled', 'expiring', 'ongoing'])
    expect(names('name', 'asc')).toEqual(['disabled', 'expiring', 'ongoing'])
    expect(names('name', 'desc')).toEqual(['ongoing', 'expiring', 'disabled'])
    expect(names('connection', 'asc')).toEqual(['expiring', 'ongoing', 'disabled'])
    expect(names('connection', 'desc')).toEqual(['disabled', 'ongoing', 'expiring'])
    expect(names('fault', 'asc')).toEqual(['expiring', 'disabled', 'ongoing'])
    expect(names('fault', 'desc')).toEqual(['ongoing', 'disabled', 'expiring'])
    expect(names('matches', 'asc')).toEqual(['expiring', 'disabled', 'ongoing'])
    expect(names('matches', 'desc')).toEqual(['ongoing', 'disabled', 'expiring'])
    expect(names('lifetime', 'asc')).toEqual(['disabled', 'expiring', 'ongoing'])
    expect(names('lifetime', 'desc')).toEqual(['ongoing', 'expiring', 'disabled'])
    expect(names('enabledAt', 'asc')).toEqual(['disabled', 'expiring', 'ongoing'])
    expect(names('enabledAt', 'desc')).toEqual(['ongoing', 'expiring', 'disabled'])
    expect(names('createdAt', 'asc')).toEqual(['expiring', 'ongoing', 'disabled'])
    expect(names('createdAt', 'desc')).toEqual(['disabled', 'ongoing', 'expiring'])
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
    expect(html).toContain('<legend>FAULT TYPE</legend>')
    expect(html).toContain('class="fault-type-options"')
    expect(html).toContain('<strong>Latency</strong><small>Delay the response</small>')
    expect(html).toContain('<strong>HTTP status</strong><small>Return an error</small>')
    expect(html).toContain('<strong>Abort</strong><small>Close the connection</small>')
  })

  it('offers only registered HTTP error statuses for a status fault', () => {
    const html = renderToStaticMarkup(<FaultHTTPStatusField value="503" disabled={false} onChange={() => undefined} />)
    const codes: readonly number[] = httpErrorStatusGroups.flatMap((group) => group.statuses.map(([code]) => code))

    expect(html).toContain('<select aria-label="HTTP status" required="">')
    expect(html).toContain('<optgroup label="4xx Client Error">')
    expect(html).toContain('<option value="404">404 · Not Found</option>')
    expect(html).toContain('<option value="503" selected="">503 · Service Unavailable</option>')
    expect(html).not.toContain('type="number"')
    expect(codes).not.toContain(200)
    expect(codes).not.toContain(499)
    expect(codes).not.toContain(599)
  })
})
