import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, environmentPath } from '../../api'
import { ActionErrorNotice } from '../../components/ActionError'
import type { Environment, TrafficExchange, TrafficTrace } from '../../types'
import { ExchangeTraceDrawer } from './ExchangeTraceDrawer'
import { TrafficControls } from './TrafficControls'
import { TrafficExchangeList } from './TrafficExchangeList'
import { traceCandidatesForExchange, traceContainsExchange } from './trafficSelection'
import { TrafficTraceList } from './TrafficTraceList'
import { traceNavigationItems, type TraceNavigationItem } from './TraceWaterfall'
import { useTrafficStream } from './useTrafficStream'
import { useTrafficViewModel, useTrafficViewState } from './useTrafficView'
import { WaterfallTraceDrawer } from './WaterfallTraceDrawer'

export function TrafficPanel({ environment }: { environment: Environment }) {
  const view = useTrafficViewState()
  const [selectedExchange, setSelectedExchange] = useState<TrafficExchange | null>(null)
  const [selectedTrace, setSelectedTrace] = useState<TrafficTrace | null>(null)
  const [detailNavigationPending, setDetailNavigationPending] = useState(false)
  const [traceNavigationScoped, setTraceNavigationScoped] = useState(false)
  const [selectedTraceNavigationKey, setSelectedTraceNavigationKey] = useState<string | null>(null)
  const [expandedTrace, setExpandedTrace] = useState<number | null>(null)
  const selectionRequest = useRef(0)
  const stream = useTrafficStream(environment, view.edgeFilter, expandedTrace)
  const traffic = useTrafficViewModel({
    traces: stream.traces,
    exchanges: stream.exchanges,
    mode: view.mode,
    search: view.search,
    edgeFilter: view.edgeFilter,
    resultFilter: view.resultFilter,
    protocol: view.protocol,
    tracePage: view.tracePage,
    exchangePage: view.exchangePage,
  })

  const resetSelection = useCallback(() => {
    selectionRequest.current += 1
    setSelectedExchange(null)
    setSelectedTrace(null)
    setDetailNavigationPending(false)
    setTraceNavigationScoped(false)
    setSelectedTraceNavigationKey(null)
  }, [])

  useEffect(() => {
    resetSelection()
    setExpandedTrace(null)
    view.resetPages()
  }, [environment.project, environment.name, resetSelection, view.edgeFilter, view.resetPages])

  useEffect(() => {
    const throughSequence = stream.lastClearedThroughSequence
    if (throughSequence === null) return
    selectionRequest.current += 1
    setSelectedExchange((current) => current && current.sequence <= throughSequence ? null : current)
    setSelectedTrace((current) => current && current.lastSequence <= throughSequence ? null : current)
    setDetailNavigationPending(false)
    setTraceNavigationScoped(false)
    setSelectedTraceNavigationKey(null)
    setExpandedTrace((current) => current !== null && current <= throughSequence ? null : current)
    view.resetPages()
  }, [stream.lastClearedThroughSequence, view.resetPages])

  useEffect(() => {
    if (view.tracePage !== traffic.tracePagination.page) view.setTracePage(traffic.tracePagination.page)
  }, [traffic.tracePagination.page, view.tracePage])

  useEffect(() => {
    if (view.exchangePage !== traffic.exchangePagination.page) view.setExchangePage(traffic.exchangePagination.page)
  }, [traffic.exchangePagination.page, view.exchangePage])

  const resolveTrace = async (exchange: TrafficExchange, hint?: TrafficTrace) => {
    for (const candidate of traceCandidatesForExchange(stream.traces, exchange, hint)) {
      let detail = candidate
      if ((candidate.spans?.length || 0) !== candidate.spanCount) {
        try { detail = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${candidate.number}`)) } catch { continue }
      }
      if (traceContainsExchange(detail, exchange.sequence)) return detail
    }
    return null
  }

  const inspectExchange = async (exchange: TrafficExchange, traceHint?: TrafficTrace, navigationKey?: string) => {
    const request = ++selectionRequest.current
    setDetailNavigationPending(true)
    if (!traceHint) setSelectedTrace(null)
    try {
      stream.dismissError()
      const [detail, trace] = await Promise.all([
        api<TrafficExchange>(environmentPath(environment, `/traffic/exchanges/${exchange.sequence}`)),
        traceHint ? resolveTrace(exchange, traceHint) : Promise.resolve(null),
      ])
      if (selectionRequest.current !== request) return
      setSelectedExchange(detail)
      setSelectedTrace(trace)
      setTraceNavigationScoped(Boolean(navigationKey))
      setSelectedTraceNavigationKey(navigationKey || null)
      if (trace) stream.mergeTrace(trace)
    } catch (value) {
      if (selectionRequest.current === request) stream.reportError("Traffic details aren't available", value)
    } finally {
      if (selectionRequest.current === request) setDetailNavigationPending(false)
    }
  }

  const inspectTraceItem = (item: TraceNavigationItem, trace: TrafficTrace) => {
    if (item.kind === 'exchange') {
      void inspectExchange(item.exchange, trace, item.key)
      return
    }
    selectionRequest.current += 1
    setDetailNavigationPending(false)
    setTraceNavigationScoped(true)
    setSelectedTraceNavigationKey(item.key)
    setSelectedExchange(item.exchange)
    setSelectedTrace(trace)
  }

  const closeExchange = () => resetSelection()

  const toggleTrace = async (trace: TrafficTrace) => {
    if (expandedTrace === trace.number) { setExpandedTrace(null); return }
    setExpandedTrace(trace.number)
    if (trace.spans?.length) return
    try {
      const detail = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${trace.number}`))
      stream.mergeTrace(detail)
    } catch (value) {
      stream.reportError("Trace details aren't available", value)
    }
  }

  const selectedTraceNavigationItems = useMemo(() => selectedTrace && traceNavigationScoped ? traceNavigationItems(selectedTrace) : undefined, [selectedTrace, traceNavigationScoped])
  const selectedTraceNavigationItem = useMemo(() => {
    if (!selectedTraceNavigationItems || !selectedExchange) return undefined
    return selectedTraceNavigationItems.find((item) => item.key === selectedTraceNavigationKey)
      || selectedTraceNavigationItems.find((item) => item.kind === 'transaction' && item.spans.some((span) => span.exchange.sequence === selectedExchange.sequence))
      || selectedTraceNavigationItems.find((item) => item.exchange.sequence === selectedExchange.sequence)
  }, [selectedTraceNavigationItems, selectedTraceNavigationKey, selectedExchange])

  useEffect(() => {
    if (!selectedTraceNavigationItem || selectedTraceNavigationItem.key === selectedTraceNavigationKey) return
    setSelectedTraceNavigationKey(selectedTraceNavigationItem.key)
    if (selectedTraceNavigationItem.kind === 'transaction') setSelectedExchange(selectedTraceNavigationItem.exchange)
  }, [selectedTraceNavigationItem, selectedTraceNavigationKey])

  const inspectVisibleExchange = (exchange: TrafficExchange) => {
    const index = traffic.visibleExchanges.findIndex((candidate) => candidate.sequence === exchange.sequence)
    if (index >= 0) view.showExchangeIndex(index)
    void inspectExchange(exchange)
  }

  return <div className="traffic-view">
    {stream.error && <ActionErrorNotice error={stream.error} onDismiss={stream.dismissError} />}
    <section className="panel traffic-panel">
      <TrafficControls
        mode={view.mode}
        onMode={view.setMode}
        stream={{
          paused: stream.paused,
          bufferedCount: stream.bufferedCount,
          clearing: stream.clearing,
          empty: stream.exchanges.length === 0 && stream.traces.length === 0 && stream.bufferedCount === 0,
          onClear: () => void stream.clearTraffic(),
          onTogglePaused: stream.togglePaused,
        }}
        summary={traffic.windowSummary}
        filters={{
          search: view.search,
          result: view.resultFilter,
          protocol: view.protocol,
          edge: view.edgeFilter,
          onSearch: view.setSearch,
          onResult: view.setResultFilter,
          onProtocol: view.setProtocol,
          onClearEdge: () => view.setEdgeFilter(''),
        }}
      />

      {view.mode === 'traces'
        ? <TrafficTraceList
          pagination={traffic.tracePagination}
          expandedTrace={expandedTrace}
          onToggleTrace={(trace) => void toggleTrace(trace)}
          onInspect={inspectTraceItem}
          onPage={view.setTracePage}
        />
        : <TrafficExchangeList pagination={traffic.exchangePagination} onInspect={inspectVisibleExchange} onPage={view.setExchangePage} />}
    </section>
    {selectedExchange && (view.mode === 'exchanges'
      ? <ExchangeTraceDrawer
        exchange={selectedExchange}
        exchanges={traffic.visibleExchanges}
        navigationPending={detailNavigationPending}
        targetBinding={environment.bindings?.find((binding) => binding.service.toLowerCase() === selectedExchange.target.toLowerCase())}
        onNavigate={inspectVisibleExchange}
        onClose={closeExchange}
      />
      : <WaterfallTraceDrawer
        exchange={selectedExchange}
        trace={selectedTrace}
        traceNavigationItems={selectedTraceNavigationItems}
        traceNavigationItem={selectedTraceNavigationItem}
        navigationPending={detailNavigationPending}
        targetBinding={environment.bindings?.find((binding) => binding.service.toLowerCase() === (selectedTraceNavigationItem?.exchange.target || selectedExchange.target).toLowerCase())}
        onNavigate={(item) => traceNavigationScoped && selectedTrace ? inspectTraceItem(item, selectedTrace) : void inspectExchange(item.exchange, selectedTrace || undefined)}
        onClose={closeExchange}
      />)}
  </div>
}
