import { api, environmentPath } from '../../api'
import type { Environment, Operation } from '../../api/contracts/environments'

export interface OperationObservationOptions {
  signal?: AbortSignal
  requestTimeoutMs?: number
  timeoutMs?: number
}

export async function waitForEnvironmentOperation(environment: Pick<Environment, 'project' | 'name'>, operation: Operation, options: OperationObservationOptions = {}): Promise<Operation> {
  let current = operation
  const deadline = new AbortController()
  const timer = options.timeoutMs === undefined ? undefined : window.setTimeout(() => deadline.abort(new Error('Operation observation timed out.')), options.timeoutMs)
  const signal = options.signal ? AbortSignal.any([options.signal, deadline.signal]) : deadline.signal
  try {
    while (current.state === 'running') {
      await waitForPoll(signal)
      const request = new AbortController()
      const requestTimer = options.requestTimeoutMs === undefined ? undefined : window.setTimeout(() => request.abort(new Error('Operation inspection timed out.')), options.requestTimeoutMs)
      const requestSignal = AbortSignal.any([signal, request.signal])
      try {
        current = await api<Operation>(environmentPath(environment, `/operations/${current.number}`), { signal: requestSignal })
      } catch (reason) {
        requestSignal.throwIfAborted()
        throw reason
      } finally {
        if (requestTimer !== undefined) window.clearTimeout(requestTimer)
      }
    }
    signal.throwIfAborted()
    return current
  } finally {
    if (timer !== undefined) window.clearTimeout(timer)
  }
}

function waitForPoll(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    signal.throwIfAborted()
    const abort = () => { window.clearTimeout(timer); reject(signal.reason) }
    const timer = window.setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, 250)
    signal.addEventListener('abort', abort, { once: true })
  })
}
