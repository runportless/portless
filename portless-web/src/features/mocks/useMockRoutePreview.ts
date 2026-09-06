import { useEffect, useRef, useState } from 'react'
import type { MockPreview, MockRequest, MockScenario } from '../../api/contracts/mocks'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'
import type { MockRouteDraft } from './MockRouteEditor'
import { mockPreviewRequest, newMockPreviewRequest, type MockPreviewRequestDraft } from './mockPreview'

export type RunMockPreview = (draft: MockRouteDraft, originalRoute: string | undefined, request: MockRequest, signal: AbortSignal) => Promise<MockPreview>

export function useMockRoutePreview(draft: MockRouteDraft, scenario: MockScenario, originalRoute: string | undefined, onPreview: RunMockPreview) {
  const [request, setRequest] = useState(() => newMockPreviewRequest(draft))
  const [result, setResult] = useState<{ fingerprint: string; value: MockPreview } | null>(null)
  const [error, setError] = useState<{ fingerprint: string; value: ActionErrorDetails } | null>(null)
  const [running, setRunning] = useState(false)
  const pending = useRef<{ controller: AbortController; fingerprint: string } | null>(null)
  const fingerprint = JSON.stringify({ draft, originalRoute, routes: scenario.routes, request })
  const currentFingerprint = useRef(fingerprint)
  currentFingerprint.current = fingerprint

  const cancel = () => {
    pending.current?.controller.abort()
    pending.current = null
    setRunning(false)
  }

  useEffect(() => {
    if (pending.current && pending.current.fingerprint !== fingerprint) cancel()
  }, [fingerprint])

  useEffect(() => () => {
    pending.current?.controller.abort()
    pending.current = null
  }, [])

  const changeRequest = (next: MockPreviewRequestDraft) => {
    setRequest(next)
    setError(null)
  }

  const run = async () => {
    if (pending.current) return
    setError(null)
    let input: MockRequest
    try { input = mockPreviewRequest(request) }
    catch (reason) { setError({ fingerprint, value: actionError("Preview couldn't run", reason) }); return }
    const controller = new AbortController()
    const attempt = { controller, fingerprint }
    pending.current = attempt
    setRunning(true)
    let timedOut = false
    const timeout = window.setTimeout(() => { timedOut = true; controller.abort() }, 10_000)
    try {
      const value = await onPreview(draft, originalRoute, input, controller.signal)
      if (!controller.signal.aborted && pending.current === attempt && currentFingerprint.current === fingerprint) {
        setResult({ fingerprint, value })
      }
    } catch (reason) {
      if (pending.current === attempt && currentFingerprint.current === fingerprint && (!controller.signal.aborted || timedOut)) {
        setError({ fingerprint, value: actionError("Preview couldn't run", timedOut ? new Error('Preview timed out. Try running it again.') : reason) })
      }
    } finally {
      window.clearTimeout(timeout)
      if (pending.current === attempt) {
        pending.current = null
        setRunning(false)
      }
    }
  }

  return {
    request, changeRequest, resetRequest: () => changeRequest(newMockPreviewRequest(draft)),
    result: result?.value ?? null, outdated: !!result && result.fingerprint !== fingerprint,
    error: error?.fingerprint === fingerprint ? error.value : null,
    dismissError: () => setError(null), running, run, cancel,
  }
}
