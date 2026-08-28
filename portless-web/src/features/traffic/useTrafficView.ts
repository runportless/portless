import { useCallback, useEffect, useMemo, useState } from 'react'
import { paginateItems } from '../../components/PanelPagination'
import type { TrafficExchange, TrafficTrace } from '../../types'
import { filterExchanges, filterTraces, trafficWindowSummary, type TrafficProtocolFilter, type TrafficResultFilter } from './trafficState'

export type TrafficMode = 'traces' | 'exchanges'

export const trafficPageSize = 25

function requestedTrafficMode() {
  return new URLSearchParams(location.search).get('mode') === 'exchanges' ? 'exchanges' : 'traces'
}

function requestedTrafficProtocol(): TrafficProtocolFilter {
  const value = new URLSearchParams(location.search).get('protocol')
  return value === 'http' || value === 'tcp' ? value : 'all'
}

export function useTrafficViewState() {
  const [mode, setMode] = useState<TrafficMode>(requestedTrafficMode)
  const [search, setSearch] = useState('')
  const [edgeFilter, setEdgeFilter] = useState(() => new URLSearchParams(location.search).get('edge') || '')
  const [resultFilter, setResultFilter] = useState<TrafficResultFilter>('all')
  const [protocol, setProtocol] = useState<TrafficProtocolFilter>(requestedTrafficProtocol)
  const [tracePage, setTracePage] = useState(0)
  const [exchangePage, setExchangePage] = useState(0)

  useEffect(() => { setTracePage(0); setExchangePage(0) }, [search, resultFilter, edgeFilter])
  useEffect(() => { setExchangePage(0) }, [protocol])

  const resetPages = useCallback(() => { setTracePage(0); setExchangePage(0) }, [])
  const showExchangeIndex = useCallback((index: number) => setExchangePage(Math.floor(index / trafficPageSize)), [])

  return {
    mode,
    setMode,
    search,
    setSearch,
    edgeFilter,
    setEdgeFilter,
    resultFilter,
    setResultFilter,
    protocol,
    setProtocol,
    tracePage,
    setTracePage,
    exchangePage,
    setExchangePage,
    resetPages,
    showExchangeIndex,
  }
}

function exchangeHasEdge(exchange: TrafficExchange, edge: string) {
  return !edge || `${exchange.source}:${exchange.target}` === edge
}

export function useTrafficViewModel({ traces, exchanges, mode, search, edgeFilter, resultFilter, protocol, tracePage, exchangePage }: {
  traces: TrafficTrace[]
  exchanges: TrafficExchange[]
  mode: TrafficMode
  search: string
  edgeFilter: string
  resultFilter: TrafficResultFilter
  protocol: TrafficProtocolFilter
  tracePage: number
  exchangePage: number
}) {
  const windowSummary = useMemo(() => trafficWindowSummary(mode === 'traces' ? exchanges.filter((exchange) => !exchange.background) : exchanges), [exchanges, mode])
  const visibleTraces = useMemo(() => filterTraces(traces, search, resultFilter), [traces, search, resultFilter])
  const visibleExchanges = useMemo(() => filterExchanges(exchanges.filter((exchange) => exchangeHasEdge(exchange, edgeFilter)), search, resultFilter, protocol), [exchanges, edgeFilter, search, resultFilter, protocol])
  const tracePagination = useMemo(() => paginateItems(visibleTraces, tracePage, trafficPageSize), [visibleTraces, tracePage])
  const exchangePagination = useMemo(() => paginateItems(visibleExchanges, exchangePage, trafficPageSize), [visibleExchanges, exchangePage])

  return { windowSummary, visibleTraces, visibleExchanges, tracePagination, exchangePagination }
}
