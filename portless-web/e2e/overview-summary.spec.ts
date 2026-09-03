import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { authenticate, controlAPI, environmentHeader, environmentPath } from './helpers'
import { readE2EState } from './state'

test('shows live Overview badges and compact header icons for recordings, faults, and mocks', async ({ page }, testInfo) => {
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
    const headerIcons = header.locator('.environment-activity-indicators--icons > a')
    const summary = page.getByRole('region', { name: `${state.project}/${state.environment} overview summary` })
    await expect(summary.getByRole('heading', { name: state.environment, level: 2, exact: true })).toBeVisible()
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(headerIcons).toHaveCount(0)
    await expect(summary.locator('.environment-clone-origin')).toHaveCount(0)

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
    await setMockEnabled(true)
    await controlAPI(`${base}/recordings`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: recording, source: 'checkout', target: 'orders', capturePayloads: false, maxEvents: 10000, maxPayloadBytes: 65536 }),
    })
    recordingCreated = true
    recordingActive = true
    await controlAPI(`${base}/faults`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: fault, source: 'checkout', target: 'orders', probability: 1, latencyMs: 5 }),
    })
    faultCreated = true

    const recordingLink = summary.getByRole('link', { name: `Recording ${recording}. Open recordings`, exact: true })
    const faultLink = summary.getByRole('link', { name: '1 active fault. Open faults', exact: true })
    const mockLink = summary.getByRole('link', { name: `Active mock scenario ${scenario}. Open scenario`, exact: true })
    const recordingIcon = header.getByRole('link', { name: `Recording ${recording}. Open recordings`, exact: true })
    const faultIcon = header.getByRole('link', { name: '1 active fault. Open faults', exact: true })
    const mockIcon = header.getByRole('link', { name: `Active mock scenario ${scenario}. Open scenario`, exact: true })
    const icons = [recordingIcon, faultIcon, mockIcon]
    await expect(summary.getByRole('link')).toHaveCount(3)
    await expect(headerIcons).toHaveCount(3)
    await expect(mockLink).toHaveAttribute('href', `${environmentPath('mocks')}&scenario=${scenario}`)
    await expect(recordingIcon).toHaveAttribute('title', `Recording ${recording}`)
    await expect(faultIcon).toHaveAttribute('title', '1 active fault')
    await expect(mockIcon).toHaveAttribute('title', `Active mock scenario: ${scenario}`)
    const themeColors: string[][] = []
    for (const theme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      for (const width of [1280, 390, 320]) {
        await page.setViewportSize({ width, height: 844 })
        for (const link of [recordingLink, faultLink, mockLink]) await expect(link).toBeVisible()
        for (const icon of icons) {
          await expect(icon).toBeVisible()
          await expect(icon).toHaveText('')
          await expect(icon.locator('svg')).toHaveAttribute('aria-hidden', 'true')
          await expect(icon).toHaveCSS('border-top-width', '0px')
          expect(await icon.boundingBox()).toMatchObject({ width: 32, height: 32 })
        }
        await expect(recordingLink.locator('.recording-indicator__name')).toBeVisible()
        await expect(mockLink.locator('.mock-indicator__name')).toBeVisible()
        await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
        if (width === 1280) await header.screenshot({ path: testInfo.outputPath(`header-activity-${theme}.png`), animations: 'disabled' })
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
    await expect(summary.getByRole('link')).toHaveCount(3)
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
      if (view === 'Mocks') await expect(page.getByRole('region', { name: `${scenario} mock scenario`, exact: true })).toBeVisible()
      await page.goBack()
    }

    await recordingLink.focus()
    await recordingLink.press('Enter')
    await expect(page).toHaveURL(new RegExp('tab=recordings$'))
    await expect(page.locator('.recording-active-control')).toContainText(recording)
    await expect(summary).toHaveCount(0)
    await page.goBack()
    await faultLink.click()
    await expect(page).toHaveURL(new RegExp('tab=faults$'))
    await expect(page.getByRole('button', { name: `Fault actions for ${fault}`, exact: true })).toBeVisible()
    await page.goBack()
    await mockLink.click()
    await expect(page.getByRole('region', { name: `${scenario} mock scenario`, exact: true })).toBeVisible()
    await expect(summary).toHaveCount(0)
    await page.goBack()

    await controlAPI(`${base}/recordings/${recording}/stop`, { method: 'POST' })
    recordingActive = false
    await controlAPI(`${base}/faults/${fault}/disable`, { method: 'POST' })
    await setMockEnabled(false)
    await expect(summary.getByRole('link')).toHaveCount(0)
    await expect(headerIcons).toHaveCount(0)
    await expect(summary.getByRole('heading', { name: state.environment, level: 2, exact: true })).toBeVisible()
    await page.reload()
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
