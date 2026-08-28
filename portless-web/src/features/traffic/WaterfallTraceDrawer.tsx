import { useState } from 'react'
import type { ComponentBinding } from '../../api/contracts/topology'
import type { TrafficExchange, TrafficTrace } from '../../api/contracts/traffic'
import { TrafficDrawerShell } from './detail/TrafficDrawerShell'
import { TrafficNavigationArrow, useTrafficDrawerNavigationKeys } from './TrafficDrawerNavigation'
import type { TraceNavigationItem } from './TraceWaterfall'

export function orderedTraceExchanges(trace: TrafficTrace | null | undefined) {
  return [...(trace?.spans || [])]
    .sort((left, right) => left.startOffsetMs - right.startOffsetMs || left.exchange.sequence - right.exchange.sequence)
    .map((span) => span.exchange)
}

function TraceNavigator({ trace, exchange, items, itemKey, pending, onNavigate }: {
  trace?: TrafficTrace | null
  exchange: TrafficExchange
  items?: TraceNavigationItem[]
  itemKey?: string
  pending: boolean
  onNavigate?: (item: TraceNavigationItem) => void
}) {
  const [requestedScope, setRequestedScope] = useState<'http' | 'all'>('http')
  const allItems: TraceNavigationItem[] = items || orderedTraceExchanges(trace).map((candidate) => ({ kind: 'exchange', key: `exchange:${candidate.sequence}`, exchange: candidate }))
  const httpItems = allItems.filter((candidate) => candidate.exchange.protocol === 'http')
  const scope = requestedScope === 'http' && httpItems.length > 0 ? 'http' : 'all'
  const navigationItems = scope === 'http' ? httpItems : allItems
  const allIndex = itemKey ? allItems.findIndex((candidate) => candidate.key === itemKey) : allItems.findIndex((candidate) => candidate.exchange.sequence === exchange.sequence)
  const index = itemKey ? navigationItems.findIndex((candidate) => candidate.key === itemKey) : navigationItems.findIndex((candidate) => candidate.exchange.sequence === exchange.sequence)
  const itemIndex = new Map(allItems.map((candidate, candidateIndex) => [candidate.key, candidateIndex]))
  const previous = index >= 0
    ? navigationItems[index - 1]
    : [...navigationItems].reverse().find((candidate) => (itemIndex.get(candidate.key) ?? -1) < allIndex)
  const next = index >= 0
    ? navigationItems[index + 1]
    : navigationItems.find((candidate) => (itemIndex.get(candidate.key) ?? Number.MAX_SAFE_INTEGER) > allIndex)
  const first = index === 0
  const last = index === navigationItems.length - 1
  const positionLabel = index >= 0 ? `Span ${index + 1} of ${navigationItems.length}` : `Current span is outside HTTP navigation; ${navigationItems.length} HTTP ${navigationItems.length === 1 ? 'span' : 'spans'} available`
  useTrafficDrawerNavigationKeys({
    previous,
    next,
    pending,
    enabled: Boolean(onNavigate && allItems.length >= 2 && allIndex >= 0),
    onNavigate,
  })
  if (!onNavigate || allItems.length < 2 || allIndex < 0) return null
  const changeScope = (nextScope: 'http' | 'all') => {
    if (nextScope === scope) return
    const nextItems = nextScope === 'http' ? httpItems : allItems
    const firstItem = nextItems[0]
    if (!firstItem) return
    setRequestedScope(nextScope)
    onNavigate(firstItem)
  }

  return <nav className="traffic-detail-navigator traffic-trace-navigator" aria-label="Trace span navigation" aria-busy={pending}>
    <div className="traffic-trace-navigator__scope" role="group" aria-label="Trace navigation scope">
      <button type="button" title="Step through HTTP interactions only" aria-pressed={scope === 'http'} disabled={pending || httpItems.length === 0} onClick={() => changeScope('http')}>HTTP</button>
      <button type="button" title="Step through all visible interactions" aria-pressed={scope === 'all'} disabled={pending} onClick={() => changeScope('all')}>ALL</button>
    </div>
    <div className="traffic-trace-navigator__controls" role="group" aria-label="Navigate visible trace spans">
      <button type="button" title="First visible span in trace" aria-label="First visible span in trace" disabled={pending || navigationItems.length === 0 || first} onClick={() => onNavigate(navigationItems[0])}><TrafficNavigationArrow direction="previous" boundary /></button>
      <button type="button" title="Previous visible span in trace" aria-label="Previous visible span in trace" aria-keyshortcuts="ArrowLeft" disabled={pending || !previous} onClick={() => previous && onNavigate(previous)}><TrafficNavigationArrow direction="previous" /></button>
      <output aria-live="polite" aria-label={positionLabel}><strong>{index >= 0 ? index + 1 : '—'}</strong><span>OF</span><strong>{navigationItems.length}</strong></output>
      <button type="button" title="Next visible span in trace" aria-label="Next visible span in trace" aria-keyshortcuts="ArrowRight" disabled={pending || !next} onClick={() => next && onNavigate(next)}><TrafficNavigationArrow direction="next" /></button>
      <button type="button" title="Last visible span in trace" aria-label="Last visible span in trace" disabled={pending || navigationItems.length === 0 || last} onClick={() => onNavigate(navigationItems[navigationItems.length - 1])}><TrafficNavigationArrow direction="next" boundary /></button>
    </div>
  </nav>
}

export function WaterfallTraceDrawer({ exchange, trace, traceNavigationItems, traceNavigationItem, navigationPending = false, targetBinding, onNavigate, onClose }: {
  exchange: TrafficExchange
  trace?: TrafficTrace | null
  traceNavigationItems?: TraceNavigationItem[]
  traceNavigationItem?: TraceNavigationItem
  navigationPending?: boolean
  targetBinding?: ComponentBinding
  onNavigate?: (item: TraceNavigationItem) => void
  onClose: () => void
}) {
  const detailExchange = traceNavigationItem?.kind === 'transaction' ? traceNavigationItem.exchange : exchange
  const navigation = <TraceNavigator
    trace={trace}
    exchange={detailExchange}
    items={traceNavigationItems}
    itemKey={traceNavigationItem?.key}
    pending={navigationPending}
    onNavigate={onNavigate}
  />
  return <TrafficDrawerShell
    exchange={exchange}
    traceNavigationItem={traceNavigationItem}
    navigation={navigation}
    targetBinding={targetBinding}
    onClose={onClose}
  />
}
