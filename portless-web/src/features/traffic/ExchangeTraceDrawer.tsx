import type { ComponentBinding } from '../../api/contracts/topology'
import type { TrafficExchange } from '../../api/contracts/traffic'
import { TrafficDrawerShell } from './detail/TrafficDrawerShell'
import { TrafficNavigationArrow, useTrafficDrawerNavigationKeys } from './TrafficDrawerNavigation'

function ExchangeNavigator({ exchange, items, pending, onNavigate }: {
  exchange: TrafficExchange
  items: TrafficExchange[]
  pending: boolean
  onNavigate: (exchange: TrafficExchange) => void
}) {
  const index = items.findIndex((candidate) => candidate.sequence === exchange.sequence)
  const previous = items[index - 1]
  const next = items[index + 1]
  useTrafficDrawerNavigationKeys({
    previous,
    next,
    pending,
    enabled: items.length >= 2 && index >= 0,
    onNavigate,
  })
  if (items.length < 2 || index < 0) return null

  return <nav className="traffic-detail-navigator traffic-exchange-navigator" aria-label="Exchange navigation" aria-busy={pending}>
    <div role="group" aria-label="Navigate filtered exchanges">
      <button type="button" title="Previous exchange" aria-label="Previous exchange" aria-keyshortcuts="ArrowLeft" disabled={pending || !previous} onClick={() => previous && onNavigate(previous)}><TrafficNavigationArrow direction="previous" /></button>
      <output aria-live="polite" aria-label={`Exchange ${index + 1} of ${items.length}`}><strong>{index + 1}</strong><span>OF</span><strong>{items.length}</strong></output>
      <button type="button" title="Next exchange" aria-label="Next exchange" aria-keyshortcuts="ArrowRight" disabled={pending || !next} onClick={() => next && onNavigate(next)}><TrafficNavigationArrow direction="next" /></button>
    </div>
  </nav>
}

export function ExchangeTraceDrawer({ exchange, exchanges = [], navigationPending = false, targetBinding, onNavigate, onClose }: {
  exchange: TrafficExchange
  exchanges?: TrafficExchange[]
  navigationPending?: boolean
  targetBinding?: ComponentBinding
  onNavigate?: (exchange: TrafficExchange) => void
  onClose: () => void
}) {
  const navigation = onNavigate
    ? <ExchangeNavigator exchange={exchange} items={exchanges} pending={navigationPending} onNavigate={onNavigate} />
    : undefined
  return <TrafficDrawerShell exchange={exchange} navigation={navigation} targetBinding={targetBinding} onClose={onClose} />
}
