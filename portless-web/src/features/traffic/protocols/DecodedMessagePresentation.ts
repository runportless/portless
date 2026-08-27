import type { TrafficExchange, TrafficMessage } from '../../../types'
import type { ProtocolMessagePresentation } from '../detail/CommandResultLayout'
import type { TrafficDirection } from '../detail/trafficDetailTypes'

const protocolMessageNoise = new Set([
  'bind-complete', 'close-complete', 'column-definition', 'column-end', 'definition-end', 'describe', 'empty-query',
  'execute', 'flush', 'no-data', 'parameter-description', 'parse-complete', 'portal-suspended', 'ready',
  'row-description', 'sync',
])

function displayedProtocolMessageSummary(message: TrafficMessage) {
  const summary = (message.summary || message.type).trim()
  const content = message.content?.trim()
  if (!content) return summary
  const normalizedSummary = summary.replace(/\s+/g, ' ').toLowerCase()
  const normalizedContent = content.replace(/\s+/g, ' ').toLowerCase()
  return normalizedSummary.includes(normalizedContent) ? '' : summary
}

export function decodedMessagePresentation(exchange: TrafficExchange, direction: TrafficDirection, transformRequestContent?: (content: string, messages: TrafficMessage[]) => string): ProtocolMessagePresentation {
  const request = direction === 'request'
  const messages = (request ? exchange.tcp?.requestMessages : exchange.tcp?.responseMessages) || []
  const preferred = request
    ? messages.find((message) => message.contentType?.toLowerCase().startsWith('text/x-sql'))
      || messages.find((message) => message.content && message.encoding !== 'base64')
      || messages.find((message) => !protocolMessageNoise.has(message.type.toLowerCase()))
      || messages[0]
    : messages.find((message) => message.type.toLowerCase() === 'error')
      || messages.find((message) => message.content && message.encoding !== 'base64')
      || messages.find((message) => ['command-complete', 'ok', 'response', 'result-end'].includes(message.type.toLowerCase()))
      || [...messages].reverse().find((message) => !protocolMessageNoise.has(message.type.toLowerCase()))
      || messages[messages.length - 1]
  const label = request ? 'COMMAND' : 'RESULT'
  const fallback = request
    ? exchange.tcp?.operation || 'Operation'
    : exchange.error || (exchange.tcp?.outcome === 'one-way' ? 'No response' : exchange.tcp?.outcome || 'No result captured')
  const fields = (preferred?.fields || []).filter((field) => field.value.trim())
  const content = preferred?.encoding === 'base64' ? '' : preferred?.content || ''
  const contentType = preferred?.contentType || ''
  const type = preferred?.type || ''
  const renderedContent = request && content && transformRequestContent && contentType.toLowerCase().startsWith('text/x-sql')
    ? transformRequestContent(content, messages)
    : content
  return {
    label,
    title: preferred ? displayedProtocolMessageSummary(preferred) || (request ? exchange.tcp?.operation || preferred.type : preferred.summary || preferred.type) : fallback,
    showTitle: !(renderedContent && ((request && contentType.toLowerCase().startsWith('text/x-sql')) || type.toLowerCase() === 'data-row')),
    type,
    content: renderedContent,
    contentType,
    binary: preferred?.encoding === 'base64',
    fields,
    truncated: Boolean(preferred?.truncated),
    contentBytes: Math.max(0, preferred?.contentBytes || 0),
    capturedBytes: Math.max(0, preferred?.capturedBytes || 0),
    emptyText: direction === 'request'
      ? 'No command content was captured.'
      : exchange.tcp?.outcome === 'one-way' ? 'This command does not have a result.' : 'No result content was captured.',
  }
}
