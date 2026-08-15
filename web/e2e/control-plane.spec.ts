import { expect, test } from '@playwright/test'
import { applicationRequest, authenticate, controlAPI, environmentPath, issueBrowserClaim, readDownload } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('requires and consumes a one-use browser claim', async ({ page, request }) => {
  const state = readE2EState()
  await page.goto(`${state.baseURL}/projects`)
  await expect(page.getByRole('heading', { name: 'Open this control plane from the CLI.' })).toBeVisible()

  const claim = await issueBrowserClaim()
  await page.goto(claim)
  await expect(page).toHaveURL(new RegExp(`${environmentPath()}$`))
  await expect(page.getByRole('heading', { name: state.environment, exact: true })).toBeVisible()

  const reuse = await request.get(claim)
  expect(reuse.status()).toBe(401)
  expect((await reuse.json()).error.code).toBe('INVALID_BROWSER_CLAIM')
})

test('keeps projects, environments, and breadcrumbs navigable', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  const breadcrumbs = page.getByRole('navigation', { name: 'Breadcrumb' })
  await breadcrumbs.getByRole('link', { name: state.project }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${state.project}$`))
  await expect(page.getByRole('heading', { name: state.project, exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*healthy`) })).toBeVisible()

  await breadcrumbs.getByRole('link', { name: 'projects' }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
  await expect(page.locator('.project-nav__project-link').filter({ hasText: state.project })).toBeVisible()
})

test('creates a cloned environment from the project modal without duplicating sources', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()

  await page.getByRole('button', { name: 'CREATE ENVIRONMENT' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create environment' })
  const name = dialog.getByLabel('NAME')
  await expect(name).toBeFocused()
  await expect(name).toHaveValue('')
  await expect(name).toHaveAttribute('placeholder', 'qa-local')
  await expect(name).toHaveAttribute('autocomplete', 'off')
  const create = dialog.getByRole('button', { name: 'CREATE ENVIRONMENT', exact: true })
  await expect(create).toBeDisabled()
  await name.fill('qa-ui')
  await create.click()

  await expect(page).toHaveURL(new RegExp(`/environments/${state.project}/qa-ui$`))
  await expect(page.getByRole('heading', { name: 'qa-ui', exact: true })).toBeVisible()
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page.getByRole('button', { name: /qa-ui.*stopped/ })).toBeVisible()
  await expect(page.locator('.project-source-row:not(.table-row--header)')).toHaveCount(1)
  await expect(page.locator('.project-sources-panel')).toContainText('local, qa-ui')
})

test('persists the selected browser theme', async ({ page }) => {
  await authenticate(page)
  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible()

  const theme = page.getByRole('radiogroup', { name: 'Theme' })
  await theme.getByRole('radio', { name: /Light/ }).click()
  await expect(theme.getByRole('radio', { name: /Light/ })).toHaveAttribute('aria-checked', 'true')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  expect(await page.evaluate(() => localStorage.getItem('portless.theme'))).toBe('light')

  await page.reload()
  await expect(page.getByRole('radiogroup', { name: 'Theme' }).getByRole('radio', { name: /Light/ })).toHaveAttribute('aria-checked', 'true')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
})

test('renders real services, endpoints, topology, and service details', async ({ page }) => {
  await authenticate(page)
  const services = page.locator('.service-row--interactive')
  await expect(services).toHaveCount(3)
  for (const name of ['checkout', 'inventory', 'orders']) {
    await expect(services.filter({ hasText: name })).toBeVisible()
  }
  await expect(services.filter({ hasText: 'checkout' })).toContainText('http://checkout.local.ui-e2e.localhost')

  await services.filter({ hasText: 'checkout' }).getByRole('button', { name: 'Copy checkout endpoint' }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('http://checkout.local.ui-e2e.localhost')

  const topology = page.getByRole('region', { name: 'Service topology' })
  for (const edge of ['external to checkout', 'checkout to inventory', 'checkout to orders']) {
    await expect(topology.getByRole('button', { name: `Inspect traffic from ${edge}` })).toBeVisible()
  }
  await expect(topology.getByRole('button', { name: 'Inspect traffic from checkout to redis' })).toHaveCount(0)

  await services.filter({ hasText: 'checkout' }).getByRole('button', { name: 'INSPECT' }).click()
  const drawer = page.getByRole('dialog', { name: 'checkout service' })
  await expect(drawer).toContainText('http://checkout.local.ui-e2e.localhost')
  await drawer.getByRole('button', { name: 'logs' }).click()
  await expect(drawer).toContainText('checkout ready on')
  await drawer.getByRole('button', { name: 'Close' }).click()
})

test('shows captured request and response details', async ({ page }) => {
  await authenticate(page)
  const response = await applicationRequest('/checkout?sku=coffee-mug&quantity=2', {
    Authorization: 'Bearer browser-e2e-secret',
    'X-E2E-Trace': 'visible',
  })
  expect(response.status).toBe(200)

  await page.getByRole('navigation', { name: 'ui-e2e/local views' }).getByRole('button', { name: 'Traffic' }).click()
  const row = page.locator('button.traffic-row').filter({ hasText: '/checkout' }).filter({ hasText: 'external' }).first()
  await expect(row).toBeVisible()
  await row.click()

  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail).toContainText('GET /checkout')
  await expect(detail.locator('.traffic-message--request')).toContainText('[REDACTED]')
  await expect(detail.locator('.traffic-message--request')).toContainText('visible')
  await expect(detail.locator('.traffic-message--response')).toContainText('"checkout": "accepted"')
})

test('creates, captures, exports, and deletes a recording', async ({ page }) => {
  await authenticate(page, environmentPath('recordings'))
  await page.getByLabel('NAME').fill('ui-recording')
  await page.getByLabel('Recording traffic scope').selectOption('checkout:orders')
  await page.getByRole('button', { name: /START RECORDING/ }).click()

  let row = page.locator('.experiment-row').filter({ hasText: 'ui-recording' })
  await expect(row).toContainText('checkout → orders')
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
  await row.getByRole('button', { name: 'STOP' }).click()
  row = page.locator('.experiment-row').filter({ hasText: 'ui-recording' })
  await expect(row).toContainText('1 events')

  const downloadEvent = page.waitForEvent('download')
  await row.getByRole('link', { name: 'EXPORT' }).click()
  const download = await downloadEvent
  const exported = JSON.parse(await readDownload(await download.path())) as { recording: string; traffic: Array<{ source: string; target: string }> }
  expect(exported.recording).toBe('ui-recording')
  expect(exported.traffic).toHaveLength(1)
  expect(exported.traffic[0]).toMatchObject({ source: 'checkout', target: 'orders' })

  await row.getByRole('button', { name: 'DELETE' }).click()
  await expect(page.locator('.experiment-row').filter({ hasText: 'ui-recording' })).toHaveCount(0)
})

test('applies, disables, re-enables, and deletes a persistent fault', async ({ page }) => {
  await authenticate(page, environmentPath('faults'))
  await page.getByLabel('NAME').fill('ui-orders-fault')
  await page.getByLabel('Fault connection').selectOption('checkout:orders')
  await page.locator('.segmented').getByRole('button', { name: 'status' }).click()
  await page.getByRole('button', { name: 'ENABLE FAULT' }).click()

  let row = page.locator('.experiment-row').filter({ hasText: 'ui-orders-fault' })
  await expect(row).toContainText('active until disabled')
  const failed = await applicationRequest('/checkout?sku=coffee-mug&quantity=1')
  expect(failed.status).toBe(502)
  expect(failed.body).toContain('orders: returned 503 Service Unavailable')

  await row.getByRole('button', { name: 'DISABLE' }).click()
  row = page.locator('.experiment-row').filter({ hasText: 'ui-orders-fault' })
  await expect(row).toContainText('1 matches')
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)

  await row.getByRole('button', { name: 'ENABLE' }).click()
  await expect(row.getByRole('button', { name: 'DISABLE' })).toBeVisible()
  await row.getByRole('button', { name: 'DELETE' }).click()
  await expect(page.locator('.experiment-row').filter({ hasText: 'ui-orders-fault' })).toHaveCount(0)
})

test('stops and starts the environment from the UI', async ({ page }) => {
  await authenticate(page)
  await page.getByRole('button', { name: 'STOP ALL' }).click()
  await expect(page.getByRole('button', { name: 'START ALL' })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByRole('heading', { name: 'local', exact: true }).locator('..')).toContainText('stopped')

  await page.getByRole('button', { name: 'START ALL' }).click()
  await expect(page.getByRole('button', { name: 'STOP ALL' })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByRole('heading', { name: 'local', exact: true }).locator('..')).toContainText('healthy')
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
})

test('shows semver daemon details and reconnects after restart', async ({ page }) => {
  await authenticate(page)
  const before = await controlAPI<{ instanceId: string; pid: number }>('/api/v1/daemon')
  const beforeEnvironment = await controlAPI<{ services: Array<{ name: string; pid: number }> }>('/api/v1/environments/ui-e2e/local')

  await page.locator('.sidebar__footer').click()
  const drawer = page.getByRole('dialog', { name: 'Portless Daemon' })
  await expect(drawer).toContainText('PROTOCOL')
  await expect(drawer).toContainText('API')
  await expect(drawer.getByText(/^2\.0\.0$/)).toBeVisible()
  await expect(drawer.getByText(/^6\.0\.0$/)).toBeVisible()
  await expect(drawer).not.toContainText('Version')

  await drawer.getByRole('button', { name: 'FULL SCREEN' }).click()
  await expect(drawer).toHaveClass(/drawer--fullscreen/)
  await drawer.getByRole('button', { name: 'RESTORE' }).click()
  await drawer.getByRole('button', { name: 'RESTART DAEMON' }).click()
  await page.getByRole('alertdialog', { name: 'Confirm daemon restart' }).getByRole('button', { name: 'RESTART AND RECONNECT' }).click()
  await expect(drawer).toContainText('Daemon restarted', { timeout: 30_000 })

  await expect.poll(async () => (await controlAPI<{ instanceId: string }>('/api/v1/daemon')).instanceId).not.toBe(before.instanceId)
  const afterEnvironment = await controlAPI<{ services: Array<{ name: string; pid: number }> }>('/api/v1/environments/ui-e2e/local')
  expect(afterEnvironment.services.map(({ name, pid }) => ({ name, pid }))).toEqual(beforeEnvironment.services.map(({ name, pid }) => ({ name, pid })))
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
})
