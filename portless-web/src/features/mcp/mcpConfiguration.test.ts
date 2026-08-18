import { describe, expect, it } from 'vitest'
import {
  buildMCPArguments,
  buildMCPClientConfiguration,
  buildMCPCommand,
  defaultMCPCapabilities,
  mcpAccessLabel,
  mcpToolCount,
  serializeMCPClientConfiguration,
  suggestedMCPServerName,
  validMCPConfiguration,
  type MCPConfiguration,
} from './mcpConfiguration'

const configuration = (overrides: Partial<MCPConfiguration> = {}): MCPConfiguration => ({
  serverName: 'portless-store-local',
  executable: 'portless',
  scope: { kind: 'environment', environment: 'store/local' },
  capabilities: { ...defaultMCPCapabilities },
  ...overrides,
})

describe('MCP client configuration', () => {
  it('pins an environment with the global flag before the MCP command', () => {
    const current = configuration()

    expect(buildMCPArguments(current)).toEqual(['--env', 'store/local', 'mcp', 'serve'])
    expect(buildMCPClientConfiguration(current)).toEqual({
      mcpServers: {
        'portless-store-local': { command: 'portless', args: ['--env', 'store/local', 'mcp', 'serve'] },
      },
    })
  })

  it('uses a working directory for workspace scope without adding a scope flag', () => {
    const current = configuration({ executable: '/Applications/Portless CLI/portless', scope: { kind: 'workspace', directory: '/Users/dev/Store Checkout' } })

    expect(buildMCPClientConfiguration(current).mcpServers['portless-store-local']).toEqual({
      command: '/Applications/Portless CLI/portless',
      args: ['mcp', 'serve'],
      cwd: '/Users/dev/Store Checkout',
    })
    expect(buildMCPCommand(current)).toBe("cd '/Users/dev/Store Checkout' && '/Applications/Portless CLI/portless' mcp serve")
  })

  it('generates all-environment capabilities in stable command order', () => {
    const current = configuration({
      scope: { kind: 'all' },
      capabilities: { lifecycle: true, trafficControl: true, sensitiveTraffic: true },
    })

    expect(buildMCPArguments(current)).toEqual([
      'mcp', 'serve', '--all-environments', '--allow-lifecycle', '--allow-traffic-control', '--allow-sensitive-traffic',
    ])
    expect(mcpToolCount(current.capabilities)).toBe(24)
    expect(mcpAccessLabel(current.capabilities)).toBe('SENSITIVE')
  })

  it('serializes deterministic client JSON and suggests readable names', () => {
    const current = configuration()

    expect(serializeMCPClientConfiguration(current)).toBe(`${JSON.stringify(buildMCPClientConfiguration(current), null, 2)}`)
    expect(suggestedMCPServerName({ kind: 'environment', environment: 'Store/QA Assisted' })).toBe('portless-store-qa-assisted')
    expect(suggestedMCPServerName({ kind: 'workspace', directory: '/tmp/store' })).toBe('portless-workspace')
    expect(suggestedMCPServerName({ kind: 'all' })).toBe('portless-all')
  })

  it('requires the executable, server name, and selected scope value', () => {
    expect(validMCPConfiguration(configuration())).toBe(true)
    expect(validMCPConfiguration(configuration({ serverName: ' ' }))).toBe(false)
    expect(validMCPConfiguration(configuration({ executable: '' }))).toBe(false)
    expect(validMCPConfiguration(configuration({ scope: { kind: 'environment', environment: '' } }))).toBe(false)
    expect(validMCPConfiguration(configuration({ scope: { kind: 'workspace', directory: '' } }))).toBe(false)
  })
})
