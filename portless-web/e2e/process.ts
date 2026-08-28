import { cpSync, existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { join } from 'node:path'

interface Installation {
  root: string
  home: string
  checkout: string
  binary: string
}

export function runCLI(binary: string, home: string, checkout: string, args: string[], tolerateFailure = false) {
  const result = spawnSync(binary, args, {
    cwd: checkout,
    env: { ...process.env, PORTLESS_HOME: home, NO_COLOR: '1' },
    encoding: 'utf8',
    timeout: 180_000,
  })
  const output = `${result.stdout || ''}${result.stderr || ''}`
  if (result.error && !tolerateFailure) throw result.error
  if (result.status !== 0 && !tolerateFailure) {
    const daemonLog = existsSync(join(home, 'daemon.log')) ? readFileSync(join(home, 'daemon.log'), 'utf8') : '<not available>'
    throw new Error(`portless ${args.join(' ')} exited ${result.status}\n${output}\nDaemon log:\n${daemonLog}`)
  }
  return { status: result.status, output }
}

export function stopInstallation(installation: Installation) {
  const artifactDirectory = process.env.PORTLESS_E2E_ARTIFACT_DIR
  const daemonLog = join(installation.home, 'daemon.log')
  if (artifactDirectory && existsSync(daemonLog)) {
    mkdirSync(artifactDirectory, { recursive: true })
    cpSync(daemonLog, join(artifactDirectory, 'daemon.log'))
  }
  runCLI(installation.binary, installation.home, installation.checkout, ['reset', '--force', '--yes'], true)
  runCLI(installation.binary, installation.home, installation.checkout, ['daemon', 'stop', '--force'], true)
  rmSync(installation.root, { recursive: true, force: true })
}
