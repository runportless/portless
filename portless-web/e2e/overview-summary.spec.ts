import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { authenticate, controlAPI, environmentHeader, environmentPath } from './helpers'
import { readE2EState } from './state'

test('keeps live activity in the header without duplicate Overview controls', async ({ page }, testInfo) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const recording = 'overview-capture-with-a-long-descriptive-name'
  const fault = 'overview-orders-delay'
  const scenario = 'overview-mock-with-a-long-descriptive-name'
  const scenarioBase = `${base}/mocks/${scenario}`
  let scenarioCreated = false
  let mockEnabled = false
  let recordingCreated = false
  let recordingActive = false
  let faultCreated = false
  const setMockEnabled = async (enabled: boolean) => {
    const operation = await controlAPI<{ number: number }>(`${scenarioBase}/activation`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() },
      body: JSON.stringify({ enabled }),
    })
    mockEnabled = enabled
    await expect.poll(async () => (await controlAPI<{ state: string }>(`${base}/operations/${operation.number}`)).state, { timeout: 30_000 }).toBe('succeeded')
  }

  try {
    await authenticate(page)
    const header = environmentHeader(page)
    const headerIcons = header.locator('.environment-activity-indicators > a')
    const summary = page.getByRole('region', { name: `${state.project}/${state.environment} overview summary` })
    const mocksNavigation = page.getByRole('button', { name: 'Mocks', exact: true, includeHidden: true })
    const mocksCount = mocksNavigation.locator('.view-nav__count')
    const recordingsNavigation = page.getByRole('button', { name: 'Recordings', exact: true, includeHidden: true })
    const recordingsCount = recordingsNavigation.locator('.view-nav__count')
    await expect(summary.getByRole('heading', { name: state.environment, level: 2, exact: true })).toBeVisible()
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(headerIcons).toHaveCount(0)
    await expect(mocksCount).toHaveCount(0)
    await expect(recordingsCount).toHaveCount(0)
    await expect(summary.locator('.environment-clone-origin')).toHaveCount(0)
    const initialHeadingBounds = await summary.boundingBox()

    await controlAPI(`${base}/mocks`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: scenario }) })
    scenarioCreated = true
    for (const service of ['inventory', 'orders']) {
      await controlAPI(`${scenarioBase}/routes/${service}-health`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: `${service}-health`, service, method: 'GET', path: '/health', status: 200, body: '{}', delayMs: 0, enabled: true }),
      })
    }
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(headerIcons).toHaveCount(0)
    await expect(mocksCount).toHaveCount(0)
    await setMockEnabled(true)
    await expect(mocksCount).toBeVisible()
    await expect(mocksCount).toHaveText('1')
    await expect(mocksNavigation).toHaveAccessibleDescription('1 active mock')
    await controlAPI(`${base}/recordings`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: recording, source: 'checkout', target: 'orders', capturePayloads: false, maxEvents: 10000, maxPayloadBytes: 65536 }),
    })
    recordingCreated = true
    recordingActive = true
    await expect(recordingsCount).toBeVisible()
    await expect(recordingsCount).toHaveText('1')
    await expect(recordingsNavigation).toHaveAccessibleDescription('1 active recording')
    await controlAPI(`${base}/faults`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: fault, source: 'checkout', target: 'orders', probability: 1, latencyMs: 5 }),
    })
    faultCreated = true

    const recordingIcon = header.getByRole('link', { name: `Recording ${recording}. Open recordings`, exact: true })
    const faultIcon = header.getByRole('link', { name: '1 active fault. Open faults', exact: true })
    const mockIcon = header.getByRole('link', { name: `Active mock scenario ${scenario}. Open mocks`, exact: true })
    const icons = [recordingIcon, faultIcon, mockIcon]
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(summary.getByRole('button')).toHaveCount(0)
    await expect.poll(() => summary.boundingBox()).toEqual(initialHeadingBounds)
    await expect(headerIcons).toHaveCount(3)
    await expect(mockIcon).toHaveAttribute('href', environmentPath('mocks'))
    await expect(recordingIcon).toHaveAttribute('title', 'Recording')
    await expect(faultIcon).toHaveAttribute('title', '1 Active Fault')
    await expect(mockIcon).toHaveAttribute('title', '1 Active Mock')
    const expectAlignedServiceText = async () => {
      const textCells = page.locator('.services-panel .service-row--interactive .service-list-endpoint > .truncate')
      await expect(textCells).toHaveCount(3)
      await expect(textCells.filter({ hasText: `mock scenario ${scenario}` })).toHaveCount(2)
      await expect(page.getByRole('button', { name: 'Copy checkout endpoint', exact: true })).toBeVisible()
      const positions = await textCells.evaluateAll((cells) => cells.map((cell) => {
        const textBounds = cell.getBoundingClientRect()
        const row = cell.closest('.service-row')!
        const rowBounds = row.getBoundingClientRect()
        return { left: textBounds.x, centerOffset: textBounds.y + textBounds.height / 2 - rowBounds.y - row.clientTop - row.clientHeight / 2 }
      }))
      for (const position of positions) {
        expect(position.left).toBeCloseTo(positions[0].left, 1)
        expect(position.centerOffset).toBeCloseTo(0, 1)
      }
    }
    const themeColors: string[][] = []
    for (const theme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      for (const width of [1280, 390, 320]) {
        await page.setViewportSize({ width, height: 844 })
        await expect(summary.getByRole('link')).toHaveCount(0)
        await expect(summary.getByRole('button')).toHaveCount(0)
        await expect(header.getByRole('button', { name: 'Search', exact: true }).locator('em')).toBeVisible()
        await expect(header.getByRole('link', { name: 'OPEN APP' })).toHaveCSS('border-top-width', '1px')
        for (const icon of icons) {
          await expect(icon).toBeVisible()
          await expect(icon).toHaveText('')
          await expect(icon.locator('svg')).toHaveAttribute('aria-hidden', 'true')
          await expect(icon).toHaveCSS('border-top-width', '0px')
          expect(await icon.boundingBox()).toMatchObject({ width: 32, height: 32 })
        }
        await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
        if (width === 1280) {
          await expectAlignedServiceText()
          await header.screenshot({ path: testInfo.outputPath(`header-activity-${theme}.png`), animations: 'disabled' })
          await page.screenshot({ path: testInfo.outputPath(`overview-${theme}.png`), animations: 'disabled' })
        }
        if (width === 320) await header.screenshot({ path: testInfo.outputPath(`header-narrow-${theme}.png`), animations: 'disabled' })
      }
      const colors = await Promise.all(icons.map((icon) => icon.evaluate((element) => getComputedStyle(element).color)))
      expect(new Set(colors).size).toBe(3)
      themeColors.push(colors)
      await summary.screenshot({ path: testInfo.outputPath(`overview-summary-${theme}.png`), animations: 'disabled' })
    }
    expect(themeColors[0]).not.toEqual(themeColors[1])
    await page.keyboard.press('Control+Shift+F')
    await expect(page.locator('.shell')).toHaveClass(/shell--focus-mode/)
    await expect(summary).toBeVisible()
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(summary.getByRole('button')).toHaveCount(0)
    await page.setViewportSize({ width: 1280, height: 844 })
    await expectAlignedServiceText()
    await page.setViewportSize({ width: 320, height: 844 })
    await header.getByRole('link', { name: /health: healthy/ }).focus()
    for (const icon of icons) {
      await page.keyboard.press('Tab')
      await expect(icon).toBeFocused()
      await expect(icon).toHaveCSS('outline-style', 'solid')
    }

    for (const [index, view] of ['Recordings', 'Faults', 'Mocks'].entries()) {
      await icons[index].focus()
      await icons[index].press('Enter')
      await expect(header.getByRole('heading', { name: view, exact: true })).toBeVisible()
      await expect(headerIcons).toHaveCount(3)
      await expect(summary).toHaveCount(0)
      if (view === 'Recordings') await expect(page.locator('.recording-active-control')).toContainText(recording)
      if (view === 'Faults') await expect(page.getByRole('button', { name: `Fault actions for ${fault}`, exact: true })).toBeVisible()
      if (view === 'Mocks') {
        await expect(page).toHaveURL(/\?tab=mocks$/)
        await expect(page.getByRole('button', { name: `Open ${scenario} mock scenario`, exact: true })).toBeVisible()
      }
      await page.goBack()
    }

    await controlAPI(`${base}/recordings/${recording}/stop`, { method: 'POST' })
    recordingActive = false
    await expect(recordingsCount).toHaveCount(0)
    await controlAPI(`${base}/faults/${fault}/disable`, { method: 'POST' })
    await setMockEnabled(false)
    await expect(mocksCount).toHaveCount(0)
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(headerIcons).toHaveCount(0)
    await expect(summary.getByRole('heading', { name: state.environment, level: 2, exact: true })).toBeVisible()
    await page.reload()
    await expect(mocksCount).toHaveCount(0)
    await expect(recordingsCount).toHaveCount(0)
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(headerIcons).toHaveCount(0)
    await expect(page.locator('.shell')).toHaveClass(/shell--focus-mode/)
  } finally {
    if (recordingActive) await controlAPI(`${base}/recordings/${recording}/stop`, { method: 'POST' })
    if (recordingCreated) await controlAPI(`${base}/recordings/${recording}`, { method: 'DELETE' })
    if (faultCreated) await controlAPI(`${base}/faults/${fault}`, { method: 'DELETE' })
    if (mockEnabled) await setMockEnabled(false)
    if (scenarioCreated) await controlAPI(scenarioBase, { method: 'DELETE' })
  }
})
