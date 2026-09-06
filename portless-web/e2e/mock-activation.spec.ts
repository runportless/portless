import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import type { Environment, Operation } from '../src/api/contracts/environments'
import type { MockRoute, MockScenario, MockScenarioList } from '../src/api/contracts/mocks'
import { applicationRequest, authenticate, controlAPI, environmentPath } from './helpers'
import { readE2EState } from './state'

test('disables all scenarios with sequential restoration, partial failure recovery, and retained routes', async ({ page }, testInfo) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const suffix = randomUUID().slice(0, 8)
  const names = ['inventory', 'orders', 'disabled'].map((name) => `bulk-${name}-${suffix}`)
  const created: string[] = []
  const before = await controlAPI<Environment>(base)
  const setEnabled = async (name: string, enabled: boolean) => {
    const operation = await controlAPI<Operation>(`${base}/mocks/${name}/activation`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() },
      body: JSON.stringify({ enabled }),
    })
    await expect.poll(async () => (await controlAPI<Operation>(`${base}/operations/${operation.number}`)).state, { timeout: 30_000 }).toBe('succeeded')
  }
  let releaseFirst = () => {}
  const firstReleased = new Promise<void>((resolve) => { releaseFirst = resolve })
  let finishFirst = () => {}
  const firstFinished = new Promise<void>((resolve) => { finishFirst = resolve })
  let firstStarted = false
  const activationPattern = `**${base}/mocks/*/activation`
  try {
    for (const [index, service] of ['inventory', 'orders', 'checkout'].entries()) {
      const name = names[index]
      await controlAPI(`${base}/mocks`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }),
      })
      created.push(name)
      for (const enabled of [true, false]) {
        const route: MockRoute = { name: enabled ? 'health' : 'unused', service, method: 'GET', path: enabled ? '/health' : '/unused', status: 200, body: '{}', delayMs: 0, enabled }
        await controlAPI(`${base}/mocks/${name}/routes/${route.name}`, {
          method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(route),
        })
      }
      if (index < 2) await setEnabled(name, true)
    }
    const saved = (await controlAPI<MockScenarioList>(`${base}/mocks`)).scenarios
    const activeNames = saved.filter((scenario) => scenario.activation.state !== 'disabled').map(({ name }) => name)
    expect(activeNames).toHaveLength(2)
    const [first, second] = activeNames
    const requests: string[] = []
    let rejectSecond = true
    await authenticate(page, environmentPath('mocks'))
    await page.emulateMedia({ colorScheme: 'dark' })
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    const panel = page.locator('.mock-scenarios-panel')
    const bulkAction = panel.locator('.mock-scenarios-bulk-actions').getByRole('button')
    const scenarioSwitch = (name: string) => panel.getByRole('switch', { name: `${name} enabled`, exact: true })
    await expect(bulkAction).toHaveText('DISABLE ALL')
    await expect(bulkAction).toBeEnabled()
    await expect(scenarioSwitch(first)).toBeChecked()
    await expect(scenarioSwitch(second)).toBeChecked()
    await expect(scenarioSwitch(names[2])).not.toBeChecked()
    const heading = (await panel.locator('.panel-title').boundingBox())!
    const bulkRow = (await panel.locator('.mock-scenarios-bulk-actions').boundingBox())!
    const columns = (await panel.locator('.mock-scenario-row--header').boundingBox())!
    expect(heading.y + heading.height).toBeLessThanOrEqual(bulkRow.y)
    expect(bulkRow.y + bulkRow.height).toBeLessThanOrEqual(columns.y)
    await page.screenshot({ path: testInfo.outputPath('mock-scenarios-disable-all-dark.png'), animations: 'disabled' })

    await page.route(activationPattern, async (intercept) => {
      const request = intercept.request()
      const name = request.url().split('/').at(-2)!
      requests.push(name)
      expect(request.method()).toBe('PUT')
      expect(request.postDataJSON()).toEqual({ enabled: false })
      if (name === first) {
        firstStarted = true
        try {
          await firstReleased
          await intercept.continue()
        } finally { finishFirst() }
      } else if (name === second && rejectSecond) {
        rejectSecond = false
        await intercept.abort('failed')
      } else await intercept.continue()
    })
    await bulkAction.focus()
    await page.keyboard.press('Enter')
    await expect(bulkAction).toHaveText('DISABLING…')
    await expect(bulkAction).toBeDisabled()
    await expect(panel.getByRole('button', { name: 'CREATE SCENARIO', exact: true })).toBeDisabled()
    for (const name of activeNames) {
      await expect(scenarioSwitch(name)).toBeDisabled()
      await expect(scenarioSwitch(name)).toHaveAttribute('aria-busy', 'true')
      await expect(panel.getByRole('button', { name: `Mock scenario actions for ${name}`, exact: true })).toBeDisabled()
    }
    await expect(scenarioSwitch(names[2])).toBeDisabled()
    await expect(scenarioSwitch(names[2])).toHaveAttribute('aria-busy', 'false')
    expect(requests).toEqual([first])
    await expect(page).toHaveURL(new RegExp(`${environmentPath('mocks').replace('?', '\\?')}$`))
    releaseFirst()
    await firstFinished

    await expect(page.getByRole('alert')).toContainText("Couldn't finish disabling all scenarios")
    await expect(page.getByRole('alert')).toContainText(`1 of 2 scenarios disabled. Stopped at ${second}.`)
    await expect(scenarioSwitch(first)).not.toBeChecked()
    await expect(scenarioSwitch(second)).toBeChecked()
    await expect(bulkAction).toHaveText('DISABLE ALL')
    await expect(bulkAction).toBeEnabled()
    expect(requests).toEqual([first, second])

    await bulkAction.click()
    await expect(bulkAction).toHaveText('DISABLE ALL')
    await expect(bulkAction).toBeDisabled()
    await expect(page.getByRole('alert')).toHaveCount(0)
    expect(requests).toEqual([first, second, second])
    for (const name of names) {
      await expect(scenarioSwitch(name)).not.toBeChecked()
      await expect(scenarioSwitch(name)).toBeEnabled()
      const current = await controlAPI<MockScenario>(`${base}/mocks/${name}`)
      expect(current.activation.state).toBe('disabled')
      expect(current.routes).toEqual(saved.find((scenario) => scenario.name === name)!.routes)
    }
    const restored = await controlAPI<Environment>(base)
    expect(restored.status).toBe('healthy')
    expect(restored.services.find(({ name }) => name === 'checkout')?.pid).toBe(before.services.find(({ name }) => name === 'checkout')?.pid)
    for (const service of ['inventory', 'orders']) {
      expect(restored.bindings?.find((binding) => binding.service === service)?.provider).toBe('local')
      expect(restored.services.find(({ name }) => name === service)?.pid).toBeGreaterThan(0)
    }
    expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
    await page.emulateMedia({ colorScheme: 'light' })
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await page.screenshot({ path: testInfo.outputPath('mock-scenarios-disable-all-light.png'), animations: 'disabled' })
    await page.reload()
    await expect(bulkAction).toBeDisabled()
    for (const name of names) await expect(scenarioSwitch(name)).not.toBeChecked()
  } finally {
    releaseFirst()
    if (firstStarted) await firstFinished
    await page.unroute(activationPattern)
    for (const name of created) {
      const scenario = await controlAPI<MockScenario>(`${base}/mocks/${name}`)
      if (scenario.activation.state !== 'disabled') await setEnabled(name, false)
      await controlAPI(`${base}/mocks/${name}`, { method: 'DELETE' })
    }
  }
})
