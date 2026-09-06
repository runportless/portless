import { expect, test } from '@playwright/test'
import { authenticate, environmentPath } from './helpers'
import { readE2EState } from './state'

test('forwards native browser WebSockets and explains handshake-only inspection', async ({ page }) => {
  const state = readE2EState()
  const endpoint = new URL('/browser-websocket', state.baseURL)
  endpoint.hostname = state.applicationHost
  await page.goto(endpoint.href)
  await expect(page.locator('#text')).toHaveText('hello websocket')
  await expect(page.locator('#binary')).toHaveText('0,1,127,128,255')
  await expect(page.locator('#protocol')).toHaveText('portless-test')
  await expect(page.locator('#closed')).toHaveText('Closed 1000 clean=true')

  await authenticate(page, environmentPath('traffic'))
  await page.getByRole('tab', { name: 'EXCHANGES', exact: true }).click()
  const row = page.locator('button.traffic-row').filter({ hasText: '/api/ws' }).filter({ hasText: 'external' }).first()
  await expect(row).toBeVisible()
  await expect(row.locator('strong')).toHaveText('WS')
  await row.click()
  await expect(page.locator('.traffic-detail__protocol-badge')).toHaveText('WS')
  await expect(page.getByRole('note')).toHaveText('WebSocket handshake — messages are not captured.')
  await page.getByRole('tab', { name: 'BODY', exact: true }).click()
  await expect(page.getByText('WebSocket messages are not captured.', { exact: true })).toBeVisible()
})
