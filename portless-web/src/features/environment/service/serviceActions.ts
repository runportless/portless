import { useRef, useState } from 'react'
import { api, environmentPath } from '../../../api'
import type { Environment, Operation } from '../../../api/contracts/environments'
import type { Service } from '../../../api/contracts/topology'
import { actionError, type ActionErrorDetails } from '../../../components/ActionError'
import { waitForEnvironmentOperation } from '../operationPolling'
import { bindingFor } from './servicePresentation'

export type ServiceAction = 'restart' | 'stop' | 'start' | 'debug' | 'manage'

export type ServiceActionOption = {
  action: ServiceAction
  label: string
  danger?: boolean
}

type BusyServiceAction = {
  service: string
  action: ServiceAction
}

export function useServiceActions(environment: Environment, onChanged: () => void) {
  const [busy, setBusy] = useState<BusyServiceAction | null>(null)
  const [error, setError] = useState<ActionErrorDetails | null>(null)
  const actionInFlight = useRef(false)

  const run = async (service: Pick<Service, 'name'>, name: ServiceAction) => {
    if (actionInFlight.current) return
    actionInFlight.current = true
    setBusy({ service: service.name, action: name })
    setError(null)
    const base = environmentPath(environment, `/services/${encodeURIComponent(service.name)}`)
    try {
      const operation = await api<Operation>(`${base}/${name}`, { method: 'POST' })
      onChanged()
      const completed = await waitForEnvironmentOperation(environment, operation)
      if (completed.state === 'failed') throw new Error(completed.error || `${service.name} ${name} failed`)
      onChanged()
    } catch (value) {
      setError(actionError(`Couldn't ${serviceActionDescription(name)} ${service.name}`, value))
    } finally {
      actionInFlight.current = false
      setBusy(null)
      onChanged()
    }
  }

  return { busy, error, dismissError: () => setError(null), run }
}

export function serviceActionOptions(environment: Pick<Environment, 'bindings'>, service: Service): ServiceActionOption[] {
  if (bindingFor(environment, service.name)?.provider === 'remote' || serviceIsTransitioning(service)) return []

  const stopped = serviceIsStopped(service)
  const options: ServiceActionOption[] = [{
    action: stopped ? 'start' : 'restart',
    label: stopped ? 'START' : 'RESTART',
  }]
  const localProcess = service.kind === 'process' && bindingFor(environment, service.name)?.provider === 'local'
  if (localProcess && service.debug) {
    options.push(service.launchMode === 'debug'
      ? { action: 'manage', label: 'RUN NORMALLY' }
      : { action: 'debug', label: stopped ? 'START IN DEBUG' : 'DEBUG' })
  }
  if (!stopped) options.push({ action: 'stop', label: 'STOP', danger: true })
  return options
}

export function serviceActionProgressLabel(action: ServiceAction) {
  switch (action) {
    case 'debug': return 'STARTING DEBUG…'
    case 'manage': return 'RUNNING NORMALLY…'
    case 'restart': return 'RESTARTING…'
    case 'start': return 'STARTING…'
    case 'stop': return 'STOPPING…'
  }
}

function serviceActionDescription(action: ServiceAction) {
  switch (action) {
    case 'debug': return 'start debugging'
    case 'manage': return 'run'
    default: return action
  }
}

function serviceIsStopped(service: Service) {
  return ['planned', 'exited', 'failed', 'stopped', 'unknown'].includes(service.status)
}

function serviceIsTransitioning(service: Service) {
  return ['starting', 'recovering', 'stopping'].includes(service.status)
}
