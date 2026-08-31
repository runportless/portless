import { expect, test } from '@playwright/test'
import { applicationRequest, authenticate, controlAPI, environmentPath } from './helpers'

test.describe.configure({ mode: 'serial' })

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
  await trace.getByRole('button', { name: /^Inspect / }).first().click()
  const traceDetail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(traceDetail).toBeVisible()
  await expect(trace).toHaveClass(/trace-waterfall--maximized/)
  await page.keyboard.press('Escape')
  await expect(traceDetail).toHaveCount(0)
  await expect(trace).toHaveClass(/trace-waterfall--maximized/)
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
