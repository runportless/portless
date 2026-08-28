import { describe, expect, it } from 'vitest'
import type { Environment } from '../api/contracts/environments'
import type { Recording } from '../api/contracts/experiments'
import type { Service } from '../api/contracts/topology'
import { experimentScopes, preferredFaultScope, recordingScopeLabel } from './experimentScopes'

const service = (name: string, kind: Service['kind']): Service => ({ name, kind } as Service)

const environment = {
  project: 'store',
  name: 'local',
  primaryService: 'checkout',
  services: [
    service('checkout', 'process'),
    service('inventory', 'process'),
    service('orders', 'process'),
		service('postgres', 'resource'),
		service('redis', 'resource'),
  ],
  connections: [
    { source: 'checkout', target: 'inventory', protocol: 'http', required: true },
    { source: 'checkout', target: 'orders', protocol: 'http', required: true },
		{ source: 'orders', target: 'postgres', protocol: 'tcp', binding: 'postgres', required: true },
		{ source: 'orders', target: 'redis', protocol: 'tcp', binding: 'valkey', required: true },
  ],
} as Environment

describe('experiment scopes', () => {
  it('offers only directed connections from the accepted project graph', () => {
    expect(experimentScopes(environment)).toEqual([
      { id: 'external:checkout', source: 'external', target: 'checkout', protocol: 'http', label: 'external → checkout · HTTP' },
      { id: 'checkout:inventory', source: 'checkout', target: 'inventory', protocol: 'http', label: 'checkout → inventory · HTTP' },
      { id: 'checkout:orders', source: 'checkout', target: 'orders', protocol: 'http', label: 'checkout → orders · HTTP' },
		{ id: 'orders:postgres', source: 'orders', target: 'postgres', protocol: 'tcp', label: 'orders → postgres · POSTGRES' },
		{ id: 'orders:redis', source: 'orders', target: 'redis', protocol: 'tcp', label: 'orders → redis · VALKEY' },
    ])
    expect(experimentScopes(environment).some((scope) => scope.id === 'orders:checkout')).toBe(false)
  })

  it('prefers the primary service downstream connection for a new fault', () => {
    expect(preferredFaultScope(environment)?.id).toBe('checkout:inventory')
  })

  it('describes complete and partial recording scopes clearly', () => {
    expect(recordingScopeLabel({} as Recording)).toBe('all traffic')
    expect(recordingScopeLabel({ source: 'checkout' } as Recording)).toBe('checkout → any target')
    expect(recordingScopeLabel({ target: 'orders' } as Recording)).toBe('any source → orders')
    expect(recordingScopeLabel({ source: 'checkout', target: 'orders' } as Recording)).toBe('checkout → orders')
  })
})
