import type { TrafficMode } from './useTrafficView'

export function TrafficTableHeader({ mode }: { mode: TrafficMode }) {
  if (mode === 'traces') return <div className="trace-row trace-row--header"><span>Timestamp</span><span>Root request</span><span>Result</span><span>Duration</span><span>Spans</span><span>Correlation</span></div>
  return <div className="table-row table-row--header traffic-row"><span>Seq</span><span>Timestamp</span><span>Protocol</span><span>Request / operation</span><span>Edge</span><span>Result</span><span>Duration</span><span>Fault / recording</span></div>
}
