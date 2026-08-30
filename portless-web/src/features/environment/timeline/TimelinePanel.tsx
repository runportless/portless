import { useMemo, useState } from 'react'
import { paginateItems, PanelPagination } from '../../../components/PanelPagination'
import type { TimelineEvent } from '../../../api/contracts/environments'

const timelinePageSizes = [25, 50, 100] as const

export function TimelinePanel({ timeline }: { timeline: TimelineEvent[] }) {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState<number>(timelinePageSizes[0])
  const pagination = useMemo(() => paginateItems(timeline, page, pageSize), [timeline, page, pageSize])
  const groups = useMemo(() => pagination.items.reduce<Record<string, TimelineEvent[]>>((result, event) => {
    const key = new Date(event.timestamp).toLocaleDateString()
    ;(result[key] ||= []).push(event)
    return result
  }, {}), [pagination.items])

  return <section className="panel timeline-panel">
    <div className="panel-title">
      <span>RECENT ACTIVITY</span>
      <label className="timeline-page-size"><span>ROWS PER PAGE</span><select aria-label="Timeline rows per page" value={pageSize} onChange={(event) => { setPageSize(Number(event.target.value)); setPage(0) }}>{timelinePageSizes.map((size) => <option value={size} key={size}>{size}</option>)}</select></label>
    </div>
    {Object.entries(groups).map(([date, events]) => <div className="timeline-group" key={date}><div className="timeline-date">{date}</div>{events.map((event) => <div className="timeline-event" key={event.sequence}><time>{new Date(event.timestamp).toLocaleTimeString()}</time><span className={`timeline-dot timeline-dot--${event.severity}`} /><div><strong>{event.summary}</strong><small>{event.type} · {event.actor}{event.subject ? ` · ${event.subject}` : ''}</small></div><code>#{event.sequence}</code></div>)}</div>)}
    {timeline.length === 0 && <div className="empty-row">The timeline will capture lifecycle, recording, and fault events.</div>}
    <PanelPagination label="timeline" pagination={pagination} onPage={setPage} />
  </section>
}
