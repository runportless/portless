import type { TrafficExchange, TrafficTrace } from '../../api/contracts/traffic'

export function trafficResultTone(error: boolean | string | undefined, status: number | undefined) {
  if (error || (status || 0) >= 500) return 'danger-text'
  if ((status || 0) >= 400) return 'warning-text'
  return ''
}

export function traceRequest(trace: TrafficTrace) {
  const value = `${trace.method || ''} ${trace.requestTarget || ''}`.trim()
  return value || `${trace.source} → ${trace.target}`
}

export function exchangeOperation(exchange: TrafficExchange) {
  if (exchange.protocol === 'http') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  const application = exchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
  return `${application} ${exchange.tcp?.operation || 'SESSION'}`
}

export function exchangeResult(exchange: TrafficExchange) {
  if (exchange.error || exchange.tcp?.outcome === 'error') return 'ERR'
  if (exchange.status) return exchange.status
  if (exchange.tcp?.outcome === 'one-way') return 'SENT'
  if (exchange.tcp?.outcome === 'incomplete') return 'INCOMPLETE'
  return 'OK'
}
