import { api, connectEvents, environmentPath } from '../../api'
import type { Environment } from '../../api/contracts/environments'
import type { TrafficClearResponse, TrafficExchange, TrafficTrace, TrafficTraceList } from '../../api/contracts/traffic'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'
import { createTrafficRefresh } from './trafficRefresh'
import { loadTrafficSnapshot, loadTrafficTraces } from './trafficSnapshot'
import { mergeExchanges, mergeTraces, reconcileExchanges, reconcileTraces } from './trafficState'

export interface TrafficSessionState {
  traces: TrafficTrace[]
  exchanges: TrafficExchange[]
  clearing: boolean
  paused: boolean
  bufferedCount: number
  error: ActionErrorDetails | null
  lastClearedThroughSequence: number | null
}

export function initialTrafficState(): TrafficSessionState {
  return { traces: [], exchanges: [], clearing: false, paused: false, bufferedCount: 0, error: null, lastClearedThroughSequence: null }
}

const bufferLimit = 5000

// This environment-scoped session owns requests and bounded live buffers. React
// receives one update per batch; all async responses share its generation guard.
export function createTrafficSession(environment: Pick<Environment, 'project' | 'name'>, edgeFilter: string, onChange: (state: TrafficSessionState) => void) {
  let state = initialTrafficState()
  let active = true
  let generation = 0
  let abort = new AbortController()
  let minimumRevision = 0
  let clearRevision = 0
  let clearedThrough = 0
  let wantedRevision = 0
  let fullSnapshotNeeded = true
  let expanded: number | null = null
  let expandedRevision = 0
  let clearRequest = 0
  let flushTimer: ReturnType<typeof setTimeout> | undefined
  const exchangeBuffer = new Map<number, TrafficExchange>()
  const traceBuffer = new Map<number, TrafficTrace>()
  let knownExchanges = new Set<number>()

  const update = (patch: Partial<TrafficSessionState>) => {
    if (!active) return
    state = { ...state, ...patch }
    onChange(state)
  }
  const invalidate = () => {
    generation++
    abort.abort()
    abort = new AbortController()
  }
  const resetBuffers = () => {
    exchangeBuffer.clear()
    traceBuffer.clear()
    clearTimeout(flushTimer)
    flushTimer = undefined
  }
  const buffer = <T>(items: Map<number, T>, key: number, value: T) => {
    items.set(key, value)
    if (items.size > bufferLimit) {
      items.delete(items.keys().next().value!)
      fullSnapshotNeeded = true
    }
  }
  const flush = () => {
    clearTimeout(flushTimer)
    flushTimer = undefined
    if (state.paused) {
      update({ bufferedCount: exchangeBuffer.size })
      return
    }
    const exchanges = mergeExchanges(state.exchanges, [...exchangeBuffer.values()])
    const traces = edgeFilter ? state.traces : mergeTraces(state.traces, [...traceBuffer.values()])
    resetBuffers()
    update({ exchanges, traces, bufferedCount: 0 })
  }
  const scheduleFlush = () => { flushTimer ??= setTimeout(flush, 50) }

  const acceptDetail = (trace: TrafficTrace) => {
    if (!active || trace.project !== environment.project || trace.environment !== environment.name || trace.revision <= clearRevision || trace.lastSequence <= clearedThrough) return
    // A delayed detail must never restore a trace removed by a snapshot or Clear.
    if (!state.traces.some((current) => current.number === trace.number)) return
    update({ traces: mergeTraces(state.traces, [trace]) })
  }

  const detailRefresh = createTrafficRefresh(async () => {
    if (!active || state.paused || expanded === null) return
    const number = expanded
    const current = state.traces.find((trace) => trace.number === number)
    if (!current) return
    const required = Math.max(current.revision, expandedRevision)
    if (current.spans?.length === current.spanCount && current.revision >= required) return
    const requestGeneration = generation
    try {
      const detail = await api<TrafficTrace>(environmentPath(environment, `/traffic/traces/${number}`), { signal: abort.signal })
      if (!active || generation !== requestGeneration || expanded !== number || state.paused) return
      acceptDetail(detail)
      if (expandedRevision > detail.revision) detailRefresh.request()
    } catch (value) {
      if (active && generation === requestGeneration && expanded === number) update({ error: actionError("Trace details aren't available", value) })
    }
  })

  const snapshotRefresh = createTrafficRefresh(async () => {
    if (!active || state.paused || (!fullSnapshotNeeded && wantedRevision <= minimumRevision)) return
    const full = fullSnapshotNeeded
    fullSnapshotNeeded = false
    const requestGeneration = generation
    try {
      const snapshot: TrafficTraceList & { exchanges?: TrafficExchange[] } = full
        ? await loadTrafficSnapshot(environment, edgeFilter, abort.signal)
        : await loadTrafficTraces(environment, edgeFilter, abort.signal)
      if (!active || generation !== requestGeneration || state.paused || snapshot.revision < minimumRevision) return
      flush()
      const traces = reconcileTraces(state.traces, snapshot.traces, snapshot.revision)
      const exchanges = snapshot.exchanges ? reconcileExchanges(state.exchanges, snapshot.exchanges) : state.exchanges
      minimumRevision = snapshot.revision
      update({ traces, exchanges, error: state.error?.code === 'DAEMON_UNAVAILABLE' ? null : state.error })
      detailRefresh.request()
    } catch (value) {
      if (active && generation === requestGeneration) {
        fullSnapshotNeeded ||= full
        update({ error: actionError("Traffic couldn't be loaded", value) })
      }
    }
  })

  const requestSnapshot = (full = false, immediate = false) => {
    fullSnapshotNeeded ||= full
    snapshotRefresh.request(immediate)
  }

  const applyClear = (result: TrafficClearResponse) => {
    if (!active || result.revision <= clearRevision) return
    invalidate()
    clearRevision = result.revision
    minimumRevision = Math.max(minimumRevision, result.revision)
    clearedThrough = Math.max(clearedThrough, result.throughSequence)
    for (const sequence of exchangeBuffer.keys()) if (sequence <= clearedThrough) exchangeBuffer.delete(sequence)
    for (const [number, trace] of traceBuffer) if (trace.revision <= clearRevision || trace.lastSequence <= clearedThrough) traceBuffer.delete(number)
    knownExchanges = new Set([...knownExchanges].filter((sequence) => sequence > clearedThrough))
    update({
      exchanges: state.exchanges.filter((exchange) => exchange.sequence > clearedThrough),
      traces: state.traces.filter((trace) => trace.revision > clearRevision && trace.lastSequence > clearedThrough),
      bufferedCount: exchangeBuffer.size,
      lastClearedThroughSequence: clearedThrough,
    })
    requestSnapshot(true, true)
  }

  const disconnect = connectEvents(environment, ['traffic.exchange', 'traffic.trace', 'traffic.cleared'], (type, value) => {
    if (!active) return
    if (type === 'traffic.cleared') { applyClear(value as TrafficClearResponse); return }
    if (type === 'traffic.exchange') {
      const exchange = value as TrafficExchange
      if (exchange.sequence <= clearedThrough || (state.paused && knownExchanges.has(exchange.sequence))) return
      buffer(exchangeBuffer, exchange.sequence, exchange)
      scheduleFlush()
      return
    }
    const trace = value as TrafficTrace
    if (trace.revision <= minimumRevision || trace.lastSequence <= clearedThrough) return
    wantedRevision = Math.max(wantedRevision, trace.revision)
    const previous = traceBuffer.get(trace.number)
    if (!previous || trace.revision > previous.revision) buffer(traceBuffer, trace.number, trace)
    if (trace.number === expanded) {
      expandedRevision = Math.max(expandedRevision, trace.revision)
      detailRefresh.request()
    }
    scheduleFlush()
    requestSnapshot()
  }, () => {
    // Revisions are daemon-local. A reconnect invalidates pending responses and
    // establishes a fresh baseline, including after daemon replacement.
    invalidate()
    minimumRevision = clearRevision = clearedThrough = wantedRevision = expandedRevision = 0
    resetBuffers()
    knownExchanges.clear()
    update({ traces: [], exchanges: [], bufferedCount: 0 })
    requestSnapshot(true, true)
  })
  requestSnapshot(true, true)
  const poll = setInterval(() => requestSnapshot(true), 5000)

  return {
    setExpanded(number: number | null) {
      if (expanded !== number) expandedRevision = 0
      expanded = number
      detailRefresh.request(true)
    },
    mergeTrace: acceptDetail,
    togglePaused() {
      if (!state.paused) {
        flush()
        knownExchanges = new Set(state.exchanges.map((exchange) => exchange.sequence))
        update({ paused: true })
        return
      }
      // Buffered events are only a bounded count/preview. Resume obtains the
      // complete retained window, which also removes merged or evicted rows.
      invalidate()
      resetBuffers()
      update({ paused: false, bufferedCount: 0 })
      requestSnapshot(true, true)
    },
    async clearTraffic() {
      if (!active || state.clearing) return
      const request = ++clearRequest
      const requestGeneration = generation
      update({ clearing: true, error: null })
      try {
        const result = await api<TrafficClearResponse>(environmentPath(environment, '/traffic'), { method: 'DELETE', signal: abort.signal })
        if (active && generation === requestGeneration) applyClear(result)
      } catch (value) {
        if (active && generation === requestGeneration) update({ error: actionError("Traffic couldn't be cleared", value) })
      } finally {
        if (active && request === clearRequest) update({ clearing: false })
      }
    },
    dismissError() { update({ error: null }) },
    reportError(title: string, value: unknown) { update({ error: actionError(title, value) }) },
    dispose() {
      active = false
      invalidate()
      disconnect()
      clearInterval(poll)
      resetBuffers()
      snapshotRefresh.dispose()
      detailRefresh.dispose()
    },
  }
}
