import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Operation } from '../../api/contracts/environments'
import { waitForEnvironmentOperation } from './operationPolling'

const environment = { project: 'Store Demo', name: 'QA / local' }
const operation: Operation = { project: environment.project, environment: environment.name, number: 7, type: 'down', state: 'running', actor: 'browser', startedAt: '', events: [] }

beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal('window', globalThis) })
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

describe('environment operation observation', () => {
  it('follows an accepted operation through completion without resubmitting it', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(operation), { headers: { 'Content-Type': 'application/json' } })).mockResolvedValueOnce(new Response(JSON.stringify({ ...operation, state: 'succeeded' }), { headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetch)
    const result = waitForEnvironmentOperation(environment, operation, { requestTimeoutMs: 1000, timeoutMs: 5000 })
    await vi.advanceTimersByTimeAsync(500)
    expect((await result).state).toBe('succeeded')
    expect(fetch).toHaveBeenCalledTimes(2)
    for (const [path, init] of fetch.mock.calls) {
      expect(path).toBe('/api/v1/environments/Store%20Demo/QA%20%2F%20local/operations/7')
      expect(init.method).toBeUndefined()
    }
    expect(vi.getTimerCount()).toBe(0)
  })

  it('preserves a terminal failure for structured presentation', async () => {
    const failed = { ...operation, state: 'failed', error: 'orders could not start' }
    expect(await waitForEnvironmentOperation(environment, failed)).toEqual(failed)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('cancels local observation without making another request', async () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    const result = waitForEnvironmentOperation(environment, operation, { signal: controller.signal, timeoutMs: 5000 })
    const rejection = expect(result).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await rejection
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetch).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('bounds a stalled inspection and aborts its request', async () => {
    let requestSignal: AbortSignal | undefined
    vi.stubGlobal('fetch', vi.fn((_input, options: RequestInit) => new Promise((_resolve, reject) => {
      requestSignal = options.signal as AbortSignal
      requestSignal.addEventListener('abort', () => reject(requestSignal?.reason), { once: true })
    })))
    const result = waitForEnvironmentOperation(environment, operation, { requestTimeoutMs: 1000, timeoutMs: 5000 })
    const rejection = expect(result).rejects.toThrow('Operation inspection timed out.')
    await vi.advanceTimersByTimeAsync(1250)
    await rejection
    expect(requestSignal?.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('bounds total observation even while inspections keep succeeding', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(operation), { headers: { 'Content-Type': 'application/json' } })))
    const result = waitForEnvironmentOperation(environment, operation, { requestTimeoutMs: 1000, timeoutMs: 1100 })
    const rejection = expect(result).rejects.toThrow('Operation observation timed out.')
    await vi.advanceTimersByTimeAsync(1100)
    await rejection
    expect(vi.getTimerCount()).toBe(0)
  })
})
