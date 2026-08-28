import { api, environmentPath } from '../../api'
import type { Environment, Operation } from '../../types'

export async function waitForEnvironmentOperation(environment: Pick<Environment, 'project' | 'name'>, operation: Operation): Promise<Operation> {
  let current = operation
  while (current.state === 'running') {
    await new Promise((resolve) => window.setTimeout(resolve, 250))
    current = await api<Operation>(environmentPath(environment, `/operations/${current.number}`))
  }
  return current
}
