import type { TrafficExchange } from '../../api/contracts/traffic'

export function isWebSocketHandshake(exchange: Pick<TrafficExchange, 'protocol' | 'status' | 'error' | 'responseHeaders'>) {
  if (exchange.protocol !== 'http' || exchange.status !== 101 || exchange.error) return false
  const upgrades = Object.entries(exchange.responseHeaders || {})
    .filter(([name]) => name.toLowerCase() === 'upgrade')
    .flatMap(([, values]) => values)
  // Exchange summaries omit headers. Portless only accepts WebSocket upgrades.
  return upgrades.length === 0 || upgrades.some((value) => value.trim().toLowerCase() === 'websocket')
}
