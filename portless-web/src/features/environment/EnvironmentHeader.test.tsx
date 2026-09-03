import { createRef } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/contracts/environments'
import type { FaultRule, Recording } from '../../api/contracts/experiments'
import type { Service } from '../../api/contracts/topology'
import { EnvironmentActivityIndicators, EnvironmentHeaderActions, EnvironmentHeaderContext, EnvironmentOverviewHeading } from './EnvironmentHeader'
import { ForgetEnvironmentDialog } from './ForgetEnvironmentDialog'
import { environmentLifecycleLabel, type EnvironmentActions } from './useEnvironmentActions'

const environment: Environment = { project: 'billing', name: 'local', status: 'healthy', revision: 1, createdAt: '', updatedAt: '', services: [], connections: [] }
const actions: EnvironmentActions = { identity: 'billing/local', busy: null, error: null, forgetError: null, trackingInterrupted: false, disabled: false, run: async () => undefined, forget: async () => undefined, resumeTracking: () => undefined, dismissError: () => undefined, dismissForgetError: () => undefined }
const service: Service = { name: 'checkout', kind: 'process', launchMode: 'managed', required: true, health: { kind: 'http', timeout: 0, interval: 0 }, status: 'ready', generation: 1, restartCount: 0, recentRequests: 0, endpoints: [{ kind: 'public', protocol: 'http', host: 'checkout.local.billing.localhost', port: 80, url: 'http://checkout.local.billing.localhost' }] }

describe('overview environment heading', () => {
  it('shows the environment name and clone provenance without inactive indicators', () => {
    const render = (value: Environment) => renderToStaticMarkup(<EnvironmentOverviewHeading environment={value} activeFaultCount={0} onNavigate={() => undefined} />)
    const markup = render({ ...environment, name: 'qa-preview', clonedFrom: 'local' })

    expect(markup).toContain('aria-label="billing/qa-preview overview summary"')
    expect(markup).toContain('<h2>qa-preview</h2>')
    expect(markup).toContain('title="Created by cloning billing/local; changes are independent."')
    expect(markup).toContain('FROM <strong>local</strong>')
    expect(markup).not.toContain('environment-activity-indicators')
    expect(render(environment)).not.toContain('environment-clone-origin')
  })

  it('links live activity and lists each bound mock scenario once', () => {
    const value: Environment = { ...environment, bindings: [
      { service: 'checkout', provider: 'mock', mock: { scenario: 'sold-out' } },
      { service: 'orders', provider: 'mock', mock: { scenario: 'sold-out' } },
      { service: 'inventory', provider: 'mock', mock: { scenario: 'slow-inventory' } },
      { service: 'restored', provider: 'local', mock: { scenario: 'old-scenario' } },
    ] }
    const markup = renderToStaticMarkup(<EnvironmentOverviewHeading environment={value} activeRecording={{ name: 'checkout-flow', status: 'active' } as Recording} activeFaultCount={2} onNavigate={() => undefined} />)

    expect(markup).toContain('aria-label="Recording checkout-flow. Open recordings"')
    expect(markup).toContain('aria-label="2 active faults. Open faults"')
    expect(markup).toContain('href="/environments/billing/local?tab=mocks&amp;scenario=sold-out"')
    expect(markup.match(/aria-label="Active mock scenario sold-out\. Open scenario"/g)).toHaveLength(1)
    expect(markup).toContain('aria-label="Active mock scenario slow-inventory. Open scenario"')
    expect(markup.indexOf('scenario=slow-inventory')).toBeLessThan(markup.indexOf('scenario=sold-out'))
    expect(markup).not.toContain('old-scenario')
    expect(markup).toContain('REC <span class="recording-indicator__name">checkout-flow</span>')
    expect(markup).toContain('<span class="fault-indicator__active">ACTIVE </span>FAULTS')
    expect(markup).toContain('MOCK <span class="mock-indicator__name">sold-out</span>')
    expect(markup).not.toContain('environment-activity-indicators--icons')
  })
})

describe('persistent environment header', () => {
  it.each(['healthy', 'starting', 'recovering', 'stopping', 'degraded', 'failed', 'stopped', 'unknown'] as const)('preserves authoritative %s health and counts all services', (status) => {
    const markup = renderToStaticMarkup(<EnvironmentHeaderContext environment={{ ...environment, status, services: [service, { ...service, name: 'optional', required: false, status: 'stopped' }] }} live onNavigate={() => undefined} />)
    expect(markup).toContain(`health: ${status}; 1/2 ready. Open service overview`)
    expect(markup).toContain(`title="${status}"`)
    expect(markup).toContain('href="/environments/billing/local"')
  })

  it('marks a stale empty snapshot and preserves clone provenance', () => {
    const markup = renderToStaticMarkup(<EnvironmentHeaderContext environment={{ ...environment, clonedFrom: 'qa' }} live={false} onNavigate={() => undefined} />)
    expect(markup).toContain('health: reconnecting, last known healthy; No services.')
    expect(markup).toContain('>RECONNECTING</span>')
    expect(markup).not.toContain('0/0 ready')
    expect(markup).toContain('title="Created by cloning billing/qa; changes are independent."')
  })

  it('opens only the primary public HTTP endpoint', () => {
    const render = (value: Environment) => renderToStaticMarkup(<EnvironmentHeaderActions environment={value} activity={{ recordings: [], faults: [] }} actions={actions} live onNavigate={() => undefined} />)
    expect(render({ ...environment, primaryService: 'checkout', services: [service] })).toContain('aria-label="OPEN APP" href="http://checkout.local.billing.localhost" target="_blank" rel="noreferrer"')
    expect(render({ ...environment, services: [service] })).not.toContain('OPEN APP')
    expect(render({ ...environment, primaryService: 'worker', services: [service] })).not.toContain('OPEN APP')
    expect(render({ ...environment, primaryService: 'checkout', services: [{ ...service, endpoints: [] }] })).not.toContain('OPEN APP')
    expect(render({ ...environment, primaryService: 'checkout', services: [{ ...service, endpoints: [{ kind: 'public', protocol: 'tcp', host: 'db.local.billing.localhost', port: 5432, url: 'tcp://db.local.billing.localhost:5432' }] }] })).not.toContain('OPEN APP')
  })

  it('uses the pending operation label even before the environment snapshot changes', () => {
    expect(environmentLifecycleLabel(environment, 'down')).toBe('STOPPING…')
    expect(environmentLifecycleLabel({ status: 'stopped' }, 'up')).toBe('STARTING…')
    expect(environmentLifecycleLabel({ status: 'recovering' }, null)).toBe('RECOVERING…')
    const markup = renderToStaticMarkup(<EnvironmentHeaderActions environment={environment} activity={{ recordings: [], faults: [] }} actions={{ ...actions, busy: 'down', disabled: true }} live onNavigate={() => undefined} />)
    expect(markup).toContain('aria-label="STOPPING…" disabled=""')
    expect(markup).toContain('aria-expanded="false" disabled=""')
  })

  it('links active recording and fault indicators to their environment views', () => {
    const activeRecording = { name: 'checkout-flow', status: 'active' } as Recording
    const markup = renderToStaticMarkup(<EnvironmentActivityIndicators environment={environment} activeRecording={activeRecording} activeFaultCount={2} onNavigate={() => undefined} />)
    expect(markup).toContain('href="/environments/billing/local?tab=recordings"')
    expect(markup).toContain('aria-label="Recording checkout-flow. Open recordings"')
    expect(markup).toContain('href="/environments/billing/local?tab=faults"')
    expect(markup).toContain('aria-label="2 active faults. Open faults"')
    expect(renderToStaticMarkup(<EnvironmentActivityIndicators environment={environment} activeFaultCount={0} onNavigate={() => undefined} />)).toBe('')
  })

  it('uses icon-only top-bar links with descriptive names and tooltips', () => {
    const value: Environment = { ...environment, bindings: [
      { service: 'checkout', provider: 'mock', mock: { scenario: 'sold-out' } },
      { service: 'orders', provider: 'mock', mock: { scenario: 'sold-out' } },
    ] }
    const activity = {
      recordings: [{ name: 'old-capture', status: 'completed' }, { name: 'checkout-flow', status: 'active' }] as Recording[],
      faults: [{ enabled: true }, { enabled: false }] as FaultRule[],
    }
    const markup = renderToStaticMarkup(<EnvironmentHeaderActions environment={value} activity={activity} actions={actions} live onNavigate={() => undefined} />)
    const links = [...markup.matchAll(/<a class="(?:recording|fault|mock)-indicator"[^>]*>(.*?)<\/a>/g)]

    expect(markup).toContain('environment-activity-indicators--icons')
    expect(links).toHaveLength(3)
    for (const [, content] of links) {
      expect(content).toContain('<svg viewBox="0 0 16 16" aria-hidden="true">')
      expect(content.replace(/<[^>]+>/g, '')).toBe('')
    }
    expect(markup).toContain('aria-label="Recording checkout-flow. Open recordings" title="Recording checkout-flow"')
    expect(markup).toContain('aria-label="1 active fault. Open faults" title="1 active fault"')
    expect(markup).toContain('href="/environments/billing/local?tab=mocks&amp;scenario=sold-out" aria-label="Active mock scenario sold-out. Open scenario" title="Active mock scenario: sold-out"')
    expect(markup).not.toContain('old-capture')
  })

  it('groups multiple bound scenarios into one icon that opens the scenario list', () => {
    const value: Environment = { ...environment, bindings: [
      { service: 'checkout', provider: 'mock', mock: { scenario: 'sold-out' } },
      { service: 'orders', provider: 'mock', mock: { scenario: 'sold-out' } },
      { service: 'inventory', provider: 'mock', mock: { scenario: 'slow-inventory' } },
      { service: 'restored', provider: 'local', mock: { scenario: 'old-scenario' } },
    ] }
    const markup = renderToStaticMarkup(<EnvironmentHeaderActions environment={value} activity={{ recordings: [], faults: [] }} actions={actions} live onNavigate={() => undefined} />)

    expect(markup.match(/class="mock-indicator"/g)).toHaveLength(1)
    expect(markup).toContain('href="/environments/billing/local?tab=mocks" aria-label="2 active mock scenarios. Open mocks"')
    expect(markup).toContain('title="2 active mock scenarios: slow-inventory, sold-out"')
    expect(markup).not.toContain('old-scenario')
  })

  it.each([1, 2])('keeps the active fault count (%i) in its icon label and tooltip', (count) => {
    const markup = renderToStaticMarkup(<EnvironmentActivityIndicators environment={environment} activeFaultCount={count} appearance="icons" onNavigate={() => undefined} />)
    const summary = `${count} active ${count === 1 ? 'fault' : 'faults'}`
    expect(markup).toContain(`aria-label="${summary}. Open faults" title="${summary}"`)
    expect(markup).not.toContain('fault-indicator__active')
  })

  it('omits inactive recording, fault, and restored mock icons', () => {
    const value: Environment = { ...environment, bindings: [{ service: 'orders', provider: 'local', mock: { scenario: 'old-scenario' } }] }
    const markup = renderToStaticMarkup(<EnvironmentHeaderActions environment={value} activity={{ recordings: [{ status: 'completed' } as Recording], faults: [{ enabled: false } as FaultRule] }} actions={actions} live onNavigate={() => undefined} />)
    expect(markup).not.toContain('environment-activity-indicators')
    expect(markup).not.toContain('old-scenario')
  })

  it('keeps forgetting behind an impact preview and blocks it when running or disconnected', () => {
    const props = { busy: false, error: null, restoreFocusRef: createRef<HTMLElement>(), onDismissError: () => undefined, onClose: () => undefined, onForget: async () => undefined }
    const header = renderToStaticMarkup(<EnvironmentHeaderActions environment={environment} activity={{ recordings: [], faults: [] }} actions={actions} live onNavigate={() => undefined} />)
    const stopped = renderToStaticMarkup(<ForgetEnvironmentDialog {...props} environment={{ ...environment, status: 'stopped' }} />)
    const running = renderToStaticMarkup(<ForgetEnvironmentDialog {...props} environment={environment} />)
    const disconnected = renderToStaticMarkup(<ForgetEnvironmentDialog {...props} environment={{ ...environment, status: 'stopped' }} unavailable />)
    expect(header).toContain('aria-label="Environment actions for billing/local" aria-haspopup="menu" aria-expanded="false"')
    expect(header).not.toContain('>FORGET ENVIRONMENT</button>')
    expect(stopped).toContain('Source files and checkouts on disk are not deleted.')
    expect(stopped).toContain('Source checkouts and managed data volumes')
    expect(stopped).toContain('<button class="button button--danger" type="button">FORGET ENVIRONMENT</button>')
    expect(running).toContain('Stop this environment before forgetting it.')
    expect(disconnected).toContain('Wait for the control plane to reconnect')
    for (const markup of [running, disconnected]) expect(markup).toContain('<button class="button button--danger" type="button" disabled="">FORGET ENVIRONMENT</button>')
  })
})
