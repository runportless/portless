import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { TrafficExchange } from '../../../types'
import { redisTrafficPresentation, RedisCommandContent } from './RedisPresentation'

function redisExchange(operation: string, command: unknown[], response: unknown, responseSummary: string): TrafficExchange {
  return {
    project: 'store', environment: 'local', sequence: 42, protocol: 'tcp', source: 'orders', target: 'orders-redis', background: false,
    startedAt: '2026-08-27T12:00:00Z', completedAt: '2026-08-27T12:00:00.001Z', durationMs: 1, requestBytes: 80, responseBytes: 80,
    tcp: {
      kind: 'operation', applicationProtocol: 'redis', operation, inspection: 'decoded', outcome: 'success',
      requestMessages: [{ type: 'command', offsetMs: 0, summary: `${operation} ${String(command[1] || '')}`.trim(), wireBytes: 80, contentType: 'application/json', encoding: 'utf8', content: JSON.stringify(command, null, 2) }],
      responseMessages: [{ type: 'response', offsetMs: 1, summary: responseSummary, wireBytes: 80, contentType: 'application/json', encoding: 'utf8', content: JSON.stringify(response) }],
    },
  }
}

describe('Redis traffic presentation', () => {
  it('renders a SET as one readable Redis command without the wire JSON array', () => {
    const exchange = redisExchange('SET', ['SET', 'store:order:56', '{"id":56,"state":"created"}', 'EX', '60'], 'OK', 'OK')
    const presentation = redisTrafficPresentation(exchange, 'request')!
    const markup = renderToStaticMarkup(<RedisCommandContent command={presentation.command!} />)

    expect(presentation).toMatchObject({ meta: '4 arguments', content: 'SET store:order:56 \'{"id":56,"state":"created"}\' EX 60' })
    expect(markup).toContain('traffic-redis-command__name">SET</span>')
    expect(markup).toContain('traffic-redis-command__key">store:order:56</span>')
    expect(markup).toContain('traffic-redis-command__value">&#x27;{&quot;id&quot;:56,&quot;state&quot;:&quot;created&quot;}&#x27;</span>')
    expect(markup).toContain('traffic-redis-command__option">EX</span>')
    expect(markup).toContain('traffic-redis-command__number">60</span>')
  })

  it('unwraps a cached JSON string into highlighted-result-ready JSON', () => {
    const cached = '{"id":56,"sku":"coffee-mug","state":"created"}'
    const exchange = redisExchange('GET', ['GET', 'store:order:56'], cached, `${cached.length} byte value`)

    expect(redisTrafficPresentation(exchange, 'response')).toMatchObject({
      meta: 'string',
      content: '{\n  "id": 56,\n  "sku": "coffee-mug",\n  "state": "created"\n}',
      json: true,
    })
  })

  it('renders simple Redis results without JSON string quotes', () => {
    const exchange = redisExchange('GET', ['GET', 'status'], 'cached', '6 byte value')
    expect(redisTrafficPresentation(exchange, 'response')).toMatchObject({ meta: 'string', content: 'cached', json: false })
  })

  it.each([
    ['42', 'integer'],
    ['3.5', 'number'],
    ['t', 'boolean'],
    ['', 'null'],
  ])('labels a scalar %s response as %s', (response, type) => {
    const exchange = redisExchange('GET', ['GET', 'value'], response, 'response')
    expect(redisTrafficPresentation(exchange, 'response')).toMatchObject({ meta: type })
  })

  it('labels aggregate results as arrays', () => {
    const exchange = redisExchange('LRANGE', ['LRANGE', 'orders', '0', '-1'], ['first', 'second'], '2 value response')
    expect(redisTrafficPresentation(exchange, 'response')).toMatchObject({ meta: 'array' })
  })
})
