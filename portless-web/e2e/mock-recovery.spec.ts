import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { applicationRequest, authenticate, controlAPI, environmentHeader, environmentPath } from './helpers'
import { runCLI } from './process'
import { readE2EState } from './state'

test('recovers a mocked caller after daemon restart and allows disabling and deleting its scenario', async ({ page }) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const scenario = 'checkout-recovery'
  const scenarioBase = `${base}/mocks/${scenario}`
  type Runtime = { name: string; status: string; pid?: number; generation: number }
  type Snapshot = { status: string; services: Runtime[]; bindings: Array<{ service: string; provider: string }> }

  // Only the isolated fixture is stopped. Enabling before startup leaves no
  // saved outgoing proxy ports for checkout, matching the stuck-state report.
  runCLI(state.binary, state.home, state.checkout, ['down', '--env', `${state.project}/${state.environment}`])
  await controlAPI(`${base}/mocks`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: scenario }),
  })
  await controlAPI(`${scenarioBase}/routes/checkout`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: 'checkout', service: 'checkout', method: 'GET', path: '/checkout', status: 409, body: '{"mocked":true}', delayMs: 0, enabled: true }),
  })
  const activation = await controlAPI<{ number: number }>(`${scenarioBase}/activation`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() },
    body: JSON.stringify({ enabled: true }),
  })
  await expect.poll(async () => (await controlAPI<{ state: string }>(`${base}/operations/${activation.number}`)).state).toBe('succeeded')
  runCLI(state.binary, state.home, state.checkout, ['up', '--env', `${state.project}/${state.environment}`, '--no-open', '--timeout', '2m'])
  const before = await controlAPI<Snapshot>(base)
  expect(before.status).toBe('healthy')
  expect(before.services.find(({ name }) => name === 'checkout')?.pid).toBeFalsy()
  expect((await applicationRequest('/checkout')).status).toBe(409)

  await authenticate(page, `${environmentPath('mocks')}&scenario=${scenario}`)
  const workspace = page.getByRole('region', { name: `${scenario} mock scenario` })
  const toggle = workspace.getByRole('switch', { name: `${scenario} enabled` })
  await expect(toggle).toBeChecked()
  await expect(toggle).toBeEnabled()

  await page.locator('.sidebar__footer').click()
  const system = page.getByRole('dialog', { name: 'Portless System' })
  await system.getByRole('tab', { name: 'RUNTIME' }).click()
  await expect(system.getByRole('button', { name: 'RESTART DAEMON', exact: true })).toBeEnabled()
  await system.getByRole('button', { name: 'RESTART DAEMON', exact: true }).click()
  await page.getByRole('alertdialog', { name: 'Confirm daemon restart' }).getByRole('button', { name: 'RESTART AND RECONNECT' }).click()
  await expect(system).toContainText('Daemon restarted', { timeout: 5_000 })
  await system.getByRole('button', { name: 'Close', exact: true }).click()

  await expect(environmentHeader(page).getByRole('link', { name: /health: healthy; 3\/3 ready/ })).toBeVisible()
  await expect(page.locator('.environment-notices')).toHaveCount(0)
  await expect(toggle).toBeChecked()
  await expect(toggle).toBeEnabled()
  const recovered = await controlAPI<Snapshot>(base)
  expect(recovered.status).toBe('healthy')
  expect(recovered.services.map(({ name, pid, generation }) => ({ name, pid, generation }))).toEqual(before.services.map(({ name, pid, generation }) => ({ name, pid, generation })))
  expect(await applicationRequest('/checkout')).toMatchObject({ status: 409, body: '{"mocked":true}' })

  await workspace.locator('.mock-scenario-toggle').click()
  await expect(toggle).not.toBeChecked()
  await expect(toggle).toBeEnabled()
  const restored = await controlAPI<Snapshot>(base)
  expect(restored.status).toBe('healthy')
  expect(restored.bindings.find(({ service }) => service === 'checkout')?.provider).toBe('local')
  for (const name of ['inventory', 'orders']) {
    const previous = before.services.find((service) => service.name === name)!
    expect(restored.services.find((service) => service.name === name)).toMatchObject({ pid: previous.pid, generation: previous.generation, status: 'ready' })
  }
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
  await workspace.getByRole('button', { name: 'Back to mock scenarios' }).click()
  const row = page.locator('.mock-scenario-row').filter({ has: page.getByRole('button', { name: `Open ${scenario} mock scenario`, exact: true }) })
  await row.getByRole('button', { name: `Mock scenario actions for ${scenario}`, exact: true }).click()
  const menu = row.getByRole('menu', { name: `${scenario} mock scenario actions` })
  await expect(menu.getByRole('menuitem', { name: `Delete ${scenario}`, exact: true })).toBeEnabled()
  await menu.getByRole('menuitem', { name: `Delete ${scenario}`, exact: true }).click()
  await menu.getByRole('menuitem', { name: `Confirm delete ${scenario}`, exact: true }).click()
  await expect(row).toHaveCount(0)
})
