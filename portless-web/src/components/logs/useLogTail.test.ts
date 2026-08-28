import { afterEach, describe, expect, it, vi } from 'vitest'
import { startLogTailPolling } from './useLogTail'

type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void; reject: (error: unknown) => void }

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((onResolve, onReject) => { resolve = onResolve; reject = onReject })
  return { promise, resolve, reject }
}

async function flushPromises() {
  await Promise.resolve()
  await Promise.resolve()
}

afterEach(() => vi.useRealTimers())

describe('log tail polling', () => {
  it('never overlaps requests and waits the full interval after a response', async () => {
    vi.useFakeTimers()
    const first = deferred<string>()
    const second = deferred<string>()
    const signals: AbortSignal[] = []
    const load = vi.fn((signal: AbortSignal) => {
      signals.push(signal)
      return load.mock.calls.length === 1 ? first.promise : second.promise
    })
    const onLoad = vi.fn()
    const stop = startLogTailPolling({ load, onLoad, onError: vi.fn(), pollMilliseconds: 1_000 })

    expect(load).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5_000)
    expect(load).toHaveBeenCalledTimes(1)

    first.resolve('first')
    await flushPromises()
    expect(onLoad).toHaveBeenCalledWith('first')
    await vi.advanceTimersByTimeAsync(999)
    expect(load).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(load).toHaveBeenCalledTimes(2)

    stop()
    expect(signals[1].aborted).toBe(true)
  })

  it('cancels an old source and ignores its late response', async () => {
    vi.useFakeTimers()
    const oldResult = deferred<string>()
    let oldSignal: AbortSignal | undefined
    const accepted: string[] = []
    const stopOld = startLogTailPolling({
      load: (signal) => { oldSignal = signal; return oldResult.promise },
      onLoad: (value) => accepted.push(value),
      onError: vi.fn(),
    })

    stopOld()
    expect(oldSignal?.aborted).toBe(true)
    const stopNew = startLogTailPolling({
      load: async () => 'new source',
      onLoad: (value) => accepted.push(value),
      onError: vi.fn(),
    })
    await flushPromises()
    expect(accepted).toEqual(['new source'])

    oldResult.resolve('stale source')
    await flushPromises()
    expect(accepted).toEqual(['new source'])
    stopNew()
  })

  it('reports failures and continues polling without overlap', async () => {
    vi.useFakeTimers()
    const failure = new Error('unavailable')
    const load = vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce('recovered')
    const onLoad = vi.fn()
    const onError = vi.fn()
    const stop = startLogTailPolling({ load, onLoad, onError, pollMilliseconds: 1_000 })

    await flushPromises()
    expect(onError).toHaveBeenCalledWith(failure)
    expect(load).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(load).toHaveBeenCalledTimes(2)
    await flushPromises()
    expect(onLoad).toHaveBeenCalledWith('recovered')
    stop()
  })
})
