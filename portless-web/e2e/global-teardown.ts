import { existsSync, readFileSync, rmSync } from 'node:fs'
import { stopInstallation } from './process'
import { stateFile, type E2EState } from './state'

export default async function globalTeardown() {
  const file = stateFile()
  if (!existsSync(file)) return
  const state = JSON.parse(readFileSync(file, 'utf8')) as E2EState
  stopInstallation(state)
  rmSync(state.root, { recursive: true, force: true })
  rmSync(file, { force: true })
}
