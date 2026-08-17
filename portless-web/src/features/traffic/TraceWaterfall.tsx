import type { CSSProperties } from 'react'
import { duration } from '../../components/Status'
import type { TrafficExchange, TrafficTrace } from '../../types'

function spanOperation(exchange: TrafficExchange) {
  if (exchange.protocol === 'http') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  return `${exchange.protocol.toUpperCase()} session`
}

export function TraceWaterfall({ trace, onExchange }: { trace: TrafficTrace; onExchange: (exchange: TrafficExchange) => void }) {
  const total = Math.max(1, trace.durationMs)
  return <div className="trace-waterfall" aria-label={`Trace ${trace.number} waterfall`}>
    <div className="trace-waterfall__axis"><span>SERVICE / OPERATION</span><div><i>0</i><i>{duration(Math.round(total/2))}</i><i>{duration(total)}</i></div></div>
    {(trace.spans || []).map((span) => {
      const exchange = span.exchange
      const left = Math.max(0, Math.min(100, span.startOffsetMs/total*100))
      const width = Math.max(1.25, Math.min(100-left, exchange.durationMs/total*100))
      const style = { '--span-left': `${left}%`, '--span-width': `${width}%`, '--span-depth': span.depth } as CSSProperties
      const tone = exchange.error || (exchange.status || 0) >= 500 ? ' is-error' : exchange.fault ? ' is-faulted' : exchange.protocol === 'tcp' ? ' is-tcp' : ''
      return <button className={`trace-span${tone}`} style={style} type="button" key={exchange.sequence} onClick={() => onExchange(exchange)} aria-label={`Inspect ${exchange.source} to ${exchange.target} ${spanOperation(exchange)}`}>
        <span className="trace-span__label"><strong>{exchange.source} <i>→</i> {exchange.target}</strong><small>{spanOperation(exchange)}</small></span>
        <span className="trace-span__track"><i /><small>{duration(exchange.durationMs)}</small></span>
        <span className={`correlation-badge correlation-badge--${span.correlation}`}>{span.correlation}</span>
      </button>
    })}
  </div>
}
