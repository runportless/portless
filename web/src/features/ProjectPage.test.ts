import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Environment, Service } from '../types'
import { buildTopology, EnvironmentPage } from './ProjectPage'

const service = (name: string): Service => ({ name } as Service)

describe('environment topology', () => {
  it('renders as a bounded panel with a maximize control', () => {
    const environment = {
      project: 'billing', name: 'local', status: 'healthy', revision: 1,
      createdAt: new Date(0).toISOString(), updatedAt: new Date(0).toISOString(),
      services: [], connections: [],
    } as Environment

    const markup = renderToStaticMarkup(createElement(EnvironmentPage, {
      environment, tab: 'overview', onNavigate: () => undefined, onChanged: () => undefined,
    }))

    expect(markup).toContain('class="panel topology-panel"')
    expect(markup).toContain('aria-label="Maximize topology"')
    expect(markup).toContain('aria-pressed="false"')
    expect(markup).toContain('class="topology-size-button"')
    expect(markup).toContain('aria-label="Topology canvas; drag to pan"')
    expect(markup).toContain('tabindex="0"')
    expect(markup).not.toContain('>MAXIMIZE<')
  })

  it('lays out services by their directed dependencies', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'orders', 'redis', 'postgres'].map(service),
      connections: [
        { source: 'checkout', target: 'orders', protocol: 'http', required: true },
        { source: 'orders', target: 'redis', protocol: 'redis', required: true },
        { source: 'orders', target: 'postgres', protocol: 'postgres', required: true },
      ],
    } as Environment

    const topology = buildTopology(environment)

    expect(topology.levels.map((level) => level.map((item) => item.key))).toEqual([
      ['external'],
      ['checkout'],
      ['orders'],
      ['redis', 'postgres'],
    ])
    expect(topology.edges.map(({ source, target }) => `${source}:${target}`)).toEqual([
      'external:checkout',
      'checkout:orders',
      'orders:redis',
      'orders:postgres',
    ])
  })

  it('does not flatten disconnected services beneath the client', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'worker'].map(service),
      connections: [],
    } as unknown as Environment

    const topology = buildTopology(environment)

    expect(topology.edges).toEqual([{ source: 'external', target: 'checkout', protocol: 'http' }])
  })

  it('places a shared dependency below its deepest caller', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'orders', 'redis'].map(service),
      connections: [
        { source: 'checkout', target: 'orders', protocol: 'http', required: true },
        { source: 'checkout', target: 'redis', protocol: 'redis', required: true },
        { source: 'orders', target: 'redis', protocol: 'redis', required: true },
      ],
    } as Environment

    expect(buildTopology(environment).levels.map((level) => level.map((item) => item.key))).toEqual([
      ['external'],
      ['checkout'],
      ['orders'],
      ['redis'],
    ])
  })

  it('terminates when service dependencies contain a cycle', () => {
    const environment = {
      primaryService: 'checkout',
      services: ['checkout', 'orders'].map(service),
      connections: [
        { source: 'checkout', target: 'orders', protocol: 'http', required: true },
        { source: 'orders', target: 'checkout', protocol: 'http', required: true },
      ],
    } as Environment

    expect(buildTopology(environment).levels.map((level) => level.map((item) => item.key))).toEqual([
      ['external'],
      ['checkout'],
      ['orders'],
    ])
  })
})
