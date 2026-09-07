import { useId } from 'react'
import type { TrafficComparisonSection } from '../../../api/contracts/traffic_replay'
import type { TrafficPayloadView } from '../detail/trafficDetailTypes'

export function ReplayDifferenceDetails({ section, representation, expanded, onExpandedChange }: {
  section: TrafficComparisonSection
  representation: TrafficPayloadView
  expanded: boolean
  onExpandedChange: (expanded: boolean) => void
}) {
  const id = useId()
  const label = section.state === 'equal' ? 'No differences' : section.state === 'different' ? 'Differences found' : section.state === 'partial' ? 'Partial comparison' : 'Comparison unavailable'
  const hasDetails = Boolean(section.reason || section.changes?.length)
  return <section className={`replay-diff__details is-${section.state}`}>
    {hasDetails ? <button type="button" className="replay-diff__state replay-diff__toggle" aria-expanded={expanded} aria-controls={id} onClick={() => onExpandedChange(!expanded)}>
      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 4 4 4-4 4" /></svg><strong>{label}</strong>
    </button> : <div className="replay-diff__state"><strong>{label}</strong></div>}
    {hasDetails && <div id={id} hidden={!expanded}>
      {section.reason && <p className="replay-diff__reason">{section.reason}</p>}
      {!!section.changes?.length && <table className="replay-diff-table" aria-label={`${representation} changes`}><thead><tr><th scope="col">PATH / HEADER</th><th scope="col">ORIGINAL</th><th scope="col">REPLAYED</th></tr></thead><tbody>{section.changes.slice(0, 1000).map((change, index) => <tr key={`${change.path}-${index}`}><th scope="row"><code>{change.path || '/'}</code><small>{change.kind}</small></th><td><pre>{change.before ?? '—'}</pre></td><td><pre>{change.after ?? '—'}</pre></td></tr>)}</tbody></table>}
    </div>}
  </section>
}
