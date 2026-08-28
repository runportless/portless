import type { TrafficExchange } from '../../../api/contracts/traffic'
import { CommandResultLayout, ProtocolMessageCard } from '../detail/CommandResultLayout'
import { TrafficTextContent } from '../detail/TrafficFormatting'
import type { TrafficDetailView, TrafficDirection } from '../detail/trafficDetailTypes'
import { decodedMessagePresentation } from './DecodedMessagePresentation'

export function genericTcpTrafficPresentation(exchange: TrafficExchange, direction: TrafficDirection) {
  return decodedMessagePresentation(exchange, direction)
}

function GenericTcpMessage({ exchange, direction }: { exchange: TrafficExchange; direction: TrafficDirection }) {
  const presentation = genericTcpTrafficPresentation(exchange, direction)
  const content = presentation.content
    ? <TrafficTextContent content={presentation.content} contentType={presentation.contentType} />
    : undefined
  return <ProtocolMessageCard direction={direction} presentation={presentation} content={content} />
}

export function GenericTcpTrafficDetail({ exchanges, view }: { exchanges: TrafficExchange[]; view: TrafficDetailView }) {
  return <CommandResultLayout exchanges={exchanges} view={view} renderMessage={(exchange, direction) => <GenericTcpMessage exchange={exchange} direction={direction} />} />
}
