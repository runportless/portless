import type { TrafficExchange } from '../../../types'
import { CommandResultLayout, ProtocolMessageCard, type ProtocolMessagePresentation } from '../detail/CommandResultLayout'
import { TrafficTextContent } from '../detail/TrafficFormatting'
import type { TrafficDetailView, TrafficDirection } from '../detail/trafficDetailTypes'
import { redisTrafficPresentation, RedisCommandContent } from './RedisPresentation'

function RedisMessage({ exchange, direction }: { exchange: TrafficExchange; direction: TrafficDirection }) {
  const redis = redisTrafficPresentation(exchange, direction)
  if (!redis) return null
  const presentation: ProtocolMessagePresentation = {
    label: direction === 'request' ? 'COMMAND' : 'RESULT',
    title: '',
    showTitle: false,
    type: redis.message?.type || '',
    content: redis.content,
    contentType: redis.json ? 'application/json' : direction === 'request' ? 'text/x-redis' : 'text/plain',
    binary: redis.binary,
    fields: [],
    truncated: Boolean(redis.message?.truncated),
    contentBytes: Math.max(0, redis.message?.contentBytes || 0),
    capturedBytes: Math.max(0, redis.message?.capturedBytes || 0),
    meta: redis.meta,
    emptyText: redis.emptyText,
  }
  const content = redis.command
    ? <RedisCommandContent command={redis.command} />
    : redis.content ? <TrafficTextContent content={redis.content} contentType={presentation.contentType} json={redis.json} /> : undefined
  return <ProtocolMessageCard direction={direction} presentation={presentation} content={content} />
}

export function RedisTrafficDetail({ exchanges, view }: { exchanges: TrafficExchange[]; view: TrafficDetailView }) {
  return <CommandResultLayout exchanges={exchanges} view={view} renderMessage={(exchange, direction) => <RedisMessage exchange={exchange} direction={direction} />} />
}
