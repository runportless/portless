import { expect, test } from '@playwright/test'
import { authenticate, controlAPI, environmentHeader, environmentPath, openCommandPalette } from './helpers'
import { readE2EState } from './state'

test('keeps every destination and primary action reachable on a narrow screen', async ({ page }) => {
  const state = readE2EState()
  await page.setViewportSize({ width: 390, height: 844 })
  await authenticate(page)
  const header = environmentHeader(page)
  const navigation = header.getByRole('button', { name: 'Open navigation', exact: true })
  const overlay = page.getByRole('dialog', { name: 'Navigation', exact: true })
  await expect(page.locator('.sidebar')).toHaveAttribute('inert')

  for (const view of ['Overview', 'Topology', 'Traffic', 'Mocks', 'Recordings', 'Faults', 'Bindings', 'Timeline']) {
    await navigation.click()
    await expect(overlay).toBeVisible()
    await overlay.getByRole('button', { name: view, exact: true }).click()
    await expect(overlay).toHaveCount(0)
    await expect(header.getByRole('heading', { name: view, exact: true })).toBeFocused()
    await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible()
    await expect(header.getByRole('link', { name: 'OPEN APP' })).toBeVisible()
    await expect(header.getByRole('link', { name: 'OPEN APP' })).toHaveText('Open ↗')
    await expect(header.getByRole('link', { name: 'OPEN APP' })).toHaveCSS('border-top-width', '1px')
    await expect(header.getByRole('link', { name: 'OPEN APP' })).toHaveCSS('width', '100px')
    await expect(header.getByRole('button', { name: 'STOP ALL' })).toHaveCount(0)
    await expect(header.getByRole('button', { name: `Environment actions for ${state.project}/${state.environment}` })).toHaveCount(0)
    const search = header.getByRole('button', { name: 'Search', exact: true })
    await expect(search.locator('em')).toBeVisible()
    const palette = await openCommandPalette(page, 'Stop environment')
    await expect(palette.getByRole('button', { name: /Stop environment/ })).toBeEnabled()
    await expect(palette.getByRole('textbox', { name: 'Search', exact: true })).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(search).toBeFocused()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)
    if (view === 'Topology') {
      const topology = page.locator('.topology-panel--page')
      expect(Math.round((await topology.boundingBox())!.y + (await topology.boundingBox())!.height)).toBe(830)
      await topology.getByRole('button', { name: 'Maximize topology' }).click()
      await expect(topology).toHaveClass(/topology-panel--maximized/)
      expect(await topology.boundingBox()).toMatchObject({ x: 0, y: 0, width: 390, height: 844 })
      await page.keyboard.press('Escape')
      await expect(topology).not.toHaveClass(/topology-panel--maximized/)
    }
  }

  await navigation.click()
  await overlay.getByRole('button', { name: `Create environment in ${state.project}` }).click()
  const create = page.getByRole('dialog', { name: 'Create Environment', exact: true })
  await expect(create.getByLabel('NAME', { exact: true })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(create).toHaveCount(0)
  await expect(overlay).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(navigation).toBeFocused()

  await page.setViewportSize({ width: 1280, height: 844 })
  await page.getByRole('button', { name: 'Collapse navigation' }).click()
  await page.setViewportSize({ width: 760, height: 844 })
  await expect(navigation).toBeVisible()
  await page.setViewportSize({ width: 1280, height: 844 })
  await expect(page.locator('.sidebar')).toHaveCSS('width', '64px')
})

test('sizes workspaces below a wrapping header with long names in both themes', async ({ page }) => {
  const state = readE2EState()
  const name = 'qa-assisted-preview-with-a-long-environment-name'
  await controlAPI('/api/v1/environments', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ project: state.project, name, from: state.environment }) })
  try {
    await authenticate(page, `/environments/${state.project}/${name}?tab=topology`)
    const header = environmentHeader(page, state.project, name)
    for (const theme of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: theme as 'light' | 'dark' })
      for (const width of [1440, 1024, 768, 760, 390, 320]) {
        await page.setViewportSize({ width, height: 640 })
        await expect(header.getByRole('heading', { name: 'Topology', exact: true })).toBeVisible()
        await expect(header.getByRole('link', { name: /health: stopped/ })).toBeVisible()
        await expect(header.locator('.environment-clone-origin')).toHaveCount(0)
        await expect(header.getByRole('button', { name: 'Start', exact: true })).toBeVisible()
        await expect(header.getByRole('button', { name: 'Start', exact: true })).toHaveCSS('width', '100px')
        await expect(header.getByRole('link', { name: 'OPEN APP', exact: true })).toHaveCount(0)
        await expect(header.getByRole('button', { name: 'Search', exact: true }).locator('em')).toBeVisible()
        await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
        const panel = page.locator('.topology-panel--page')
        await expect.poll(async () => Math.round((await panel.boundingBox())!.y + (await panel.boundingBox())!.height)).toBe(640 - (width <= 760 ? 14 : 28))
        expect((await panel.boundingBox())!.height).toBeGreaterThan(300)
      }
    }
    await header.getByRole('link', { name: /health: stopped/ }).click()
    const summary = page.getByRole('region', { name: `${state.project}/${name} overview summary` })
    await expect(summary.getByRole('heading', { name, level: 2, exact: true })).toBeVisible()
    await expect(summary.locator('.environment-clone-origin')).toHaveText(`FROM ${state.environment}`)
    await expect(summary.locator('.environment-clone-origin')).toBeVisible()
    await expect(summary.locator('.environment-clone-origin')).toHaveAttribute('title', `Created by cloning ${state.project}/${state.environment}; changes are independent.`)
    await expect(header.locator('.environment-clone-origin')).toHaveCount(0)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(320)
    await page.keyboard.press('Control+Shift+F')
    await expect(summary.getByRole('heading', { name, level: 2, exact: true })).toBeVisible()
    await expect(summary.locator('.environment-clone-origin')).toBeVisible()
    await expect(header.locator('.environment-clone-origin')).toHaveCount(0)
    await expect(header.getByRole('button', { name: 'Search', exact: true }).locator('em')).toBeVisible()
    await expect(header.getByRole('button', { name: 'Start', exact: true })).toHaveCSS('width', '100px')
  } finally {
    await controlAPI(`/api/v1/environments/${state.project}/${name}`, { method: 'DELETE' })
  }
})

test('shares a single activity subscription across views and discards a late previous-environment snapshot', async ({ page }) => {
  const state = readE2EState()
  let streams = 0
  let recordings = 0
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname === `/api/v1/environments/${state.project}/${state.environment}/stream` && url.searchParams.getAll('topic').includes('recording.state')) streams++
    if (url.pathname === `/api/v1/environments/${state.project}/${state.environment}/recordings`) recordings++
  })
  let release!: () => void
  let finish!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  const finished = new Promise<void>((resolve) => { finish = resolve })
  await page.route(`**/api/v1/environments/${state.project}/${state.environment}/recordings`, async (route) => {
    await gate
    try {
      if (!route.request().failure()) await route.fulfill({ json: { recordings: [{ project: state.project, environment: state.environment, name: 'previous-environment-recording', status: 'active', eventCount: 0, maxEvents: 100 }] } })
    } catch (error) {
      // Switching scope can abort the browser request before Playwright fulfills it.
      if (!String(error).includes('Route is already handled!')) throw error
    } finally { finish() }
  })
  try {
    await authenticate(page, environmentPath('timeline'))
    await expect.poll(() => streams).toBe(1)
    await expect.poll(() => recordings).toBe(1)
    const views = page.getByRole('navigation', { name: `${state.project}/${state.environment} views` })
    await views.getByRole('button', { name: 'Faults', exact: true }).click()
    await views.getByRole('button', { name: 'Recordings', exact: true }).click()
    await views.getByRole('button', { name: 'Timeline', exact: true }).click()
    expect(streams).toBe(1)
    expect(recordings).toBe(1)

    await page.keyboard.press('Control+K')
    const palette = page.getByRole('dialog', { name: 'Command palette' })
    await palette.getByPlaceholder('Search').fill(`${state.debugProject}/${state.environment}`)
    await palette.getByRole('button', { name: new RegExp(`${state.debugProject}/${state.environment}`) }).click()
    await expect(environmentHeader(page, state.debugProject)).toBeVisible()
    release()
    await finished
    await expect(environmentHeader(page, state.debugProject).getByRole('link', { name: /Recording/ })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Recordings', exact: true })).not.toHaveAttribute('aria-describedby')
    await expect(page.locator('.environment-notices')).toHaveCount(0)
  } finally {
    release()
    await page.unrouteAll({ behavior: 'ignoreErrors' })
  }
})

test('shares the pending lifecycle action between the header and command palette', async ({ page }) => {
  const state = readE2EState()
  const apiPath = `/api/v1/environments/${state.project}/${state.environment}`
  await authenticate(page)
  const header = environmentHeader(page)
  const search = header.getByRole('button', { name: 'Search', exact: true })
  let requests = 0
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  await page.route(`**${apiPath}/down`, async (route) => {
    requests++
    expect(route.request().postDataJSON()).toEqual({ removeVolumes: false })
    const response = await route.fetch()
    await gate
    await route.fulfill({ response })
  })
  try {
    const stop = (await openCommandPalette(page, 'Stop environment')).getByRole('button', { name: /Stop environment/ })
    const searchBounds = await search.boundingBox()
    await stop.evaluate((button: HTMLButtonElement) => { button.click(); button.click() })
    await expect(search).toBeEnabled()
    await expect(header.getByRole('status')).toHaveText('Stopping…')
    await expect(header.locator('.environment-lifecycle')).toHaveCount(0)
    expect(await search.boundingBox()).toEqual(searchBounds)
    await expect(header.getByRole('menu')).toHaveCount(0)
    await expect(page.getByRole('dialog', { name: 'Command palette' })).toHaveCount(0)
    await expect.poll(() => requests).toBe(1)
    await page.getByRole('navigation', { name: `${state.project}/${state.environment} views` }).getByRole('button', { name: 'Topology', exact: true }).click()
    await expect(search).toBeEnabled()
    await expect(header.getByRole('status')).toHaveText('Stopping…')
    await expect(header.locator('.environment-lifecycle')).toHaveCount(0)
    await page.keyboard.press('Control+K')
    const palette = page.getByRole('dialog', { name: 'Command palette' })
    await palette.getByPlaceholder('Search').fill('environment')
    await expect(palette.getByRole('button', { name: /^(Start|Stop) environment/ })).toHaveCount(0)
    await page.keyboard.press('Escape')
    release()
    await expect(environmentHeader(page).getByRole('button', { name: 'Start', exact: true })).toBeEnabled({ timeout: 30_000 })
    expect(requests).toBe(1)
    await page.keyboard.press('Control+K')
    await palette.getByPlaceholder('Search').fill('Start environment')
    await palette.getByRole('button', { name: /Start environment/ }).click()
    await expect(header.getByRole('link', { name: /health: healthy/ })).toBeVisible({ timeout: 30_000 })
    await expect(search).toBeEnabled()
    await expect(header.locator('.environment-lifecycle')).toHaveCount(0)
    await page.unroute(`**${apiPath}/down`)
    await page.route(`**${apiPath}/down`, (route) => route.fulfill({ status: 409, json: { error: { code: 'ENVIRONMENT_BUSY', message: 'The environment is busy with another operation.' } } }))
    await page.keyboard.press('Control+K')
    await palette.getByPlaceholder('Search').fill('Stop environment')
    await palette.getByRole('button', { name: /Stop environment/ }).click()
    const error = page.locator('.environment-notices .action-error')
    await expect(error).toContainText('The environment is busy with another operation.')
    await page.getByRole('navigation', { name: `${state.project}/${state.environment} views` }).getByRole('button', { name: 'Timeline', exact: true }).click()
    await expect(error).toContainText('ENVIRONMENT_BUSY')
    await expect((await openCommandPalette(page, 'Stop environment')).getByRole('button', { name: /Stop environment/ })).toBeEnabled()
    await page.keyboard.press('Escape')
  } finally {
    release()
    await page.unroute(`**${apiPath}/down`)
    const snapshot = await controlAPI<{ status: string }>(apiPath)
    if (snapshot.status === 'stopped') await controlAPI(`${apiPath}/up`, { method: 'POST' })
  }
})
