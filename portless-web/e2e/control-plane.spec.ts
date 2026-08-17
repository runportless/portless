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

test('only edits providers while stopped and persists a remote binding', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page, environmentPath('bindings'))

  await expect(page.getByRole('button', { name: 'SAVE PROVIDER' })).toBeDisabled()
  await expect(page.getByText('Stop the environment before changing providers.')).toBeVisible()

  const clonePath = `/environments/${state.project}/qa-ui?tab=bindings`
  await page.goto(`${state.baseURL}${clonePath}`)
  await expect(page).toHaveURL(new RegExp(`${clonePath.replace('?', '\\?')}$`))
  await expect(page.getByRole('heading', { name: 'qa-ui', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'qa-ui', exact: true }).locator('..')).toContainText('stopped')

  const providerForm = page.locator('.bindings-layout .experiment-form')
  await providerForm.locator('label').filter({ hasText: /^SERVICE/ }).locator('select').selectOption('inventory')
  await providerForm.locator('label').filter({ hasText: /^PROVIDER/ }).locator('select').selectOption('remote')
  await providerForm.locator('label').filter({ hasText: /^REMOTE URL/ }).locator('input').fill('https://inventory.qa.example.test')
  await providerForm.locator('label').filter({ hasText: /^CLASSIFICATION/ }).locator('select').selectOption('staging')
  await providerForm.locator('label').filter({ hasText: /^WRITE POLICY/ }).locator('select').selectOption('read-write')
  await providerForm.locator('label').filter({ hasText: /^HEALTH PATH/ }).locator('input').fill('/ready')
  await providerForm.getByRole('button', { name: 'SAVE PROVIDER' }).click()
  await expect(page.getByText('inventory now uses the remote provider')).toBeVisible()

  type Binding = { service: string; provider: string; source?: string; remote?: { url: string; classification: string; writePolicy: string; healthPath: string } }
  const remote = await controlAPI<{ bindings: Binding[] }>(`/api/v1/environments/${state.project}/qa-ui`)
  expect(remote.bindings.find((binding) => binding.service === 'inventory')).toMatchObject({
    provider: 'remote',
    remote: { url: 'https://inventory.qa.example.test', classification: 'staging', writePolicy: 'read-write', healthPath: '/ready' },
  })

  await providerForm.locator('label').filter({ hasText: /^PROVIDER/ }).locator('select').selectOption('local')
  const source = providerForm.locator('label').filter({ hasText: /^SOURCE/ }).locator('select')
  const firstSource = await source.locator('option').first().evaluate((option: HTMLOptionElement) => option.value)
  await source.selectOption(firstSource)
  await providerForm.getByRole('button', { name: 'SAVE PROVIDER' }).click()
  await expect(page.getByText('inventory now uses the local provider')).toBeVisible()
  const restored = await controlAPI<{ bindings: Binding[] }>(`/api/v1/environments/${state.project}/qa-ui`)
  expect(restored.bindings.find((binding) => binding.service === 'inventory')).toMatchObject({ provider: 'local', source: firstSource })
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

  await expect(page.getByRole('button', { name: 'daemon reconnecting' })).toBeVisible({ timeout: 8_000 })
  await page.unroute(environmentsEndpoint)
  await expect(page.getByRole('button', { name: 'daemon ready' })).toBeVisible({ timeout: 8_000 })
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
	await expect(drawer.getByText(/^8\.2\.0$/)).toBeVisible()
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
