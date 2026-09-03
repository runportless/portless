import { useCallback, useEffect, useRef, useState } from 'react'
import { api, environmentPath, jsonBody } from '../../api'
import type { Environment, Operation } from '../../api/contracts/environments'
import { actionError, type ActionErrorDetails } from '../../components/ActionError'
import { waitForEnvironmentOperation } from './operationPolling'

const operationRequestTimeoutMs = 10_000
const operationObservationTimeoutMs = 120_000
type LifecycleAction = 'up' | 'down'
type EnvironmentAction = LifecycleAction | 'forget'
type PendingAction = {
  identity: string
  environment: Pick<Environment, 'project' | 'name'>
  action: EnvironmentAction
  controller: AbortController
  operation?: Operation
}
type ActionState = {
  identity: string
  busy: EnvironmentAction | null
  error: ActionErrorDetails | null
  forgetError: ActionErrorDetails | null
  trackingInterrupted: boolean
}

const idleState = { busy: null, error: null, forgetError: null, trackingInterrupted: false }

export function environmentLifecycleLabel(environment: Pick<Environment, 'status'>, busy: EnvironmentAction | null) {
  if (busy === 'up' || environment.status === 'starting') return 'STARTING…'
  if (busy === 'down' || environment.status === 'stopping') return 'STOPPING…'
  if (environment.status === 'recovering') return 'RECOVERING…'
  return environment.status === 'stopped' ? 'START ALL' : 'STOP ALL'
}

export function useEnvironmentActions(environment: Environment | undefined, identity: string, live: boolean, latestOperation: Operation | undefined, onChanged: () => Promise<void>, onNavigate: (path: string) => void) {
  const [state, setState] = useState<ActionState>({ ...idleState, identity })
  const pending = useRef<PendingAction | null>(null)
  const currentIdentity = useRef(identity)
  const changed = useRef(onChanged)
  const navigate = useRef(onNavigate)
  currentIdentity.current = identity
  changed.current = onChanged
  navigate.current = onNavigate

  useEffect(() => {
    setState({ ...idleState, identity })
    return () => {
      if (pending.current?.identity === identity) {
        pending.current.controller.abort()
        pending.current = null
      }
    }
  }, [identity])

  const isCurrent = useCallback((action: PendingAction) => pending.current === action && currentIdentity.current === action.identity, [])

  const finish = useCallback((action: PendingAction, completed: Operation) => {
    if (!isCurrent(action)) return
    pending.current = null
    action.controller.abort()
    setState({ ...idleState, identity: action.identity, error: completed.state === 'failed'
      ? actionError(`Environment ${action.action === 'up' ? 'startup' : 'shutdown'} failed`, new Error(completed.error || 'The operation failed. Open Timeline for details.')) : null })
    void changed.current()
  }, [isCurrent])

  const observe = useCallback(async (action: PendingAction) => {
    if (!action.operation || !isCurrent(action)) return
    const controller = action.controller
    try {
      const completed = await waitForEnvironmentOperation(action.environment, action.operation, {
        signal: controller.signal, requestTimeoutMs: operationRequestTimeoutMs, timeoutMs: operationObservationTimeoutMs,
      })
      if (action.controller === controller) finish(action, completed)
    } catch (reason) {
      if (!isCurrent(action) || controller.signal.aborted || action.controller !== controller) return
      const error = actionError("Environment progress couldn't be confirmed", reason)
      setState((current) => ({ ...current, trackingInterrupted: true, error: { ...error, message: `${error.message} The operation may still be running. Resume tracking to check its outcome.` } }))
      void changed.current()
    }
  }, [finish, isCurrent])

  useEffect(() => {
    const action = pending.current
    if (!action?.operation || !latestOperation || latestOperation.number !== action.operation.number || latestOperation.project !== action.environment.project || latestOperation.environment !== action.environment.name) return
    if (latestOperation.state !== 'running') finish(action, latestOperation)
  }, [finish, latestOperation])

  const active = state.identity === identity ? state : { ...idleState, identity }
  const transitioning = !!environment && ['starting', 'stopping', 'recovering'].includes(environment.status)
  const disabled = !environment || !live || !!active.busy || transitioning

  const run = useCallback(async (name: LifecycleAction) => {
    if (!environment || !live || ['starting', 'stopping', 'recovering'].includes(environment.status) || pending.current?.identity === identity) return
    if ((name === 'up') !== (environment.status === 'stopped')) return
    const action: PendingAction = { identity, environment, action: name, controller: new AbortController() }
    pending.current = action
    setState({ ...idleState, identity, busy: name })
    try {
      const operation = await api<Operation>(environmentPath(environment, `/${name}`), {
        method: 'POST', signal: AbortSignal.any([action.controller.signal, AbortSignal.timeout(operationRequestTimeoutMs)]),
        ...(name === 'down' ? jsonBody({ removeVolumes: false }) : {}),
      })
      if (!isCurrent(action)) return
      action.operation = operation
      void changed.current()
      await observe(action)
    } catch (reason) {
      if (!isCurrent(action) || action.controller.signal.aborted) return
      pending.current = null
      setState({ ...idleState, identity, error: actionError(`Couldn't ${name === 'up' ? 'start' : 'stop'} ${environment.name}`, reason) })
      void changed.current()
    }
  }, [environment, identity, isCurrent, live, observe])

  const resumeTracking = useCallback(() => {
    const action = pending.current
    if (!action?.operation || !isCurrent(action)) return
    action.controller.abort()
    action.controller = new AbortController()
    setState((current) => ({ ...current, error: null, trackingInterrupted: false }))
    void observe(action)
  }, [isCurrent, observe])

  const forget = useCallback(async () => {
    if (!environment || !live || environment.status !== 'stopped' || pending.current?.identity === identity) return
    const action: PendingAction = { identity, environment, action: 'forget', controller: new AbortController() }
    pending.current = action
    setState({ ...idleState, identity, busy: 'forget' })
    try {
      await api(environmentPath(environment), { method: 'DELETE', signal: AbortSignal.any([action.controller.signal, AbortSignal.timeout(operationRequestTimeoutMs)]) })
      if (isCurrent(action)) {
        pending.current = null
        setState({ ...idleState, identity })
        navigate.current('/projects')
      }
      await changed.current()
    } catch (reason) {
      if (!isCurrent(action) || action.controller.signal.aborted) return
      pending.current = null
      setState({ ...idleState, identity, forgetError: actionError("Environment couldn't be forgotten", reason) })
    }
  }, [environment, identity, isCurrent, live])

  return {
    ...active, disabled, run, forget, resumeTracking,
    dismissError: () => setState((current) => ({ ...current, error: null })),
    dismissForgetError: () => setState((current) => ({ ...current, forgetError: null })),
  }
}

export type EnvironmentActions = ReturnType<typeof useEnvironmentActions>
