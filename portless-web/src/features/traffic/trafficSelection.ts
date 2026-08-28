import type { TrafficExchange, TrafficTrace } from '../../api/contracts/traffic'

export function traceContainsExchange(trace: TrafficTrace, sequence: number) {
  return Boolean(trace.spans?.some((span) => span.exchange.sequence === sequence))
}

export function traceCandidatesForExchange(traces: TrafficTrace[], exchange: TrafficExchange, hint?: TrafficTrace) {
  const candidates = new Map<number, TrafficTrace>()
  for (const trace of traces) candidates.set(trace.number, trace)
  if (hint) candidates.set(hint.number, hint)
  const priority = (trace: TrafficTrace) => {
    if (traceContainsExchange(trace, exchange.sequence)) return 0
    if (exchange.traceId && trace.traceId === exchange.traceId) return 1
    if (trace.rootSequence === exchange.sequence) return 2
    if (trace.number <= exchange.sequence && trace.lastSequence >= exchange.sequence) return 3
    return 4
  }
  return [...candidates.values()]
    .filter((trace) => priority(trace) < 4)
    .sort((left, right) => priority(left) - priority(right) || (left.lastSequence - left.number) - (right.lastSequence - right.number) || right.number - left.number)
}
