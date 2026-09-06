import { randomUUID } from 'node:crypto'
import { expect, test, type Locator, type Page } from '@playwright/test'
import type { MockRoute, MockScenario, PreviewMockRequest } from '../src/api/contracts/mocks'
import { authenticate, controlAPI, environmentHeader, environmentPath } from './helpers'
import { readE2EState } from './state'

test('reloads and leaves unsaved mock routes without a native confirmation', async ({ page }) => {
  const fixture = await createScenario('navigation', [route('lookup', '/saved', '{"saved":true}')])
  const nativeDialogs: string[] = []
  page.on('dialog', async (dialog) => {
    nativeDialogs.push(dialog.type())
    // Let a regression finish navigation so the assertion reports the unwanted prompt.
    await dialog.accept()
  })
  try {
    const saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    await editor.getByLabel('PATH', { exact: true }).click()
    await editor.getByLabel('PATH', { exact: true }).fill('/unsaved-edit')
    await expect(editor).toContainText('Unsaved changes')
    await page.reload()
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/saved')
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    await expect(editor.getByText('All changes saved', { exact: true })).toHaveCount(0)
    expect(nativeDialogs).toEqual([])

    await page.getByRole('button', { name: 'ADD ROUTE', exact: true }).click()
    const newRoute = page.getByRole('region', { name: 'Create Route', exact: true })
    await newRoute.getByLabel('PATH', { exact: true }).click()
    await newRoute.getByLabel('PATH', { exact: true }).fill('/unsaved-new-route')
    await expect(newRoute).toContainText('Unsaved changes')
    const overviewURL = new URL(environmentPath(), readE2EState().baseURL).href
    await page.goto(overviewURL)
    await expect(page).toHaveURL(overviewURL)
    await expect(environmentHeader(page).getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
    expect(nativeDialogs).toEqual([])
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(saved)
  } finally {
    await fixture.cleanup()
  }
})

test('renames a route with its draft, previews the new name, and follows the saved selection', async ({ page }) => {
  const original = route('lookup', '/inventory/{sku}', '{"phase":"saved"}', {
    query: { warehouse: { match: 'regex', value: 'central|east' } },
    headers: { 'Content-Type': 'application/json', 'X-Custom': 'preserved' }, status: 202, delayMs: 10,
  })
  const fixture = await createScenario('rename', [original, route('peer', '/health', 'healthy')])
  try {
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    const name = editor.getByLabel('ROUTE NAME', { exact: true })
    await expect(name).toBeEnabled()
    const before = await controlAPI<MockScenario>(fixture.scenarioBase)
    await name.fill('renamed')
    await editor.getByRole('tab', { name: 'Response', exact: true }).click()
    await editor.getByLabel('RESPONSE BODY', { exact: true }).fill('{"phase":"renamed"}')
    await page.getByRole('button', { name: 'Edit peer route', exact: true }).click()
    await expect(name).toHaveValue('peer')
    await page.getByRole('button', { name: 'Edit lookup route', exact: true }).click()
    await expect(name).toHaveValue('renamed')
    await name.fill('peer')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText('Route name peer is duplicated')
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(before)
    await name.fill('renamed')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const preview = editor.getByRole('form', { name: 'Mock request preview' })
    await preview.getByLabel('Query parameter value 1', { exact: true }).fill('central')
    const previewRequest = page.waitForRequest((request) => request.method() === 'POST' && request.url().endsWith('/preview'))
    await preview.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    expect((await previewRequest).postDataJSON()).toMatchObject({ originalRoute: 'lookup', draft: { name: 'renamed', body: '{"phase":"renamed"}' } })
    await expect(editor.getByRole('region', { name: 'Preview response' })).toContainText('Matched renamed')
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(before)
    const saveRequest = page.waitForRequest((request) => request.method() === 'PUT' && request.url().endsWith('/routes/lookup'))
    await preview.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    expect((await saveRequest).postDataJSON()).toMatchObject({ ...original, name: 'renamed', body: '{"phase":"renamed"}' })
    await expect(page).toHaveURL(/route=renamed$/)
    await expect(page.getByRole('button', { name: 'Edit lookup route', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Edit renamed route', exact: true })).toBeVisible()
    await expect(name).toHaveValue('renamed')
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    const saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    expect(saved.routes).toHaveLength(2)
    expect(saved.routes.find((route) => route.name === 'renamed')).toMatchObject({ ...original, name: 'renamed', body: '{"phase":"renamed"}', createdAt: before.routes.find((route) => route.name === 'lookup')!.createdAt })
    expect(saved.routes.find((route) => route.name === 'peer')).toEqual(before.routes.find((route) => route.name === 'peer'))
    await page.reload()
    await expect(name).toHaveValue('renamed')
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/inventory/{sku}')
    await expect(editor.getByLabel('Query parameter match 1', { exact: true })).toHaveValue('regex')

    await name.fill('renamed-again')
    await editor.getByRole('tab', { name: 'Response', exact: true }).click()
    await expect(editor.getByLabel('RESPONSE BODY', { exact: true })).toHaveValue('{"phase":"renamed"}')
    const secondSave = page.waitForRequest((request) => request.method() === 'PUT' && request.url().endsWith('/routes/renamed'))
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    expect((await secondSave).postDataJSON().name).toBe('renamed-again')
    await expect(page).toHaveURL(/route=renamed-again$/)
    await expect(name).toHaveValue('renamed-again')
    await name.fill('discard-this-name')
    await editor.getByRole('button', { name: 'Discard route changes', exact: true }).click()
    await expect(name).toHaveValue('renamed-again')
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: 'Back to mock scenarios', exact: true }).click()
    await expect(page).toHaveURL(/\?tab=mocks$/)
    await expect(page.getByRole('dialog')).toHaveCount(0)
  } finally {
    await fixture.cleanup()
  }
})

test('configures path and query matching with compact selectors', async ({ page }, testInfo) => {
  const fixture = await createScenario('match-controls', [route('lookup', '/inventory', '{"available":true}', { query: { coupon: { match: 'exists' } } })])
  try {
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    const pathMatch = editor.getByLabel('Path match', { exact: true })
    const path = editor.getByLabel('PATH', { exact: true })
    const query = editor.getByRole('table', { name: 'Required query parameters', exact: true })
    const couponMatch = query.getByLabel('Query parameter match 1', { exact: true })
    const couponValue = query.getByLabel('Query parameter value 1', { exact: true })
    await expect(pathMatch).toHaveValue('exact')
    await expect(couponMatch).toHaveValue('exists')
    await expect(couponValue).toBeDisabled()
    await expect(couponValue).toHaveAttribute('placeholder', 'Any value')
    await expect(editor.getByText('An empty value matches any value.', { exact: true })).toHaveCount(0)

    await pathMatch.selectOption('template')
    await expect(path).toHaveValue('/inventory')
    await expect(path).toHaveAttribute('placeholder', '/inventory/{sku}')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText('needs a path parameter')
    await path.fill('/inventory/{sku}')
    await couponMatch.selectOption('equals')
    await expect(couponValue).toBeEnabled()
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText('choose Exists')
    await couponValue.fill('summer')
    await couponMatch.selectOption('exists')
    await expect(couponValue).toBeDisabled()
    await couponMatch.selectOption('equals')
    await expect(couponValue).toHaveValue('summer')
    await couponMatch.selectOption('exists')

    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const requestForm = page.getByRole('form', { name: 'Mock request preview' })
    await expect(requestForm.getByLabel('PREVIEW PATH', { exact: true })).toHaveValue('/inventory/sku-123')
    await expect(requestForm.getByLabel('Query parameter value 1', { exact: true })).toHaveValue('')
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(page.getByRole('region', { name: 'Preview response' })).toContainText('Matched lookup')
    await requestForm.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(requestForm.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    expect((await controlAPI<MockScenario>(fixture.scenarioBase)).routes[0]).toMatchObject({ path: '/inventory/{sku}', query: { coupon: { match: 'exists' } } })
    await page.reload()
    await expect(pathMatch).toHaveValue('template')
    await expect(couponMatch).toHaveValue('exists')

    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await page.screenshot({ path: testInfo.outputPath(`mock-match-controls-${theme}.png`), animations: 'disabled' })
    }
    await page.setViewportSize({ width: 760, height: 800 })
    await expect(pathMatch).toBeInViewport()
    await expect(couponMatch).toBeInViewport()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(760)
    await expect.poll(() => editor.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: testInfo.outputPath('mock-match-controls-narrow.png'), animations: 'disabled' })

    await pathMatch.selectOption('exact')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText('Choose Template')
    await path.fill('/inventory/coffee-mug')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    await page.reload()
    await expect(pathMatch).toHaveValue('exact')
    await path.fill('/inventory/{sku}')
    await expect(pathMatch).toHaveValue('template')
  } finally {
    await fixture.cleanup()
  }
})

test('edits, previews, validates, and saves regex query matchers', async ({ page }, testInfo) => {
  const fixture = await createScenario('query-regex', [route('lookup', '/inventory', '{"available":true}')])
  try {
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    const query = editor.getByRole('table', { name: 'Required query parameters', exact: true })
    const match = query.getByLabel('Query parameter match 1', { exact: true })
    const value = query.getByLabel('Query parameter value 1', { exact: true })
    await query.getByLabel('New query parameter name', { exact: true }).fill('sku')
    await match.selectOption('regex')
    await match.focus()
    await page.keyboard.press('Tab')
    await expect(value).toBeFocused()
    await value.fill('[')
    const before = await controlAPI<MockScenario>(fixture.scenarioBase)
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText('query parameter sku has an invalid regex')
    await expect(value).toHaveValue('[')
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(before)

    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const preview = editor.getByRole('form', { name: 'Mock request preview' })
    await preview.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(preview.getByRole('alert')).toContainText('invalid regex')
    await preview.getByRole('button', { name: 'EDIT', exact: true }).click()
    const pattern = 'coffee-(mug|beans)'
    await value.fill(pattern)
    await match.selectOption('exists')
    await expect(value).toBeDisabled()
    await match.selectOption('regex')
    await expect(value).toHaveValue(pattern)

    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await preview.getByRole('button', { name: 'RESET REQUEST', exact: true }).click()
    const sample = preview.getByLabel('Query parameter value 1', { exact: true })
    await expect(sample).toHaveValue('')
    await expect(preview.getByLabel('Query parameter match 1', { exact: true })).toHaveCount(0)
    const response = editor.getByRole('region', { name: 'Preview response' })
    await sample.fill('coffee-mug')
    await preview.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('Matched lookup')
    await sample.fill('iced-coffee-mug')
    await preview.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('No route matched')
    await addQueryParameter(preview.getByRole('table', { name: 'Preview query parameters', exact: true }), 'sku', 'coffee-beans')
    await preview.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('Matched lookup')
    await preview.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(preview.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    expect((await controlAPI<MockScenario>(fixture.scenarioBase)).routes[0].query).toEqual({ sku: { match: 'regex', value: pattern } })
    await page.reload()
    await expect(match).toHaveValue('regex')
    await expect(value).toHaveValue(pattern)
    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await page.screenshot({ path: testInfo.outputPath(`mock-query-regex-${theme}.png`), animations: 'disabled' })
    }
    await page.setViewportSize({ width: 760, height: 800 })
    await expect(match).toBeInViewport()
    await expect(value).toBeInViewport()
    await expect.poll(() => editor.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: testInfo.outputPath('mock-query-regex-narrow.png'), animations: 'disabled' })

    await match.selectOption('equals')
    await expect(value).toHaveValue(pattern)
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    expect((await controlAPI<MockScenario>(fixture.scenarioBase)).routes[0].query).toEqual({ sku: { match: 'equals', value: pattern } })
    await match.selectOption('exists')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    await page.reload()
    await expect(match).toHaveValue('exists')
    await expect(value).toBeDisabled()
    expect((await controlAPI<MockScenario>(fixture.scenarioBase)).routes[0].query).toEqual({ sku: { match: 'exists' } })
  } finally {
    await fixture.cleanup()
  }
})

test('previews a new unsaved route in a disabled scenario without changing saved or runtime state', async ({ page }) => {
  const fixture = await createScenario('new')
  try {
    await authenticate(page, fixture.path)
    await page.getByRole('button', { name: 'ADD ROUTE', exact: true }).click()
    const editor = page.getByRole('region', { name: 'Create Route', exact: true })
    await editor.getByLabel('SERVICE', { exact: true }).selectOption('inventory')
    await editor.getByLabel('PATH', { exact: true }).fill('/inventory/{sku}')
    await editor.getByLabel('ROUTE NAME', { exact: true }).fill('lookup')
    await addQueryParameter(await openRequiredQuery(editor), 'warehouse', 'central')
    await editor.getByRole('tab', { name: 'Response', exact: true }).click()
    await editor.getByLabel('RESPONSE STATUS').selectOption('503')
    await editor.getByLabel('RESPONSE BODY').fill('{"available":false,"message":"<script>preview</script>"}')
    await editor.getByLabel('DELAY (MS)', { exact: true }).fill('25')
    const headerTable = await openResponseHeaders(editor)
    await expect(headerTable.getByLabel('Header name 1', { exact: true })).toHaveValue('Content-Type')
    await expect(headerTable.getByLabel('Header value 1', { exact: true })).toHaveValue('application/json')
    await headerTable.getByLabel('New header name', { exact: true }).fill('X-Preview')
    await headerTable.getByLabel('Header value 2', { exact: true }).fill('unsaved')

    const before = await readPreviewSideEffects(fixture.base, fixture.scenarioBase)
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const requestForm = page.getByRole('form', { name: 'Mock request preview' })
    await expect(requestForm).toBeVisible()
    await expect(requestForm.getByLabel('PREVIEW SERVICE', { exact: true })).toHaveValue('inventory')
    await expect(requestForm.getByLabel('PREVIEW METHOD', { exact: true })).toHaveValue('GET')
    await expect(requestForm.getByLabel('PREVIEW PATH', { exact: true })).toHaveValue(/^\/inventory\/[^{}]+$/)
    const queryTable = requestForm.getByRole('table', { name: 'Preview query parameters', exact: true })
    await expect(queryTable.getByLabel('Query parameter name 1', { exact: true })).toHaveValue('warehouse')
    await expect(queryTable.getByLabel('Query parameter value 1', { exact: true })).toHaveValue('central')
    await queryTable.getByRole('button', { name: 'Remove query parameter 1', exact: true }).click()
    await expect(queryTable).toBeVisible()
    await expect(queryTable.getByLabel('New query parameter name', { exact: true })).toBeFocused()
    await expect(editor).toContainText('Current route draft with saved scenario routes.')
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).fill('/inventory/coffee-mug')
    await addQueryParameter(queryTable, 'warehouse', 'central')
    await addQueryParameter(queryTable, 'tag', 'one')
    await addQueryParameter(queryTable, 'tag', 'two')
    await addQueryParameter(queryTable, 'empty', '')
    const submitted = page.waitForRequest((request) => request.method() === 'POST' && request.url().endsWith(`${fixture.scenarioBase}/preview`))
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).press('Enter')
    const envelope = (await submitted).postDataJSON() as PreviewMockRequest
    expect(envelope).toMatchObject({
      request: { service: 'inventory', method: 'GET', path: '/inventory/coffee-mug', query: { warehouse: ['central'], tag: ['one', 'two'], empty: [''] } },
      draft: { name: 'lookup', service: 'inventory', status: 503, enabled: true },
    })
    expect(envelope).not.toHaveProperty('originalRoute')

    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched lookup')
    await expect(response).toContainText('503')
    await expect(response).toContainText('25 ms')
    await expect(response.getByRole('tab', { name: 'Body', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(response.getByRole('tabpanel')).toContainText('"available": false')
    await expect(response.getByRole('tabpanel')).toContainText('<script>preview</script>')
    await expect(response.locator('script')).toHaveCount(0)
    await response.getByRole('button', { name: 'Raw', exact: true }).click()
    await expect(response.getByRole('tabpanel')).toHaveText('{"available":false,"message":"<script>preview</script>"}')
    await response.getByRole('button', { name: 'Formatted', exact: true }).click()
    await response.getByRole('tab', { name: 'Headers', exact: true }).click()
    await expect(response.getByRole('tabpanel')).toContainText('X-Preview')
    await expect(response.getByRole('tabpanel')).toContainText('unsaved')
    await expect(response.getByRole('tabpanel')).toContainText('application/json')
    expect(await readPreviewSideEffects(fixture.base, fixture.scenarioBase)).toEqual(before)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await expect(editor.getByLabel('RESPONSE BODY')).toHaveValue('{"available":false,"message":"<script>preview</script>"}')
    await expect(editor.getByLabel('DELAY (MS)', { exact: true })).toHaveValue('25')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await expect(requestForm.getByLabel('PREVIEW PATH', { exact: true })).toHaveValue('/inventory/coffee-mug')
    await expect(response).toContainText('Matched lookup')
    await expect(editor.getByText('Preview is outdated. Run it again.', { exact: true })).toHaveCount(0)
  } finally {
    await fixture.cleanup()
  }
})

test('keeps request and response configuration in accessible tabs and saves the complete draft from either tab', async ({ page }, testInfo) => {
  const fixture = await createScenario('tabs', [route('lookup', '/original', '{"saved":true}')])
  try {
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    const tabs = editor.getByRole('tablist', { name: 'Mock route configuration', exact: true })
    const requestTab = tabs.getByRole('tab', { name: 'Request', exact: true })
    const responseTab = tabs.getByRole('tab', { name: 'Response', exact: true })
    await expect(requestTab).toHaveAttribute('aria-selected', 'true')
    await expect(responseTab).toHaveAttribute('aria-selected', 'false')
    await expect(editor.getByRole('tabpanel')).toHaveCount(1)
    await expect(editor.getByRole('tabpanel', { name: 'Request', exact: true })).toBeVisible()
    await expect(editor.getByLabel('RESPONSE BODY')).toHaveCount(0)
    await expect(editor.getByLabel('ROUTE ENABLED', { exact: true })).toBeChecked()
    await editor.getByLabel('PATH', { exact: true }).fill('/changed')
    const required = editor.getByRole('table', { name: 'Required query parameters', exact: true })
    await addQueryParameter(required, 'warehouse', 'central')

    await requestTab.focus()
    await page.keyboard.press('ArrowRight')
    await expect(responseTab).toBeFocused()
    await expect(responseTab).toHaveAttribute('aria-selected', 'true')
    await expect(editor.getByRole('tabpanel')).toHaveCount(1)
    await expect(editor.getByRole('tabpanel', { name: 'Response', exact: true })).toBeVisible()
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveCount(0)
    await expect(required).toHaveCount(0)
    await expect(editor.getByLabel('ROUTE ENABLED', { exact: true })).toBeChecked()
    await editor.getByLabel('RESPONSE STATUS').selectOption('201')
    await editor.getByLabel('DELAY (MS)', { exact: true }).fill('12')
    await editor.getByLabel('RESPONSE BODY').fill('{"phase":1}')
    const headers = editor.getByRole('table', { name: 'Response headers', exact: true })
    await headers.getByLabel('New header name', { exact: true }).fill('X-Phase')
    await headers.getByLabel('Header value 2', { exact: true }).fill('one')
    await responseTab.focus()
    await page.keyboard.press('Home')
    await expect(requestTab).toBeFocused()
    await expect(requestTab).toHaveAttribute('aria-selected', 'true')
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/changed')
    await expect(required.getByLabel('Query parameter value 1', { exact: true })).toHaveValue('central')
    await page.keyboard.press('End')
    await expect(responseTab).toBeFocused()
    await expect(editor.getByLabel('RESPONSE BODY')).toHaveValue('{"phase":1}')
    await expect(editor.getByLabel('DELAY (MS)', { exact: true })).toHaveValue('12')
    await expect(headers.getByLabel('Header value 2', { exact: true })).toHaveValue('one')
    await page.keyboard.press('ArrowLeft')
    await expect(requestTab).toBeFocused()
    await page.keyboard.press('ArrowRight')
    await expect(responseTab).toBeFocused()

    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await page.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched lookup')
    await expect(response).toContainText('201')
    await expect(response).toContainText('12 ms')
    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await expect(responseTab).toHaveAttribute('aria-selected', 'true')
    await expect(editor.getByLabel('RESPONSE BODY')).toHaveValue('{"phase":1}')
    await requestTab.click()
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    let saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    expect(saved.routes[0]).toMatchObject({ name: 'lookup', method: 'GET', path: '/changed', query: { warehouse: { match: 'equals', value: 'central' } }, status: 201, delayMs: 12, body: '{"phase":1}', headers: { 'Content-Type': 'application/json', 'X-Phase': 'one' } })

    await editor.getByLabel('METHOD', { exact: true }).selectOption('POST')
    await editor.getByLabel('PATH', { exact: true }).fill('/changed-again')
    await responseTab.click()
    await editor.getByLabel('RESPONSE BODY').fill('{"phase":2}')
    await editor.getByLabel('RESPONSE STATUS').selectOption('202')
    await headers.getByLabel('Header value 2', { exact: true }).fill('two')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    expect(saved.routes[0]).toMatchObject({ name: 'lookup', method: 'POST', path: '/changed-again', query: { warehouse: { match: 'equals', value: 'central' } }, status: 202, delayMs: 12, body: '{"phase":2}', headers: { 'Content-Type': 'application/json', 'X-Phase': 'two' } })

    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      for (const tab of ['Request', 'Response'] as const) {
        await tabs.getByRole('tab', { name: tab, exact: true }).click()
        await expect(editor.getByRole('tabpanel', { name: tab, exact: true })).toBeVisible()
        await expect(tabs).toBeInViewport()
        await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeInViewport()
        await page.screenshot({ path: testInfo.outputPath(`mock-config-${tab.toLowerCase()}-${theme}.png`), animations: 'disabled' })
      }
    }
    await page.setViewportSize({ width: 760, height: 800 })
    for (const tab of ['Request', 'Response'] as const) {
      await tabs.getByRole('tab', { name: tab, exact: true }).click()
      await editor.getByRole('table', { name: tab === 'Request' ? 'Required query parameters' : 'Response headers', exact: true }).scrollIntoViewIfNeeded()
      await expect(tabs).toBeInViewport()
      await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeInViewport()
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(760)
      await page.screenshot({ path: testInfo.outputPath(`mock-config-${tab.toLowerCase()}-narrow.png`), animations: 'disabled' })
    }
  } finally {
    await fixture.cleanup()
  }
})

test('edits required and preview query rows while preserving matching, repeated values, and request reset', async ({ page }, testInfo) => {
  const fixture = await createScenario('query', [
    route('lookup', '/query', '{"query":true}', { query: { warehouse: { match: 'equals', value: 'central' } } }),
    route('other', '/other', '{"other":true}'),
  ])
  try {
    const saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    const required = await openRequiredQuery(editor)
    await expect(required.getByLabel('Query parameter name 1', { exact: true })).toHaveValue('warehouse')
    await expect(required.getByLabel('Query parameter value 1', { exact: true })).toHaveValue('central')
    await required.getByLabel('Query parameter value 1', { exact: true }).fill('east=zone')
    await required.getByLabel('New query parameter name', { exact: true }).pressSequentially('include')
    await expect(required.getByLabel('Query parameter name 2', { exact: true })).toHaveValue('include')
    await expect(required.getByLabel('Query parameter name 2', { exact: true })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(required.getByLabel('Query parameter match 2', { exact: true })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(required.getByLabel('Query parameter value 2', { exact: true })).toBeFocused()
    await required.getByLabel('Query parameter value 2', { exact: true }).fill('availability')
    await addQueryParameter(required, 'remove', 'temporary')
    await required.getByRole('button', { name: 'Remove query parameter 3', exact: true }).click()
    await expect(required.getByLabel('Query parameter name 3', { exact: true })).toHaveCount(0)
    await expect(required.getByLabel('New query parameter name', { exact: true })).toBeFocused()
    await page.getByRole('button', { name: 'Edit other route', exact: true }).click()
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/other')
    await page.getByRole('button', { name: 'Edit lookup route', exact: true }).click()
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/query')
    await openRequiredQuery(editor)
    await expect(required.getByLabel('Query parameter value 1', { exact: true })).toHaveValue('east=zone')
    await expect(required.getByLabel('Query parameter value 2', { exact: true })).toHaveValue('availability')
    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await required.scrollIntoViewIfNeeded()
      await page.screenshot({ path: testInfo.outputPath(`mock-required-query-${theme}.png`), animations: 'disabled' })
    }
    const originalViewport = page.viewportSize()!
    await page.setViewportSize({ width: 760, height: 800 })
    await required.scrollIntoViewIfNeeded()
    await expect(required.getByLabel('Query parameter value 2', { exact: true })).toBeInViewport()
    await expect(required.getByRole('button', { name: 'Remove query parameter 2', exact: true })).toBeInViewport()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(760)
    await expect.poll(() => required.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: testInfo.outputPath('mock-required-query-narrow.png'), animations: 'disabled' })
    await page.setViewportSize(originalViewport)

    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const requestForm = page.getByRole('form', { name: 'Mock request preview' })
    const query = requestForm.getByRole('table', { name: 'Preview query parameters', exact: true })
    await expect(query).toBeVisible()
    await expect(query.getByLabel('Query parameter name 1', { exact: true })).toHaveValue('warehouse')
    await expect(query.getByLabel('Query parameter value 1', { exact: true })).toHaveValue('east=zone')
    await expect(query.getByLabel('Query parameter name 2', { exact: true })).toHaveValue('include')
    await expect(query.getByLabel('Query parameter value 2', { exact: true })).toHaveValue('availability')
    await addQueryParameter(query, 'tag', 'one')
    await addQueryParameter(query, 'tag', 'two')
    await addQueryParameter(query, 'empty', '')
    const submitted = page.waitForRequest((request) => request.method() === 'POST' && request.url().endsWith(`${fixture.scenarioBase}/preview`))
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    expect((await submitted).postDataJSON()).toMatchObject({
      request: { query: { warehouse: ['east=zone'], include: ['availability'], tag: ['one', 'two'], empty: [''] } },
      draft: { query: { warehouse: { match: 'equals', value: 'east=zone' }, include: { match: 'equals', value: 'availability' } } },
    })
    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched lookup')
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(saved)
    const outdated = editor.getByText('Preview is outdated. Run it again.', { exact: true })
    await query.getByLabel('Query parameter value 2', { exact: true }).fill('pricing')
    await expect(outdated).toBeVisible()
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('No route matched')
    await expect(response).toContainText('501')
    await requestForm.getByRole('button', { name: 'RESET REQUEST', exact: true }).click()
    await expect(query.getByLabel('Query parameter value 2', { exact: true })).toHaveValue('availability')
    await expect(query.getByLabel('Query parameter name 3', { exact: true })).toHaveCount(0)
    await expect(query.getByLabel('New query parameter name', { exact: true })).toHaveValue('')
    await expect(outdated).toBeVisible()
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('Matched lookup')
    await expect(outdated).toHaveCount(0)
    await query.getByLabel('New query parameter value', { exact: true }).fill('orphan')
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText(/query.*name|name.*query/i)
    await query.getByRole('button', { name: 'Remove query parameter 3', exact: true }).click()
    await expect(editor.getByRole('alert')).toHaveCount(0)
    await addQueryParameter(query, 'tag', 'one')
    await addQueryParameter(query, 'tag', 'two')
    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await openRequiredQuery(editor)
    await expect(required.getByLabel('Query parameter name 3', { exact: true })).toHaveCount(0)
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await expect(query.getByLabel('Query parameter name 3', { exact: true })).toHaveValue('tag')
    await expect(query.getByLabel('Query parameter value 3', { exact: true })).toHaveValue('one')
    await expect(query.getByLabel('Query parameter name 4', { exact: true })).toHaveValue('tag')
    await expect(query.getByLabel('Query parameter value 4', { exact: true })).toHaveValue('two')
    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await query.scrollIntoViewIfNeeded()
      await page.screenshot({ path: testInfo.outputPath(`mock-preview-query-${theme}.png`), animations: 'disabled' })
    }
    await page.setViewportSize({ width: 760, height: 800 })
    await query.scrollIntoViewIfNeeded()
    await expect(query.getByLabel('Query parameter value 4', { exact: true })).toBeInViewport()
    await expect(query.getByRole('button', { name: 'Remove query parameter 4', exact: true })).toBeInViewport()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(760)
    await expect.poll(() => query.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: testInfo.outputPath('mock-preview-query-narrow.png'), animations: 'disabled' })
    await page.setViewportSize(originalViewport)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await openRequiredQuery(editor)
    await required.getByLabel('Query parameter name 2', { exact: true }).fill('warehouse')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText(/duplicat/i)
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(saved)
    await required.getByLabel('Query parameter name 2', { exact: true }).fill('')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText(/query.*name|name.*query/i)
    await required.getByLabel('Query parameter name 2', { exact: true }).fill('include')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    const updated = await controlAPI<MockScenario>(fixture.scenarioBase)
    expect(updated.routes.find(({ name }) => name === 'lookup')?.query).toEqual({ warehouse: { match: 'equals', value: 'east=zone' }, include: { match: 'equals', value: 'availability' } })
  } finally {
    await fixture.cleanup()
  }
})

test('edits response header rows, retains drafts, validates incomplete headers, and saves their exact values', async ({ page }, testInfo) => {
  const fixture = await createScenario('headers', [
    route('lookup', '/headers', '{"headers":true}'),
    route('other', '/other', '{"other":true}'),
  ])
  try {
    const saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    const headers = await openResponseHeaders(editor)
    await expect(headers.getByLabel('Header name 1', { exact: true })).toHaveValue('Content-Type')
    await headers.getByLabel('New header name', { exact: true }).pressSequentially('X-Trace')
    await expect(headers.getByLabel('Header name 2', { exact: true })).toHaveValue('X-Trace')
    await expect(headers.getByLabel('Header name 2', { exact: true })).toBeFocused()
    await expect(headers.getByLabel('New header name', { exact: true })).toHaveValue('')
    await page.keyboard.press('Tab')
    await expect(headers.getByLabel('Header value 2', { exact: true })).toBeFocused()
    await headers.getByLabel('Header value 2', { exact: true }).fill('temporary')
    await headers.getByLabel('Header name 2', { exact: true }).fill('X-Location')
    await headers.getByLabel('Header value 2', { exact: true }).fill('https://example.com/region:central')
    await headers.getByLabel('New header name', { exact: true }).fill('X-Remove')
    await headers.getByLabel('Header value 3', { exact: true }).fill('remove-me')
    await headers.getByRole('button', { name: 'Remove header 3', exact: true }).click()
    await expect(headers.getByLabel('Header name 3', { exact: true })).toHaveCount(0)
    await expect(headers.getByLabel('New header name', { exact: true })).toHaveValue('')
    await expect(headers.getByLabel('New header name', { exact: true })).toBeFocused()

    await page.getByRole('button', { name: 'Edit other route', exact: true }).click()
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/other')
    await page.getByRole('button', { name: 'Edit lookup route', exact: true }).click()
    await expect(editor.getByLabel('PATH', { exact: true })).toHaveValue('/headers')
    await openResponseHeaders(editor)
    await expect(headers.getByLabel('Header name 2', { exact: true })).toHaveValue('X-Location')
    await expect(headers.getByLabel('Header value 2', { exact: true })).toHaveValue('https://example.com/region:central')
    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await headers.scrollIntoViewIfNeeded()
      await page.screenshot({ path: testInfo.outputPath(`mock-response-headers-${theme}.png`), animations: 'disabled' })
    }
    const originalViewport = page.viewportSize()!
    await page.setViewportSize({ width: 760, height: 800 })
    await headers.scrollIntoViewIfNeeded()
    await expect(headers.getByLabel('Header value 2', { exact: true })).toBeInViewport()
    await expect(headers.getByRole('button', { name: 'Remove header 2', exact: true })).toBeInViewport()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(760)
    await expect.poll(() => headers.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: testInfo.outputPath('mock-response-headers-narrow.png'), animations: 'disabled' })
    await page.setViewportSize(originalViewport)

    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await page.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched lookup')
    await response.getByRole('tab', { name: 'Headers', exact: true }).click()
    await expect(response.getByRole('tabpanel')).toContainText('X-Location')
    await expect(response.getByRole('tabpanel')).toContainText('https://example.com/region:central')
    await expect(response.getByRole('tabpanel')).not.toContainText('X-Remove')
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(saved)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await openResponseHeaders(editor)
    await expect(headers.getByLabel('Header value 2', { exact: true })).toHaveValue('https://example.com/region:central')
    await headers.getByLabel('Header name 2', { exact: true }).fill('content-type')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText(/duplicat/i)
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(saved)
    await headers.getByLabel('Header name 2', { exact: true }).fill('X-Location')
    await expect(editor.getByRole('alert')).toHaveCount(0)
    await headers.getByLabel('Header name 2', { exact: true }).fill('')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await page.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText(/header.*name|name.*header/i)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await openResponseHeaders(editor)
    await expect(headers.getByLabel('Header value 2', { exact: true })).toHaveValue('https://example.com/region:central')
    await headers.getByLabel('Header name 2', { exact: true }).fill('X-Location')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await page.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(editor.getByRole('alert')).toHaveCount(0)
    await expect(response).toContainText('Matched lookup')
    await editor.getByRole('button', { name: 'SAVE ROUTE', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SAVE ROUTE', exact: true })).toBeDisabled()
    const updated = await controlAPI<MockScenario>(fixture.scenarioBase)
    expect(updated.routes.find(({ name }) => name === 'lookup')?.headers).toEqual({
      'Content-Type': 'application/json',
      'X-Location': 'https://example.com/region:central',
    })
    await page.reload()
    await openResponseHeaders(editor)
    await expect(headers.getByLabel('Header name 2', { exact: true })).toHaveValue('X-Location')
    await expect(headers.getByLabel('Header value 2', { exact: true })).toHaveValue('https://example.com/region:central')
    await expect(headers.getByLabel('New header name', { exact: true })).toHaveValue('')
    await expect(headers.getByLabel('New header value', { exact: true })).toHaveValue('')
  } finally {
    await fixture.cleanup()
  }
})

test('previews saved route overlays, precedence, no matches, disabled routes, and outdated results', async ({ page }) => {
  const fixture = await createScenario('matching', [
    route('lookup', '/inventory/{sku}', '{"saved":true}'),
    route('special', '/inventory/special', '{"special":true}', { status: 202 }),
  ])
  try {
    const saved = await controlAPI<MockScenario>(fixture.scenarioBase)
    await authenticate(page, `${fixture.path}&route=lookup`)
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    await editor.getByRole('tab', { name: 'Response', exact: true }).click()
    await editor.getByLabel('RESPONSE STATUS').selectOption('503')
    await editor.getByLabel('RESPONSE BODY').fill('{"draft":true}')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const requestForm = page.getByRole('form', { name: 'Mock request preview' })
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).fill('/inventory/coffee-mug')
    const submitted = page.waitForRequest((request) => request.method() === 'POST' && request.url().endsWith(`${fixture.scenarioBase}/preview`))
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    expect((await submitted).postDataJSON()).toMatchObject({ originalRoute: 'lookup', draft: { name: 'lookup', body: '{"draft":true}', status: 503 } })
    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched lookup')
    await expect(response).toContainText('503')
    await expect(response.getByRole('tabpanel')).toContainText('"draft": true')

    const outdated = editor.getByText('Preview is outdated. Run it again.', { exact: true })
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).fill('/inventory/special')
    await expect(outdated).toBeVisible()
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('Matched special')
    await expect(response).toContainText('202')
    await expect(outdated).toHaveCount(0)
    await requestForm.getByLabel('PREVIEW METHOD', { exact: true }).selectOption('POST')
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('No route matched')
    await expect(response).toContainText('501')
    await expect(editor.getByRole('alert')).toHaveCount(0)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await expect(editor.getByLabel('RESPONSE BODY')).toHaveValue('{"draft":true}')
    await editor.getByLabel('RESPONSE BODY').fill('{"draft":"updated"}')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await expect(requestForm.getByLabel('PREVIEW METHOD', { exact: true })).toHaveValue('POST')
    await expect(requestForm.getByLabel('PREVIEW PATH', { exact: true })).toHaveValue('/inventory/special')
    await expect(outdated).toBeVisible()
    await requestForm.getByRole('button', { name: 'RESET REQUEST', exact: true }).click()
    await expect(requestForm.getByLabel('PREVIEW METHOD', { exact: true })).toHaveValue('GET')
    await expect(requestForm.getByLabel('PREVIEW PATH', { exact: true })).toHaveValue(/^\/inventory\/[^{}]+$/)
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('Matched lookup')
    await expect(response.getByRole('tabpanel')).toContainText('"draft": "updated"')
    await expect(outdated).toHaveCount(0)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await editor.getByLabel('ROUTE ENABLED', { exact: true }).uncheck()
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await expect(editor).toContainText('This route is disabled and will not match.')
    await expect(outdated).toBeVisible()
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('No route matched')
    await expect(response).toContainText('501')
    expect(await controlAPI<MockScenario>(fixture.scenarioBase)).toEqual(saved)

    const changedRoute = route('special', '/inventory/special', '{"saved":"updated elsewhere"}', { status: 202 })
    await controlAPI(`${fixture.scenarioBase}/routes/special`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(changedRoute) })
    await expect(outdated).toBeVisible()
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).fill('/inventory/special')
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(response).toContainText('Matched special')
    await expect(response.getByRole('tabpanel')).toContainText('updated elsewhere')
    await expect(outdated).toHaveCount(0)
  } finally {
    await fixture.cleanup()
  }
})

test('keeps preview validation errors local and recovers after correcting the draft and request', async ({ page }) => {
  const fixture = await createScenario('validation', [route('lookup', '/saved', '{"saved":true}')])
  try {
    await authenticate(page, fixture.path)
    await page.getByRole('button', { name: 'ADD ROUTE', exact: true }).click()
    const editor = page.getByRole('region', { name: 'Create Route', exact: true })
    await editor.getByLabel('SERVICE', { exact: true }).selectOption('inventory')
    await editor.getByLabel('PATH', { exact: true }).fill('/draft')
    await editor.getByLabel('ROUTE NAME', { exact: true }).fill('lookup')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    const requestForm = page.getByRole('form', { name: 'Mock request preview' })
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(editor.getByRole('alert')).toContainText(/already exists|duplicate/i)
    await expect(requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true })).toBeEnabled()
    await expect(page.getByRole('region', { name: 'Preview response' }).getByRole('tabpanel')).toHaveCount(0)

    await editor.getByRole('button', { name: 'EDIT', exact: true }).click()
    await editor.getByLabel('ROUTE NAME', { exact: true }).fill('new-route')
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    let previewRequests = 0
    page.on('request', (request) => {
      if (request.method() === 'POST' && request.url().endsWith(`${fixture.scenarioBase}/preview`)) previewRequests++
    })
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).fill('https://example.com/draft')
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await expect(editor.getByRole('alert')).toBeVisible()
    expect(previewRequests).toBe(0)
    await requestForm.getByLabel('PREVIEW PATH', { exact: true }).fill('/draft')
    await requestForm.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched new-route')
    await expect(editor.getByRole('alert')).toHaveCount(0)
    await expect(response.getByRole('tabpanel')).toContainText(/has no body/i)
    expect((await controlAPI<MockScenario>(fixture.scenarioBase)).routes.map(({ name }) => name)).toEqual(['lookup'])
  } finally {
    await fixture.cleanup()
  }
})

test('discards late preview results after switching routes and keeps the response pane usable across layouts', async ({ page }, testInfo) => {
  const fixture = await createScenario('pending', [
    route('first', '/first', '{"route":"first"}'),
    route('second', '/second', JSON.stringify({ route: 'second', lines: Array.from({ length: 100 }, (_, index) => `response line ${index}`) })),
  ])
  let releaseResponse = () => {}
  const responseReleased = new Promise<void>((resolve) => { releaseResponse = resolve })
  let finishIntercept = () => {}
  const interceptFinished = new Promise<void>((resolve) => { finishIntercept = resolve })
  let notifyFetched = () => {}
  const responseFetched = new Promise<void>((resolve) => { notifyFetched = resolve })
  const previewPattern = `**${fixture.scenarioBase}/preview`
  let interceptStarted = false
  let delayed = false
  try {
    await authenticate(page, `${fixture.path}&route=first`)
    await page.route(previewPattern, async (intercept) => {
      if (delayed) return await intercept.continue()
      delayed = true
      interceptStarted = true
      try {
        // Use the real matcher result; only delivery timing is controlled.
        const actual = await intercept.fetch()
        notifyFetched()
        await responseReleased
        await intercept.fulfill({ response: actual }).catch(() => {
          // Leaving a route aborts its request before the delayed result arrives.
        })
      } finally {
        finishIntercept()
      }
    })
    const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await page.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    await responseFetched
    await expect(page.getByRole('button', { name: 'RUNNING…', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: 'Edit second route', exact: true }).click()
    await editor.getByRole('button', { name: 'PREVIEW', exact: true }).click()
    await page.getByRole('button', { name: 'RUN PREVIEW', exact: true }).click()
    const response = page.getByRole('region', { name: 'Preview response' })
    await expect(response).toContainText('Matched second')
    releaseResponse()
    await interceptFinished
    await expect(response).toContainText('Matched second')
    await expect(response).not.toContainText('Matched first')
    await expect(page.getByLabel('PREVIEW PATH', { exact: true })).toHaveValue('/second')
    await expect(page.getByRole('button', { name: 'RUN PREVIEW', exact: true })).toBeEnabled()

    await response.getByRole('tab', { name: 'Body', exact: true }).focus()
    await expect(response.getByRole('tab', { name: 'Body', exact: true })).toBeFocused()
    await page.keyboard.press('ArrowRight')
    await expect(response.getByRole('tab', { name: 'Headers', exact: true })).toBeFocused()
    await expect(response.getByRole('tab', { name: 'Headers', exact: true })).toHaveAttribute('aria-selected', 'true')
    await response.getByRole('tab', { name: 'Body', exact: true }).click()
    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await assertPreviewFits(page)
      await page.screenshot({ path: testInfo.outputPath(`mock-preview-${theme}.png`), animations: 'disabled' })
    }
    await page.mouse.move(640, 350)
    await page.keyboard.press('Control+Shift+F')
    await expect(page.locator('.shell')).toHaveClass(/shell--focus-mode/)
    await assertPreviewFits(page)
    await page.screenshot({ path: testInfo.outputPath('mock-preview-focus.png'), animations: 'disabled' })
    await page.keyboard.press('Control+Shift+F')
    await page.setViewportSize({ width: 760, height: 800 })
    await assertPreviewFits(page)
    await page.screenshot({ path: testInfo.outputPath('mock-preview-narrow.png'), animations: 'disabled' })
  } finally {
    releaseResponse()
    if (interceptStarted) await interceptFinished
    await page.unroute(previewPattern)
    await fixture.cleanup()
  }
})

function route(name: string, path: string, body: string, overrides: Partial<MockRoute> = {}): MockRoute {
  return { name, service: 'inventory', method: 'GET', path, status: 200, body, headers: { 'Content-Type': 'application/json' }, enabled: true, ...overrides }
}

async function openResponseHeaders(editor: Locator) {
  await editor.getByRole('tab', { name: 'Response', exact: true }).click()
  const table = editor.getByRole('table', { name: 'Response headers', exact: true })
  await expect(table).toBeVisible()
  return table
}

async function openRequiredQuery(editor: Locator) {
  await editor.getByRole('tab', { name: 'Request', exact: true }).click()
  const table = editor.getByRole('table', { name: 'Required query parameters', exact: true })
  await expect(table).toBeVisible()
  return table
}

async function addQueryParameter(table: Locator, name: string, value: string) {
  const index = await table.getByLabel(/^Query parameter name \d+$/).count() + 1
  await table.getByLabel('New query parameter name', { exact: true }).fill(name)
  await table.getByLabel(`Query parameter value ${index}`, { exact: true }).fill(value)
}

async function createScenario(kind: string, routes: MockRoute[] = []) {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const name = `preview-${kind}-${randomUUID().slice(0, 8)}`
  const scenarioBase = `${base}/mocks/${name}`
  await controlAPI(`${base}/mocks`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) })
  const cleanup = async () => { await controlAPI(scenarioBase, { method: 'DELETE' }) }
  try {
    for (const value of routes) {
      await controlAPI(`${scenarioBase}/routes/${value.name}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) })
    }
    return { base, scenarioBase, path: `${environmentPath('mocks')}&scenario=${name}`, cleanup }
  } catch (error) {
    await cleanup()
    throw error
  }
}

async function readPreviewSideEffects(base: string, scenarioBase: string) {
  type Snapshot = { services: Array<{ name: string; pid?: number; generation: number; status: string }>; bindings: unknown[] }
  const [scenario, snapshot, timeline, traffic, recordings] = await Promise.all([
    controlAPI<MockScenario>(scenarioBase),
    controlAPI<Snapshot>(base),
    controlAPI(`${base}/timeline?limit=1000`),
    controlAPI(`${base}/traffic/exchanges?protocol=http&limit=1000`),
    controlAPI(`${base}/recordings`),
  ])
  return { scenario, services: snapshot.services.map(({ name, pid, generation, status }) => ({ name, pid, generation, status })), bindings: snapshot.bindings, timeline, traffic, recordings }
}

async function assertPreviewFits(page: Page) {
  const editor = page.getByRole('region', { name: 'Edit Route', exact: true })
  const response = page.getByRole('region', { name: 'Preview response' })
  await expect(page.getByRole('form', { name: 'Mock request preview' })).toBeVisible()
  await expect(response).toBeVisible()
  await expect(editor.getByRole('button', { name: 'EDIT', exact: true })).toBeInViewport()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width)
  await expect.poll(() => editor.evaluate((element) => element.getBoundingClientRect().bottom)).toBeLessThanOrEqual(page.viewportSize()!.height + 1)
  await expect.poll(() => response.getByRole('tabpanel').evaluate((element) => element.getBoundingClientRect().height)).toBeGreaterThan(60)
}
