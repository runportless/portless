import type { TrafficExchange, TrafficMessage } from '../../../api/contracts/traffic'

export type RedisCommandToken = {
  kind: 'key' | 'number' | 'option' | 'value'
  text: string
}

export type RedisCommandPresentation = {
  name: string
  arguments: RedisCommandToken[]
}

export type RedisTrafficPresentation = {
  message?: TrafficMessage
  meta: string
  content: string
  json: boolean
  binary: boolean
  command?: RedisCommandPresentation
  emptyText: string
}

const redisOptions = new Set([
  'ABSTTL', 'CH', 'EX', 'EXAT', 'GET', 'GT', 'KEEPTTL', 'LT', 'NX', 'PX', 'PXAT', 'XX',
])

function decodedContent(message: TrafficMessage | undefined) {
  if (!message?.content || message.encoding === 'base64') return { found: false, value: undefined }
  try {
    return { found: true, value: JSON.parse(message.content) as unknown }
  } catch {
    return { found: false, value: undefined }
  }
}

function redisArgument(value: unknown) {
  const raw = typeof value === 'string' ? value : JSON.stringify(value) || String(value)
  if (raw.length > 0 && /^[A-Za-z0-9_:.@/+\-=]+$/.test(raw)) return raw
  return `'${raw.replaceAll('\\', '\\\\').replaceAll("'", "\\'")}'`
}

function redisCommand(exchange: TrafficExchange, message: TrafficMessage | undefined, value: unknown): RedisCommandPresentation {
  if (Array.isArray(value) && value.length > 0) {
    const [name, ...values] = value
    return {
      name: String(name || exchange.tcp?.operation || 'COMMAND').toUpperCase(),
      arguments: values.map((argument, index) => {
        const raw = typeof argument === 'string' ? argument : JSON.stringify(argument) || String(argument)
        const upper = raw.toUpperCase()
        const kind: RedisCommandToken['kind'] = index === 0
          ? 'key'
          : /^-?\d+(?:\.\d+)?$/.test(raw) ? 'number'
            : redisOptions.has(upper) ? 'option' : 'value'
        return { kind, text: redisArgument(argument) }
      }),
    }
  }
  return {
    name: (exchange.tcp?.operation || message?.summary || 'COMMAND').split(/\s+/, 1)[0].toUpperCase(),
    arguments: [],
  }
}

function redisResult(value: unknown) {
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      try {
        return { content: JSON.stringify(JSON.parse(value), null, 2), json: true }
      } catch { /* The Redis value is ordinary text that happens to begin with JSON punctuation. */ }
    }
    return { content: value || '""', json: false }
  }
  if (value === null) return { content: '(nil)', json: false }
  if (typeof value === 'object') return { content: JSON.stringify(value, null, 2), json: true }
  return { content: String(value), json: false }
}

function redisResultMeta(message: TrafficMessage | undefined, value: unknown) {
  const summary = message?.summary?.trim() || ''
  if (/^error\b/i.test(summary)) return 'error'
  if (/^\d+ byte value$/i.test(summary)) return 'string'
  if (/^\d+ value response$/i.test(summary) || Array.isArray(value)) return 'array'
  if (value === null || (summary.toLowerCase() === 'response' && value === '')) return 'null'
  if (typeof value === 'number') return Number.isInteger(value) ? 'integer' : 'number'
  if (typeof value === 'boolean') return 'boolean'
  if (summary.toLowerCase() === 'response' && typeof value === 'string') {
    if (/^[+-]?\d+$/.test(value)) return 'integer'
    if (/^[+-]?(?:\d+\.\d*|\d*\.\d+)(?:e[+-]?\d+)?$/i.test(value)) return 'number'
    if (/^(?:t|f|true|false)$/i.test(value)) return 'boolean'
  }
  if (typeof value === 'string' || (summary && summary.toLowerCase() !== 'response')) return 'string'
  if (typeof value === 'object') return 'object'
  return message ? 'unknown' : 'not captured'
}

export function redisTrafficPresentation(exchange: TrafficExchange, direction: 'request' | 'response'): RedisTrafficPresentation | null {
  if (exchange.tcp?.applicationProtocol?.toLowerCase() !== 'redis') return null
  const messages = direction === 'request' ? exchange.tcp.requestMessages || [] : exchange.tcp.responseMessages || []
  const message = direction === 'request'
    ? messages.find((candidate) => candidate.type.toLowerCase() === 'command') || messages[0]
    : messages.find((candidate) => candidate.type.toLowerCase() === 'error') || messages.find((candidate) => candidate.content) || messages[0]
  const decoded = decodedContent(message)
  const binary = message?.encoding === 'base64'

  if (direction === 'request') {
    const command = redisCommand(exchange, message, decoded.value)
    return {
      message,
      meta: `${command.arguments.length} ${command.arguments.length === 1 ? 'argument' : 'arguments'}`,
      content: [command.name, ...command.arguments.map((argument) => argument.text)].join(' '),
      json: false,
      binary,
      command,
      emptyText: 'No Redis command content was captured.',
    }
  }

  const result = decoded.found ? redisResult(decoded.value) : { content: '', json: false }
  return {
    message,
    meta: redisResultMeta(message, decoded.value),
    content: result.content,
    json: result.json,
    binary,
    emptyText: exchange.tcp.outcome === 'one-way' ? 'This Redis command does not have a result.' : 'No Redis result was captured.',
  }
}

export function RedisCommandContent({ command }: { command: RedisCommandPresentation }) {
  return <pre className="traffic-redis-command"><span className="traffic-redis-command__name">{command.name}</span>{command.arguments.map((argument, index) => <span key={index}> <span className={`traffic-redis-command__${argument.kind}`}>{argument.text}</span></span>)}</pre>
}
