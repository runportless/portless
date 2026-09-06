import { describe, expect, it } from 'vitest'
import { isWebSocketHandshake } from './trafficProtocol'

describe('WebSocket handshake classification', () => {
  it('recognizes compact HTTP 101 summaries without captured headers', () => {
    expect(isWebSocketHandshake({ protocol: 'http', status: 101 })).toBe(true)
  })

  it('recognizes case-insensitive upgrade headers in exchange details', () => {
    expect(isWebSocketHandshake({ protocol: 'http', status: 101, responseHeaders: { uPgRaDe: [' WebSocket '] } })).toBe(true)
  })

  it('does not label ordinary HTTP, rejected upgrades, or other protocols as WebSockets', () => {
    expect(isWebSocketHandshake({ protocol: 'http', status: 200 })).toBe(false)
    expect(isWebSocketHandshake({ protocol: 'http', status: 403, responseHeaders: { Upgrade: ['websocket'] } })).toBe(false)
    expect(isWebSocketHandshake({ protocol: 'http', status: 101, error: 'handshake failed' })).toBe(false)
    expect(isWebSocketHandshake({ protocol: 'http', status: 101, responseHeaders: { Upgrade: ['h2c'] } })).toBe(false)
    expect(isWebSocketHandshake({ protocol: 'tcp', status: 101 })).toBe(false)
  })
})
