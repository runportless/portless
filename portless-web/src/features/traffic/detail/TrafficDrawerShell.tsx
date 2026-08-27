import { useEffect, useState, type ReactNode } from 'react'
import { DrawerSizeButton } from '../../../components/DrawerSizeButton'
import { duration } from '../../../components/Status'
import type { ComponentBinding, TrafficExchange } from '../../../types'
import { HttpTrafficDetail } from '../protocols/HttpTrafficDetail'
import { TcpTrafficDetail } from '../protocols/TcpTrafficDetail'
import { traceTransactionCommandSpans, type TraceNavigationItem } from '../TraceWaterfall'
import { formatTrafficBytes } from './TrafficFormatting'
import { TrafficInterventionBadges, TrafficOverview } from './TrafficOverview'
import type { TrafficDetailView } from './trafficDetailTypes'

export function defaultTrafficDetailView(_exchange: TrafficExchange): TrafficDetailView {
  return 'request'
}

function statusTone(exchange: TrafficExchange) {
  if (exchange.error || exchange.tcp?.outcome === 'error' || exchange.tcp?.outcome === 'incomplete' || (exchange.status || 0) >= 500) return 'is-error'
  if ((exchange.status || 0) >= 400) return 'is-warning'
  return 'is-success'
}

export function TrafficDrawerShell({ exchange, traceNavigationItem, navigation, targetBinding, onClose }: {
  exchange: TrafficExchange
  traceNavigationItem?: TraceNavigationItem
  navigation?: ReactNode
  targetBinding?: ComponentBinding
  onClose: () => void
}) {
  const transaction = traceNavigationItem?.kind === 'transaction' ? traceNavigationItem : undefined
  const transactionCommands = transaction ? traceTransactionCommandSpans(transaction.spans).map((span) => span.exchange) : []
  const detailExchange = transaction?.exchange || exchange
  const http = !transaction && detailExchange.protocol === 'http'
  const decodedTCP = !transaction && !http && detailExchange.tcp?.kind === 'operation'
  const semanticTCP = Boolean(transaction || decodedTCP)
  const tcpCommandExchanges = transaction ? transactionCommands : decodedTCP ? [detailExchange] : []
  const [maximized, setMaximized] = useState(false)
  const [view, setView] = useState<TrafficDetailView>(() => defaultTrafficDetailView(detailExchange))

  useEffect(() => {
    setView((current) => current === 'compare' && (http || semanticTCP) ? current : defaultTrafficDetailView(detailExchange))
  }, [detailExchange.project, detailExchange.environment, detailExchange.sequence, http, semanticTCP, traceNavigationItem?.key])

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (maximized) { setMaximized(false); setView((current) => current === 'compare' ? 'request' : current) } else onClose()
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [maximized, onClose])

  const toggleMaximized = () => {
    if (maximized) {
      setMaximized(false)
      setView((current) => current === 'compare' ? 'request' : current)
      return
    }
    setMaximized(true)
    if (http || semanticTCP) setView('compare')
  }

  const tcpStatus = detailExchange.tcp?.outcome === 'success' ? 'OK' : detailExchange.tcp?.outcome === 'one-way' ? 'SENT' : detailExchange.tcp?.outcome === 'incomplete' ? 'INCOMPLETE' : detailExchange.tcp?.outcome === 'error' ? 'ERROR' : 'SESSION'
  const status = detailExchange.error ? 'ERROR' : detailExchange.status ? String(detailExchange.status) : http ? 'OK' : tcpStatus
  const totalBytes = Math.max(0, detailExchange.requestBytes) + Math.max(0, detailExchange.responseBytes)
  const requestTarget = detailExchange.requestTarget || detailExchange.path || '/'
  const applicationProtocol = detailExchange.tcp?.applicationProtocol?.toUpperCase() || 'TCP'
  const operation = detailExchange.tcp?.operation || 'SESSION'
  const commandCount = transaction ? transactionCommands.length : decodedTCP ? 1 : 0
  const commandLabel = `${commandCount} ${commandCount === 1 ? 'command' : 'commands'}`
  const protocolBadge = http ? 'HTTP' : 'TCP'

  return <aside className={`traffic-detail${maximized ? ' traffic-detail--maximized' : ''}`} role="dialog" aria-label={`Traffic request and response ${detailExchange.sequence}`}>
    <header className="traffic-detail__header">
      <div className="traffic-detail__heading"><span className="traffic-detail__protocol-badge">{protocolBadge}</span><h3><span>{http ? detailExchange.method || 'HTTP' : applicationProtocol}</span><code>{transaction ? 'TRANSACTION' : http ? requestTarget : operation}</code></h3><small>{transaction && <><span className="traffic-detail__transaction-count">{commandLabel}</span><i aria-hidden="true">·</i></>}<code>{detailExchange.source}</code><i>→</i><code>{detailExchange.target}</code></small></div>
      <div className="traffic-detail__outcome"><b className={statusTone(detailExchange)}>{status}</b><span><strong>{duration(detailExchange.durationMs)}</strong></span><span><strong>{formatTrafficBytes(totalBytes)}</strong></span></div>
      <div className="traffic-detail__actions"><DrawerSizeButton fullScreen={maximized} subject="traffic details" onToggle={toggleMaximized} /><button type="button" onClick={onClose} aria-label="Close traffic details" title="Close">×</button></div>
      <div className="traffic-detail__header-context">
        {navigation}
        <TrafficInterventionBadges exchange={detailExchange} />
      </div>
    </header>

    <div className="traffic-detail__content">
      {!maximized && <TrafficOverview exchange={detailExchange} targetBinding={targetBinding} />}
      {http && <HttpTrafficDetail exchange={detailExchange} maximized={maximized} view={view} onView={setView} />}
      {semanticTCP && <TcpTrafficDetail exchange={detailExchange} exchanges={tcpCommandExchanges} maximized={maximized} view={view} onView={setView} />}
    </div>
  </aside>
}
