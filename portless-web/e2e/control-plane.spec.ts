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
  await expect(page.getByRole('button', { name: 'CREATE ENVIRONMENT' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'ADD SOURCE' })).toBeVisible()

  await breadcrumbs.getByRole('link', { name: 'projects' }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
  await expect(page.locator('.project-nav__project-link').filter({ hasText: state.project })).toBeVisible()
  await expect(page.getByRole('button', { name: 'CREATE ENVIRONMENT' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'ADD SOURCE' })).toHaveCount(0)
})

test('toggles settings back to the exact environment view', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  const topologyPath = environmentPath('topology')
  await page.goto(`${state.baseURL}${topologyPath}`)

  const settings = page.getByRole('button', { name: 'Settings' })
  await settings.click()
  await expect(page).toHaveURL(/\/settings$/)
  await expect(settings).toHaveAttribute('aria-current', 'page')

  await settings.click()
  await expect(page).toHaveURL(new RegExp(`${topologyPath.replace('?', '\\?')}$`))
  await expect(page.getByRole('navigation', { name: `${state.project}/${state.environment} views` }).getByRole('button', { name: 'Topology' })).toHaveAttribute('aria-current', 'page')
})

test('keeps scrollable pages within the standard bottom gutter', async ({ page }) => {
  const state = readE2EState()
  await page.setViewportSize({ width: 1280, height: 400 })
  await authenticate(page)

  const expectBottomGutter = async (lastContent: string) => {
    await expect(page.locator(lastContent)).toBeVisible()
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0)
    const gap = await page.locator('.page').evaluate((element) => {
      const last = element.lastElementChild
      if (!last) return -1
      return Math.round(element.getBoundingClientRect().bottom - last.getBoundingClientRect().bottom)
    })
    expect(gap).toBe(28)
  }

  await expectBottomGutter('.overview-grid')
  await page.goto(`${state.baseURL}${environmentPath('timeline')}`)
  await expectBottomGutter('.timeline-panel')
})

test('collapses the sidebar into a persistent icon navigation rail', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  const shell = page.locator('.shell')
  const sidebar = page.locator('.sidebar')
  const projectNavigation = page.getByRole('navigation', { name: 'Projects' })
  const environmentViews = page.getByRole('navigation', { name: `${state.project}/${state.environment} views` })
  await page.getByRole('button', { name: 'Collapse navigation' }).click()

  await expect(shell).toHaveClass(/shell--sidebar-collapsed/)
  await expect(sidebar).toHaveCSS('width', '64px')
  await expect(page.locator('.brand__wordmark')).toBeHidden()
  const project = projectNavigation.getByRole('button', { name: new RegExp(`${state.project} project`) })
  const environment = projectNavigation.getByRole('button', { name: new RegExp(`${state.project}/${state.environment}`) })
  const topology = environmentViews.getByRole('button', { name: 'Topology' })
  for (const item of [project, environment, topology, page.getByRole('button', { name: 'Settings' })]) {
    await expect(item).toBeVisible()
    await expect(item.locator('svg')).toBeVisible()
  }

  await topology.click()
  await expect(page).toHaveURL(new RegExp(`${environmentPath('topology').replace('?', '\\?')}$`))
  expect(await page.evaluate(() => localStorage.getItem('portless.sidebar-collapsed'))).toBe('true')

  await page.reload()
  await expect(page.locator('.shell')).toHaveClass(/shell--sidebar-collapsed/)
  await expect(page.locator('.sidebar')).toHaveCSS('width', '64px')
  await page.getByRole('button', { name: 'Expand navigation' }).click()
  await expect(page.locator('.shell')).not.toHaveClass(/shell--sidebar-collapsed/)
  await expect(page.locator('.brand__wordmark')).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('portless.sidebar-collapsed'))).toBe('false')
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

  let serviceLogRequests = 0
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname.endsWith('/logs') && url.searchParams.get('service') === 'checkout') serviceLogRequests++
  })
  await services.filter({ hasText: 'checkout' }).getByRole('button', { name: 'INSPECT' }).click()
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
  await services.filter({ hasText: 'inventory' }).getByRole('button', { name: 'INSPECT' }).click()
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

test('inspects captured request and response details in the exchange workbench', async ({ page }) => {
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
  const exchangeDetailPattern = '**/api/v1/environments/**/traffic/exchanges/*'
  await page.route(exchangeDetailPattern, async (route) => {
    const response = await route.fetch()
    const exchange = await response.json() as Record<string, unknown>
    await route.fulfill({ response, json: { ...exchange, fault: 'ui-fault', recording: 'ui-recording', mockProfile: 'ui-mock', mockRoute: 'checkout' } })
  })
  await row.click()

  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail).toContainText('GET /checkout')
  await page.unroute(exchangeDetailPattern)
  const viewport = page.viewportSize()
  const detailBox = await detail.boundingBox()
  expect(detailBox?.y).toBe(0)
  expect(detailBox?.height).toBe(viewport?.height)
  expect(detailBox?.width).toBe(900)
  expect((detailBox?.x ?? 0) + (detailBox?.width ?? 0)).toBe(viewport?.width)
  const detailScroll = await detail.locator('.traffic-detail__content').evaluate((element) => ({
    clientHeight: element.clientHeight,
    scrollHeight: element.scrollHeight,
  }))
  expect(detailScroll.scrollHeight - detailScroll.clientHeight).toBeLessThan(80)
  const overview = detail.getByRole('region', { name: 'Exchange overview' })
  await expect(overview.locator('.traffic-overview__heading')).toHaveCount(0)
  await expect(overview.locator('.traffic-overview__summary')).toHaveCount(0)
  await expect(overview.locator('.traffic-trace-context')).toHaveCount(0)
  await expect(overview.getByRole('list', { name: 'Exchange interventions' })).toHaveCount(0)
  const detailHeader = detail.locator('.traffic-detail__header')
  const interventions = detailHeader.getByRole('list', { name: 'Exchange interventions' })
  await expect(interventions).toHaveCSS('justify-content', 'flex-end')
  await expect(interventions.getByRole('listitem', { name: 'FAULT ui-fault' })).toHaveClass(/traffic-intervention-badge--fault/)
  await expect(interventions.getByRole('listitem', { name: 'RECORDING ui-recording' })).toHaveClass(/traffic-intervention-badge--recording/)
  await expect(interventions.getByRole('listitem', { name: 'MOCK ui-mock \/ checkout' })).toHaveClass(/traffic-intervention-badge--mock/)
  const interventionBox = await interventions.boundingBox()
  const overviewDataBox = await overview.locator('.traffic-overview__context').boundingBox()
  const detailHeaderBox = await detailHeader.boundingBox()
  expect((interventionBox?.y ?? 0) + (interventionBox?.height ?? 0)).toBeLessThanOrEqual((detailHeaderBox?.y ?? 0) + (detailHeaderBox?.height ?? 0))
  expect((interventionBox?.y ?? 0) + (interventionBox?.height ?? 0)).toBeLessThanOrEqual(overviewDataBox?.y ?? 0)
  const exchangeNavigation = detailHeader.getByRole('navigation', { name: 'Exchange navigation' })
  await expect(exchangeNavigation).toBeVisible()
  await expect(detailHeader.getByRole('navigation', { name: 'Trace span navigation' })).toHaveCount(0)
  await expect(exchangeNavigation.getByRole('button')).toHaveCount(2)
  const previousExchange = exchangeNavigation.getByRole('button', { name: 'Previous exchange' })
  const nextExchange = exchangeNavigation.getByRole('button', { name: 'Next exchange' })
  const exchangePosition = exchangeNavigation.locator('output')
  const initialPosition = await exchangePosition.getAttribute('aria-label')
  const positionMatch = initialPosition?.match(/^Exchange (\d+) of (\d+)$/)
  expect(positionMatch).not.toBeNull()
  const initialIndex = Number(positionMatch?.[1])
  const exchangeLength = Number(positionMatch?.[2])
  expect(initialIndex).toBeGreaterThanOrEqual(1)
  expect(initialIndex).toBeLessThan(exchangeLength)
  if (initialIndex === 1) await expect(previousExchange).toBeDisabled()
  else await expect(previousExchange).toBeEnabled()
  await expect(nextExchange).toBeEnabled()
  const initialDialogLabel = await detail.getAttribute('aria-label')
  await nextExchange.click()
  await expect(exchangePosition).toHaveAttribute('aria-label', `Exchange ${initialIndex + 1} of ${exchangeLength}`)
  await expect(detail).not.toHaveAttribute('aria-label', initialDialogLabel || '')
  await expect(previousExchange).toBeEnabled()
  await previousExchange.click()
  await expect(exchangePosition).toHaveAttribute('aria-label', `Exchange ${initialIndex} of ${exchangeLength}`)
  await expect(detail).toHaveAttribute('aria-label', initialDialogLabel || '')
  await expect(overview.locator('.traffic-overview__context > div').first()).toHaveCSS('padding-bottom', '14px')
  await expect(overview).toContainText('ENVIRONMENT')
  await expect(overview).toContainText('TARGET BINDING')
  await expect(overview.locator('.traffic-overview__context > div').filter({ hasText: 'STARTED' }).locator('strong')).toHaveText(/\d{1,2}:\d{2}:\d{2}\.\d{3}/)
  await expect(overview).not.toContainText('TARGET PROVIDER')
  await expect(overview).toContainText(/· local/)
  await expect(detail.getByRole('tab', { name: 'OVERVIEW' })).toHaveCount(0)
  const requestTab = detail.getByRole('tab', { name: /^REQUEST/ })
  await expect(requestTab).toHaveAttribute('aria-selected', 'true')
  await expect(requestTab).toHaveCSS('font-size', '10px')
  await expect.poll(async () => requestTab.evaluate((element) => getComputedStyle(element).fontSize === getComputedStyle(document.querySelector('.tabs button') as HTMLElement).fontSize)).toBe(true)
  const request = detail.locator('.traffic-message-workbench--request')
  await expect(request.locator('.traffic-message-workbench__summary')).not.toContainText(/captured|transferred/i)
  await expect(request.locator('.traffic-message-workbench__summary')).not.toContainText('content type not reported')
  await expect(request.getByRole('tab', { name: 'HEADERS', exact: true })).toHaveAttribute('aria-selected', 'true')
  const headerKey = request.locator('.traffic-headers__key').first()
  const headerValue = request.locator('.traffic-headers__value').first()
  await expect(headerKey).toBeVisible()
  await expect(headerValue).toBeVisible()
  await expect.poll(() => headerKey.evaluate((element) => getComputedStyle(element).color === getComputedStyle(element.parentElement as HTMLElement).color)).toBe(true)
  await expect.poll(async () => {
    const [keyColor, valueColor] = await Promise.all([
      headerKey.evaluate((element) => getComputedStyle(element).color),
      headerValue.evaluate((element) => getComputedStyle(element).color),
    ])
    return valueColor !== keyColor
  }).toBe(true)
  await expect(request).toContainText('Authorization: [REDACTED]')
  await expect(request).not.toContainText('Bearer browser-e2e-secret')
  await expect(request).toContainText(/X-E2E-Trace: visible/i)
  await expect(request).not.toContainText(/Traceparent:/i)
  await request.getByRole('tab', { name: 'RAW' }).click()
  await expect(request).toContainText('GET /checkout')
  await expect(request).toContainText('Authorization: [REDACTED]')

  await detail.getByRole('tab', { name: /^RESPONSE/ }).click()
  const capturedResponse = detail.locator('.traffic-message-workbench--response')
  await expect(capturedResponse.locator('.traffic-message-workbench__summary')).not.toContainText('captured')
  await expect(capturedResponse.getByRole('tab', { name: 'BODY' })).toHaveAttribute('aria-selected', 'true')
  await expect(capturedResponse).toContainText('"checkout": "accepted"')
  await expect(capturedResponse.locator('pre.traffic-json')).toBeVisible()
  const jsonKey = capturedResponse.locator('.traffic-json__key').first()
  await expect(jsonKey).toBeVisible()
  await expect.poll(() => jsonKey.evaluate((element) => getComputedStyle(element).color === getComputedStyle(element.parentElement as HTMLElement).color)).toBe(true)
  await expect(capturedResponse.locator('.traffic-json__string').first()).toBeVisible()
  await expect(capturedResponse.locator('.traffic-json__number').first()).toBeVisible()

  await detail.getByRole('button', { name: 'Full screen traffic details' }).click()
  await expect(detail).toHaveClass(/traffic-detail--maximized/)
  await expect(detail.getByRole('tab', { name: 'COMPARE' })).toHaveAttribute('aria-selected', 'true')
  await expect(detail.locator('.traffic-message-workbench--request')).toBeVisible()
  await expect(detail.locator('.traffic-message-workbench--response')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(detail).not.toHaveClass(/traffic-detail--maximized/)
  await expect(detail.getByRole('tab', { name: 'COMPARE' })).toHaveCount(0)
  await expect(detail.getByRole('tab', { name: /^REQUEST/ })).toHaveAttribute('aria-selected', 'true')

  await detail.getByRole('button', { name: 'Close traffic details' }).click()
  const downstreamRow = page.locator('button.traffic-row').filter({ hasText: 'checkout→' }).first()
  await expect(downstreamRow).toBeVisible()
  await downstreamRow.click()
  const downstreamDetail = page.getByRole('dialog', { name: /Traffic request and response/ })
  const downstreamOverview = downstreamDetail.getByRole('region', { name: 'Exchange overview' })
  await expect(downstreamOverview.locator('.traffic-trace-context')).toHaveCount(0)
  const downstreamRequest = downstreamDetail.locator('.traffic-message-workbench--request')
  await downstreamRequest.getByRole('tab', { name: 'HEADERS', exact: true }).click()
  await expect(downstreamRequest).not.toContainText(/Traceparent:|\bB3:|X-B3-Trace-Id:|X-Datadog-Trace-Id:/i)
})

test('keeps short trace payloads pinned and preserves compare while navigating', async ({ page }) => {
  await authenticate(page)
  const response = await applicationRequest(`/checkout?sku=coffee-mug&quantity=1&layout=${Date.now()}`)
  expect(response.status).toBe(200)

  await page.getByRole('navigation', { name: 'ui-e2e/local views' }).getByRole('button', { name: 'Traffic' }).click()
  const row = page.locator('button.trace-row').filter({ hasText: '/checkout' }).first()
  await expect(row).toBeVisible()
  await row.click()
  await page.getByRole('region', { name: 'Trace waterfall' }).getByRole('button', { name: /Inspect external to checkout GET \/checkout/ }).click()

  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  const tracePosition = detail.getByRole('navigation', { name: 'Trace span navigation' }).locator('output')
  const payloadBottomGap = () => detail.evaluate((element) => {
    const payload = element.querySelector('.traffic-payload, .traffic-semantic-card')
    if (!(payload instanceof HTMLElement)) return null
    return Math.round(element.getBoundingClientRect().bottom - payload.getBoundingClientRect().bottom)
  })

  const initialPayloadBottomGap = await payloadBottomGap()
  expect(initialPayloadBottomGap).toBeGreaterThanOrEqual(15)
  expect(initialPayloadBottomGap).toBeLessThanOrEqual(18)

  await detail.getByRole('button', { name: 'Next visible span in trace' }).click()
  await expect(tracePosition).toHaveAttribute('aria-label', /Span 2 of \d+/)
  expect(await payloadBottomGap()).toBe(initialPayloadBottomGap)

  await detail.getByRole('button', { name: 'Previous visible span in trace' }).click()
  await expect(tracePosition).toHaveAttribute('aria-label', /Span 1 of \d+/)
  await detail.getByRole('button', { name: 'Full screen traffic details' }).click()
  const compareTab = detail.getByRole('tab', { name: 'COMPARE' })
  await detail.getByRole('tab', { name: 'REQUEST', exact: true }).click()
  await compareTab.click()
  await expect(compareTab).toHaveAttribute('aria-selected', 'true')

  await detail.getByRole('button', { name: 'Next visible span in trace' }).click()
  await expect(tracePosition).toHaveAttribute('aria-label', /Span 2 of \d+/)
  await expect(detail).toHaveClass(/traffic-detail--maximized/)
  await expect(compareTab).toHaveAttribute('aria-selected', 'true')
  await expect(detail.locator('.traffic-message-workbench--request')).toBeVisible()
  await expect(detail.locator('.traffic-message-workbench--response')).toBeVisible()

  await detail.getByRole('button', { name: 'Previous visible span in trace' }).click()
  await expect(tracePosition).toHaveAttribute('aria-label', /Span 1 of \d+/)
  await expect(detail).toHaveClass(/traffic-detail--maximized/)
  await expect(compareTab).toHaveAttribute('aria-selected', 'true')
})

test('preserves maximized compare mode while paging through exchanges', async ({ page }) => {
  await authenticate(page, environmentPath('traffic'))
  const marker = `/exchange-drawer-state-${Date.now()}`
  expect((await applicationRequest(`${marker}-first`)).status).toBe(404)
  expect((await applicationRequest(`${marker}-second`)).status).toBe(404)

  const filter = page.getByPlaceholder('filter path, service, edge, status…')
  await filter.fill(marker)
  await page.getByRole('tab', { name: 'EXCHANGES' }).click()
  const rows = page.locator('button.traffic-row').filter({ hasText: marker })
  await expect(rows).toHaveCount(2)
  await rows.first().click()

  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  const navigation = detail.getByRole('navigation', { name: 'Exchange navigation' })
  const position = navigation.locator('output')
  await expect(position).toHaveAttribute('aria-label', 'Exchange 1 of 2')
  await detail.getByRole('button', { name: 'Full screen traffic details' }).click()
  const compareTab = detail.getByRole('tab', { name: 'COMPARE' })
  await expect(detail).toHaveClass(/traffic-detail--maximized/)
  await expect(compareTab).toHaveAttribute('aria-selected', 'true')

  await navigation.getByRole('button', { name: 'Next exchange' }).click()
  await expect(position).toHaveAttribute('aria-label', 'Exchange 2 of 2')
  await expect(detail).toHaveClass(/traffic-detail--maximized/)
  await expect(compareTab).toHaveAttribute('aria-selected', 'true')

  await navigation.getByRole('button', { name: 'Previous exchange' }).click()
  await expect(position).toHaveAttribute('aria-label', 'Exchange 1 of 2')
  await expect(detail).toHaveClass(/traffic-detail--maximized/)
  await expect(compareTab).toHaveAttribute('aria-selected', 'true')
})

test('resets trace navigation to the first span when changing scope', async ({ page }) => {
  await authenticate(page, environmentPath('traffic'))
  const response = await applicationRequest(`/checkout?sku=coffee-mug&quantity=1&scope=${Date.now()}`)
  expect(response.status).toBe(200)

  const row = page.locator('button.trace-row').filter({ hasText: '/checkout' }).first()
  await expect(row).toBeVisible()
  await row.click()
  const waterfall = page.getByRole('region', { name: 'Trace waterfall' })
  await waterfall.getByRole('button', { name: /Inspect external to checkout GET \/checkout/ }).click()

  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  const navigation = detail.getByRole('navigation', { name: 'Trace span navigation' })
  const httpScope = navigation.getByRole('button', { name: 'HTTP' })
  const allScope = navigation.getByRole('button', { name: 'ALL' })
  const position = navigation.locator('output')
  await expect(httpScope).toHaveAttribute('aria-pressed', 'true')
  await expect(position).toHaveAttribute('aria-label', /Span 1 of \d+/)
  await navigation.getByRole('button', { name: 'Next visible span in trace' }).click()
  await expect(position).toHaveAttribute('aria-label', /Span 2 of \d+/)

  await allScope.click()
  await expect(allScope).toHaveAttribute('aria-pressed', 'true')
  await expect(position).toHaveAttribute('aria-label', /Span 1 of \d+/)

  await httpScope.click()
  await expect(httpScope).toHaveAttribute('aria-pressed', 'true')
  await expect(position).toHaveAttribute('aria-label', /Span 1 of \d+/)
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
  const profileName = profileDialog.getByLabel('NAME')
  await expect(profileName).toBeFocused()
  await profileDialog.getByRole('button', { name: 'Close create mock' }).focus()
  await page.keyboard.press('Shift+Tab')
  await expect(profileDialog.getByRole('button', { name: 'CANCEL' })).toBeFocused()
  await profileName.fill('sold-out')
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
  await page.keyboard.press('Escape')
  await expect(mockDrawer).toHaveCount(0)
  const profileRow = page.locator('.mock-profile-row').filter({ hasText: 'sold-out' })
  const openProfile = profileRow.getByRole('button', { name: 'OPEN' })
  await openProfile.click()
  await expect(mockDrawer).toBeVisible()

  const addRoute = mockDrawer.getByRole('button', { name: 'ADD ROUTE', exact: true })
  await addRoute.click()
  let routeDialog = page.getByRole('dialog', { name: 'Add mock route' })
  await expect(routeDialog.getByLabel('NAME', { exact: true })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(routeDialog).toHaveCount(0)
  await expect(mockDrawer).toBeVisible()
  await expect(addRoute).toBeFocused()
  await addRoute.click()
  routeDialog = page.getByRole('dialog', { name: 'Add mock route' })
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

test('renders database transactions as aggregate waterfall spans with command details in the drawer', async ({ page }) => {
  await authenticate(page, environmentPath('traffic'))
  const marker = `/transaction-waterfall-${Date.now()}`
  expect((await applicationRequest(marker)).status).toBe(404)

  const filter = page.getByPlaceholder('filter path, service, edge, status…')
  await filter.fill(marker)
  const row = page.locator('button.trace-row').filter({ hasText: marker }).first()
  await expect(row).toBeVisible()
  await expect(page.getByRole('button', { name: 'SHOW TCP ROOTS' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'SHOW BACKGROUND' })).toHaveCount(0)

  const syntheticExchanges = new Map<number, Record<string, unknown>>()
  const selectRows = Array.from({ length: 12 }, (_, index) => ({
    id: String(42 + index),
    state: index === 0 ? 'created' : index === 1 ? 'paid' : 'queued',
    note: index === 0 ? null : index === 1 ? 'priority' : `note-${42 + index}`,
  }))
  await page.route('**/traffic/exchanges/*', async (route) => {
    const sequence = Number(new URL(route.request().url()).pathname.split('/').pop())
    const exchange = syntheticExchanges.get(sequence)
    if (!exchange) { await route.continue(); return }
    await route.fulfill({ json: exchange })
  })

  await page.route('**/traffic/traces/*', async (route) => {
    const response = await route.fetch()
    const trace = await response.json() as {
      lastSequence: number
      spans: Array<{ exchange: Record<string, unknown>; depth: number; startOffsetMs: number; correlation: string }>
    }
    const root = trace.spans[0]
    const rootExchange = root.exchange as { sequence: number; project: string; environment: string; startedAt: string; completedAt: string }
    const tcpSpan = (offset: number, operation: string, background = false, transactionGroup?: number, source = 'inventory', target = 'inventory-postgres') => {
      const sequence = rootExchange.sequence + offset
      const query = operation === 'UPDATE'
        ? 'UPDATE store_inventory SET on_hand = on_hand - $1 WHERE sku = $2'
        : operation === 'SELECT' ? 'SELECT id, state, note FROM orders ORDER BY id' : operation
      const requestMessages = [
        { type: operation === 'UPDATE' ? 'parse' : 'query', offsetMs: 0, summary: operation === 'UPDATE' ? `Parse ${query}` : operation, wireBytes: 6, content: query, contentType: 'text/x-sql', encoding: 'utf8' },
        ...(operation === 'UPDATE' ? [{ type: 'bind', offsetMs: 0, summary: 'Bind parameters', wireBytes: 20, content: '[1,"coffee-mug"]', contentType: 'application/json', encoding: 'utf8' }] : []),
      ]
      const responseMessages = operation === 'SELECT' ? [
        { type: 'row-description', offsetMs: 0, summary: '3 columns', wireBytes: 24, fields: [{ name: 'column', value: 'id' }, { name: 'column', value: 'state' }, { name: 'column', value: 'note' }] },
        ...selectRows.map((row, index) => ({ type: 'data-row', offsetMs: index + 1, summary: 'Data row', wireBytes: 24, content: JSON.stringify(row), contentType: 'application/json', encoding: 'utf8' })),
        { type: 'command-complete', offsetMs: selectRows.length + 1, summary: 'SELECT 12', wireBytes: 6, fields: [{ name: 'command', value: 'SELECT 12' }] },
      ] : [{ type: 'command-complete', offsetMs: 1, summary: operation === 'UPDATE' ? 'UPDATE 1' : operation, wireBytes: 6, fields: [{ name: 'command', value: operation === 'UPDATE' ? 'UPDATE 1' : operation }] }]
      const exchange = {
        project: rootExchange.project, environment: rootExchange.environment, sequence: rootExchange.sequence + offset,
        protocol: 'tcp', source, target, background,
        startedAt: rootExchange.startedAt, completedAt: rootExchange.completedAt, durationMs: 2,
        requestBytes: 6, responseBytes: 6,
        tcp: {
          kind: 'operation', applicationProtocol: 'postgresql', operation, inspection: 'decoded', outcome: 'success',
          requestMessageCount: requestMessages.length, responseMessageCount: responseMessages.length,
          requestMessages,
          responseMessages,
        },
      }
      syntheticExchanges.set(sequence, exchange)
      return { exchange, parentSequence: rootExchange.sequence, depth: 1, startOffsetMs: offset * 2, correlation: 'inferred', transactionGroup }
    }
    const redisSpan = (offset: number) => {
      const sequence = rootExchange.sequence + offset
      const cachedOrder = JSON.stringify({ id: 56, sku: 'coffee-mug', quantity: 1, state: 'created' })
      const exchange = {
        project: rootExchange.project, environment: rootExchange.environment, sequence,
        protocol: 'tcp', source: 'orders', target: 'orders-redis', background: false,
        startedAt: rootExchange.startedAt, completedAt: rootExchange.completedAt, durationMs: 1,
        requestBytes: 34, responseBytes: cachedOrder.length + 7,
        tcp: {
          kind: 'operation', applicationProtocol: 'redis', operation: 'GET', inspection: 'decoded', outcome: 'success',
          requestMessageCount: 1, responseMessageCount: 1,
          requestMessages: [{ type: 'command', offsetMs: 0, summary: 'GET store:order:56', wireBytes: 34, contentType: 'application/json', encoding: 'utf8', content: JSON.stringify(['GET', 'store:order:56'], null, 2) }],
          responseMessages: [{ type: 'response', offsetMs: 1, summary: `${cachedOrder.length} byte value`, wireBytes: cachedOrder.length + 7, contentType: 'application/json', encoding: 'utf8', content: JSON.stringify(cachedOrder) }],
        },
      }
      syntheticExchanges.set(sequence, exchange)
      return { exchange, parentSequence: rootExchange.sequence, depth: 1, startOffsetMs: offset * 2, correlation: 'inferred' }
    }
    const spans = [
      root,
      tcpSpan(1, 'QUERY', true),
      tcpSpan(2, 'BEGIN', false, 1),
      tcpSpan(3, 'UPDATE', false, 1),
      tcpSpan(4, 'COMMIT', false, 1),
      tcpSpan(5, 'SELECT', false, undefined, 'orders', 'orders-postgres'),
      redisSpan(6),
    ]
    await route.fulfill({ response, json: { ...trace, lastSequence: rootExchange.sequence + 6, spanCount: spans.length, spans } })
  })

  await row.click()
  const waterfall = page.getByRole('region', { name: 'Trace waterfall' })
  const transaction = waterfall.locator('.trace-span--transaction')
  await expect(transaction).toBeVisible()
  const standaloneOperation = waterfall.getByRole('button', { name: /Inspect orders to orders-postgres POSTGRESQL · SELECT/ })
  const redisOperation = waterfall.getByRole('button', { name: /Inspect orders to orders-redis REDIS · GET/ })
  await expect(standaloneOperation).toBeVisible()
  await expect(redisOperation).toBeVisible()
  await expect(transaction).toHaveClass(/trace-span--dependency-summary/)
  await expect(transaction).toContainText('POSTGRESQL · TRANSACTION')
  await expect(transaction.locator('.trace-span__track small')).toHaveText('6ms')
  await expect(waterfall.locator('.trace-span__disclosure')).toHaveCount(0)
  await expect(standaloneOperation).toHaveClass(/trace-span--dependency-summary/)
  const [transactionBackground, standaloneBackground] = await Promise.all([
    transaction.evaluate((element) => getComputedStyle(element).backgroundColor),
    standaloneOperation.evaluate((element) => getComputedStyle(element).backgroundColor),
  ])
  expect(standaloneBackground).toBe(transactionBackground)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · QUERY/ })).toHaveCount(0)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · BEGIN/ })).toHaveCount(0)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · UPDATE/ })).toHaveCount(0)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · COMMIT/ })).toHaveCount(0)

  await waterfall.getByRole('button', { name: /Inspect inventory to inventory-postgres POSTGRESQL transaction/ }).click()
  let detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail.locator('.traffic-detail__protocol-badge')).toHaveText('TCP')
  await expect(detail.locator('.traffic-detail__heading .eyebrow')).toHaveCount(0)
  await expect(detail.locator('.traffic-detail__heading h3 > span')).toHaveText('POSTGRESQL')
  await expect(detail.locator('.traffic-detail__heading h3 code')).toHaveText('TRANSACTION')
  await expect(detail.locator('.traffic-detail__transaction-count')).toHaveText('1 command')
  const transactionOverview = detail.getByRole('region', { name: 'Exchange overview' })
  await expect(transactionOverview).toContainText('ENVIRONMENT')
  await expect(transactionOverview).toContainText('TARGET BINDING')
  await expect(transactionOverview).toContainText('STARTED')
  await expect(transactionOverview).toContainText('COMPLETED')
  const transactionCommandTab = detail.getByRole('tab', { name: 'COMMAND', exact: true })
  const transactionResultTab = detail.getByRole('tab', { name: 'RESULT', exact: true })
  await expect(transactionCommandTab).toHaveAttribute('aria-selected', 'true')
  await expect(detail.getByRole('tablist', { name: 'Exchange payload' }).getByRole('tab')).toHaveCount(2)
  await expect(detail.getByRole('tab', { name: 'TCP DETAILS' })).toHaveCount(0)
  const transactionCommand = detail.getByRole('region', { name: 'Command', exact: true })
  await expect(transactionCommand).toContainText("UPDATE store_inventory SET on_hand = on_hand - 1 WHERE sku = 'coffee-mug'")
  await expect(transactionCommand.getByRole('region', { name: 'Bound parameters' })).toHaveCount(0)
  await expect(transactionCommand).not.toContainText('$1')
  await expect(transactionCommand).not.toContainText('$2')
  await expect(transactionCommand).not.toContainText('UPDATE 1')
  await expect(transactionCommand).not.toContainText('BEGIN')
  await expect(transactionCommand).not.toContainText('COMMIT')
  await transactionResultTab.click()
  const transactionResult = detail.getByRole('region', { name: 'Result', exact: true })
  await expect(transactionResult).toContainText('UPDATE 1')
  await expect(transactionResult).not.toContainText('UPDATE store_inventory SET')
  await transactionCommandTab.click()
  const transactionNavigation = detail.getByRole('navigation', { name: 'Trace span navigation' })
  await expect(transactionNavigation.getByRole('button', { name: 'HTTP' })).toHaveAttribute('aria-pressed', 'true')
  await expect(transactionNavigation.locator('output')).toHaveAttribute('aria-label', 'Current span is outside HTTP navigation; 1 HTTP span available')
  await detail.getByRole('button', { name: 'Close traffic details' }).click()

  await standaloneOperation.click()
  detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail.locator('.traffic-detail__protocol-badge')).toHaveText('TCP')
  await expect(detail.locator('.traffic-detail__heading')).toContainText('SELECT')
  await expect(detail.getByRole('region', { name: 'Exchange overview' })).toBeVisible()
  const standaloneCommand = detail.getByRole('region', { name: 'Command', exact: true })
  await expect(standaloneCommand.getByText('COMMAND', { exact: true })).toBeVisible()
  await expect(standaloneCommand).toContainText('SELECT id, state, note FROM orders ORDER BY id')
  await detail.getByRole('tab', { name: 'RESULT', exact: true }).click()
  const standaloneResult = detail.getByRole('region', { name: 'Result', exact: true })
  await expect(standaloneResult.getByText('RESULT', { exact: true })).toBeVisible()
  await expect(standaloneResult).toContainText('12 rows · 3 columns')
  const resultRows = standaloneResult.getByRole('table', { name: 'Database result rows' })
  await expect(resultRows.getByRole('columnheader')).toHaveText(['id', 'state', 'note'])
  const databaseRows = resultRows.getByRole('row')
  await expect(databaseRows).toHaveCount(11)
  await expect(databaseRows.nth(1)).toContainText('42')
  await expect(databaseRows.nth(1)).toContainText('created')
  await expect(databaseRows.nth(1)).toContainText('NULL')
  await expect(databaseRows.nth(2)).toContainText('43')
  await expect(databaseRows.nth(2)).toContainText('paid')
  await expect(databaseRows.nth(2)).toContainText('priority')
  const resultPagination = standaloneResult.getByLabel('database result rows pagination')
  await expect(resultPagination).toContainText('1–10 of 12')
  await resultPagination.getByRole('button', { name: 'Next database result rows page' }).click()
  await expect(resultPagination).toContainText('11–12 of 12')
  await expect(databaseRows).toHaveCount(3)
  await expect(databaseRows.nth(1)).toContainText('52')
  await expect(databaseRows.nth(2)).toContainText('53')
  await expect(resultPagination.getByRole('button', { name: 'Next database result rows page' })).toBeDisabled()
  await expect(standaloneResult.locator('.traffic-json')).toHaveCount(0)
  await expect(standaloneResult.locator('.traffic-semantic-card__body--table')).toHaveCSS('padding', '0px')
  await expect(resultRows.locator('..')).toHaveCSS('border-top-width', '0px')
  await expect(databaseRows.last().locator('td').first()).toHaveCSS('border-bottom-width', '1px')
  const copyCSV = standaloneResult.getByRole('button', { name: 'Copy database results as CSV' })
  await expect(copyCSV).toHaveText('COPY')
  await copyCSV.click()
  const expectedCSV = ['id,state,note', ...selectRows.map((row) => `${row.id},${row.state},${row.note || ''}`)].join('\n')
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(expectedCSV)

  await detail.getByRole('button', { name: 'Close traffic details' }).click()
  await redisOperation.click()
  detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail.locator('.traffic-detail__heading h3 > span')).toHaveText('REDIS')
  await expect(detail.locator('.traffic-detail__heading h3 code')).toHaveText('GET')
  const redisCommand = detail.getByRole('region', { name: 'Command', exact: true })
  await expect(redisCommand.locator('.traffic-redis-command')).toHaveText('GET store:order:56')
  await expect(redisCommand).toContainText('1 argument')
  await expect(redisCommand).not.toContainText('[\n  "GET"')
  await expect(redisCommand.locator('dl')).toHaveCount(0)
  await detail.getByRole('tab', { name: 'RESULT', exact: true }).click()
  const redisResult = detail.getByRole('region', { name: 'Result', exact: true })
  await expect(redisResult.getByText('string', { exact: true })).toBeVisible()
  await expect(redisResult.locator('.traffic-json')).toBeVisible()
  await expect(redisResult).toContainText('coffee-mug')
  await expect(redisResult).not.toContainText('\\"id\\"')
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
  await expect(traceRows.first().locator('span').first()).toHaveText(/\d{1,2}:\d{2}:\d{2}\.\d{3}/)
  await expect(traceRows.first().locator('span').first()).not.toContainText(/#\d+/)
  await expect(tracePagination).toContainText('1–25 of 26')
  await traceRows.first().click()
  const trace = page.locator('.trace-waterfall').first()
  await expect(trace).toBeVisible()
  await trace.getByRole('button', { name: /Maximize trace/ }).click()
  await expect(trace).toHaveClass(/trace-waterfall--maximized/)
  await expect(trace.locator('.panel-title')).toContainText('TRACE WATERFALL')
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
  await exchangeRows.last().click()
  const exchangeDetail = page.getByRole('dialog', { name: /Traffic request and response/ })
  const exchangeDetailPagination = exchangeDetail.getByRole('navigation', { name: 'Exchange navigation' })
  await expect(exchangeDetailPagination.locator('output')).toHaveAttribute('aria-label', 'Exchange 25 of 26')
  await exchangeDetailPagination.getByRole('button', { name: 'Next exchange' }).click()
  await expect(exchangeDetailPagination.locator('output')).toHaveAttribute('aria-label', 'Exchange 26 of 26')
  await expect(exchangeDetailPagination.getByRole('button', { name: 'Next exchange' })).toBeDisabled()
  await expect(exchangePagination).toContainText('26–26 of 26')
  await exchangeDetailPagination.getByRole('button', { name: 'Previous exchange' }).click()
  await expect(exchangeDetailPagination.locator('output')).toHaveAttribute('aria-label', 'Exchange 25 of 26')
  await expect(exchangePagination).toContainText('1–25 of 26')
  await exchangeDetail.getByRole('button', { name: 'Close traffic details' }).click()
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
  await expect.poll(async () => {
    const snapshot = await controlAPI<{ traces: Array<{ protocol: string; provisional: boolean }> }>('/api/v1/environments/ui-e2e/local/traffic/traces?background=include&limit=100')
    return snapshot.traces.some((trace) => trace.protocol === 'http' && !trace.provisional)
  }).toBe(true)

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
