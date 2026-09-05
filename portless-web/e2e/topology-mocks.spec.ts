import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { authenticate, controlAPI, environmentPath } from './helpers'
import { readE2EState } from './state'

test('shows bound mock scenarios on topology cards and opens their route workspace', async ({ page }, testInfo) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const scenario = 'topology-mock'
  const scenarioBase = `${base}/mocks/${scenario}`
  await controlAPI(`${base}/mocks`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: scenario, description: 'Topology mock indicators' }),
  })
  for (const service of ['inventory', 'orders']) {
    await controlAPI(`${scenarioBase}/routes/${service}-health`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: `${service}-health`, service, method: 'GET', path: '/health', status: 200, body: '{}', delayMs: 0, enabled: true }),
    })
  }
  const setEnabled = async (enabled: boolean) => {
    const operation = await controlAPI<{ number: number }>(`${scenarioBase}/activation`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() },
      body: JSON.stringify({ enabled }),
    })
    await expect.poll(async () => (await controlAPI<{ state: string }>(`${base}/operations/${operation.number}`)).state, { timeout: 30_000 }).toBe('succeeded')
  }

  await authenticate(page, environmentPath('topology'))
  const topology = page.getByRole('region', { name: 'Service topology' })
  const inventory = topology.locator('.topology-node[data-service="inventory"]')
  const orders = topology.locator('.topology-node[data-service="orders"]')
  const checkout = topology.locator('.topology-node[data-service="checkout"]')
  const preview = topology.getByRole('tooltip')
  const center = topology.getByRole('button', { name: 'Center topology' })
  const layout = () => inventory.evaluate((element) => {
    const card = element.getBoundingClientRect()
    const name = element.querySelector(':scope > strong')!.getBoundingClientRect()
    const endpoint = element.querySelector(':scope > small')!.getBoundingClientRect()
    return { width: card.width, height: card.height, nameY: name.y - card.y, endpointY: endpoint.y - card.y }
  })
  await expect(inventory).toBeVisible()
  await expect(topology.locator('.topology-node__mock')).toHaveCount(0)
  const originalLayout = await layout()
  const originalEndpoint = await inventory.locator(':scope > small').innerText()

  // The visible canvas must update from the live binding, without a reload.
  await setEnabled(true)
  await expect(topology.locator('.topology-node__mock')).toHaveCount(2)
  await expect(inventory.getByText('MOCK', { exact: true })).toBeVisible()
  await expect(orders.getByText('MOCK', { exact: true })).toBeVisible()
  await expect(checkout.getByText('MOCK', { exact: true })).toHaveCount(0)
  expect(await layout()).toEqual(originalLayout)
  await expect(inventory.locator(':scope > small')).toHaveText(originalEndpoint)
  await expect(inventory.locator('.status')).toHaveClass('status status--success')

  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    const badge = inventory.getByText('MOCK', { exact: true })
    const badgeBounds = (await badge.boundingBox())!
    const cardBounds = (await inventory.boundingBox())!
    expect(badgeBounds.x + badgeBounds.width).toBeLessThan(cardBounds.x + cardBounds.width)
    expect(badgeBounds.y).toBeGreaterThan(cardBounds.y)
    expect(await badge.evaluate((element) => getComputedStyle(element).color)).not.toBe(await inventory.locator('.status').evaluate((element) => getComputedStyle(element).color))
    await center.focus()
    await center.hover()
    await expect(preview).toHaveCount(0)
    await page.screenshot({ path: testInfo.outputPath(`topology-mock-${theme}.png`), animations: 'disabled' })
    await inventory.hover()
    await expect(preview).toContainText(`Mock scenario: ${scenario}`)
    await expect(preview).toContainText(`http://${originalEndpoint}`)
    await expect(preview.locator('.topology-service-preview__notice--danger')).toHaveCount(0)
  }

  await center.focus()
  await center.hover()
  await expect(preview).toHaveCount(0)
  await page.keyboard.press('Control+Shift+F')
  await expect(page.locator('.shell')).toHaveClass(/shell--focus-mode/)
  await expect(page.locator('.stage')).toHaveCSS('margin-left', '0px')
  await expect(inventory.getByText('MOCK', { exact: true })).toBeVisible()
  expect(await layout()).toEqual(originalLayout)
  await page.keyboard.press('Control+Shift+F')
  await expect(page.locator('.shell')).not.toHaveClass(/shell--focus-mode/)
  await expect(page.locator('.stage')).toHaveCSS('margin-left', '258px')
  await inventory.focus()
  await expect(inventory).toHaveAccessibleDescription(new RegExp(`Mock scenario: ${scenario}`))
  await page.screenshot({ path: testInfo.outputPath('topology-mock-preview.png'), animations: 'disabled' })
  await inventory.press('Enter')
  const drawer = page.getByRole('dialog', { name: 'inventory service' })
  await expect(drawer).toBeVisible()
  await expect(drawer.locator('.service-mock-binding')).toContainText(`Mock scenario: ${scenario}`)
  await expect(drawer.locator('.service-endpoint-list')).toContainText(`http://${originalEndpoint}`)
  const scenarioLink = drawer.getByRole('link', { name: 'VIEW SCENARIO →', exact: true })
  await expect(scenarioLink).toHaveAttribute('href', `${environmentPath('mocks')}&scenario=${scenario}`)
  await scenarioLink.focus()
  await expect(scenarioLink).toHaveCSS('outline-style', 'solid')
  await page.screenshot({ path: testInfo.outputPath('topology-mock-service.png'), animations: 'disabled' })
  await scenarioLink.press('Enter')
  await expect(page).toHaveURL(new RegExp(`tab=mocks&scenario=${scenario}$`))
  await expect(drawer).toHaveCount(0)
  await expect(page.getByRole('region', { name: `${scenario} mock scenario` })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Edit Route' })).toBeVisible()
  await page.goBack()
  await expect(topology).toBeVisible()
  await expect(drawer).toHaveCount(0)

  await setEnabled(false)
  await expect(topology.locator('.topology-node__mock')).toHaveCount(0)
  expect(await layout()).toEqual(originalLayout)
  await inventory.hover()
  await expect(preview).not.toContainText('Mock scenario:')
  await inventory.click()
  await expect(drawer.locator('.service-mock-binding')).toHaveCount(0)
  await expect(drawer.getByRole('link', { name: 'VIEW SCENARIO →', exact: true })).toHaveCount(0)
  await controlAPI(scenarioBase, { method: 'DELETE' })
})
