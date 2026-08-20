import { mkdirSync, realpathSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
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
  await expect(page.locator('.project-sources-panel')).not.toContainText('local, qa-ui')
})

test('stops one or every running environment from the project page', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${state.project}$`))

  const stopEnvironment = page.getByRole('button', { name: `Stop ${state.environment}`, exact: true })
  await expect(stopEnvironment).toBeEnabled()
  await stopEnvironment.click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*stopped`) })).toBeVisible({ timeout: 30_000 })
  const startEnvironment = () => page.getByRole('button', { name: `Start ${state.environment}`, exact: true })
  await expect(startEnvironment()).toBeEnabled()

  await startEnvironment().click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*healthy`) })).toBeVisible({ timeout: 30_000 })

  const stopAll = page.getByRole('button', { name: `Stop all ${state.project} environments` })
  await expect(stopAll).toBeEnabled()
  await stopAll.click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*stopped`) })).toBeVisible({ timeout: 30_000 })
  await expect(stopAll).toBeDisabled()
  await expect(startEnvironment()).toBeEnabled()

  await startEnvironment().click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*healthy`) })).toBeVisible({ timeout: 30_000 })
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

test('generates scoped MCP client configuration without persisting elevated access', async ({ page }) => {
  const state = readE2EState()
  const selector = `${state.project}/${state.environment}`
  await authenticate(page)

  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('tab', { name: 'MCP' }).click()
  await expect(page).toHaveURL(/\/settings\?tab=mcp$/)
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible()
  await expect(page.getByText('CONFIGURE CLIENT')).toBeVisible()

  await page.getByLabel('MCP environment').selectOption(selector)
  const preview = page.getByLabel('Generated MCP configuration')
  await expect(preview).toContainText(selector)
  expect(JSON.parse(await preview.textContent() || '')).toEqual({
    mcpServers: {
      [`portless-${state.project}-${state.environment}`]: {
        command: 'portless',
        args: ['--env', selector, 'mcp', 'serve'],
      },
    },
  })

  await page.getByRole('checkbox', { name: /^Lifecycle/ }).check()
  await page.getByRole('checkbox', { name: /^Sensitive traffic/ }).check()
  await expect(preview).toContainText('--allow-lifecycle')
  await expect(preview).toContainText('--allow-sensitive-traffic')
  await expect(page.locator('.mcp-preview')).toContainText('SENSITIVE · 19 TOOLS')

  await page.getByRole('button', { name: 'COPY' }).click()
  await expect(page.getByRole('button', { name: 'COPIED' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(await preview.textContent())

  await page.goto(`${state.baseURL}${environmentPath()}`)
  await expect(page.getByRole('heading', { name: state.environment, exact: true })).toBeVisible()
  await page.keyboard.press('Control+K')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  const input = palette.getByPlaceholder('jump to a project or environment')
  await input.fill('Configure MCP')
  await input.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/settings\\?tab=mcp&env=${state.project}%2F${state.environment}$`))
  await expect(page.getByLabel('MCP environment')).toHaveValue(selector)

  await page.reload()
  await expect(page.getByRole('checkbox', { name: /^Lifecycle/ })).not.toBeChecked()
  await expect(page.getByRole('checkbox', { name: /^Sensitive traffic/ })).not.toBeChecked()
})

test('renders real services, endpoints, topology, and service details', async ({ page }) => {
  await authenticate(page)
  const bindingsCard = page.locator('.state-panel').filter({ hasText: 'BINDINGS' })
  await expect(bindingsCard).toContainText('LOCAL')
  await expect(bindingsCard).toContainText('3 services local')
  await expect(page.locator('.state-panel').filter({ hasText: 'REVISION' })).toHaveCount(0)
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

  const ingressEdge = topology.locator('.topology-edge').first()
  const ingressLine = ingressEdge.locator('.topology-edge__line')
  const edgeGeometry = () => ingressLine.evaluate((element) => {
    const path = element as SVGPathElement
    const markerID = path.style.markerEnd.match(/#([^)"]+)/)?.[1] || ''
    const marker = document.getElementById(markerID) as SVGMarkerElement | null
    return { strokeWidth: path.style.strokeWidth, markerID, markerWidth: marker?.markerWidth.baseVal.value, markerUnits: marker?.markerUnits.baseVal }
  })
  await expect.poll(edgeGeometry).toEqual({ strokeWidth: '1', markerID: 'topology-arrow-inactive', markerWidth: 6, markerUnits: 1 })

  expect((await applicationRequest('/checkout?sku=topology-active&quantity=1')).status).toBe(200)
  await expect.poll(async () => (await edgeGeometry()).markerID).toBe('topology-arrow-active')
  const activeGeometry = await edgeGeometry()
  expect(activeGeometry).toMatchObject({ strokeWidth: '1.77', markerID: 'topology-arrow-active', markerUnits: 1 })
  expect(activeGeometry.markerWidth).toBeCloseTo(10.62)
  await Promise.all(Array.from({ length: 24 }, (_, index) => applicationRequest(`/checkout?sku=topology-load-${index}&quantity=1`)))
  await expect.poll(() => ingressEdge.locator('.topology-edge__pulse').count()).toBeGreaterThan(1)
  expect(await edgeGeometry()).toEqual(activeGeometry)

  await services.filter({ hasText: 'checkout' }).getByRole('button', { name: 'INSPECT' }).click()
  const drawer = page.getByRole('dialog', { name: 'checkout service' })
  await expect(drawer).toContainText('http://checkout.local.ui-e2e.localhost')
  await drawer.getByRole('button', { name: 'logs' }).click()
  await expect(drawer).toContainText('checkout ready on')
  await drawer.getByRole('button', { name: 'Close' }).click()
})

test('starts a Portless-owned debugger and returns the service to normal mode', async ({ page }) => {
  const state = readE2EState()
  const debugPath = `/environments/${state.debugProject}/${state.environment}`
  await authenticate(page, debugPath)

  const checkout = page.locator('.service-row--interactive').filter({ hasText: 'checkout' })
  await expect(checkout).toContainText('managed')
  await checkout.getByRole('button', { name: 'INSPECT' }).click()

  const drawer = page.getByRole('dialog', { name: 'checkout service' })
  await drawer.getByRole('button', { name: 'DEBUG' }).click()
  await expect(drawer.getByRole('button', { name: 'STARTING DEBUG…' })).toBeDisabled()
  await expect(drawer.getByRole('button', { name: /^(RESTART|START)$/ })).toBeDisabled()
  await expect(drawer.getByRole('button', { name: 'STOP' })).toBeDisabled()
  await expect(drawer).toContainText('node-inspector', { timeout: 30_000 })
  await expect(drawer).toContainText('listening', { timeout: 30_000 })
  await expect(drawer).toContainText('Attach to Process')
  await expect(checkout).toContainText('debug')

  const environment = await controlAPI<{ services: Array<{ name: string; pid: number; launchMode: string; debugger?: { host: string; port: number; state: string } }> }>(`/api/v1/environments/${state.debugProject}/${state.environment}`)
  const debugService = environment.services.find((service) => service.name === 'checkout')
  expect(debugService).toMatchObject({ launchMode: 'debug', debugger: { host: '127.0.0.1', state: 'listening' } })
  expect(debugService?.pid).toBeGreaterThan(0)
  expect(debugService?.debugger?.port).toBeGreaterThan(0)
  await expect(page.getByRole('heading', { name: state.environment, exact: true }).locator('..')).toContainText('healthy')
  await expect(page.locator('body')).not.toContainText('development')
  await expect(page.locator('body')).not.toContainText('debug services are ready')

  const operationPattern = '**/api/v1/environments/**/operations/*'
  const managePattern = '**/api/v1/environments/**/services/checkout/manage'
  let releaseOperation!: () => void
  const operationGate = new Promise<void>((resolve) => { releaseOperation = resolve })
  let operationPolls = 0
  let manageRequests = 0
  await page.route(operationPattern, async (route) => {
    operationPolls++
    await operationGate
    await route.continue()
  })
  await page.route(managePattern, async (route) => {
    manageRequests++
    await route.continue()
  })
  await drawer.getByRole('button', { name: 'RUN NORMALLY' }).click()
  try {
    const runningNormally = drawer.getByRole('button', { name: 'RUNNING NORMALLY…' })
    await expect(runningNormally).toBeDisabled()
    await expect(drawer.getByRole('button', { name: /^(RESTART|START)$/ })).toBeDisabled()
    await expect(drawer.getByRole('button', { name: 'STOP' })).toBeDisabled()
    await expect.poll(() => operationPolls).toBeGreaterThan(0)
    await runningNormally.evaluate((button: HTMLButtonElement) => button.click())
    expect(manageRequests).toBe(1)
  } finally {
    releaseOperation()
    await page.unroute(operationPattern)
    await page.unroute(managePattern)
  }
  await expect(drawer.getByRole('button', { name: 'DEBUG' })).toBeVisible({ timeout: 30_000 })
  await expect(drawer).not.toContainText('node-inspector')
  await expect(checkout).toContainText('managed')
})

test('shows captured request and response details', async ({ page }) => {
  await authenticate(page)
  const response = await applicationRequest('/checkout?sku=coffee-mug&quantity=2', {
    Authorization: 'Bearer browser-e2e-secret',
    'X-E2E-Trace': 'visible',
  })
  expect(response.status).toBe(200)

  await page.getByRole('navigation', { name: 'ui-e2e/local views' }).getByRole('button', { name: 'Traffic' }).click()
	await page.getByRole('tab', { name: 'EXCHANGES' }).click()
  const row = page.locator('button.traffic-row').filter({ hasText: '/checkout' }).filter({ hasText: 'external' }).first()
  await expect(row).toBeVisible()
  await row.click()

  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail).toContainText('GET /checkout')
	await expect(detail.locator('.traffic-message--request')).toContainText('Authorization: [REDACTED]')
	await expect(detail.locator('.traffic-message--request')).not.toContainText('Bearer browser-e2e-secret')
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
	const exported = JSON.parse(await readDownload(await download.path())) as { recording: string; exchanges: Array<{ source: string; target: string }> }
  expect(exported.recording).toBe('ui-recording')
	expect(exported.exchanges).toHaveLength(1)
	expect(exported.exchanges[0]).toMatchObject({ source: 'checkout', target: 'orders' })

  await row.getByRole('button', { name: 'DELETE' }).click()
  await expect(page.locator('.experiment-row').filter({ hasText: 'ui-recording' })).toHaveCount(0)
})

test('creates and hot-binds a mock profile without restarting peer services', async ({ page }) => {
  const state = readE2EState()
  type Runtime = { name: string; pid?: number; generation: number; status: string }
  type Snapshot = { services: Runtime[]; bindings: Array<{ service: string; provider: string; mock?: { profile: string } }> }
  const before = await controlAPI<Snapshot>(`/api/v1/environments/${state.project}/${state.environment}`)
  const beforeByName = new Map(before.services.map((service) => [service.name, service]))
  await authenticate(page, environmentPath('mocks'))

  await page.getByRole('button', { name: 'CREATE PROFILE', exact: true }).click()
  const profileDialog = page.getByRole('dialog', { name: 'Create mock profile' })
  await profileDialog.getByLabel('NAME').fill('sold-out')
  await profileDialog.getByLabel('SERVICE').selectOption('inventory')
  await profileDialog.getByLabel('DESCRIPTION').fill('Inventory has no available stock')
  await profileDialog.getByRole('button', { name: 'CREATE PROFILE', exact: true }).click()
  await expect(profileDialog).toHaveCount(0)
  await expect(page.locator('.mock-profile-row').filter({ hasText: 'sold-out' })).toBeVisible()
  const mockDrawer = page.getByRole('dialog', { name: 'sold-out mock profile' })
  await expect(mockDrawer).toBeVisible()
  await expect(page).toHaveURL(/tab=mocks&profile=sold-out/)
  await mockDrawer.getByRole('button', { name: 'Full screen sold-out mock profile' }).click()
  await expect(mockDrawer).toHaveClass(/drawer--fullscreen/)
  await page.keyboard.press('Escape')
  await expect(mockDrawer).not.toHaveClass(/drawer--fullscreen/)

  await mockDrawer.getByRole('button', { name: 'ADD ROUTE', exact: true }).click()
  const routeDialog = page.getByRole('dialog', { name: 'Add mock route' })
  await routeDialog.getByLabel('NAME', { exact: true }).fill('lookup')
  await routeDialog.getByLabel('PATH').fill('/inventory/{sku}')
  await routeDialog.getByLabel('RESPONSE STATUS').selectOption('200')
  await routeDialog.getByLabel('RESPONSE BODY').fill('{"available":false,"reason":"mocked sold out"}')
  await routeDialog.getByRole('button', { name: 'SAVE ROUTE' }).click()
  await expect(routeDialog).toHaveCount(0)
  await expect(mockDrawer.locator('.mock-route-row').filter({ hasText: 'lookup' })).toContainText('GET /inventory/{sku}')

  await mockDrawer.getByRole('button', { name: 'PREVIEW REQUEST' }).click()
  const previewDialog = page.getByRole('dialog', { name: 'Preview request' })
  await expect(previewDialog.getByLabel('REQUEST BODY')).toHaveCount(0)
  await previewDialog.getByLabel('METHOD').selectOption('POST')
  await previewDialog.getByLabel('PATH AND QUERY').fill('/inventory/coffee-mug?warehouse=central')
  await previewDialog.getByLabel('REQUEST HEADERS · ONE NAME: VALUE PER LINE').fill('Content-Type: application/json\nX-Trace: one\nX-Trace: two')
  await previewDialog.getByLabel('REQUEST BODY').fill('{"sku":"coffee-mug","quantity":1}')
  const previewRequest = page.waitForRequest((request) => request.method() === 'POST' && request.url().endsWith('/mocks/sold-out/preview'))
  await previewDialog.getByRole('button', { name: 'PREVIEW', exact: true }).click()
  const previewPayload = (await previewRequest).postDataJSON() as { method: string; path: string; query: Record<string, string[]>; headers: Record<string, string[]>; body: string }
  expect(previewPayload).toEqual({ method: 'POST', path: '/inventory/coffee-mug', query: { warehouse: ['central'] }, headers: { 'Content-Type': ['application/json'], 'X-Trace': ['one', 'two'] }, body: '{"sku":"coffee-mug","quantity":1}' })
  await expect(previewDialog).toContainText('NO MATCH')
  await previewDialog.getByRole('button', { name: 'CLOSE', exact: true }).click()

  await mockDrawer.getByRole('button', { name: 'Close mock profile' }).click()
  await expect(mockDrawer).toHaveCount(0)
  await expect(page).toHaveURL(/\?tab=mocks$/)
  await page.goBack()
  const reopenedMockDrawer = page.getByRole('dialog', { name: 'sold-out mock profile' })
  await expect(reopenedMockDrawer).toBeVisible()
  await reopenedMockDrawer.getByRole('button', { name: 'Close mock profile' }).click()

  await page.getByRole('navigation', { name: `${state.project}/${state.environment} views` }).getByRole('button', { name: 'Bindings' }).click()
  const inventoryRow = page.locator('.configured-providers-panel .provider-row').filter({ hasText: 'inventory' })
  await inventoryRow.getByRole('button', { name: 'EDIT' }).click()
  const bindingDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await bindingDialog.getByLabel('Provider', { exact: true }).selectOption('mock')
  await expect(bindingDialog.locator('.provider-info-card--mock')).toContainText('LOCAL MOCK')
  await expect(bindingDialog.locator('.provider-info-card--mock')).toContainText('keeps its clean URL')
  await bindingDialog.getByLabel('Mock profile', { exact: true }).selectOption('sold-out')
  await bindingDialog.getByRole('button', { name: 'SWITCH PROVIDER' }).click()
  await expect(bindingDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(inventoryRow.locator('.provider-kind')).toHaveText('Mock')
  await expect(inventoryRow.locator('.provider-configuration')).toHaveText('sold-out')

  const mocked = await controlAPI<Snapshot>(`/api/v1/environments/${state.project}/${state.environment}`)
  const mockedByName = new Map(mocked.services.map((service) => [service.name, service]))
  expect(mockedByName.get('checkout')?.pid).toBe(beforeByName.get('checkout')?.pid)
  expect(mockedByName.get('orders')?.pid).toBe(beforeByName.get('orders')?.pid)
  expect(mockedByName.get('inventory')).toMatchObject({ status: 'ready', generation: beforeByName.get('inventory')?.generation })
  expect(mockedByName.get('inventory')?.pid || 0).toBe(0)
  expect(mocked.bindings.find((binding) => binding.service === 'inventory')).toMatchObject({ provider: 'mock', mock: { profile: 'sold-out' } })

  const checkout = await applicationRequest('/checkout?sku=coffee-mug&quantity=1')
  expect(checkout.status).toBe(409)
  expect(checkout.body).toContain('mocked sold out')
  await expect.poll(async () => {
    const traffic = await controlAPI<{ exchanges: Array<{ target: string; targetProvider?: string; mockProfile?: string; mockRoute?: string }> }>(`/api/v1/environments/${state.project}/${state.environment}/traffic/exchanges?protocol=http&limit=100`)
    return traffic.exchanges.some((exchange) => exchange.target === 'inventory' && exchange.targetProvider === 'mock' && exchange.mockProfile === 'sold-out' && exchange.mockRoute === 'lookup')
  }).toBe(true)

  await inventoryRow.getByRole('button', { name: 'EDIT' }).click()
  const restoreDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await restoreDialog.getByRole('button', { name: 'RESET TO DEFAULT' }).click()
  await expect(restoreDialog).toHaveCount(0, { timeout: 30_000 })
  const restored = await controlAPI<Snapshot>(`/api/v1/environments/${state.project}/${state.environment}`)
  const restoredByName = new Map(restored.services.map((service) => [service.name, service]))
  expect(restoredByName.get('checkout')?.pid).toBe(beforeByName.get('checkout')?.pid)
  expect(restoredByName.get('orders')?.pid).toBe(beforeByName.get('orders')?.pid)
  expect(restoredByName.get('inventory')?.pid).toBeGreaterThan(0)
  expect(restoredByName.get('inventory')?.generation).toBe((beforeByName.get('inventory')?.generation || 0) + 1)
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)

  await page.getByRole('navigation', { name: `${state.project}/${state.environment} views` }).getByRole('button', { name: 'Mocks' }).click()
  const profileRow = page.locator('.mock-profile-row').filter({ hasText: 'sold-out' })
  await profileRow.getByRole('button', { name: 'DELETE' }).click()
  await profileRow.getByRole('button', { name: 'CONFIRM' }).click()
  await expect(profileRow).toHaveCount(0)
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

test('switches one active provider and persists stopped-environment bindings', async ({ page }) => {
  const state = readE2EState()
  type Binding = { service: string; provider: string; source?: string; modifiedAt?: string; remote?: { url: string; classification: string; writePolicy: string; healthPath: string } }
  type Runtime = { name: string; pid?: number; upstreamPort?: number; generation: number; status: string }
  type EnvironmentSnapshot = { status: string; bindings: Binding[]; services: Runtime[] }
  await authenticate(page, environmentPath('bindings'))

  const providerHeader = page.locator('.provider-row--header')
  await expect(providerHeader).toContainText('Service')
  await expect(providerHeader).toContainText('Provider')
  await expect(providerHeader).toContainText('Configuration')
  await expect(providerHeader).toContainText('Modified')
  await expect(providerHeader).not.toContainText('Actions')
  const firstProviderRow = page.locator('.configured-providers-panel .provider-row:not(.provider-row--header)').first()
  await expect(firstProviderRow.getByRole('cell').first()).toContainText('checkout')
  await expect(firstProviderRow.getByRole('cell').first().locator('.status__mark')).toHaveText('●')
  await expect(firstProviderRow.getByRole('cell').first()).not.toContainText('healthy')
  const modified = firstProviderRow.locator('time')
  await expect(modified).toHaveAttribute('datetime', /\d{4}-\d{2}-\d{2}T/)
  await expect(modified).toContainText(/\w{3} \d{1,2}, \d{4}, \d{1,2}:\d{2} [AP]M/)
  await expect(modified).not.toContainText('ago')
  const headerColumns = await providerHeader.getByRole('columnheader').evaluateAll((columns) => columns.map((column) => Math.round(column.getBoundingClientRect().x)))
  const providerCells = await firstProviderRow.getByRole('cell').evaluateAll((cells) => cells.map((cell) => Math.round(cell.getBoundingClientRect().x)))
  expect(providerCells).toEqual(headerColumns)

  const configureProvider = page.getByRole('button', { name: 'CONFIGURE PROVIDER', exact: true })
  await expect(configureProvider).toBeVisible()
  await configureProvider.click()
  let providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await expect(providerDialog.getByLabel('Service', { exact: true })).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(providerDialog).toHaveCount(0)
  await expect(configureProvider).toBeFocused()

  const firstEdit = firstProviderRow.getByRole('button', { name: 'EDIT', exact: true })
  await firstEdit.click()
  providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  const providerSelect = providerDialog.getByLabel('Provider', { exact: true })
  await expect(providerSelect).toBeFocused()
  await expect(providerDialog.getByLabel('Service', { exact: true })).toHaveCount(0)
  const lockedService = providerDialog.locator('.provider-service-value')
  await expect(lockedService.getByText('SERVICE', { exact: true })).toBeVisible()
  await expect(lockedService.getByText('checkout', { exact: true })).toBeVisible()
  await expect(providerDialog.getByRole('button', { name: 'SWITCH PROVIDER', exact: true })).toBeDisabled()
  await expect(providerDialog.getByText('Choose how Portless should run or route this service in this environment.')).toBeVisible()
  await expect(providerDialog.getByText('Only checkout will switch providers. Other running services stay available.')).toHaveCount(0)
  let environmentRefreshes = 0
  page.on('response', (response) => {
    if (response.request().method() === 'GET' && new URL(response.url()).pathname === '/api/v1/environments') environmentRefreshes++
  })
  const refreshesBeforeEdit = environmentRefreshes
  await providerSelect.selectOption('remote')
  await page.mouse.move(0, 0)
  await expect.poll(() => environmentRefreshes, { timeout: 6_000 }).toBeGreaterThan(refreshesBeforeEdit)
  await expect(providerSelect).toHaveValue('remote')
  await page.keyboard.press('Escape')
  await expect(providerDialog).toHaveCount(0)
  await expect(firstEdit).toBeFocused()

  const before = await controlAPI<EnvironmentSnapshot>(`/api/v1/environments/${state.project}/local`)
  const remoteProviderPort = before.services.find((service) => service.name === 'orders')?.upstreamPort
  expect(remoteProviderPort).toBeTruthy()
  const remoteProviderURL = `http://127.0.0.1:${remoteProviderPort}`
  const stableBefore = Object.fromEntries(before.services.filter((service) => service.name === 'checkout' || service.name === 'orders').map((service) => [service.name, { pid: service.pid, generation: service.generation }]))
  const activeInventoryRow = page.locator('.configured-providers-panel .provider-row:not(.provider-row--header)').filter({ hasText: 'inventory' })
  await activeInventoryRow.getByRole('button', { name: 'EDIT', exact: true }).click()
  providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await providerDialog.getByLabel('Provider', { exact: true }).selectOption('remote')
  await expect(providerDialog.locator('.provider-info-card--remote')).toContainText('REMOTE BOUNDARY')
  await expect(providerDialog.locator('.provider-info-card--remote')).toContainText('recordings and faults remain available')
  await providerDialog.getByLabel('Remote URL', { exact: true }).fill(remoteProviderURL)
  await providerDialog.getByLabel('Classification', { exact: true }).selectOption('qa')
  await providerDialog.getByLabel('Write policy', { exact: true }).selectOption('read-only')
  await providerDialog.getByLabel('Health path', { exact: true }).fill('/health')
  await providerDialog.getByRole('button', { name: 'SWITCH PROVIDER', exact: true }).click()
  await expect(providerDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(activeInventoryRow.locator('.provider-configuration')).toHaveText(remoteProviderURL)

  const remoteActive = await controlAPI<EnvironmentSnapshot>(`/api/v1/environments/${state.project}/local`)
  expect(remoteActive.status).toBe('healthy')
  expect(remoteActive.bindings.find((binding) => binding.service === 'inventory')).toMatchObject({ provider: 'remote', remote: { url: remoteProviderURL } })
  for (const serviceName of ['checkout', 'orders']) {
    const runtime = remoteActive.services.find((service) => service.name === serviceName)
    expect({ pid: runtime?.pid, generation: runtime?.generation }).toEqual(stableBefore[serviceName])
  }

  await activeInventoryRow.getByRole('button', { name: 'EDIT', exact: true }).click()
  providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await providerDialog.getByRole('button', { name: 'RESET TO DEFAULT', exact: true }).click()
  await expect(providerDialog).toHaveCount(0, { timeout: 30_000 })
  const restoredActive = await controlAPI<EnvironmentSnapshot>(`/api/v1/environments/${state.project}/local`)
  expect(restoredActive.bindings.find((binding) => binding.service === 'inventory')?.provider).toBe('local')
  for (const serviceName of ['checkout', 'orders']) {
    const runtime = restoredActive.services.find((service) => service.name === serviceName)
    expect({ pid: runtime?.pid, generation: runtime?.generation }).toEqual(stableBefore[serviceName])
  }

  const clonePath = `/environments/${state.project}/qa-ui?tab=bindings`
  await page.goto(`${state.baseURL}${clonePath}`)
  await expect(page).toHaveURL(new RegExp(`${clonePath.replace('?', '\\?')}$`))
  await expect(page.getByRole('heading', { name: 'qa-ui', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'qa-ui', exact: true }).locator('..')).toContainText('stopped')

  const inventoryRow = page.locator('.configured-providers-panel .provider-row:not(.provider-row--header)').filter({ hasText: 'inventory' })
  await inventoryRow.getByRole('button', { name: 'EDIT', exact: true }).click()
  providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await providerDialog.getByLabel('Provider', { exact: true }).selectOption('remote')
  await providerDialog.getByLabel('Remote URL', { exact: true }).fill('https://inventory.qa.example.test')
  await providerDialog.getByLabel('Classification', { exact: true }).selectOption('staging')
  await providerDialog.getByLabel('Write policy', { exact: true }).selectOption('read-write')
  await providerDialog.getByLabel('Health path', { exact: true }).fill('/ready')
  await providerDialog.getByRole('button', { name: 'SAVE CHANGES', exact: true }).click()
  await expect(providerDialog).toHaveCount(0)
  await expect(page.locator('.configured-providers-panel .experiment-row').filter({ hasText: 'inventory' }).locator('.provider-configuration')).toHaveText('https://inventory.qa.example.test')

  const remote = await controlAPI<{ bindings: Binding[] }>(`/api/v1/environments/${state.project}/qa-ui`)
  const remoteInventory = remote.bindings.find((binding) => binding.service === 'inventory')
  expect(remoteInventory).toMatchObject({
    provider: 'remote',
    remote: { url: 'https://inventory.qa.example.test', classification: 'staging', writePolicy: 'read-write', healthPath: '/ready' },
  })
  expect(remoteInventory?.modifiedAt).toMatch(/^\d{4}-\d{2}-\d{2}T/)

  await inventoryRow.getByRole('button', { name: 'EDIT', exact: true }).click()
  providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await expect(providerDialog.getByRole('button', { name: 'RESET TO DEFAULT', exact: true })).toBeVisible()
  await providerDialog.getByRole('button', { name: 'RESET TO DEFAULT', exact: true }).click()
  await expect(providerDialog).toHaveCount(0)
  const restored = await controlAPI<{ bindings: Binding[] }>(`/api/v1/environments/${state.project}/qa-ui`)
  const restoredInventory = restored.bindings.find((binding) => binding.service === 'inventory')
  expect(restoredInventory?.provider).toBe('local')
  expect(restoredInventory?.source).toBeTruthy()
  const localInventory = page.locator('.configured-providers-panel .experiment-row').filter({ hasText: 'inventory' })
  await expect(localInventory.locator('.provider-configuration')).toHaveText(restoredInventory?.source || '')
})

test('renders durable lifecycle events from the environment timeline', async ({ page }) => {
  await authenticate(page, environmentPath('timeline'))
  type TimelineEvent = { sequence: number; type: string; summary: string }
  const result = await controlAPI<{ timeline: TimelineEvent[] }>('/api/v1/environments/ui-e2e/local/timeline?limit=1000')
  expect(result.timeline.length).toBeGreaterThan(0)
  expect(result.timeline.some((event) => event.type === 'environment.healthy')).toBe(true)
  expect(result.timeline.some((event) => event.type === 'environment.stopped')).toBe(true)

  const rows = page.locator('.timeline-event')
  await expect(rows).toHaveCount(Math.min(25, result.timeline.length))
  await expect(rows.first()).toContainText(result.timeline[0].summary)
  await expect(rows.first()).toContainText(result.timeline[0].type)

  await page.getByLabel('Timeline rows per page').selectOption('50')
  await expect(rows).toHaveCount(Math.min(50, result.timeline.length))
  await page.reload()
  await expect(page.getByText('ENVIRONMENT TIMELINE')).toBeVisible()
  await expect(page.locator('.timeline-event').filter({ hasText: result.timeline[0].summary }).first()).toBeVisible()
})

test('shows a concise traffic error while the daemon reconnects', async ({ page }) => {
  let unavailable = true
  await page.route(/\/api\/v1\/environments\/[^/]+\/[^/]+\/traffic\/traces(?:\?|$)/, (route) => unavailable ? route.fulfill({
    status: 503,
    contentType: 'text/html',
    body: '<!doctype html><html><body><style>relay fallback markup</style></body></html>',
  }) : route.continue())
  await authenticate(page, environmentPath('traffic'))

  const notice = page.getByRole('alert').filter({ hasText: "Traffic couldn't be loaded" })
  await expect(notice).toContainText('DAEMON_UNAVAILABLE')
  await expect(notice).toContainText('Portless is reconnecting to the local daemon. Try again in a moment.')
  await expect(notice).not.toContainText('<!doctype html>')
  await expect(notice).not.toContainText('relay fallback markup')

  unavailable = false
  await expect(notice).toHaveCount(0, { timeout: 8_000 })
})

test('paginates traces and exchanges at 25 rows', async ({ page }) => {
  await authenticate(page, environmentPath('traffic'))
  const marker = `/pagination-e2e-${Date.now()}`
  const responses = await Promise.all(Array.from({ length: 26 }, (_, index) => applicationRequest(`${marker}-${index}`)))
  expect(responses.every((response) => response.status === 404)).toBe(true)

  const filter = page.getByPlaceholder('filter path, service, edge, status…')
  await filter.fill(marker)
  const traceRows = page.locator('button.trace-row')
  const tracePagination = page.getByLabel('traces pagination')
  await expect(traceRows).toHaveCount(25)
  await expect(tracePagination).toContainText('1–25 of 26')
  await traceRows.first().click()
  const trace = page.locator('.trace-waterfall').first()
  await expect(trace).toBeVisible()
  await trace.getByRole('button', { name: /Maximize trace/ }).click()
  await expect(trace).toHaveClass(/trace-waterfall--maximized/)
  await expect(trace.locator('.panel-title')).toContainText('TRACE #')
  await expect(trace.getByRole('button', { name: /Restore trace/ })).toHaveText('×')
  await expect(trace.getByRole('button', { name: /Restore trace/ })).toHaveClass(/icon-button/)
  await page.keyboard.press('Escape')
  await expect(trace).not.toHaveClass(/trace-waterfall--maximized/)
  await tracePagination.getByRole('button', { name: 'Next traces page' }).click()
  await expect(traceRows).toHaveCount(1)
  await expect(tracePagination).toContainText('26–26 of 26')

  await page.getByRole('tab', { name: 'EXCHANGES' }).click()
  const exchangeRows = page.locator('button.traffic-row')
  const exchangePagination = page.getByLabel('exchanges pagination')
  await expect(exchangeRows).toHaveCount(25)
  await expect(exchangePagination).toContainText('1–25 of 26')
  await exchangePagination.getByRole('button', { name: 'Next exchanges page' }).click()
  await expect(exchangeRows).toHaveCount(1)
  await expect(exchangePagination).toContainText('26–26 of 26')

  await page.getByRole('button', { name: 'CLEAR', exact: true }).click()
  await expect(page.getByText('No matching exchanges yet.')).toBeVisible()
  await expect(exchangeRows).toHaveCount(0)
  await expect.poll(async () => {
    const snapshot = await controlAPI<{ exchanges: unknown[] }>('/api/v1/environments/ui-e2e/local/traffic/exchanges?protocol=all&limit=1000')
    return snapshot.exchanges.length
  }).toBe(0)
  await page.getByRole('tab', { name: 'TRACES' }).click()
  await expect(page.getByText('No matching traces yet. Open an application endpoint or exercise a service connection to capture one.')).toBeVisible()
  await expect(traceRows).toHaveCount(0)
})

test('filters, pauses, resumes, and switches live traffic protocols', async ({ page }) => {
  await authenticate(page, environmentPath('traffic'))
  expect((await applicationRequest('/')).status).toBe(404)
  expect((await applicationRequest('/checkout?sku=traffic-controls&quantity=1')).status).toBe(200)

  const tracesTab = page.getByRole('tab', { name: 'TRACES' })
  const trafficHeader = page.locator('.traffic-header')
  await expect(tracesTab).toHaveAttribute('aria-selected', 'true')
  await expect.poll(async () => {
    const [headerBox, tabBox] = await Promise.all([trafficHeader.boundingBox(), tracesTab.boundingBox()])
    if (!headerBox || !tabBox) return false
    const top = tabBox.y - headerBox.y
    const bottom = headerBox.y + headerBox.height - tabBox.y - tabBox.height
    return top >= 0 && top <= 1 && bottom >= 0 && bottom <= 1
  }).toBe(true)
  await page.getByRole('tab', { name: 'EXCHANGES' }).click()
  const filter = page.getByPlaceholder('filter path, service, edge, status…')
  const rows = page.locator('button.traffic-row')
  await filter.fill('/checkout')
  await expect(rows.first()).toBeVisible()
  expect((await rows.allTextContents()).every((text) => text.includes('/checkout'))).toBe(true)

  const marker = `/paused-e2e-${Date.now()}`
  await filter.fill(marker)
  await page.getByRole('button', { name: 'PAUSE' }).click()
  await expect(page.locator('.live-count')).toContainText('PAUSED')
  expect((await applicationRequest(marker)).status).toBe(404)
  await expect.poll(async () => {
    const snapshot = await controlAPI<{ exchanges: Array<{ path: string }> }>('/api/v1/environments/ui-e2e/local/traffic/exchanges?protocol=http&limit=500')
    return snapshot.exchanges.some((event) => event.path === marker)
  }).toBe(true)
  await expect(page.locator('.live-count')).toContainText('BUFFERED')
  await expect(rows).toHaveCount(0)

  await page.getByRole('button', { name: 'RESUME' }).click()
  await expect(page.locator('.live-count')).toContainText('STREAMING')
  await expect(rows.first()).toContainText(marker)

  await filter.fill('')
  const protocol = page.getByRole('group', { name: 'Traffic protocol' })
  await protocol.getByRole('button', { name: 'TCP' }).click()
  await expect(protocol.getByRole('button', { name: 'TCP' })).toHaveClass(/is-active/)
  await protocol.getByRole('button', { name: 'HTTP' }).click()
  await expect(protocol.getByRole('button', { name: 'HTTP' })).toHaveClass(/is-active/)
})

test('supports keyboard topology inspection and command palette navigation', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page, environmentPath('topology'))

  const canvas = page.getByLabel('Topology canvas; drag to pan')
  const topologyIsCentered = () => canvas.evaluate((element) => {
    const viewport = element as HTMLElement
    const targetLeft = Math.max(0, (viewport.scrollWidth-viewport.clientWidth)/2)
    const targetTop = Math.min(120, Math.max(0, (viewport.scrollHeight-viewport.clientHeight)/2))
    return Math.abs(viewport.scrollLeft-targetLeft) < 2 && Math.abs(viewport.scrollTop-targetTop) < 2
  })
  await expect.poll(topologyIsCentered).toBe(true)
  await canvas.evaluate((element) => {
    element.scrollLeft = 0
    element.scrollTop = 0
  })
  await expect.poll(topologyIsCentered).toBe(false)
  await page.getByRole('button', { name: 'Center topology' }).click()
  await expect.poll(topologyIsCentered).toBe(true)

  const edge = page.getByRole('button', { name: 'Inspect traffic from external to checkout' })
  await edge.focus()
  await expect(edge).toBeFocused()
  await edge.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/environments/${state.project}/${state.environment}\\?tab=traffic&edge=external%3Acheckout&protocol=http$`))
	await expect(page.locator('.traffic-filter-chip')).toContainText('external → checkout')

  await page.keyboard.press('Control+K')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  const input = palette.getByPlaceholder('jump to a project or environment')
  await expect(input).toBeFocused()
  await input.fill('Settings')
  await input.press('Enter')
  await expect(page).toHaveURL(new RegExp('/settings$'))
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible()
  await expect(page.getByRole('radiogroup', { name: 'Theme' })).toBeVisible()
  await page.getByRole('tab', { name: 'RUNTIME' }).click()
  await expect(page.getByText('CONTAINER RUNTIME')).toBeVisible()
  expect(await page.locator('.runtime-candidate').count()).toBeGreaterThan(0)

  await page.keyboard.press('Control+K')
  await palette.getByPlaceholder('jump to a project or environment').fill('nothing-matches-this-command')
  await expect(palette).toContainText('No matching project, environment, or action.')
  await page.keyboard.press('Escape')
  await expect(palette).toHaveCount(0)
})

test('shows useful not-found routes and returns to projects', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page, '/projects')

  await page.goto(`${state.baseURL}/projects/not-a-portless-project`)
  await expect(page.getByText('PROJECT NOT FOUND')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'not-a-portless-project' })).toBeVisible()
  await page.getByRole('button', { name: 'ALL PROJECTS' }).click()
  await expect(page).toHaveURL(new RegExp('/projects$'))

  await page.goto(`${state.baseURL}/environments/${state.project}/not-an-environment`)
  await expect(page.getByText('ENVIRONMENT NOT FOUND')).toBeVisible()
  await expect(page.getByRole('heading', { name: `${state.project}/not-an-environment` })).toBeVisible()
  await page.getByRole('button', { name: 'ALL PROJECTS' }).click()
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
})

test('surfaces a failed control-plane refresh and reconnects automatically', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  const environmentsEndpoint = `${state.baseURL}/api/v1/environments`
  await page.route(environmentsEndpoint, (route) => route.fulfill({
    status: 503,
    contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'E2E_UNAVAILABLE', message: 'temporary test outage' } }),
  }))

  await expect(page.locator('.sidebar__footer')).toContainText('reconnecting', { timeout: 8_000 })
  await page.unroute(environmentsEndpoint)
  await expect(page.locator('.sidebar__footer')).toContainText('ready', { timeout: 8_000 })
  expect((await applicationRequest('/checkout?sku=reconnected&quantity=1')).status).toBe(200)
})

test('shows semver daemon details and reconnects after restart', async ({ page }) => {
  await authenticate(page)
  const before = await controlAPI<{ instanceId: string; pid: number }>('/api/v1/daemon')
  const beforeEnvironment = await controlAPI<{ services: Array<{ name: string; pid: number }> }>('/api/v1/environments/ui-e2e/local')

  await page.locator('.sidebar__footer').click()
  const drawer = page.getByRole('dialog', { name: 'Portless Daemon' })
  await expect(drawer).toContainText('PROTOCOL')
  await expect(drawer).toContainText('API')
  await expect(drawer.getByText(/^3\.0\.0$/)).toBeVisible()
  await expect(drawer.getByText(/^10\.6\.0$/)).toBeVisible()
  await expect(drawer).not.toContainText('Version')

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
  await page.getByRole('alertdialog', { name: 'Confirm daemon restart' }).getByRole('button', { name: 'RESTART AND RECONNECT' }).click()
  await expect(drawer).toContainText('Daemon restarted', { timeout: 30_000 })

  await expect.poll(async () => (await controlAPI<{ instanceId: string }>('/api/v1/daemon')).instanceId).not.toBe(before.instanceId)
  const afterEnvironment = await controlAPI<{ services: Array<{ name: string; pid: number }> }>('/api/v1/environments/ui-e2e/local')
  expect(afterEnvironment.services.map(({ name, pid }) => ({ name, pid }))).toEqual(beforeEnvironment.services.map(({ name, pid }) => ({ name, pid })))
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
})

test('manages project sources separately from environment checkouts', async ({ page }) => {
  const state = readE2EState()
  const catalogSource = join(state.root, 'catalog-source')
  const catalogWorktree = join(state.root, 'catalog-worktree')
  mkdirSync(catalogSource, { recursive: true })
  mkdirSync(catalogWorktree, { recursive: true })
  writeFileSync(join(catalogSource, 'package.json'), JSON.stringify({
    name: 'catalog',
    scripts: { start: 'node server.js' },
    dependencies: { express: '1.0.0' },
  }))
  writeFileSync(join(catalogSource, 'server.js'), "require('http').createServer((_request, response) => response.end('catalog')).listen(Number(process.env.PORT))\n")
  writeFileSync(join(catalogWorktree, 'package.json'), JSON.stringify({
    name: 'catalog',
    scripts: { start: 'node server.js' },
    dependencies: { express: '1.0.0' },
  }))
  writeFileSync(join(catalogWorktree, 'server.js'), "require('http').createServer((_request, response) => response.end('catalog worktree')).listen(Number(process.env.PORT))\n")

  const pickerInitialPaths: string[] = []
  await page.route('**/api/v1/system/directories/select', async (route) => {
    const input = route.request().postDataJSON() as { initialPath?: string }
    pickerInitialPaths.push(input.initialPath || '')
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ path: realpathSync(catalogWorktree) }),
    })
  })

  await authenticate(page, environmentPath('bindings'))
  await page.getByRole('button', { name: 'STOP ALL' }).click()
  await expect(page.getByRole('button', { name: 'START ALL' })).toBeVisible({ timeout: 30_000 })

  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${state.project}$`))

  await page.getByRole('button', { name: 'ADD SOURCE', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Add source' })
  await expect(dialog.getByLabel('NAME', { exact: true })).toBeFocused()
  await expect(dialog.getByRole('button', { name: 'BROWSE…' })).toBeVisible()
  await dialog.getByLabel('NAME', { exact: true }).fill('catalog')
  await dialog.getByLabel('Initial environment').selectOption(state.environment)
  await dialog.getByLabel('INITIAL CHECKOUT PATH', { exact: true }).fill(catalogSource)
  await dialog.getByRole('button', { name: 'ADD SOURCE', exact: true }).click()
  await expect(dialog).toHaveCount(0, { timeout: 30_000 })

  const projectSource = page.locator('.project-source-row:not(.table-row--header)').filter({ hasText: 'catalog' })
  await expect(projectSource).toBeVisible()
  await expect(projectSource.locator('code')).toHaveText(realpathSync(catalogSource))
  await expect(projectSource.locator('small')).toHaveCount(0)

  await page.goto(`${state.baseURL}${environmentPath('bindings')}`)
  const checkout = page.locator('.source-checkouts-panel .source-table tbody tr').filter({ hasText: 'catalog' })
  await expect(checkout).toBeVisible()
  await expect(checkout.locator('code')).toHaveText(realpathSync(catalogSource))
  await expect(checkout.locator('time')).toHaveAttribute('datetime', /^\d{4}-\d{2}-\d{2}T/)
  const createdAt = await checkout.locator('time').getAttribute('datetime')
  await checkout.getByRole('button', { name: 'EDIT', exact: true }).click()
  const editDialog = page.getByRole('dialog', { name: 'Edit catalog' })
  await expect(editDialog.getByLabel('CHECKOUT PATH')).toHaveValue(realpathSync(catalogSource))
  await editDialog.getByRole('button', { name: 'BROWSE…' }).click()
  await expect(editDialog.getByLabel('CHECKOUT PATH')).toHaveValue(realpathSync(catalogWorktree))
  expect(pickerInitialPaths).toEqual([realpathSync(catalogSource)])
  await editDialog.getByRole('button', { name: 'SAVE CHANGES' }).click()
  await expect(editDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(checkout.locator('code')).toHaveText(realpathSync(catalogWorktree))
  await expect(checkout.locator('time')).toHaveAttribute('datetime', createdAt || '')

  const catalogProvider = page.locator('.configured-providers-panel .provider-row:not(.provider-row--header)').filter({ hasText: 'catalog' })
  await catalogProvider.getByRole('button', { name: 'EDIT', exact: true }).click()
  const providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await providerDialog.getByLabel('Provider', { exact: true }).selectOption('remote')
  await providerDialog.getByLabel('Remote URL', { exact: true }).fill('https://catalog.qa.example.com')
  await providerDialog.getByLabel('Classification', { exact: true }).selectOption('qa')
  await providerDialog.getByLabel('Write policy', { exact: true }).selectOption('read-only')
  await providerDialog.getByRole('button', { name: 'SAVE CHANGES', exact: true }).click()
  await expect(providerDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(catalogProvider.locator('.provider-kind')).toHaveText('Remote')

  await checkout.getByRole('button', { name: 'REMOVE', exact: true }).click()
  const removeDialog = page.getByRole('alertdialog', { name: 'Remove catalog checkout?' })
  await expect(removeDialog).toContainText(`The project source, its services, and every other environment stay unchanged.`)
  await removeDialog.getByRole('button', { name: 'REMOVE CHECKOUT', exact: true }).click()
  await expect(removeDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(checkout).toContainText('Not configured')
  await expect(checkout.getByRole('button', { name: 'CONFIGURE', exact: true })).toBeVisible()
  await expect(checkout.locator('code')).toHaveCount(0)

  let project = await controlAPI<{ sources: Array<{ name: string }> }>(`/api/v1/projects/${state.project}`)
  expect(project.sources.map((item) => item.name)).toContain('catalog')

  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  const retainedProjectSource = page.locator('.project-source-row:not(.table-row--header)').filter({ hasText: 'catalog' })
  await expect(retainedProjectSource).toContainText('not bound locally')
  await retainedProjectSource.getByRole('button', { name: 'DELETE' }).click()
  const deleteDialog = page.getByRole('alertdialog', { name: 'Delete catalog?' })
  await expect(deleteDialog).toContainText('catalog')
  await deleteDialog.getByRole('button', { name: 'DELETE SOURCE', exact: true }).click()
  await expect(deleteDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(retainedProjectSource).toHaveCount(0)

  project = await controlAPI<{ sources: Array<{ name: string }> }>(`/api/v1/projects/${state.project}`)
  expect(project.sources.map((item) => item.name)).not.toContain('catalog')
  expect(project.sources.length).toBeGreaterThan(0)
})
