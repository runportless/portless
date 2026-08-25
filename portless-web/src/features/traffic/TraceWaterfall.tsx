import { useEffect, useState, type CSSProperties } from 'react'
import { duration } from '../../components/Status'
import type { TrafficExchange, TrafficTrace } from '../../types'

function spanOperation(exchange: TrafficExchange) {
  if (exchange.protocol === 'http') return `${exchange.method || 'HTTP'} ${exchange.requestTarget || exchange.path || '/'}`
  return `${exchange.protocol.toUpperCase()} session`
}

export function TraceWaterfall({ trace, onExchange }: { trace: TrafficTrace; onExchange: (exchange: TrafficExchange) => void }) {
  const [maximized, setMaximized] = useState(false)
  useEffect(() => {
    if (!maximized) return
    const restore = (event: KeyboardEvent) => { if (event.key === 'Escape') setMaximized(false) }
    window.addEventListener('keydown', restore)
    return () => window.removeEventListener('keydown', restore)
  }, [maximized])
  const total = Math.max(1, trace.durationMs)
  const inspect = (exchange: TrafficExchange) => { setMaximized(false); onExchange(exchange) }
  return <div className={`trace-waterfall${maximized ? ' panel trace-waterfall--maximized' : ''}`} role="region" aria-label="Trace waterfall">
    {maximized && <div className="panel-title trace-waterfall__toolbar"><span>TRACE WATERFALL</span><div><button className="icon-button" type="button" title="Restore trace" aria-label="Restore trace" aria-pressed="true" onClick={() => setMaximized(false)}>×</button></div></div>}
    <div className="trace-waterfall__content">
    <div className="trace-waterfall__axis"><span>SERVICE / OPERATION</span><div><i>0</i><i>{duration(Math.round(total/2))}</i><i>{duration(total)}</i></div>{!maximized && <button className="trace-waterfall__size" type="button" title="Maximize trace" aria-label="Maximize trace" aria-pressed="false" onClick={() => setMaximized(true)}><TraceSizeIcon /></button>}</div>
    {(trace.spans || []).map((span) => {
      const exchange = span.exchange
      const left = Math.max(0, Math.min(100, span.startOffsetMs/total*100))
      const width = Math.max(1.25, Math.min(100-left, exchange.durationMs/total*100))
      const style = { '--span-left': `${left}%`, '--span-width': `${width}%`, '--span-depth': span.depth } as CSSProperties
      const tone = exchange.error || (exchange.status || 0) >= 500 ? ' is-error' : exchange.fault ? ' is-faulted' : exchange.protocol === 'tcp' ? ' is-tcp' : ''
      return <button className={`trace-span${tone}`} style={style} type="button" key={exchange.sequence} onClick={() => inspect(exchange)} aria-label={`Inspect ${exchange.source} to ${exchange.target} ${spanOperation(exchange)}`}>
        <span className="trace-span__label"><strong>{exchange.source} <i>→</i> {exchange.target}</strong><small>{spanOperation(exchange)}</small></span>
        <span className="trace-span__track"><i /><small>{duration(exchange.durationMs)}</small></span>
        <span className={`correlation-badge correlation-badge--${span.correlation}`}>{span.correlation}</span>
      </button>
    })}
    </div>
  </div>
}

function TraceSizeIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4" /></svg>
}
