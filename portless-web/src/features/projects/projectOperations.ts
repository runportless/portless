import { api, environmentPath } from '../../api'
import type { Environment, Operation } from '../../api/contracts/environments'

export type EnvironmentAction = 'up' | 'down' | 'restart'
type DirectEnvironmentAction = Exclude<EnvironmentAction, 'restart'>

export function environmentRoute(environment: Pick<Environment, 'project' | 'name'>) {
  return `/environments/${encodeURIComponent(environment.project)}/${encodeURIComponent(environment.name)}`
}

export function projectRoute(project: string) {
  return `/projects/${encodeURIComponent(project)}`
}

export function environmentKey(environment: Pick<Environment, 'project' | 'name'>) {
  return `${environment.project}/${environment.name}`
}

export function environmentCanStop(environment: Pick<Environment, 'status'>) {
  return !['stopped', 'stopping', 'recovering'].includes(environment.status)
}

export function environmentCanRestart(environment: Pick<Environment, 'status'>) {
  return ['healthy', 'degraded', 'failed'].includes(environment.status)
}

export async function runEnvironmentOperation(environment: Environment, action: EnvironmentAction) {
  if (action === 'restart') {
    await runDirectEnvironmentOperation(environment, 'down')
    await runDirectEnvironmentOperation(environment, 'up')
    return
  }
  await runDirectEnvironmentOperation(environment, action)
}

async function runDirectEnvironmentOperation(environment: Environment, action: DirectEnvironmentAction) {
  let operation = await api<Operation>(environmentPath(environment, `/${action}`), {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID(), ...(action === 'down' ? { 'Content-Type': 'application/json' } : {}) },
    ...(action === 'down' ? { body: JSON.stringify({ removeVolumes: false }) } : {}),
  })
  while (operation.state === 'running') {
    await new Promise((resolve) => window.setTimeout(resolve, 250))
    operation = await api<Operation>(environmentPath(environment, `/operations/${operation.number}`))
  }
  if (operation.state !== 'succeeded') throw new Error(operation.error || `${environment.name} ${action === 'up' ? 'startup' : 'shutdown'} ${operation.state}`)
}
