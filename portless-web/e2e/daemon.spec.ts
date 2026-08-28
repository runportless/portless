import { expect, test } from '@playwright/test'
import { applicationRequest, authenticate, controlAPI } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('surfaces a failed control-plane refresh and reconnects automatically', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  const environmentsEndpoint = `${state.baseURL}/api/v1/environments`
  await page.route(environmentsEndpoint, (route) => route.fulfill({
    status: 503,
    contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'E2E_UNAVAILABLE', message: 'temporary test outage' } }),
  }))

  const reconnecting = page.locator('.sidebar__footer .daemon-state--reconnecting')
  await expect(reconnecting).toHaveText('reconnecting', { timeout: 8_000 })
  await expect(reconnecting).toHaveCSS('animation-name', 'daemon-reconnecting-pulse')
  await expect(reconnecting).toHaveCSS('animation-duration', '1.4s')
  await expect.poll(() => reconnecting.evaluate((element) => getComputedStyle(element).color === getComputedStyle(element.previousElementSibling as HTMLElement).backgroundColor)).toBe(true)
  await page.unroute(environmentsEndpoint)
  await expect(page.locator('.sidebar__footer')).toContainText('ready', { timeout: 8_000 })
  await expect(reconnecting).toHaveCount(0)
  expect((await applicationRequest('/checkout?sku=reconnected&quantity=1')).status).toBe(200)
})

test('shows semver daemon details and reconnects after restart', async ({ page }) => {
  await authenticate(page)
  const before = await controlAPI<{ instanceId: string; pid: number; protocolVersion: string; apiVersion: string }>('/api/v1/daemon')
  const diagnostics = await controlAPI<{ storage?: unknown; inventory: { activeEnvironments: number }; build: { version: string; distribution: string; current: boolean }; recovery: { result: string } }>('/api/v1/daemon/diagnostics')
  const storageDiagnostics = await controlAPI<{ storage?: { databaseBytes: number; trafficExchangeLimitPerEnvironment: number; serviceLogGenerationLimit: number } }>('/api/v1/daemon/diagnostics?include=storage')
  const beforeEnvironment = await controlAPI<{ services: Array<{ name: string; pid: number }> }>('/api/v1/environments/ui-e2e/local')
  expect(before.protocolVersion).toMatch(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/)
  expect(before.apiVersion).toMatch(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/)
  expect(diagnostics.storage).toBeUndefined()
  expect(diagnostics.inventory.activeEnvironments).toBeGreaterThan(0)
  expect(diagnostics.build.version).toBeTruthy()
  expect(diagnostics.build.distribution).toBeTruthy()
  expect(diagnostics.recovery.result).toMatch(/^(healthy|degraded)$/)
  expect(storageDiagnostics.storage?.databaseBytes).toBeGreaterThan(0)
  expect(storageDiagnostics.storage?.trafficExchangeLimitPerEnvironment).toBeGreaterThan(0)
  expect(storageDiagnostics.storage?.serviceLogGenerationLimit).toBeGreaterThan(0)

  await page.locator('.sidebar__footer').click()
  const drawer = page.getByRole('dialog', { name: 'Portless Daemon' })
  const statusTab = drawer.getByRole('tab', { name: 'STATUS' })
  const runtimeTab = drawer.getByRole('tab', { name: 'RUNTIME' })
  const storageTab = drawer.getByRole('tab', { name: 'STORAGE' })
  const logsTab = drawer.getByRole('tab', { name: 'LOGS' })
  await expect(statusTab).toHaveAttribute('aria-selected', 'true')
  await expect(drawer).toContainText('PROTOCOL')
  await expect(drawer).toContainText('API')
  await expect(drawer.getByText(before.protocolVersion, { exact: true })).toBeVisible()
  await expect(drawer.getByText(before.apiVersion, { exact: true })).toBeVisible()
  await expect(drawer).toContainText('BUILD PROVENANCE')
  await expect(drawer).toContainText('CONTROL-PLANE HEALTH')
  await expect(drawer).toContainText('RECOVERY STATUS')

  await statusTab.press('ArrowRight')
  await expect(runtimeTab).toBeFocused()
  await expect(runtimeTab).toHaveAttribute('aria-selected', 'true')
  await expect(drawer).toContainText('RUNTIME ENGINE')
  await expect(drawer).toContainText('MANAGED INVENTORY')
  await expect(drawer).toContainText('LOCAL NETWORKING')
  await expect(drawer).toContainText('RUNTIME HANDOFF')
  const networkingBox = await drawer.getByText('LOCAL NETWORKING', { exact: true }).boundingBox()
  const handoffBox = await drawer.getByText('RUNTIME HANDOFF', { exact: true }).boundingBox()
  expect(networkingBox?.y).toBeLessThan(handoffBox?.y ?? 0)

  await runtimeTab.press('ArrowRight')
  await expect(storageTab).toBeFocused()
  await expect(storageTab).toHaveAttribute('aria-selected', 'true')
  await expect(drawer).toContainText('OBSERVED FOOTPRINT')
  await expect(drawer).toContainText('CONFIGURED LIMITS')
  await expect(drawer).toContainText('LAST PRUNING')

  await storageTab.press('ArrowRight')
  await expect(logsTab).toBeFocused()
  await expect(logsTab).toHaveAttribute('aria-selected', 'true')
  const daemonLogs = drawer.getByLabel('Daemon logs', { exact: true })
  await expect(daemonLogs).toContainText('Portless daemon ready')
  await expect(drawer.getByRole('button', { name: 'Open raw daemon logs in new tab' })).toBeEnabled()
  await expect(drawer.getByRole('button', { name: 'Pause daemon log tail' })).toHaveAttribute('aria-pressed', 'true')
  const logToolbar = drawer.locator('.daemon-log-view .log-view__toolbar')
  const logControls = drawer.locator('.daemon-log-view .log-view__controls')
  await expect.poll(async () => {
    const [toolbar, controls] = await Promise.all([logToolbar.boundingBox(), logControls.boundingBox()])
    if (!toolbar || !controls) return false
    return toolbar.x + toolbar.width - (controls.x + controls.width) <= 12
  }).toBe(true)
  await logsTab.press('Home')
  await expect(statusTab).toBeFocused()
  await expect(statusTab).toHaveAttribute('aria-selected', 'true')

  const fullScreenButton = drawer.getByRole('button', { name: 'Full screen Portless Daemon' })
  await expect(fullScreenButton.locator('svg')).toBeVisible()
  await expect(fullScreenButton).toHaveText('')
  await fullScreenButton.click()
  await expect(drawer).toHaveClass(/drawer--fullscreen/)
  const restoreButton = drawer.getByRole('button', { name: 'Restore Portless Daemon' })
  await expect(restoreButton.locator('svg')).toBeVisible()
  await expect(restoreButton).toHaveText('')
  await restoreButton.click()
  await drawer.getByRole('button', { name: 'RESTART DAEMON' }).click()
  await expect(runtimeTab).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('alertdialog', { name: 'Confirm daemon restart' }).getByRole('button', { name: 'RESTART AND RECONNECT' }).click()
  await expect(drawer).toContainText('Daemon restarted', { timeout: 5_000 })

  await expect.poll(async () => (await controlAPI<{ instanceId: string }>('/api/v1/daemon')).instanceId).not.toBe(before.instanceId)
  const afterEnvironment = await controlAPI<{ services: Array<{ name: string; pid: number }> }>('/api/v1/environments/ui-e2e/local')
  expect(afterEnvironment.services.map(({ name, pid }) => ({ name, pid }))).toEqual(beforeEnvironment.services.map(({ name, pid }) => ({ name, pid })))
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
})

