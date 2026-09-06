import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, connectEvents } from '../../api'
import type { TrafficExchange, TrafficTrace } from '../../api/contracts/traffic'
import { createTrafficSession, initialTrafficState, type TrafficSessionState } from './trafficSession'
import { loadTrafficSnapshot, loadTrafficTraces } from './trafficSnapshot'

vi.mock('../../api', async (original) => ({ ...await original<typeof import('../../api')>(), api: vi.fn(), connectEvents: vi.fn() }))
vi.mock('./trafficSnapshot', () => ({ loadTrafficSnapshot: vi.fn(), loadTrafficTraces: vi.fn() }))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => { resolve = complete })
  return { promise, resolve }
}

function exchange(sequence: number): TrafficExchange {
  return { project: 'store', environment: 'local', sequence, protocol: 'http', source: 'external', target: 'checkout', startedAt: '2026-09-05T00:00:00Z', completedAt: '2026-09-05T00:00:01Z', durationMs: 1000, requestBytes: 0, responseBytes: 0, background: false }
}

function trace(revision: number, number = 1): TrafficTrace {
  return { project: 'store', environment: 'local', number, lastSequence: revision, revision, protocol: 'http', source: 'external', target: 'checkout', startedAt: '2026-09-05T00:00:00Z', completedAt: '2026-09-05T00:00:01Z', durationMs: 1000, background: false, provisional: false, error: false, faulted: false, correlation: 'exact', spanCount: 1 }
}

function detail(revision: number): TrafficTrace {
  return { ...trace(revision), spans: [{ exchange: exchange(revision), depth: 0, startOffsetMs: 0, correlation: 'exact' }] }
}

function snapshot(revision: number, traces = [trace(revision)]) {
  return { traces, exchanges: [exchange(revision)], revision, throughSequence: revision }
}

const sessions: ReturnType<typeof createTrafficSession>[] = []

async function start(edge = '') {
  let event!: (type: string, value: unknown) => void
  let connected!: () => void
  let state = initialTrafficState()
  const updates: TrafficSessionState[] = []
  const disconnect = vi.fn()
  vi.mocked(connectEvents).mockImplementation((_environment, _topics, onEvent, onConnected) => {
    event = onEvent
    connected = onConnected!
    return disconnect
  })
  const session = createTrafficSession({ project: 'store', name: 'local' }, edge, (value) => { state = value; updates.push(value) })
  sessions.push(session)
  await vi.advanceTimersByTimeAsync(0)
  return { session, event, connected, disconnect, updates, get state() { return state } }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(loadTrafficSnapshot).mockResolvedValue(snapshot(1))
  vi.mocked(loadTrafficTraces).mockResolvedValue(snapshot(1))
  vi.mocked(api).mockResolvedValue(detail(1))
})

afterEach(() => {
  for (const session of sessions.splice(0)) session.dispose()
  vi.clearAllMocks()
  vi.useRealTimers()
})

describe('traffic session requests', () => {
  it('coalesces an edge-filtered burst into summaries without fetching individual traces', async () => {
    const live = await start('checkout:orders')
    const pending = deferred<ReturnType<typeof snapshot>>()
    vi.mocked(loadTrafficTraces).mockReturnValueOnce(pending.promise)
    const updatesBefore = live.updates.length
    for (let revision = 2; revision <= 50; revision++) live.event('traffic.trace', trace(revision))
    await vi.advanceTimersByTimeAsync(50)
    expect(loadTrafficTraces).toHaveBeenCalledTimes(1)
    for (let revision = 51; revision <= 100; revision++) live.event('traffic.trace', trace(revision))
    pending.resolve(snapshot(100, [trace(100, 20)]))
    await vi.advanceTimersByTimeAsync(100)
    expect(loadTrafficTraces).toHaveBeenCalledTimes(1)
    expect(api).not.toHaveBeenCalled()
    expect(live.state.traces.map((item) => item.number)).toEqual([20])
    expect(live.updates.length - updatesBefore).toBeLessThan(6)
  })

  it('allows one expanded detail request and one follow-up for the newest revision', async () => {
    const live = await start()
    const pending = deferred<TrafficTrace>()
    vi.mocked(api).mockReturnValueOnce(pending.promise).mockResolvedValueOnce(detail(20))
    vi.mocked(loadTrafficTraces).mockResolvedValue(snapshot(20))
    live.session.setExpanded(1)
    await vi.advanceTimersByTimeAsync(0)
    for (let revision = 2; revision <= 20; revision++) live.event('traffic.trace', trace(revision))
    await vi.advanceTimersByTimeAsync(100)
    expect(api).toHaveBeenCalledTimes(1)
    pending.resolve(detail(1))
    await vi.advanceTimersByTimeAsync(100)
    expect(api).toHaveBeenCalledTimes(2)
    expect(live.state.traces[0].revision).toBe(20)
    expect(live.state.traces[0].spans?.[0].exchange.sequence).toBe(20)
  })

  it('does not resurrect rows from delayed snapshots, details, or events after Clear', async () => {
    const live = await start()
    const pendingDetail = deferred<TrafficTrace>()
    const pendingSnapshot = deferred<ReturnType<typeof snapshot>>()
    vi.mocked(api).mockReturnValueOnce(pendingDetail.promise)
    vi.mocked(loadTrafficTraces).mockReturnValueOnce(pendingSnapshot.promise)
    live.session.setExpanded(1)
    live.event('traffic.trace', trace(2))
    await vi.advanceTimersByTimeAsync(50)
    vi.mocked(loadTrafficSnapshot).mockResolvedValue({ traces: [], exchanges: [], revision: 3, throughSequence: 2 })
    live.event('traffic.cleared', { cleared: 2, throughSequence: 2, revision: 3 })
    pendingSnapshot.resolve(snapshot(2))
    pendingDetail.resolve(detail(1))
    live.event('traffic.trace', trace(2))
    live.event('traffic.exchange', exchange(2))
    await vi.advanceTimersByTimeAsync(150)
    expect(live.state.traces).toEqual([])
    expect(live.state.exchanges).toEqual([])
    expect(live.state.lastClearedThroughSequence).toBe(2)
  })

  it('preserves events newer than a snapshot even when they arrived during its request', async () => {
    const live = await start()
    const pending = deferred<ReturnType<typeof snapshot>>()
    vi.mocked(loadTrafficTraces).mockReturnValueOnce(pending.promise).mockResolvedValue(snapshot(4))
    live.event('traffic.trace', trace(2))
    await vi.advanceTimersByTimeAsync(50)
    live.event('traffic.trace', trace(4))
    pending.resolve(snapshot(2))
    await vi.advanceTimersByTimeAsync(1)
    expect(live.state.traces[0].revision).toBe(4)
    await vi.advanceTimersByTimeAsync(100)
    expect(loadTrafficTraces).toHaveBeenCalledTimes(2)
  })

  it('caps paused buffers and resumes from the retained snapshot without per-event fetches', async () => {
    const live = await start()
    live.session.togglePaused()
    for (let revision = 2; revision <= 6000; revision++) {
      live.event('traffic.exchange', exchange(revision))
      live.event('traffic.trace', trace(revision, revision))
    }
    await vi.advanceTimersByTimeAsync(10_000)
    expect(live.state.bufferedCount).toBe(5000)
    expect(live.state.traces[0].number).toBe(1)
    expect(loadTrafficTraces).not.toHaveBeenCalled()
    expect(loadTrafficSnapshot).toHaveBeenCalledTimes(1)
    vi.mocked(loadTrafficSnapshot).mockResolvedValue(snapshot(6000, [trace(6000, 6000)]))
    live.session.togglePaused()
    await vi.advanceTimersByTimeAsync(50)
    expect(live.state.bufferedCount).toBe(0)
    expect(live.state.traces.map((item) => item.number)).toEqual([6000])
    expect(live.state.exchanges.map((item) => item.sequence)).toEqual([6000])
  })

  it('discards previous-environment responses and aborts their requests on disposal', async () => {
    const live = await start()
    const pending = deferred<TrafficTrace>()
    vi.mocked(api).mockReturnValueOnce(pending.promise)
    live.session.setExpanded(1)
    await vi.advanceTimersByTimeAsync(0)
    const signal = vi.mocked(api).mock.calls[0][1]?.signal
    live.session.dispose()
    const before = live.updates.length
    pending.resolve(detail(2))
    await vi.advanceTimersByTimeAsync(500)
    expect(signal?.aborted).toBe(true)
    expect(live.updates).toHaveLength(before)
    expect(live.disconnect).toHaveBeenCalledTimes(1)
  })

  it('resets daemon-local revisions on reconnect and accepts a lower new baseline', async () => {
    vi.mocked(loadTrafficSnapshot).mockResolvedValue(snapshot(100))
    const live = await start()
    vi.mocked(loadTrafficSnapshot).mockResolvedValue(snapshot(2, [trace(2, 101)]))
    live.connected()
    await vi.advanceTimersByTimeAsync(50)
    expect(live.state.traces.map((item) => [item.number, item.revision])).toEqual([[101, 2]])
  })
})
