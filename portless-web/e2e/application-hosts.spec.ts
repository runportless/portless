import { expect, test } from '@playwright/test'
import { readE2EState } from './state'

test('preserves application browser policies for inline scripts and same-origin frames', async ({ page }) => {
  const state = readE2EState()
  const endpoint = new URL('/browser-policy', state.baseURL)
  endpoint.hostname = state.applicationHost

  const response = await page.goto(endpoint.href)
  expect(response?.status()).toBe(200)
  const policies = (await response!.headersArray())
    .filter(({ name }) => name.toLowerCase() === 'content-security-policy')
    .map(({ value }) => value)
  expect(policies).toEqual(["default-src 'none'; script-src 'unsafe-inline'; frame-src 'self'; frame-ancestors 'self'"])
  expect(response!.headers()['x-frame-options']).toBe('SAMEORIGIN')
  expect(response!.headers()['referrer-policy']).toBe('origin')
  expect(response!.headers()['x-content-type-options']).toBe('nosniff')

  await expect(page.getByText('Application script ran', { exact: true })).toBeVisible()
  await expect(page.frameLocator('iframe[title="Application frame"]').getByText('Application frame loaded', { exact: true })).toBeVisible()
})
