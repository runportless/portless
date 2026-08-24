const propagatedTraceHeaders = [
  'traceparent', 'tracestate', 'baggage',
  'b3',
  'x-b3-traceid', 'x-b3-spanid', 'x-b3-parentspanid', 'x-b3-sampled', 'x-b3-flags',
  'x-datadog-trace-id', 'x-datadog-parent-id', 'x-datadog-sampling-priority', 'x-datadog-origin', 'x-datadog-tags',
]

export function forwardedTraceHeaders(incoming) {
  const outgoing = {}
  for (const name of propagatedTraceHeaders) {
    const value = incoming[name]
    if (typeof value === 'string' && value.trim()) outgoing[name] = value
  }
  return outgoing
}
