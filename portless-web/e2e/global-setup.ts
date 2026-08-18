import { chmodSync, cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runCLI, stopInstallation } from './process'
import { stateFile, type E2EState } from './state'

export default async function globalSetup() {
  const e2eDirectory = dirname(fileURLToPath(import.meta.url))
  const repository = resolve(e2eDirectory, '..', '..')
  const binary = resolve(process.env.PORTLESS_E2E_BINARY || join(repository, 'bin', 'portless-e2e'))
  const fixture = resolve(process.env.PORTLESS_E2E_FIXTURE || join(repository, 'tests', 'fixtures', 'store-lite'))
  const debugFixture = resolve(join(repository, 'tests', 'fixtures', 'debug-node'))
  const root = mkdtempSync(join(tmpdir(), 'portless-ui-e2e-'))
  const home = join(root, 'home')
  const checkout = join(root, 'store-lite')
  const debugCheckout = join(root, 'debug-node')
  mkdirSync(home, { mode: 0o700 })
  cpSync(fixture, checkout, { recursive: true })
  cpSync(debugFixture, debugCheckout, { recursive: true })

  try {
    runCLI(binary, home, checkout, ['up', '--name', 'ui-e2e', '--no-open', '--timeout', '2m'])
    runCLI(binary, home, debugCheckout, ['up', '--name', 'ui-debug', '--managed', '--no-open', '--timeout', '2m'])
    const control = JSON.parse(readFileSync(join(home, 'control.json'), 'utf8')) as { port: number }
    const baseURL = `http://127.0.0.1:${control.port}`
    const token = readFileSync(join(home, 'install.key'), 'utf8').trim()
    const state: E2EState = {
      root,
      home,
      checkout,
      binary,
      baseURL,
      token,
      project: 'ui-e2e',
      environment: 'local',
      applicationHost: 'checkout.local.ui-e2e.localhost',
      debugCheckout,
      debugProject: 'ui-debug',
    }
    writeFileSync(stateFile(), `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 })
    chmodSync(stateFile(), 0o600)
  } catch (error) {
    stopInstallation({ root, home, checkout, binary })
    throw error
  }
}
