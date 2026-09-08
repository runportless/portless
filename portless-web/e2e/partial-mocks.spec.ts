import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import type { Environment, Operation } from '../src/api/contracts/environments'
import type { MockScenario } from '../src/api/contracts/mocks'
import { applicationRequest, authenticate, controlAPI, environmentPath } from './helpers'
import { readE2EState } from './state'

test('configures partial mocks and previews forwarding while preserving the live service', async ({ page }, testInfo) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const name = `partial-${randomUUID().slice(0, 8)}`
  const before = await controlAPI<Environment>(base)
  const original = before.services.find((service) => service.name === 'checkout')!
  const scenarioBase = `${base}/mocks/${name}`
  try {
    await authenticate(page, environmentPath('mocks'))
    await page.getByRole('button', { name: 'CREATE SCENARIO', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Create Mock Scenario' })
    await dialog.getByLabel('NAME', { exact: true }).fill(name)
    await expect(dialog.getByLabel('MOCK TYPE')).toHaveValue('reject')
    await dialog.getByLabel('MOCK TYPE').selectOption('forward')
    await dialog.getByRole('button', { name: 'CREATE SCENARIO', exact: true }).click()
    await expect(page.getByRole('region', { name: `${name} mock scenario` })).toBeVisible()
    await controlAPI(`${scenarioBase}/routes/fixed`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'fixed', service: 'checkout', method: 'GET', path: '/partial-fixed', status: 503, body: 'fixed failure', enabled: true }) })
    await page.reload()
    const workspace = page.getByRole('region', { name: `${name} mock scenario` })
    const mockCount = page.locator('.view-nav button[aria-label="Mocks"] .view-nav__count')
    await expect(mockCount).toHaveCount(0)
    await workspace.getByRole('tab', { name: 'Preview', exact: true }).click()
    const preview = page.getByRole('form', { name: 'Mock request preview' })
    await preview.getByLabel('PREVIEW PATH', { exact: true }).fill('/checkout')
    await preview.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await expect(preview).toContainText('Would forward to service')
    await expect(preview).toContainText('No request was sent.')
    await workspace.getByRole('switch', { name: `${name} enabled`, exact: true }).click()
    await expect.poll(async () => (await controlAPI<MockScenario>(scenarioBase)).activation.state).toBe('enabled')
    await expect(workspace.locator('.mock-scenario-toggle__label')).toHaveText('Enabled')
    await expect(mockCount).toHaveText('1')
    await expect(page.getByRole('link', { name: `Active mock scenario ${name}. Open mocks`, exact: true })).toBeVisible()
    expect((await applicationRequest('/partial-fixed')).status).toBe(503)
    expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
    const active = (await controlAPI<Environment>(base)).services.find((service) => service.name === 'checkout')!
    expect(active.pid).toBe(original.pid)
    expect(active.generation).toBe(original.generation)
    expect(active.mock?.unmatchedRequests).toBe('forward')
    await workspace.getByRole('tab', { name: 'Edit', exact: true }).click()
    const typeControl = workspace.getByRole('group', { name: 'Mock type', exact: true })
    const fullMode = typeControl.getByRole('button', { name: 'Full mock', exact: true })
    const partialMode = typeControl.getByRole('button', { name: 'Partial mock', exact: true })
    const selectedType = typeControl.getByRole('button', { pressed: true })
    await expect(fullMode).toBeEnabled()
    await expect(partialMode).toBeEnabled()
    await expect(fullMode).toHaveAttribute('aria-pressed', 'false')
    await expect(partialMode).toHaveAttribute('aria-pressed', 'true')
    const routeEntries = workspace.locator('.mock-route-browser .mock-route-select')
    await expect(routeEntries).toHaveCount(1)
    await expect(selectedType).toHaveAttribute('title', 'Unmatched requests forward to the service.')
    await page.reload()
    await expect(workspace.getByRole('region', { name: 'Edit Route', exact: true })).toBeVisible()
    await expect(mockCount).toHaveText('1')
    for (const theme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      for (const width of [1280, 700, 390]) {
        await page.setViewportSize({ width, height: 900 })
        await expect(fullMode).toBeInViewport({ ratio: 1 })
        await expect(partialMode).toBeInViewport({ ratio: 1 })
        await expect(workspace.getByRole('button', { name: 'Edit fixed route', exact: true })).toBeInViewport()
        await expect(async () => {
          const bounds = await workspace.evaluate((element) => {
            const mode = element.querySelector('.mock-scenario-policy')!.getBoundingClientRect()
            const header = element.querySelector('.mock-scenario-header')!.getBoundingClientRect()
            const activation = element.querySelector('.mock-scenario-toggle')!.getBoundingClientRect()
            const choices = element.querySelectorAll('.mock-scenario-policy button')
            const full = choices[0].getBoundingClientRect()
            const partial = choices[1].getBoundingClientRect()
            return {
              topInset: activation.top - header.top,
              bottomInset: header.bottom - mode.bottom,
              leftInset: mode.left - header.left,
              rightInset: header.right - mode.right,
              controlGap: mode.top - activation.bottom,
              controlAlignment: Math.abs(mode.right - activation.right),
              optionGap: Math.abs(partial.left - full.right),
            }
          })
          expect(bounds.topInset).toBeGreaterThanOrEqual(9)
          expect(bounds.bottomInset).toBeGreaterThanOrEqual(9)
          expect(bounds.leftInset).toBeGreaterThanOrEqual(11)
          expect(bounds.rightInset).toBeGreaterThanOrEqual(11)
          expect(bounds.controlGap).toBeGreaterThanOrEqual(6)
          expect(bounds.controlAlignment).toBeLessThanOrEqual(1)
          expect(bounds.optionGap).toBeLessThanOrEqual(1)
        }).toPass()
        await page.screenshot({ path: testInfo.outputPath(`mock-type-${theme}-${width}.png`), animations: 'disabled' })
      }

    }
    await workspace.getByRole('button', { name: 'Edit fixed route', exact: true }).click()
    await workspace.getByLabel('PATH', { exact: true }).fill('/unsaved-route')
    const routes = (await controlAPI<MockScenario>(scenarioBase)).routes
    for (const policy of ['reject', 'forward'] as const) {
      const choice = policy === 'forward' ? partialMode : fullMode
      await choice.focus()
      await expect(choice).toBeFocused()
      await choice.press(policy === 'forward' ? 'Enter' : 'Space')
      await expect(choice).toHaveAttribute('aria-pressed', 'true')
      await expect(selectedType).toHaveCount(1)
      await expect(choice).toBeEnabled({ timeout: 30_000 })
      await expect.poll(async () => (await controlAPI<MockScenario>(scenarioBase)).unmatchedRequests).toBe(policy)
      await expect(workspace.locator('.mock-scenario-toggle__label')).toHaveText('Enabled')
      await expect(mockCount).toHaveText('1')
      await expect(selectedType).toHaveText(policy === 'forward' ? 'Partial mock' : 'Full mock')
      await expect(selectedType).toHaveAttribute('title', policy === 'forward' ? 'Unmatched requests forward to the service.' : 'Unmatched requests return 501.')
      await expect(routeEntries).toHaveCount(1)
      await expect(workspace.getByLabel('PATH', { exact: true })).toHaveValue('/unsaved-route')
      expect((await controlAPI<MockScenario>(scenarioBase)).routes).toEqual(routes)
      expect((await applicationRequest('/partial-fixed')).status).toBe(503)
      expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(policy === 'forward' ? 200 : 501)
    }
    await workspace.getByRole('button', { name: 'Discard route changes' }).click()
    await workspace.getByRole('switch', { name: `${name} enabled`, exact: true }).click()
    await expect.poll(async () => (await controlAPI<MockScenario>(scenarioBase)).activation.state).toBe('disabled')
    await expect(mockCount).toHaveCount(0)
    await expect(page.locator('.mock-indicator')).toHaveCount(0)
    await expect(fullMode).toBeEnabled()
    await expect(partialMode).toBeEnabled()
    await expect(selectedType).toHaveAttribute('title', 'Unmatched requests forward to the service.')
    expect((await controlAPI<MockScenario>(scenarioBase)).unmatchedRequests).toBe('forward')
  } finally {
    const saved = await controlAPI<MockScenario>(scenarioBase).catch(() => null)
    if (saved?.activation.state !== 'disabled' && saved) {
      const op = await controlAPI<Operation>(`${scenarioBase}/activation`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled: false }) })
      await expect.poll(async () => (await controlAPI<Operation>(`${base}/operations/${op.number}`)).state, { timeout: 30_000 }).toBe('succeeded')
    }
    if (saved) await controlAPI(scenarioBase, { method: 'DELETE' })
  }
})


test('keeps mock type separate from saved routes through creation and deletion', async ({ page }) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}/mocks`
  const name = `full-${randomUUID().slice(0, 8)}`
  const scenarioBase = `${base}/${name}`
  try {
    await authenticate(page, environmentPath('mocks'))
    await page.getByRole('button', { name: 'CREATE SCENARIO', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Create Mock Scenario' })
    await dialog.getByLabel('NAME', { exact: true }).fill(name)
    await expect(dialog.getByLabel('MOCK TYPE')).toHaveValue('reject')
    await dialog.getByRole('button', { name: 'CREATE SCENARIO', exact: true }).click()
    const workspace = page.getByRole('region', { name: `${name} mock scenario` })
    const routeEntries = workspace.locator('.mock-route-browser .mock-route-select')
    const typeControl = workspace.getByRole('group', { name: 'Mock type', exact: true })
    const selectedType = typeControl.getByRole('button', { pressed: true })
    await expect(typeControl.getByRole('button')).toHaveCount(2)
    await expect(selectedType).toHaveText('Full mock')
    await expect(selectedType).toHaveAttribute('title', 'Unmatched requests return 501.')
    await expect(routeEntries).toHaveCount(0)
    await expect(workspace.locator('.mock-scenario-header h2')).toHaveText(name)
    await expect(selectedType).toBeEnabled()
    for (const policy of ['forward', 'reject'] as const) {
      const choice = typeControl.getByRole('button', { name: policy === 'forward' ? 'Partial mock' : 'Full mock', exact: true })
      await choice.click()
      await expect(choice).toHaveAttribute('aria-pressed', 'true')
      await expect(selectedType).toHaveCount(1)
      await expect(choice).toBeEnabled()
      await expect(selectedType).toHaveAttribute('title', policy === 'forward' ? 'Unmatched requests forward to the service.' : 'Unmatched requests return 501.')
      await page.reload()
      await expect(choice).toHaveAttribute('aria-pressed', 'true')
      await expect(selectedType).toHaveText(policy === 'forward' ? 'Partial mock' : 'Full mock')
      await expect(routeEntries).toHaveCount(0)
      expect((await controlAPI<MockScenario>(scenarioBase)).activation.state).toBe('disabled')
    }
    await controlAPI(`${scenarioBase}/routes/default`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'default', service: 'checkout', method: 'GET', path: '/fixed', status: 200, enabled: true }) })
    await page.reload()
    await workspace.getByRole('button', { name: 'Edit default route', exact: true }).click()
    await expect(workspace.getByRole('region', { name: 'Edit Route', exact: true })).toBeVisible()
    await expect(routeEntries).toHaveCount(1)
    await expect(workspace.getByLabel('PATH', { exact: true })).toHaveValue('/fixed')
    await workspace.getByRole('button', { name: 'Route actions for default', exact: true }).click()
    await workspace.getByRole('menuitem', { name: 'Delete default', exact: true }).click()
    await workspace.getByRole('menuitem', { name: 'Confirm delete default', exact: true }).click()
    await expect(routeEntries).toHaveCount(0)
    await page.reload()
    await expect(selectedType).toHaveAttribute('title', 'Unmatched requests return 501.')
    await expect(routeEntries).toHaveCount(0)
    await expect(workspace.locator('.mock-scenario-header h2')).toHaveText(name)
    await expect(workspace.getByRole('tab', { name: 'Preview', exact: true })).toBeDisabled()
    const saved = await controlAPI<MockScenario>(scenarioBase)
    expect(saved.routes).toHaveLength(0)
    expect(saved.unmatchedRequests).toBe('reject')
  } finally {
    await controlAPI(scenarioBase, { method: 'DELETE' }).catch(() => undefined)
  }
})
