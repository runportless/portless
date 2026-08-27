import { useCallback, useEffect, useRef, useState } from 'react'
import { api, connectEvents, environmentPath } from '../../api'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'
import type { Environment, TrafficExchange, TrafficTrace } from '../../types'
import { loadTrafficSnapshot } from './trafficSnapshot'
import { mergeExchanges, mergeTraces, reconcileExchanges, reconcileTraces } from './trafficState'

type TrafficClearResult = { cleared: number; throughSequence: number }

function traceHasEdge(trace: TrafficTrace, edge: string) {
  if (!edge) return true
  return (trace.spans || []).some((span) => `${span.exchange.source}:${span.exchange.target}` === edge)
}

export function useTrafficStream(environment: Environment, edgeFilter: string, expandedTrace: number | null) {
  const [traces, setTraces] = useState<TrafficTrace[]>([])
  const [exchanges, setExchanges] = useState<TrafficExchange[]>([])
  const [clearing, setClearing] = useState(false)
  const [paused, setPaused] = useState(false)
  const [bufferedCount, setBufferedCount] = useState(0)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const [lastClearedThroughSequence, setLastClearedThroughSequence] = useState<number | null>(null)
  const pausedRef = useRef(false)
  const knownExchanges = useRef(new Set<number>())
  const exchangeBuffer = useRef(new Map<number, TrafficExchange>())
  const traceBuffer = useRef(new Map<number, TrafficTrace>())
  const expandedRef = useRef<number | null>(null)

  const applyTrafficClear = useCallback((throughSequence: number) => {
    setExchanges((current) => current.filter((exchange) => exchange.sequence > throughSequence))
    setTraces((current) => current.filter((trace) => trace.lastSequence > throughSequence))
    for (const sequence of knownExchanges.current) if (sequence <= throughSequence) knownExchanges.current.delete(sequence)
    for (const sequence of exchangeBuffer.current.keys()) if (sequence <= throughSequence) exchangeBuffer.current.delete(sequence)
    for (const [number, trace] of traceBuffer.current) if (trace.lastSequence <= throughSequence) traceBuffer.current.delete(number)
    setBufferedCount(exchangeBuffer.current.size)
    setLastClearedThroughSequence(throughSequence)
  }, [])

  useEffect(() => { knownExchanges.current = new Set(exchanges.map((exchange) => exchange.sequence)) }, [exchanges])
  useEffect(() => { expandedRef.current = expandedTrace }, [expandedTrace])

  useEffect(() => {
    let active = true
    let loading = false
    setTraces([])
    setExchanges([])
    setError(null)
    setLastClearedThroughSequence(null)
    pausedRef.current = false
    setPaused(false)
    knownExchanges.current.clear()
    exchangeBuffer.current.clear()
    traceBuffer.current.clear()
    setBufferedCount(0)

    const load = async () => {
      if (loading) return
      loading = true
      try {
        const snapshot = await loadTrafficSnapshot(environment, edgeFilter)
        if (!active) return
        setError((current) => current?.code === 'DAEMON_UNAVAILABLE' ? null : current)
        if (pausedRef.current) {
          for (const exchange of snapshot.exchanges) if (!knownExchanges.current.has(exchange.sequence)) exchangeBuffer.current.set(exchange.sequence, exchange)
          for (const trace of snapshot.traces) traceBuffer.current.set(trace.number, trace)
          setBufferedCount(exchangeBuffer.current.size)
        } else {
          setExchanges((current) => reconcileExchanges(current, snapshot.exchanges))
          setTraces((current) => reconcileTraces(current, snapshot.traces, snapshot.throughSequence))
        }
      } catch (value) {
        if (active) setError(actionError("Traffic couldn't be loaded", value))
      } finally {
        loading = false
      }
    }

    void load()
    const disconnect = connectEvents(environment, ['traffic.exchange', 'traffic.trace', 'traffic.cleared'], (type, value) => {
      if (type === 'traffic.cleared') {
        applyTrafficClear((value as TrafficClearResult).throughSequence)
        return
      }
      if (type === 'traffic.exchange') {
        const exchange = value as TrafficExchange
        if (pausedRef.current) {
          if (!knownExchanges.current.has(exchange.sequence)) exchangeBuffer.current.set(exchange.sequence, exchange)
          setBufferedCount(exchangeBuffer.current.size)
        } else setExchanges((current) => mergeExchanges(current, [exchange]))
        return
      }
      const trace = value as TrafficTrace
      const acceptTrace = async () => {
        let candidate = trace
        if (edgeFilter || expandedRef.current === trace.number) {
          try { candidate = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${trace.number}`)) } catch { /* Snapshot polling will reconcile an evicted trace. */ }
        }
        if (!active || (edgeFilter && !traceHasEdge(candidate, edgeFilter))) return
        if (pausedRef.current) traceBuffer.current.set(candidate.number, candidate)
        else setTraces((current) => mergeTraces(current, [candidate]))
      }
      void acceptTrace()
    })
    const timer = window.setInterval(() => void load(), 5000)
    return () => { active = false; disconnect(); window.clearInterval(timer) }
  }, [applyTrafficClear, edgeFilter, environment.name, environment.project])

  const togglePaused = useCallback(() => {
    if (!pausedRef.current) {
      pausedRef.current = true
      setPaused(true)
      return
    }
    pausedRef.current = false
    setExchanges((current) => mergeExchanges(current, [...exchangeBuffer.current.values()]))
    setTraces((current) => mergeTraces(current, [...traceBuffer.current.values()]))
    exchangeBuffer.current.clear()
    traceBuffer.current.clear()
    setBufferedCount(0)
    setPaused(false)
  }, [])

  const clearTraffic = useCallback(async () => {
    setClearing(true)
    setError(null)
    try {
      const result = await api<TrafficClearResult>(environmentPath(environment, '/traffic'), { method: 'DELETE' })
      applyTrafficClear(result.throughSequence)
    } catch (value) {
      setError(actionError("Traffic couldn't be cleared", value))
    } finally {
      setClearing(false)
    }
  }, [applyTrafficClear, environment])

  const mergeTrace = useCallback((trace: TrafficTrace) => {
    setTraces((current) => mergeTraces(current, [trace]))
  }, [])

  const dismissError = useCallback(() => setError(null), [])
  const reportError = useCallback((title: string, value: unknown) => setError(actionError(title, value)), [])

  return {
    traces,
    exchanges,
    clearing,
    paused,
    bufferedCount,
    error,
    lastClearedThroughSequence,
    togglePaused,
    clearTraffic,
    mergeTrace,
    dismissError,
    reportError,
  }
}
