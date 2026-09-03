import { expect, test } from '@playwright/test'
import { authenticate, environmentHeader, environmentPath, issueBrowserClaim } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('requires and consumes a one-use browser claim', async ({ page, request }) => {
  const state = readE2EState()
  await page.goto(`${state.baseURL}/projects`)
  await expect(page.getByRole('heading', { name: 'Open this control plane from the CLI.' })).toBeVisible()

  const claim = await issueBrowserClaim()
  await page.goto(claim)
  await expect(page).toHaveURL(new RegExp(`${environmentPath()}$`))
  await expect(environmentHeader(page).getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()

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
  await expect(page.getByRole('button', { name: 'CREATE ENVIRONMENT', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'ADD SOURCE' })).toBeVisible()

  await breadcrumbs.getByRole('link', { name: 'projects' }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: `Current project ${state.project}. Switch project` })).toBeVisible()
  await expect(page.getByRole('navigation', { name: `${state.project} environments` }).getByRole('button', { name: new RegExp(`${state.project}/${state.environment}`) })).toBeVisible()
  await expect(page.getByRole('button', { name: 'CREATE ENVIRONMENT', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'ADD SOURCE' })).toHaveCount(0)
})

test('keeps scrollable pages within the standard bottom gutter', async ({ page }) => {
  const state = readE2EState()
  await page.setViewportSize({ width: 1280, height: 400 })
  await authenticate(page)

  const expectBottomGutter = async (lastContent: string, scrollable = false) => {
    await expect(page.locator(lastContent)).toBeVisible()
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
    if (scrollable) await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0)
    const gap = await page.locator('.page').evaluate((element) => {
      const last = element.lastElementChild
      if (!last) return -1
      return Math.round(element.getBoundingClientRect().bottom - last.getBoundingClientRect().bottom)
    })
    expect(gap).toBe(28)
  }

  await expectBottomGutter('.overview-grid', true)
  await page.goto(`${state.baseURL}${environmentPath('timeline')}`)
  await expectBottomGutter('.timeline-panel')
})

test('keeps the persistent header and panel title bars consistent across views', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  const titles = [
    ['overview', '.services-panel > .panel-title'],
    ['topology', '.topology-panel--page > .panel-title'],
    ['traffic', '.traffic-header'],
    ['mocks', '.mock-scenarios-panel > .panel-title'],
    ['recordings', '.recording-control-panel > .panel-title'],
    ['faults', '.faults-panel-title'],
    ['bindings', '.configured-providers-panel > .panel-title'],
    ['timeline', '.timeline-panel > .panel-title'],
  ] as const

  for (const [tab, selector] of titles) {
    if (tab !== 'overview') await page.goto(`${state.baseURL}${environmentPath(tab)}`)
    const header = environmentHeader(page)
    await expect(header.getByRole('heading', { name: tab[0].toUpperCase() + tab.slice(1), exact: true })).toBeVisible()
    await expect(header.getByRole('link', { name: /health: healthy; 3\/3 ready/ })).toBeVisible()
    await expect(header.getByRole('link', { name: 'OPEN APP' })).toHaveAttribute('href', `http://${state.applicationHost}`)
    await expect(header.getByRole('button', { name: 'STOP ALL' })).toBeEnabled()
    await expect(page.getByRole('navigation', { name: 'Environment views', exact: true })).toHaveCount(0)
    const title = page.locator(selector)
    await expect(title).toBeVisible()
    expect(await title.evaluate((element) => Math.round(element.getBoundingClientRect().height)), `${tab} title bar height`).toBe(48)
  }
})

test('toggles persistent focus mode from the keyboard and command palette', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  const shell = page.locator('.shell')
  const stage = page.locator('.stage')
  const sidebar = page.locator('.sidebar')
  const header = environmentHeader(page)
  const navigation = header.getByRole('button', { name: 'Open navigation' })
  const normalStageLeft = await stage.evaluate((element) => Math.round(element.getBoundingClientRect().left))
  expect(normalStageLeft).toBeGreaterThan(0)

  await page.keyboard.press('Control+Shift+F')
  await expect(shell).toHaveClass(/shell--focus-mode/)
  await expect(header.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
  await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible()
  await expect(header.getByRole('link', { name: 'OPEN APP' })).toBeVisible()
  await expect(header.getByRole('button', { name: 'STOP ALL' })).toBeEnabled()
  await expect(navigation).toHaveCount(0)
  await expect.poll(() => stage.evaluate((element) => Math.round(element.getBoundingClientRect().left))).toBe(0)
  await expect.poll(() => sidebar.evaluate((element) => Math.round(element.getBoundingClientRect().right))).toBeLessThanOrEqual(0)
  await expect.poll(() => page.evaluate(() => localStorage.getItem('portless.focus-mode'))).toBe('true')

  const edge = page.getByRole('button', { name: 'Reveal navigation' })
  await edge.hover()
  await expect(shell).toHaveClass(/shell--navigation-open/)
  await expect(sidebar).not.toHaveAttribute('aria-modal')
  await expect.poll(() => sidebar.evaluate((element) => Math.round(element.getBoundingClientRect().left))).toBe(0)
  expect(await stage.evaluate((element) => Math.round(element.getBoundingClientRect().left))).toBe(0)
  await page.mouse.move(900, 400)
  await expect(shell).not.toHaveClass(/shell--navigation-open/)

  await edge.focus()
  await expect(shell).not.toHaveClass(/shell--navigation-open/)
  await page.keyboard.press('Enter')
  const overlay = page.getByRole('dialog', { name: 'Navigation', exact: true })
  await expect(overlay).toBeVisible()
  await expect.poll(() => overlay.evaluate((element) => element.contains(document.activeElement) ? 'navigation' : document.activeElement?.tagName)).toBe('navigation')
  await page.mouse.move(900, 400)
  await page.waitForTimeout(400)
  await expect(overlay).toBeVisible()
  await overlay.getByRole('button', { name: `Current project ${state.project}. Switch project` }).click()
  const switcher = page.getByRole('dialog', { name: 'Switch project', exact: true })
  await expect(switcher.getByRole('combobox', { name: 'Search projects' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(switcher).toHaveCount(0)
  await expect(overlay).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(overlay).toHaveCount(0)
  await expect(edge).toBeFocused()
  await expect(shell).not.toHaveClass(/shell--navigation-open/)

  await page.keyboard.press('Space')
  await overlay.getByRole('button', { name: 'Traffic', exact: true }).click()
  await expect(overlay).toHaveCount(0)
  await expect(header.getByRole('heading', { name: 'Traffic', exact: true })).toBeFocused()

  await page.reload()
  await expect(shell).toHaveClass(/shell--focus-mode/)
  await expect(header.getByRole('heading', { name: 'Traffic', exact: true })).toBeVisible()
  await expect(navigation).toHaveCount(0)

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(navigation).toHaveCount(0)
  await edge.focus()
  await page.keyboard.press('Enter')
  await expect(overlay).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(overlay).toHaveCount(0)
  await expect(edge).toBeFocused()
  await expect(shell).not.toHaveClass(/shell--navigation-open/)

  await page.keyboard.press('Control+K')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  await palette.getByPlaceholder('jump to a project or environment').fill('Exit focus mode')
  await palette.getByRole('button', { name: /Exit focus mode/ }).click()
  await expect(shell).not.toHaveClass(/shell--focus-mode/)
  await expect(header).toBeVisible()
  await expect(navigation).toBeVisible()
  await expect.poll(() => page.evaluate(() => localStorage.getItem('portless.focus-mode'))).toBe('false')

  await page.keyboard.press('Control+K')
  await palette.getByPlaceholder('jump to a project or environment').fill('Enter focus mode')
  await palette.getByRole('button', { name: /Enter focus mode/ }).click()
  await expect(shell).toHaveClass(/shell--focus-mode/)
  await expect(navigation).toHaveCount(0)
  await page.keyboard.press('Control+Shift+F')
  await expect(shell).not.toHaveClass(/shell--focus-mode/)
})

test('collapses the sidebar into a persistent icon navigation rail', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  const shell = page.locator('.shell')
  const sidebar = page.locator('.sidebar')
  const projectNavigation = page.getByRole('navigation', { name: `${state.project} environments` })
  const environmentViews = page.getByRole('navigation', { name: `${state.project}/${state.environment} views` })
  await page.getByRole('button', { name: 'Collapse navigation' }).click()

  await expect(shell).toHaveClass(/shell--sidebar-collapsed/)
  await expect(sidebar).toHaveCSS('width', '64px')
  await expect(page.locator('.brand__wordmark')).toBeHidden()
  const project = page.getByRole('button', { name: `Current project ${state.project}. Switch project` })
  const environment = projectNavigation.getByRole('button', { name: new RegExp(`${state.project}/${state.environment}`) })
  const topology = environmentViews.getByRole('button', { name: 'Topology' })
  for (const item of [project, environment, topology, page.getByRole('button', { name: 'Settings' })]) {
    await expect(item).toBeVisible()
    await expect(item.locator('svg').first()).toBeVisible()
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

  const orders = canvas.locator('.topology-node[data-service="orders"]')
  await orders.focus()
  const preview = canvas.getByRole('tooltip')
  await expect(preview).toBeVisible()
  await expect(preview).toContainText('orders')
  const previewInsets = await canvas.evaluate((element) => {
    const card = element.querySelector('.topology-service-preview')
    if (!(card instanceof HTMLElement)) return null
    const viewportBounds = element.getBoundingClientRect()
    const cardBounds = card.getBoundingClientRect()
    return {
      left: Math.round(cardBounds.left-viewportBounds.left),
      right: Math.round(viewportBounds.right-cardBounds.right),
      top: Math.round(cardBounds.top-viewportBounds.top),
      bottom: Math.round(viewportBounds.bottom-cardBounds.bottom),
    }
  })
  expect(previewInsets).not.toBeNull()
  for (const [edgeName, inset] of Object.entries(previewInsets!)) {
    expect(inset, `${edgeName} preview inset`).toBeGreaterThanOrEqual(9)
  }

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
  await expect(page.locator('.runtime-candidate')).toHaveCount(2)

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
