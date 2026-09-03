import { execFileSync } from 'node:child_process'
import { cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import type { Environment } from '../src/api/contracts/environments'
import { applicationRequest, authenticate, controlAPI, environmentHeader, openCommandPalette } from './helpers'
import { runCLI } from './process'
import { readE2EState } from './state'

test('starts a clone alongside its original without checkout configuration', async ({ page }) => {
  test.setTimeout(90_000)
  const state = readE2EState()
  const project = 'ui-worktrees'
  const repository = join(state.root, 'worktree-repository')
  const source = join(repository, 'examples', 'store')
  mkdirSync(repository)
  cpSync(state.checkout, source, { recursive: true })
  for (const args of [['init', '-q'], ['add', '.'], ['-c', 'user.name=Portless Test', '-c', 'user.email=test@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture']]) {
    execFileSync('git', ['-c', 'core.hooksPath=/dev/null', '-C', repository, ...args], { timeout: 10_000 })
  }
  runCLI(state.binary, state.home, source, ['up', '--name', project, '--managed', '--no-open', '--timeout', '2m'])
  const original = await controlAPI<Environment>(`/api/v1/environments/${project}/local`)
  writeFileSync(join(source, 'uncommitted.txt'), 'current source files')
  const apiPath = `/api/v1/environments/${project}/qa`
  try {
    await authenticate(page, `/environments/${project}/local`)
    await page.getByRole('button', { name: `Create environment in ${project}` }).click()
    const dialog = page.getByRole('dialog', { name: 'Create Environment' })
    await dialog.getByLabel('NAME').fill('qa')
    await dialog.getByRole('button', { name: 'CREATE ENVIRONMENT', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/environments/${project}/qa$`))
    const header = environmentHeader(page, project, 'qa')
    await header.getByRole('button', { name: 'Start', exact: true }).click()
    await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible({ timeout: 30_000 })
    await expect(page.locator('.environment-notices').getByRole('alert')).toHaveCount(0)
    const clone = await controlAPI<Environment>(apiPath)
    const checkout = clone.sources![0].path
    expect(checkout).not.toBe(original.sources![0].path)
    expect(checkout).toMatch(/\/examples\/store$/)
    expect(readFileSync(join(checkout, 'uncommitted.txt'), 'utf8')).toBe('current source files')
    for (const environment of ['local', 'qa']) {
      expect((await applicationRequest('/checkout?sku=coffee&quantity=1', { Host: `checkout.${environment}.${project}.localhost` })).status).toBe(200)
    }
    const unchanged = await controlAPI<Environment>(`/api/v1/environments/${project}/local`)
    expect(unchanged.services.map(({ name, pid, generation }) => ({ name, pid, generation }))).toEqual(original.services.map(({ name, pid, generation }) => ({ name, pid, generation })))

    await (await openCommandPalette(page, 'Stop environment')).getByRole('button', { name: /Stop environment/ }).click()
    await expect(header.getByRole('button', { name: 'Start', exact: true })).toBeVisible({ timeout: 30_000 })
    writeFileSync(join(checkout, 'keep.txt'), 'keep this edit')
    await header.getByRole('button', { name: 'Start', exact: true }).click()
    await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible({ timeout: 30_000 })
    expect((await controlAPI<Environment>(apiPath)).sources![0].path).toBe(checkout)
    expect(readFileSync(join(checkout, 'keep.txt'), 'utf8')).toBe('keep this edit')
  } finally {
    runCLI(state.binary, state.home, source, ['--env', `${project}/qa`, 'down'], true)
    runCLI(state.binary, state.home, source, ['--env', `${project}/local`, 'down'], true)
    await controlAPI(`/api/v1/projects/${project}`, { method: 'DELETE' })
  }
})
