import { expect, test } from '@playwright/test'
import { applicationRequest, authenticate, environmentHeader, controlAPI, environmentPath, openCommandPalette } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('renders real services, endpoints, topology, and service details', async ({ page }) => {
  const state = readE2EState()
  await controlAPI(`/api/v1/environments/${state.project}/${state.environment}/traffic`, { method: 'DELETE' })
  await authenticate(page)
  const bindingsCard = page.locator('.state-panel').filter({ hasText: 'BINDINGS' })
  await expect(bindingsCard).toContainText('LOCAL')
  await expect(bindingsCard).toContainText('3 services local')
  await expect(page.locator('.state-panel').filter({ hasText: 'REVISION' })).toHaveCount(0)
  const services = page.locator('.service-row--interactive')
  await expect(services).toHaveCount(3)
  const serviceHeader = page.locator('.services-panel .sortable-header-row')
  const serviceSortHeaders = serviceHeader.locator('.sortable-grid-header')
  const serviceSortControls = serviceSortHeaders.locator('.sortable-column-sort-control')
  await expect(serviceSortHeaders.locator(':scope > span:first-child')).toHaveText(['Name', 'Mode', 'State', 'Restarts', 'Requests', 'P95', 'Endpoint / reason'])
  await expect.poll(() => serviceSortControls.evaluateAll((controls) => controls.map((control) => getComputedStyle(control).opacity))).toEqual(Array(7).fill('0'))
  await serviceHeader.hover()
  await expect.poll(() => serviceSortControls.evaluateAll((controls) => controls.map((control) => getComputedStyle(control).opacity))).toEqual(Array(7).fill('1'))
  await serviceHeader.getByRole('button', { name: 'Sort Name descending' }).click()
  await expect(serviceSortHeaders.filter({ hasText: 'Name' })).toHaveAttribute('aria-sort', 'descending')
  await page.locator('.services-panel > .panel-title').hover()
  await expect.poll(() => serviceSortControls.evaluateAll((controls) => controls.map((control) => getComputedStyle(control).opacity))).toEqual(['1', '0', '0', '0', '0', '0', '0'])
  await expect(services.first().locator('strong')).toHaveText('orders')
  for (const name of ['checkout', 'inventory', 'orders']) {
    await expect(services.filter({ hasText: name })).toBeVisible()
  }
  const checkoutRow = services.filter({ hasText: 'checkout' })
  await expect(checkoutRow).toContainText('http://checkout.local.ui-e2e.localhost')
  await expect(checkoutRow).not.toContainText('DETAILS')

  const checkoutActions = checkoutRow.getByRole('button', { name: 'Service actions for checkout' })
  await checkoutActions.click()
  let actionMenu = page.getByRole('menu', { name: 'checkout actions' })
  const openCheckout = actionMenu.getByRole('menuitem', { name: 'OPEN ↗' })
  await expect(openCheckout).toHaveAttribute('href', 'http://checkout.local.ui-e2e.localhost')
  await expect(openCheckout).toHaveAttribute('target', '_blank')
  await expect(actionMenu.getByRole('menuitem', { name: 'RESTART' })).toBeVisible()
  await expect(actionMenu.getByRole('menuitem', { name: 'DEBUG' })).toHaveCount(0)
  await expect(actionMenu.getByRole('menuitem', { name: 'STOP' })).toBeVisible()
  await environmentHeader(page).getByRole('heading', { name: 'Overview', exact: true }).click()
  await expect(actionMenu).toHaveCount(0)

  await checkoutActions.click()
  actionMenu = page.getByRole('menu', { name: 'checkout actions' })
  await actionMenu.getByRole('menuitem', { name: 'RESTART' }).press('Escape')
  await expect(actionMenu).toHaveCount(0)
  await expect(checkoutActions).toBeFocused()

  await page.evaluate(() => navigator.clipboard.writeText('before endpoint copy'))
  const checkoutEndpoint = checkoutRow.getByRole('button', { name: 'Copy checkout endpoint' })
  await checkoutEndpoint.getByText('http://checkout.local.ui-e2e.localhost', { exact: true }).click()
  await expect(checkoutRow.getByRole('button', { name: 'checkout endpoint copied' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('http://checkout.local.ui-e2e.localhost')
  await expect(page.getByRole('dialog', { name: 'checkout service' })).toHaveCount(0)

  const topology = page.getByRole('region', { name: 'Service topology' })
  for (const edge of ['external to checkout', 'checkout to inventory', 'checkout to orders']) {
    await expect(topology.getByRole('button', { name: `Inspect traffic from ${edge}` })).toBeVisible()
  }
  await expect(topology.getByRole('button', { name: 'Inspect traffic from checkout to redis' })).toHaveCount(0)

  const topologyPreview = topology.getByRole('tooltip')
  const checkoutTopologyNode = topology.locator('.topology-node[data-service="checkout"]')
  const inventoryTopologyNode = topology.locator('.topology-node[data-service="inventory"]')
  await expect(topologyPreview).toHaveCount(0)
  await checkoutTopologyNode.hover()
  await expect(topologyPreview).toBeVisible()
  await expect(topologyPreview).toContainText('checkout')
  await expect(topologyPreview).toContainText('managed')
  await expect(topologyPreview).toContainText('http://checkout.local.ui-e2e.localhost')
  await expect(topologyPreview).toContainText('recent requests')
  await expect(topologyPreview).toContainText('1 inbound · 2 outbound')
  await expect(topologyPreview).toContainText('Click node for full details')

  await inventoryTopologyNode.hover()
  await expect(topologyPreview).toContainText('inventory')
  await expect(topology.locator('.topology-edge[data-source="checkout"][data-target="inventory"]')).toHaveClass(/is-preview-connected/)
  await expect(topology.locator('.topology-edge[data-source="external"][data-target="checkout"]')).toHaveClass(/is-preview-dimmed/)
  await expect(topology.locator('.topology-node[data-service="orders"]')).toHaveClass(/is-preview-dimmed/)
  await expect(checkoutTopologyNode).toHaveClass(/is-preview-related/)

  await checkoutTopologyNode.focus()
  await expect(topologyPreview).toContainText('checkout')
  await checkoutTopologyNode.press('Tab')
  await expect(inventoryTopologyNode).toBeFocused()
  await expect(topologyPreview).toContainText('inventory')
  const centerTopology = topology.getByRole('button', { name: 'Center topology' })
  await centerTopology.hover()
  await centerTopology.focus()
  await expect(topologyPreview).toHaveCount(0)

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

  let serviceLogRequests = 0
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname.endsWith('/logs') && url.searchParams.get('service') === 'checkout') serviceLogRequests++
  })
  await checkoutRow.click()
  const drawer = page.getByRole('dialog', { name: 'checkout service' })
  await expect(drawer).toContainText('http://checkout.local.ui-e2e.localhost')
  await drawer.getByRole('button', { name: 'logs' }).click()
  await expect(drawer).toContainText('checkout ready on')
  const rawLogs = drawer.getByRole('button', { name: 'Open raw logs in new tab' })
  const pauseTail = drawer.getByRole('button', { name: 'Pause live tail' })
  await expect(pauseTail).toHaveAttribute('aria-pressed', 'true')
  await expect(drawer.getByRole('status', { name: 'Live tail active' })).toBeVisible()
  await expect.poll(() => serviceLogRequests).toBeGreaterThan(1)
  const [rawPage] = await Promise.all([page.waitForEvent('popup'), rawLogs.click()])
  await rawPage.waitForLoadState()
  expect(await rawPage.evaluate(() => document.contentType)).toBe('text/plain')
  await expect(rawPage.locator('body')).toContainText('checkout ready on')
  await rawPage.close()
  await pauseTail.click()
  await expect(drawer.getByRole('button', { name: 'Resume live tail' })).toHaveAttribute('aria-pressed', 'false')
  await expect(drawer.getByRole('status', { name: 'Live tail active' })).toHaveCount(0)
  await page.waitForTimeout(100)
  const pausedLogRequests = serviceLogRequests
  await page.waitForTimeout(1_100)
  expect(serviceLogRequests).toBe(pausedLogRequests)
  const resumedLogRequest = page.waitForRequest((request) => {
    const url = new URL(request.url())
    return url.pathname.endsWith('/logs') && url.searchParams.get('service') === 'checkout'
  })
  await drawer.getByRole('button', { name: 'Resume live tail' }).click()
  await resumedLogRequest
  await expect(drawer.getByRole('button', { name: 'Pause live tail' })).toHaveAttribute('aria-pressed', 'true')
  expect(serviceLogRequests).toBeGreaterThan(pausedLogRequests)
  await drawer.getByRole('button', { name: 'Close' }).click()
  await page.waitForTimeout(100)
  const checkoutRequestsAfterClose = serviceLogRequests
  await services.filter({ hasText: 'inventory' }).click()
  const inventoryDrawer = page.getByRole('dialog', { name: 'inventory service' })
  await inventoryDrawer.getByRole('button', { name: 'logs' }).click()
  await expect(inventoryDrawer.getByLabel('inventory logs')).toBeVisible()
  await page.waitForTimeout(1_100)
  expect(serviceLogRequests).toBe(checkoutRequestsAfterClose)
  await inventoryDrawer.getByRole('button', { name: 'Close' }).click()
})

test('starts a Portless-owned debugger and returns the service to normal mode', async ({ page }) => {
  const state = readE2EState()
  const debugPath = `/environments/${state.debugProject}/${state.environment}`
  await authenticate(page, debugPath)

  const checkout = page.locator('.service-row--interactive').filter({ hasText: 'checkout' })
  await expect(checkout).toContainText('managed')
  await checkout.getByRole('button', { name: 'Service actions for checkout' }).click()
  const actionMenu = page.getByRole('menu', { name: 'checkout actions' })
  await actionMenu.getByRole('menuitem', { name: 'DEBUG' }).click()
  await expect(actionMenu).toHaveCount(0)
  await expect(checkout).toContainText('debug', { timeout: 30_000 })
  await checkout.getByRole('button', { name: 'View checkout details' }).click()

  const drawer = page.getByRole('dialog', { name: 'checkout service' })
  await expect(drawer).toContainText('node-inspector', { timeout: 30_000 })
  await expect(drawer).toContainText('listening', { timeout: 30_000 })
  await expect(drawer).toContainText('Attach to Process')
  await expect(checkout).toContainText('debug')

  const environment = await controlAPI<{ services: Array<{ name: string; pid: number; launchMode: string; debugger?: { host: string; port: number; state: string } }> }>(`/api/v1/environments/${state.debugProject}/${state.environment}`)
  const debugService = environment.services.find((service) => service.name === 'checkout')
  expect(debugService).toMatchObject({ launchMode: 'debug', debugger: { host: '127.0.0.1', state: 'listening' } })
  expect(debugService?.pid).toBeGreaterThan(0)
  expect(debugService?.debugger?.port).toBeGreaterThan(0)
  await expect(environmentHeader(page, state.debugProject)).toContainText('healthy')
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

test('replaces Open with Start in the same header slot and starts the environment', async ({ page }, testInfo) => {
  const state = readE2EState()
  const startPattern = `**/api/v1/environments/${state.project}/${state.environment}/up`
  await authenticate(page)
  const header = environmentHeader(page)
  const open = header.getByRole('link', { name: 'OPEN APP', exact: true })
  const start = header.getByRole('button', { name: 'Start', exact: true })
  await expect(open).toBeVisible()
  const openBounds = await open.boundingBox()
  expect(openBounds).toMatchObject({ width: 100, height: 32 })
  const initialHeaderHeight = await header.evaluate((element) => Math.round(element.getBoundingClientRect().height))
  await header.screenshot({ path: testInfo.outputPath('header-open.png') })
  await (await openCommandPalette(page, 'Stop environment')).getByRole('textbox', { name: 'Search', exact: true }).press('Enter')
  await expect(start).toBeEnabled({ timeout: 30_000 })
  await expect(start).toHaveText('Start')
  await expect(open).toHaveCount(0)
  await expect(header).toContainText('stopped')
  expect(await start.boundingBox()).toEqual(openBounds)
  expect(await header.evaluate((element) => Math.round(element.getBoundingClientRect().height))).toBe(initialHeaderHeight)
  await header.screenshot({ path: testInfo.outputPath('header-start.png') })

  let startRequests = 0
  let releaseStart!: () => void
  const gate = new Promise<void>((resolve) => { releaseStart = resolve })
  await page.route(startPattern, async (route) => {
    startRequests++
    expect(route.request().method()).toBe('POST')
    const response = await route.fetch()
    await gate
    await route.fulfill({ response })
  })
  try {
    await start.evaluate((button: HTMLButtonElement) => { button.click(); button.click() })
    const starting = header.getByRole('button', { name: 'Starting…', exact: true })
    await expect(starting).toBeDisabled()
    await expect(open).toHaveCount(0)
    await expect(header.locator('.environment-header-actions > .button')).toHaveCount(1)
    expect(await starting.boundingBox()).toEqual(openBounds)
    await expect.poll(() => startRequests).toBe(1)
    const palette = await openCommandPalette(page, 'environment')
    await expect(palette.getByRole('button', { name: /^(Start|Stop) environment/ })).toHaveCount(0)
    await page.keyboard.press('Escape')
    releaseStart()
    await expect(open).toBeVisible({ timeout: 30_000 })
    await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible()
    await expect(start).toHaveCount(0)
    expect(await open.boundingBox()).toEqual(openBounds)
    expect(startRequests).toBe(1)
  } finally {
    releaseStart()
    await page.unroute(startPattern)
  }
  await expect((await openCommandPalette(page, 'Stop environment')).getByRole('button', { name: /Stop environment/ })).toBeEnabled()
  await page.keyboard.press('Escape')
  expect(await header.evaluate((element) => Math.round(element.getBoundingClientRect().height))).toBe(initialHeaderHeight)
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
})

test('starts and stops all services from the Overview table without moving its header', async ({ page }, testInfo) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  await authenticate(page)
  const header = environmentHeader(page)
  const title = page.locator('.services-panel > .panel-title')
  const stopAll = title.getByRole('button', { name: 'Stop All', exact: true })
  const startAll = title.getByRole('button', { name: 'Start All', exact: true })
  await expect(title.locator('small')).toHaveCount(0)
  await expect(title).not.toContainText('workloads')
  await expect(stopAll).toBeEnabled()
  await expect(startAll).toHaveCount(0)

  const inventoryRow = page.locator('.service-row--interactive').filter({ has: page.getByRole('button', { name: 'View inventory details', exact: true }) })
  await inventoryRow.getByRole('button', { name: 'Service actions for inventory' }).click()
  await page.getByRole('menu', { name: 'inventory actions' }).getByRole('menuitem', { name: 'STOP', exact: true }).click()
  await expect(inventoryRow).toContainText('stopped', { timeout: 30_000 })
  await expect(title.getByRole('button')).toHaveCount(0)
  await expect(title).toHaveCSS('min-height', '48px')
  await inventoryRow.getByRole('button', { name: 'Service actions for inventory' }).click()
  await page.getByRole('menu', { name: 'inventory actions' }).getByRole('menuitem', { name: 'START', exact: true }).click()
  await expect(stopAll).toBeEnabled({ timeout: 30_000 })

  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    for (const width of [1280, 390, 320]) {
      await page.setViewportSize({ width, height: 900 })
      await expect(stopAll).toBeVisible()
      expect(await stopAll.boundingBox()).toMatchObject({ width: 100, height: 29 })
      expect((await title.boundingBox())?.height).toBe(48)
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
      if (width === 1280) await title.screenshot({ path: testInfo.outputPath(`services-stop-all-${theme}.png`) })
    }
  }
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.keyboard.press('Control+Shift+F')
  await expect(page.locator('.shell')).toHaveClass(/shell--focus-mode/)
  const titleBounds = await title.boundingBox()
  const buttonBounds = await stopAll.boundingBox()

  for (const action of ['down', 'up'] as const) {
    const pattern = `**${base}/${action}`
    let requests = 0
    let release!: () => void
    const gate = new Promise<void>((resolve) => { release = resolve })
    await page.route(pattern, async (route) => {
      requests++
      expect(route.request().method()).toBe('POST')
      if (action === 'down') expect(route.request().postDataJSON()).toEqual({ removeVolumes: false })
      const response = await route.fetch()
      await gate
      await route.fulfill({ response })
    })
    try {
      const button = action === 'down' ? stopAll : startAll
      await button.focus()
      await page.keyboard.press('Tab')
      await page.keyboard.press('Shift+Tab')
      await expect(button).toBeFocused()
      await expect(button).toHaveCSS('outline-style', 'solid')
      if (action === 'down') await button.press('Enter')
      else await button.evaluate((element: HTMLButtonElement) => { element.click(); element.click() })
      const pending = title.getByRole('button', { name: action === 'down' ? 'Stopping…' : 'Starting…', exact: true })
      await expect(pending).toBeDisabled()
      expect(await pending.boundingBox()).toEqual(buttonBounds)
      expect(await title.boundingBox()).toEqual(titleBounds)
      await expect(page.locator('.services-panel .service-row__menu-trigger:not(:disabled)')).toHaveCount(0)
      const palette = await openCommandPalette(page, 'environment')
      await expect(palette.getByRole('button', { name: /^(Start|Stop) environment/ })).toHaveCount(0)
      await page.keyboard.press('Escape')
      if (action === 'up') await expect(header.getByRole('button', { name: 'Starting…', exact: true })).toBeDisabled()
      await expect.poll(async () => (await controlAPI<{ status: string }>(base)).status, { timeout: 30_000 }).toBe(action === 'down' ? 'stopped' : 'healthy')
      await expect(pending).toBeDisabled()
      release()
      const next = action === 'down' ? startAll : stopAll
      await expect(next).toBeEnabled({ timeout: 30_000 })
      expect(await next.boundingBox()).toEqual(buttonBounds)
      expect(await title.boundingBox()).toEqual(titleBounds)
      expect(requests).toBe(1)
      if (action === 'down') await title.screenshot({ path: testInfo.outputPath('services-start-all-focus.png') })
    } finally {
      release()
      await page.unroute(pattern)
    }
  }
  await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible()
  expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
})

test('switches one active provider and persists stopped-environment bindings', async ({ page }) => {
  const state = readE2EState()
  const cloneName = 'qa-bindings'
  type Binding = { service: string; provider: string; source?: string; modifiedAt?: string; remote?: { url: string; classification: string; writePolicy: string; healthPath: string } }
  type Runtime = { name: string; pid?: number; upstreamPort?: number; generation: number; status: string }
  type EnvironmentSnapshot = { status: string; bindings: Binding[]; services: Runtime[] }
  await controlAPI(`/api/v1/environments`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ project: state.project, name: cloneName, from: state.environment }),
  })
  await authenticate(page, environmentPath('bindings'))

  const providerHeader = page.locator('.provider-row--header')
  await expect(providerHeader).toContainText('Service')
  await expect(providerHeader).toContainText('Provider')
  await expect(providerHeader).toContainText('Configuration')
  await expect(providerHeader).toContainText('Modified')
  await expect(providerHeader).not.toContainText('Actions')
  const providerSortHeaders = providerHeader.locator('.sortable-grid-header')
  const providerSortControls = providerSortHeaders.locator('.sortable-column-sort-control')
  await expect(providerSortHeaders.locator(':scope > span:first-child')).toHaveText(['Service', 'Provider', 'Configuration', 'Modified'])
  await expect.poll(() => providerSortControls.evaluateAll((controls) => controls.map((control) => getComputedStyle(control).opacity))).toEqual(Array(4).fill('0'))
  await providerHeader.hover()
  await expect.poll(() => providerSortControls.evaluateAll((controls) => controls.map((control) => getComputedStyle(control).opacity))).toEqual(Array(4).fill('1'))
  await providerHeader.getByRole('button', { name: 'Sort Provider ascending' }).click()
  await expect(providerSortHeaders.filter({ hasText: 'Provider' })).toHaveAttribute('aria-sort', 'ascending')
  await page.locator('.configured-providers-panel > .panel-title').hover()
  await expect.poll(() => providerSortControls.evaluateAll((controls) => controls.map((control) => getComputedStyle(control).opacity))).toEqual(['0', '1', '0', '0'])
  await expect(page.locator('.source-table tbody tr')).toHaveCount(1)
  await expect(page.locator('.source-table .sortable-column-sort-control')).toHaveCount(0)
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
  await expect.poll(() => page.evaluate(() => document.body.style.overflow)).toBe('hidden')
  await expect(providerDialog.getByLabel('Service', { exact: true })).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(providerDialog).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => document.body.style.overflow)).toBe('')
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

  const clonePath = `/environments/${state.project}/${cloneName}?tab=bindings`
  await page.goto(`${state.baseURL}${clonePath}`)
  await expect(page).toHaveURL(new RegExp(`${clonePath.replace('?', '\\?')}$`))
  await expect(environmentHeader(page, state.project, cloneName).getByRole('heading', { name: 'Bindings', exact: true })).toBeVisible()
  await expect(environmentHeader(page, state.project, cloneName)).toContainText('stopped')

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

  const remote = await controlAPI<{ bindings: Binding[] }>(`/api/v1/environments/${state.project}/${cloneName}`)
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
  const restored = await controlAPI<{ bindings: Binding[] }>(`/api/v1/environments/${state.project}/${cloneName}`)
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
  await expect(page.getByText('RECENT ACTIVITY')).toBeVisible()
  await expect(page.locator('.timeline-event').filter({ hasText: result.timeline[0].summary }).first()).toBeVisible()
})
