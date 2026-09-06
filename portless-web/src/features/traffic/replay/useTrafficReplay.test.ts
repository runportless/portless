import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { APIError } from '../../../api'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficReplayWorkspace } from '../../../api/contracts/traffic_replay'
import { createTrafficReplaySession, replayWorkspaceQuery } from './useTrafficReplay'

const baseline = { project: 'store', environment: 'local', sequence: 42, startedAt: '2026-09-06T12:00:00Z', protocol: 'http', method: 'GET', source: 'external', target: 'checkout', requestCapture: { state: 'empty', exact: true, observedBytes: 0, capturedBytes: 0 } } as TrafficExchange
const workspace = (): TrafficReplayWorkspace => ({ project: 'store', environment: 'local', number: 3, createdAt: '2026-09-06T12:00:00Z', daemonStartedAt: '2026-09-06T10:00:00Z', revision: 1, nextRunNumber: 1, baseline,
  draft: { environment: 'local', method: 'GET', requestTarget: '/checkout?tag=a&tag=b', headers: { 'X-Tag': ['a', 'b'] }, bodyMode: 'captured', body: '' }, destination: { environment: 'local', provider: 'local', url: 'http://checkout.local.store.localhost', requiresConfirmation: false },
})

describe('replay session admission and reconciliation', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-06T12:00:00Z')) })
  afterEach(() => { vi.useRealTimers() })

  it('opening and resetting prepare only; double Send admits once and GETs reconcile a lost response', async () => {
    const calls: Array<{ path: string; method: string; body?: string }> = []
    let saved = workspace()
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string, options?: RequestInit): Promise<T> => {
      const method = options?.method || 'GET'
      calls.push({ path, method, body: options?.body as string | undefined })
      if (path.endsWith('/draft')) saved = { ...saved, revision: 2 }
      if (path.endsWith('/runs')) {
        saved = { ...saved, nextRunNumber: 2, run: { number: 1, revision: 2, state: 'completed', outcome: 'response-received', startedAt: baseline.startedAt, deadline: baseline.startedAt } }
        throw new APIError(0, { code: 'DAEMON_UNAVAILABLE', message: 'connection interrupted' })
      }
      return saved as T
    })
    await session.open(baseline)
    session.reset()
    expect(calls).toHaveLength(1)
    await Promise.all([session.send(), session.send()])
    expect(calls.filter((call) => call.path.endsWith('/runs'))).toHaveLength(1)
    expect(session.snapshot().awaitingReceipt).toBe(true)
    await vi.advanceTimersByTimeAsync(500)
    expect(session.snapshot().awaitingReceipt).toBe(false)
    expect(calls.filter((call) => call.method === 'GET')).toHaveLength(2)
    expect(calls.filter((call) => call.path.endsWith('/runs'))).toHaveLength(1)
    expect(calls.find((call) => call.path.endsWith('/runs'))?.body).toContain('"runNumber":1')
    expect(calls.find((call) => call.method === 'GET')?.path).toContain('expectedDaemonStartedAt=')
    session.dispose()
  })

  it('requires a distinct confirmation after preparing a remote write', async () => {
    const calls: string[] = []
    let saved = workspace()
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string): Promise<T> => {
      calls.push(path)
      if (path.endsWith('/draft')) saved = { ...saved, revision: 2, destination: { environment: 'qa', provider: 'remote', classification: 'qa', writePolicy: 'read-write', url: 'https://qa.example.test', requiresConfirmation: true } }
      if (path.endsWith('/runs')) saved = { ...saved, run: { number: 1, revision: 2, state: 'running', outcome: 'unknown', startedAt: baseline.startedAt, deadline: baseline.startedAt } }
      return saved as T
    })
    await session.open(baseline)
    await session.send()
    expect(session.snapshot().confirming).toBe(true)
    expect(calls.some((path) => path.endsWith('/runs'))).toBe(false)
    await session.confirm()
    expect(calls.filter((path) => path.endsWith('/runs'))).toHaveLength(1)
    session.dispose()
  })

  it('freezes the original and explicitly disposes before replacing it', async () => {
    const calls: Array<{ method: string; path: string }> = []
    let count = 0
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string, options?: RequestInit): Promise<T> => {
      calls.push({ method: options?.method || 'GET', path })
      if (options?.method === 'DELETE') return undefined as T
      return { ...workspace(), number: ++count } as T
    })
    await session.open(baseline)
    await session.open(baseline)
    expect(calls).toHaveLength(1)
    await session.open({ ...baseline, sequence: 43 })
    expect(session.snapshot().workspace?.baseline?.sequence).toBe(42)
    expect(session.snapshot().replacement?.sequence).toBe(43)
    await session.replace()
    expect(calls.map((call) => call.method)).toEqual(['POST', 'DELETE', 'POST'])
    session.dispose()
  })

  it('ignores a prepare response that arrives after Clear', async () => {
    let finish!: (value: TrafficReplayWorkspace) => void
    const delayed = new Promise<TrafficReplayWorkspace>((resolve) => { finish = resolve })
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(): Promise<T> => await delayed as T)
    const opening = session.open(baseline)
    session.clear()
    finish(workspace())
    await opening
    expect(session.snapshot().workspace).toBeNull()
    expect(session.snapshot().visible).toBe(false)
    session.dispose()
  })

  it('releases a closed session and opens a fresh captured request next time', async () => {
    const calls: Array<{ method: string; path: string; body?: string; keepalive?: boolean }> = []
    let number = 0
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string, options?: RequestInit): Promise<T> => {
      calls.push({ method: options?.method || 'GET', path, body: options?.body as string | undefined, keepalive: options?.keepalive })
      return { ...workspace(), number: ++number } as T
    })
    await session.open(baseline)
    const first = session.snapshot().workspace!
    session.change({ ...session.snapshot().draft!, body: 'edited', headers: [{ id: 1, name: 'Authorization', value: 'Bearer private', omitted: false, sensitive: true }] })
    session.close()
    expect(session.snapshot()).toMatchObject({ workspace: null, draft: null, visible: false })
    expect(calls[1]).toMatchObject({ method: 'DELETE', path: expect.stringMatching(/\/1$/), keepalive: true })
    expect(JSON.parse(calls[1].body!)).toEqual({ createdAt: first.createdAt, daemonStartedAt: first.daemonStartedAt })
    await session.open(baseline)
    expect(session.snapshot().draft?.body).toBe('')
    expect(session.snapshot().workspace?.number).not.toBe(first.number)
    expect(calls.map((call) => call.method)).toEqual(['POST', 'DELETE', 'POST'])
    session.dispose()
  })

  it('keeps active sessions alive beyond an hour and cleans up one hour after the last activity', async () => {
    const calls: Array<{ method?: string; path: string }> = []
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string, options?: RequestInit): Promise<T> => {
      calls.push({ method: options?.method, path })
      return workspace() as T
    })
    await session.open(baseline)
    for (let count = 0; count < 3; count++) {
      await vi.advanceTimersByTimeAsync(40 * 60_000)
      expect(session.snapshot().visible).toBe(true)
      session.activity()
      await vi.advanceTimersByTimeAsync(1)
    }
    expect(calls.filter((call) => call.path.endsWith('/activity'))).toHaveLength(3)
    expect(calls.some((call) => call.path.endsWith('/runs') || call.path.endsWith('/draft'))).toBe(false)
    await vi.advanceTimersByTimeAsync(60 * 60_000 - 2)
    expect(session.snapshot().workspace).not.toBeNull()
    await vi.advanceTimersByTimeAsync(1)
    expect(session.snapshot()).toMatchObject({ workspace: null, draft: null, visible: false })
    expect(calls.filter((call) => call.method === 'DELETE')).toHaveLength(1)
    session.dispose()
  })

  it('batches editor activity, retries interrupted touches, and never polls an untouched session to keep it alive', async () => {
    const calls: string[] = []
    let touches = 0
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string): Promise<T> => {
      calls.push(path)
      if (path.endsWith('/activity') && ++touches === 1) throw new APIError(0, { code: 'DAEMON_UNAVAILABLE', message: 'offline' })
      return workspace() as T
    })
    await session.open(baseline)
    await vi.advanceTimersByTimeAsync(20 * 60_000)
    expect(calls).toHaveLength(1)
    for (let count = 0; count < 100; count++) session.activity()
    await vi.advanceTimersByTimeAsync(1)
    expect(touches).toBe(1)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(touches).toBe(2)
    await vi.advanceTimersByTimeAsync(30 * 60_000)
    expect(touches).toBe(2)
    session.dispose()
  })

  it('releases a late creation response after close without resurrecting the editor', async () => {
    let finish!: (value: TrafficReplayWorkspace) => void
    const delayed = new Promise<TrafficReplayWorkspace>((resolve) => { finish = resolve })
    const calls: string[] = []
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(_path: string, options?: RequestInit): Promise<T> => {
      calls.push(options?.method || 'GET')
      return options?.method === 'POST' ? await delayed as T : undefined as T
    })
    const opening = session.open(baseline)
    session.close()
    finish(workspace())
    await opening
    expect(session.snapshot()).toMatchObject({ workspace: null, visible: false })
    expect(calls).toEqual(['POST', 'DELETE'])
    session.dispose()
  })

  it('releases a pending run on close and ignores its late receipt without sending again', async () => {
    let finish!: (value: TrafficReplayWorkspace) => void
    const delayed = new Promise<TrafficReplayWorkspace>((resolve) => { finish = resolve })
    const calls: Array<{ method?: string; path: string }> = []
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string, options?: RequestInit): Promise<T> => {
      calls.push({ method: options?.method, path })
      return path.endsWith('/runs') ? await delayed as T : workspace() as T
    })
    await session.open(baseline)
    const sending = session.send()
    await vi.advanceTimersByTimeAsync(1)
    session.close()
    finish({ ...workspace(), run: { number: 1, revision: 1, state: 'running', outcome: 'unknown', startedAt: baseline.startedAt, deadline: baseline.startedAt } })
    await sending
    await vi.advanceTimersByTimeAsync(60_000)
    expect(session.snapshot()).toMatchObject({ workspace: null, visible: false })
    expect(calls.filter((call) => call.path.endsWith('/runs'))).toHaveLength(1)
    expect(calls.filter((call) => call.method === 'DELETE')).toHaveLength(1)
    expect(calls.filter((call) => !call.method)).toHaveLength(0)
    session.dispose()
  })

  it.each(['destination', 'reset'] as const)('scrubs echoed credentials from the current and reset drafts on %s', async (action) => {
    const saved = workspace()
    saved.draft = { ...saved.draft!, requestTarget: '/echo?token=private-token', bodyMode: 'replacement', body: 'private-token', headers: { Authorization: ['[REDACTED]'], 'X-Echo': ['private-token'] } }
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(): Promise<T> => saved as T)
    await session.open(baseline)
    const draft = session.snapshot().draft!
    session.change({ ...draft, headers: draft.headers.map((row) => row.name === 'Authorization' ? { ...row, value: 'Bearer private-token' } : row) })
    if (action === 'destination') session.destination('qa')
    else session[action]()
    expect(JSON.stringify(session.snapshot().draft)).not.toContain('private-token')
    expect(session.snapshot().draft?.requestTarget).toContain('[REDACTED]')
    session.reset()
    expect(JSON.stringify(session.snapshot().draft)).not.toContain('private-token')
    session.dispose()
  })

  it('monotonically scrubs the original draft after each successful preparation before later resets', async () => {
    let saved = workspace()
    saved.draft = { ...saved.draft!, requestTarget: '/echo?first=first-token&second=second-token', bodyMode: 'replacement', body: 'first-token second-token', headers: { Authorization: ['[REDACTED]'], 'X-Echo': ['first-token second-token'] } }
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string): Promise<T> => {
      if (path.endsWith('/draft')) saved = { ...saved, revision: saved.revision + 1, destination: { ...saved.destination!, requiresConfirmation: true } }
      return saved as T
    })
    await session.open(baseline)
    for (const token of ['first-token', 'second-token']) {
      const draft = session.snapshot().draft!
      session.change({ ...draft, body: 'edited body', requestTarget: '/edited', headers: [{ id: 1, name: 'Authorization', value: `Bearer ${token}`, sensitive: true, omitted: false }] })
      await session.send()
      expect(session.snapshot().confirming).toBe(true)
      session.cancelConfirmation()
    }
    // Remove the final credential before reset: cleanup must retain both earlier redactions.
    session.change({ ...session.snapshot().draft!, headers: [] })
    session.reset()
    expect(session.snapshot().draft?.body).toBe('[REDACTED] [REDACTED]')
    expect(session.snapshot().draft?.requestTarget).toBe('/echo?first=[REDACTED]&second=[REDACTED]')
    expect(session.snapshot().draft?.headers.find((row) => row.name === 'X-Echo')?.value).toBe('[REDACTED] [REDACTED]')
    session.dispose()
  })

  it('rejects a new daemon workspace during GET reconciliation instead of displaying its result', async () => {
    let saved = workspace()
    const session = createTrafficReplaySession({ project: 'store', name: 'local' }, async <T,>(path: string, options?: RequestInit): Promise<T> => {
      if (path.endsWith('/draft')) saved = { ...saved, revision: 2 }
      if (path.endsWith('/runs')) saved = { ...saved, run: { number: 1, revision: 2, state: 'running', outcome: 'unknown', startedAt: baseline.startedAt, deadline: baseline.startedAt } }
      if (!options?.method) throw new APIError(409, { code: 'REPLAY_STALE', message: 'daemon changed' })
      return saved as T
    })
    await session.open(baseline)
    await session.send()
    await vi.advanceTimersByTimeAsync(500)
    expect(session.snapshot().expired).toBe(true)
    expect(session.snapshot().workspace?.run?.state).toBe('interrupted')
    expect(session.snapshot().error?.message).toContain('may have reached')
    session.dispose()
  })

  it('puts both workspace expectations on every result retrieval', () => {
    const query = new URLSearchParams(replayWorkspaceQuery(workspace(), true).slice(1))
    expect(query.get('expectedCreatedAt')).toBe(workspace().createdAt)
    expect(query.get('expectedDaemonStartedAt')).toBe(workspace().daemonStartedAt)
    expect(query.get('include')).toBe('result')
  })
})
