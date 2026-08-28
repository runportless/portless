import type { TrafficExchange, TrafficMessage } from '../../../api/contracts/traffic'
import type { TrafficDetailView, TrafficDirection } from '../detail/trafficDetailTypes'
import { DatabaseTrafficDetail } from './DatabaseTrafficDetail'
import { decodedMessagePresentation } from './DecodedMessagePresentation'

function boundTrafficParameterValues(messages: TrafficMessage[]) {
  const bind = messages.find((message) => message.type.toLowerCase() === 'bind')
  if (!bind?.content || bind.encoding === 'base64') return []
  try {
    const values: unknown = JSON.parse(bind.content)
    return Array.isArray(values) ? values : []
  } catch {
    return []
  }
}

function postgresParameterLiteral(value: unknown) {
  if (value === null) return 'NULL'
  if (typeof value === 'number') return String(value)
  if (typeof value === 'boolean') return value ? 'TRUE' : 'FALSE'
  const text = typeof value === 'string' ? value : JSON.stringify(value) || String(value)
  return `'${text.replaceAll("'", "''")}'`
}

function queryWithBoundParameters(query: string, messages: TrafficMessage[]) {
  const values = boundTrafficParameterValues(messages)
  if (values.length === 0) return query
  return query.replace(/\$(\d+)\b/g, (placeholder, position: string) => {
    const index = Number(position) - 1
    return index >= 0 && index < values.length ? postgresParameterLiteral(values[index]) : placeholder
  })
}

export function postgresTrafficPresentation(exchange: TrafficExchange, direction: TrafficDirection) {
  return decodedMessagePresentation(exchange, direction, queryWithBoundParameters)
}

export function PostgreSQLTrafficDetail({ exchanges, view }: { exchanges: TrafficExchange[]; view: TrafficDetailView }) {
  return <DatabaseTrafficDetail exchanges={exchanges} view={view} present={postgresTrafficPresentation} />
}
