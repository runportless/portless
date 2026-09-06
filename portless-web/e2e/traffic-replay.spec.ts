import { randomUUID } from 'node:crypto'
import http from 'node:http'
import { expect, test, type Locator, type Page } from '@playwright/test'
import type { Environment, Operation } from '../src/api/contracts/environments'
import type { TrafficTrace } from '../src/api/contracts/traffic'
import type { TrafficReplayWorkspace } from '../src/api/contracts/traffic_replay'
import { applicationRequest, authenticate, controlAPI, environmentPath } from './helpers'
import { runCLI } from './process'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('rejects oversized edited bodies with the shared error notice before sending', async ({ page }) => {
  const marker = randomUUID().slice(0, 8)
  expect(await echoRequest(`/api/orders?body-limit=${marker}`, '{"value":"original"}')).toBe(200)
  await authenticate(page, environmentPath('traffic'))
  await page.getByRole('tab', { name: 'EXCHANGES', exact: true }).click()
  await page.locator('button.traffic-row').filter({ hasText: marker }).first().click()
  await page.getByRole('button', { name: 'REPLAY', exact: true }).click()
  const editor = page.getByRole('dialog', { name: /Replay request/ })
  await editor.getByRole('tablist', { name: 'Replay request fields' }).getByRole('tab', { name: 'Body', exact: true }).click()
  await editor.getByRole('combobox', { name: 'Replay body source' }).selectOption('replacement')
  await expect(editor.getByText('REPLACEMENT TEXT', { exact: true })).toBeVisible()
  await expect(editor.getByText(/MAXIMUM|25\s*M(?:i)?B/)).toHaveCount(0)
  const writes: string[] = []
  page.on('request', (request) => { if (/\/traffic\/replays\/\d+\/(draft|runs)$/.test(request.url())) writes.push(request.url()) })
  const body = editor.getByRole('textbox', { name: 'Replay request body' })
  await body.fill('é'.repeat(25 * 1024 * 1024 / 2) + 'x')
  await editor.getByRole('button', { name: 'SEND', exact: true }).click()
  const error = editor.getByRole('alert')
  await expect(error).toHaveClass(/action-error/)
  await expect(error).toContainText('The request body is too large. Reduce its size and try again.')
  await expectReplayColumnsStartAtHeader(editor)
  expect(writes).toHaveLength(0)
  await expect(editor.getByText(/MAXIMUM|25\s*M(?:i)?B/)).toHaveCount(0)
  await error.getByRole('button', { name: 'Dismiss error' }).click()
  await expect(error).toHaveCount(0)
  await body.fill('x'.repeat(65_537))
  const completed = await sendAndInspect(page)
  expect(completed.result?.request.body.length).toBe(65_537)
  expect(writes).toHaveLength(2)
  await expect(error).toHaveCount(0)
})

async function echoRequest(path: string, body: string) {
  const state = readE2EState()
  const endpoint = new URL(state.baseURL)
  return await new Promise<number>((resolve, reject) => {
    const request = http.request({ hostname: endpoint.hostname, port: endpoint.port, path, method: 'POST', headers: { Host: state.applicationHost, 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body), 'X-E2E-Application': ['original', 'second'] }, timeout: 10_000 }, (response) => { response.resume(); response.on('end', () => resolve(response.statusCode || 0)) })
    request.on('error', reject)
    request.on('timeout', () => request.destroy(new Error('Fixture echo timed out')))
    request.end(body)
  })
}

async function sendAndInspect(page: Page) {
  const response = page.waitForResponse((candidate) => candidate.request().method() === 'POST' && /\/traffic\/replays\/\d+\/runs$/.test(candidate.url()))
  await page.getByRole('button', { name: 'SEND', exact: true }).click()
  const accepted = await (await response).json() as TrafficReplayWorkspace
  await expect(page.getByRole('region', { name: 'Replay response comparison', exact: true }).locator('.replay-section-heading').getByRole('status')).toHaveText(`Run ${accepted.run!.number}`)
  await expect(page.getByRole('region', { name: 'Replay request editor', exact: true }).getByRole('status')).toHaveCount(0)
  const query = new URLSearchParams({ include: 'result', expectedCreatedAt: accepted.createdAt, expectedDaemonStartedAt: accepted.daemonStartedAt })
  return await controlAPI<TrafficReplayWorkspace>(`/api/v1/environments/${accepted.project}/${accepted.environment}/traffic/replays/${accepted.number}?${query}`)
}

async function expectReplayHeaderFits(detail: Locator) {
  const replay = detail.getByRole('button', { name: 'REPLAY', exact: true })
  await expect(replay).toBeVisible()
  const layout = await replay.evaluate((button) => {
    const range = document.createRange()
    range.selectNodeContents(button)
    const text = range.getBoundingClientRect()
    const control = button.getBoundingClientRect()
    const actions = button.parentElement!
    const header = actions.parentElement!.getBoundingClientRect()
    return {
      textFits: text.left >= control.left && text.right <= control.right && text.top >= control.top && text.bottom <= control.bottom,
      controls: [...actions.querySelectorAll('button')].map((element) => {
        const box = element.getBoundingClientRect()
        return { left: box.left, right: box.right, top: box.top, bottom: box.bottom }
      }),
      header: { left: header.left, right: header.right, top: header.top, bottom: header.bottom },
    }
  })
  expect(layout.textFits).toBe(true)
  for (const [index, control] of layout.controls.entries()) {
    expect(control.left).toBeGreaterThanOrEqual(layout.header.left)
    expect(control.right).toBeLessThanOrEqual(layout.header.right)
    expect(control.top).toBeGreaterThanOrEqual(layout.header.top)
    expect(control.bottom).toBeLessThanOrEqual(layout.header.bottom)
    if (index > 0) expect(control.left).toBeGreaterThan(layout.controls[index - 1].right)
  }
}

async function expectResponseFillsHeight(editor: Locator) {
  const layout = await editor.evaluate((element) => {
    const response = element.querySelector('.replay-response')!
    const body = response.querySelector('.replay-response__body')!
    const headings = [...element.querySelectorAll('.replay-section-heading')]
    return {
      bottomGap: element.getBoundingClientRect().bottom - response.getBoundingClientRect().bottom,
      bodyHeight: body.clientHeight,
      headingHeights: headings.map((heading) => heading.getBoundingClientRect().height),
    }
  })
  expect(layout.bottomGap).toBeGreaterThanOrEqual(15)
  expect(layout.bottomGap).toBeLessThanOrEqual(18)
  expect(layout.bodyHeight).toBeGreaterThan(30)
  expect(layout.headingHeights[0]).toBe(layout.headingHeights[1])
  for (const response of await editor.locator('.replay-response').all()) {
    const header = await response.evaluate((element) => {
      const summary = element.querySelector('.traffic-message-workbench__summary')!
      const tabs = element.querySelector('[role="tablist"]')!
      const body = element.querySelector('[role="tabpanel"]')!
      return {
        summaryBottom: summary.getBoundingClientRect().bottom,
        tabsTop: tabs.getBoundingClientRect().top,
        tabsBottom: tabs.getBoundingClientRect().bottom,
        bodyTop: body.getBoundingClientRect().top,
        controlsBody: [...tabs.querySelectorAll('button')].every((tab) => tab.getAttribute('aria-controls') === body.id),
      }
    })
    expect(header.tabsTop).toBeCloseTo(header.summaryBottom, 0)
    expect(header.bodyTop).toBeCloseTo(header.tabsBottom, 0)
    expect(header.controlsBody).toBe(true)
  }
}

async function expectDiffSummary(editor: Locator, visible: boolean) {
  await expect(editor.locator('.replay-result__summary')).toHaveCount(visible ? 1 : 0)
}

async function expectReplayColumnsStartAtHeader(editor: Locator) {
  const request = editor.getByRole('region', { name: 'Replay request editor', exact: true })
  await expect(request.getByRole('combobox', { name: 'Replay destination environment' })).toBeVisible()
  await expect(request.getByRole('combobox', { name: 'Replay method', exact: true })).toBeVisible()
  await expect(request.getByRole('textbox', { name: 'Replay path and query' })).toBeVisible()
  await expect(request.getByRole('button', { name: 'SEND', exact: true })).toBeVisible()
  const layout = await editor.evaluate((element) => {
    const header = element.querySelector('.replay-header')!.getBoundingClientRect()
    const request = element.querySelector('.replay-request')!
    const requestBox = request.getBoundingClientRect()
    const response = element.querySelector('.replay-result')!.getBoundingClientRect()
    return {
      headerBottom: header.bottom, requestTop: requestBox.top, responseTop: response.top,
      requestRight: requestBox.right, responseLeft: response.left,
      controlsFit: [...request.querySelectorAll('.replay-destination input, .replay-destination select, .replay-destination button, .replay-destination a')].every((control) => {
        const box = control.getBoundingClientRect()
        return box.left >= requestBox.left && box.right <= requestBox.right
      }),
      errorsInRequest: [...element.querySelectorAll('.replay-error')].every((notice) => request.contains(notice)),
    }
  })
  expect(layout.requestTop).toBeCloseTo(layout.headerBottom, 0)
  expect(layout.responseTop).toBeCloseTo(layout.headerBottom, 0)
  expect(layout.responseLeft).toBeCloseTo(layout.requestRight, 0)
  expect(layout.controlsFit).toBe(true)
  expect(layout.errorsInRequest).toBe(true)
}

async function expectRequestBodyFillsHeight(editor: Locator) {
  const layout = await editor.evaluate((element) => {
    const body = element.querySelector('textarea')!
    const content = element.querySelector('.replay-request__content')!
    const response = element.querySelector('.replay-response__body')!
    return {
      bottomGap: element.getBoundingClientRect().bottom - body.getBoundingClientRect().bottom,
      bodyHeight: body.clientHeight,
      outerOverflow: content.scrollHeight - content.clientHeight,
      background: getComputedStyle(body).backgroundColor,
      responseBackground: getComputedStyle(response).backgroundColor,
      text: getComputedStyle(body).color,
      responseText: getComputedStyle(response.querySelector('pre')!).color,
    }
  })
  expect(layout.bottomGap).toBeGreaterThanOrEqual(15)
  expect(layout.bottomGap).toBeLessThanOrEqual(18)
  expect(layout.bodyHeight).toBeGreaterThan(80)
  expect(layout.outerOverflow).toBeLessThanOrEqual(1)
  expect(layout.background).toBe(layout.responseBackground)
  expect(layout.text).toBe(layout.responseText)
}

async function responsePalette(body: Locator, tabs: Locator) {
  return {
    body: await body.evaluate((element) => {
      const style = getComputedStyle(element.querySelector('pre')!)
      const tokenColor = (kind: string) => getComputedStyle(element.querySelector(`.traffic-json__${kind}`)!).color
      return { background: getComputedStyle(element).backgroundColor, text: style.color, fontSize: style.fontSize, lineHeight: style.lineHeight, key: tokenColor('key'), string: tokenColor('string'), number: tokenColor('number') }
    }),
    tabs: await tabs.evaluate((element) => {
      const container = getComputedStyle(element)
      const active = element.querySelector('[aria-selected="true"]')!
      const button = getComputedStyle(active)
      const underline = getComputedStyle(active, '::after')
      return {
        background: container.backgroundColor, height: element.getBoundingClientRect().height,
        padding: container.padding, gap: container.gap, border: container.border,
        active: button.color, inactive: getComputedStyle(element.querySelector('[aria-selected="false"]')!).color,
        font: button.font, letterSpacing: button.letterSpacing, buttonPadding: button.padding,
        underline: { height: underline.height, bottom: underline.bottom, background: underline.backgroundColor },
      }
    }),
  }
}

test('opens without sending, edits one live HTTP request, and compares the real response losslessly', async ({ page }, testInfo) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  const marker = randomUUID().slice(0, 8)
  const path = `/api/orders?replay=${marker}&tag=a&tag=b&space=+&other=%20`
  const capturedBody = `{\n  "id":9007199254740993,\n  "value":"original",\n  "items":[\n${Array.from({ length: 80 }, (_, index) => `    "item ${index}"`).join(',\n')}\n  ]\n}`
  const editedBody = capturedBody.replace('"original"', '"edited"')
  expect(await echoRequest(path, capturedBody)).toBe(200)
  await authenticate(page, environmentPath('traffic'))
  await page.getByRole('tab', { name: 'EXCHANGES', exact: true }).click()
  await page.locator('button.traffic-row').filter({ hasText: marker }).first().click()
  const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expectReplayHeaderFits(detail)
  await page.screenshot({ path: testInfo.outputPath('replay-entry-desktop.png'), animations: 'disabled' })
  await detail.getByRole('button', { name: 'Full screen traffic details' }).click()
  await expectReplayHeaderFits(detail)
  await detail.getByRole('button', { name: 'Restore traffic details' }).click()
  const viewport = page.viewportSize()!
  await page.setViewportSize({ width: 580, height: 780 })
  await page.emulateMedia({ colorScheme: 'light' })
  await expectReplayHeaderFits(detail)
  await page.screenshot({ path: testInfo.outputPath('replay-entry-narrow.png'), animations: 'disabled' })
  await page.setViewportSize(viewport)
  await page.emulateMedia({ colorScheme: 'dark' })
  let runRequests = 0
  page.on('request', (request) => { if (request.method() === 'POST' && /\/traffic\/replays\/\d+\/runs$/.test(request.url())) runRequests++ })
  await detail.getByRole('button', { name: 'REPLAY', exact: true }).click()
  const editor = page.getByRole('dialog', { name: /Replay request/ })
  const requestPath = editor.getByRole('textbox', { name: 'Replay path and query' })
  await expect(requestPath).toHaveValue(path)
  await expect(requestPath).toBeFocused()
  await expect(editor).not.toContainText(/Workspace \d+|expires \d/)
  await expect(editor.getByRole('region', { name: 'Replay request editor', exact: true }).getByRole('status')).toHaveCount(0)
  await expectReplayColumnsStartAtHeader(editor)
  const endpoint = editor.getByRole('region', { name: 'Replay request editor', exact: true }).locator('.replay-endpoint a')
  await editor.getByRole('button', { name: 'Copy replay endpoint', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await endpoint.getAttribute('href'))
  await expectResponseFillsHeight(editor)
  await expect(editor.getByRole('region', { name: 'Original body', exact: true }).locator('pre')).toHaveClass('traffic-json')
  await editor.getByRole('tab', { name: 'Replayed', exact: true }).click()
  await expect(editor.getByText('No replay response available.', { exact: true })).toBeVisible()
  await editor.getByRole('tab', { name: 'Original', exact: true }).click()
  expect(runRequests).toBe(0)
  const heading = await editor.getByRole('heading', { name: /Replay request/ }).textContent()
  await requestPath.press('ArrowLeft')
  await expect(editor.getByRole('heading', { name: /Replay request/ })).toHaveText(heading!)
  await requestPath.fill(`${path}&edited=yes`)
  const rows = editor.getByRole('table', { name: 'Replay request headers' }).locator('tbody tr')
  const repeated = rows.filter({ has: page.locator('input[data-header-name]').filter({ visible: true }) })
  expect(await repeated.count()).toBeGreaterThan(1)
  await editor.getByRole('tablist', { name: 'Replay request fields' }).getByRole('tab', { name: 'Body', exact: true }).click()
  const requestBody = editor.getByRole('textbox', { name: 'Replay request body' })
  for (const mode of ['captured', 'replacement'] as const) {
    await editor.getByRole('combobox', { name: 'Replay body source' }).selectOption(mode)
    if (mode === 'replacement') await requestBody.fill(editedBody)
    await expect(requestBody).toHaveValue(mode === 'captured' ? capturedBody : editedBody)
    await expect(requestBody).toHaveJSProperty('readOnly', mode === 'captured')
    for (const [size, colorScheme] of [[viewport, 'dark'], [{ width: 1280, height: 600 }, 'light'], [{ width: 580, height: 900 }, 'light']] as const) {
      await page.setViewportSize(size)
      await page.emulateMedia({ colorScheme })
      await editor.locator('.replay-workspace').evaluate((element) => element.scrollTo(0, 0))
      await expectRequestBodyFillsHeight(editor)
      await requestBody.evaluate((element) => element.scrollTo(0, 0))
      await requestBody.hover()
      await page.mouse.wheel(0, 240)
      await expect.poll(() => requestBody.evaluate((element) => element.scrollTop)).toBeGreaterThan(0)
      await expectRequestBodyFillsHeight(editor)
      await requestBody.evaluate((element) => element.scrollTo(0, 0))
      if (size.height !== 600) await page.screenshot({ path: testInfo.outputPath(`replay-request-${mode}-${colorScheme}.png`), animations: 'disabled' })
    }
    await page.setViewportSize(viewport)
    await page.emulateMedia({ colorScheme: 'dark' })
  }
  expect(runRequests).toBe(0)
  const completed = await sendAndInspect(page)
  expect(runRequests).toBe(1)
  expect(completed.run).toMatchObject({ state: 'completed', outcome: 'response-received' })
  expect(completed.result?.exchange?.requestTarget).toBe(`${path}&edited=yes`)
  expect(completed.result?.exchange?.requestHeaders?.['X-E2e-Application']).toEqual(['original', 'second'])
  expect(completed.result?.exchange?.requestBody).toBe(editedBody)
  expect(completed.baseline?.requestTarget).toBe(path)
  expect(completed.baseline?.requestBody).toBe(capturedBody)
  await expect(editor.getByRole('tab', { name: 'Response diff', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(editor.getByRole('table', { name: 'body changes' })).toContainText('/body')
  await expectDiffSummary(editor, true)
  await expectReplayColumnsStartAtHeader(editor)
  await page.screenshot({ path: testInfo.outputPath('replay-response-diff.png'), animations: 'disabled' })
  await editor.getByRole('tab', { name: 'Original', exact: true }).click()
  await expect(editor.getByRole('region', { name: 'Original body', exact: true })).toContainText('9007199254740993')
  await expectDiffSummary(editor, false)
  await expectRequestBodyFillsHeight(editor)
  await editor.getByRole('button', { name: 'Copy original response body' }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(completed.baseline!.responseBody)
  await expectResponseFillsHeight(editor)
  const representations = editor.getByRole('tablist', { name: 'Replay response representation' })
  await representations.getByRole('tab', { name: 'Body', exact: true }).focus()
  await page.keyboard.press('ArrowRight')
  await expect(representations.getByRole('tab', { name: 'Headers', exact: true })).toBeFocused()
  await expect(editor.getByRole('region', { name: 'Original headers', exact: true }).locator('.traffic-headers__value').first()).toBeVisible()
  await expectResponseFillsHeight(editor)
  await page.keyboard.press('ArrowRight')
  await expect(representations.getByRole('tab', { name: 'Raw', exact: true })).toBeFocused()
  const raw = editor.getByRole('region', { name: 'Original raw', exact: true }).locator('pre')
  expect(await raw.textContent()).toMatch(/^HTTP 200\n/)
  expect(await raw.textContent()).toContain(`\n\n${completed.baseline!.responseBody}`)
  await expect(raw.locator('.traffic-json__number')).toHaveCount(0)
  await editor.getByRole('button', { name: 'Copy original response raw' }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await raw.textContent())
  await editor.getByRole('tab', { name: 'Response diff', exact: true }).click()
  await expect(editor.getByRole('region', { name: 'Original raw', exact: true })).toBeVisible()
  await expect(editor.getByRole('region', { name: 'Replayed raw', exact: true })).toBeVisible()
  const originalTabs = editor.getByRole('region', { name: 'Original raw', exact: true }).getByRole('tablist')
  await originalTabs.getByRole('tab', { name: 'Headers', exact: true }).click()
  await expect(editor.locator('.replay-diff__panes .traffic-headers')).toHaveCount(2)
  const replayedTabs = editor.getByRole('region', { name: 'Replayed headers', exact: true }).getByRole('tablist')
  await expect(replayedTabs.getByRole('tab', { name: 'Headers', exact: true })).toHaveAttribute('aria-selected', 'true')
  await replayedTabs.getByRole('tab', { name: 'Headers', exact: true }).press('End')
  await expect(editor.getByRole('region', { name: 'Original raw', exact: true }).getByRole('tab', { name: 'Raw', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(editor.getByRole('region', { name: 'Replayed raw', exact: true }).getByRole('tab', { name: 'Raw', exact: true })).toBeFocused()
  await expectDiffSummary(editor, true)
  await expectResponseFillsHeight(editor)
  await page.keyboard.press('Escape')
  await expect(editor).toHaveCount(0)
  await expect(detail).toBeVisible()
  await detail.getByRole('button', { name: 'REPLAY', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Replay path and query' })).toHaveValue(path)
  await expect(editor.getByRole('tab', { name: 'Original', exact: true })).toHaveAttribute('aria-selected', 'true')
  expect(runRequests).toBe(1)
})

test('opens the root request from expanded and maximized waterfall replay icons without sending', async ({ page }, testInfo) => {
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const marker = randomUUID()
  const requestTarget = `/checkout?sku=coffee-mug&quantity=1&waterfall-replay=${marker}`
  expect((await applicationRequest(requestTarget)).status).toBe(200)
  await authenticate(page, environmentPath('traffic'))
  await page.getByPlaceholder('filter path, service, edge, status…').fill(marker)
  await page.locator('button.trace-row').filter({ hasText: marker }).click()
  const waterfall = page.getByRole('region', { name: 'Trace waterfall', exact: true })
  const replay = waterfall.getByRole('button', { name: 'Replay trace', exact: true })
  const editor = page.getByRole('dialog', { name: /Replay request/ })
  const snapshot = await controlAPI<{ traces: TrafficTrace[] }>(`${base}/traffic/traces?background=include&limit=1000`)
  const trace = snapshot.traces.find((candidate) => candidate.requestTarget === requestTarget)!
  expect(trace.rootSequence).toBeGreaterThan(0)
  expect(trace.spanCount).toBeGreaterThan(1)
  let runRequests = 0
  page.on('request', (request) => { if (request.method() === 'POST' && /\/traffic\/replays\/\d+\/runs$/.test(request.url())) runRequests++ })

  for (const maximized of [false, true]) {
    if (maximized) await waterfall.getByRole('button', { name: 'Maximize trace', exact: true }).click()
    await expect(replay).toBeEnabled()
    await replay.focus()
    await page.screenshot({ path: testInfo.outputPath(`waterfall-replay-${maximized ? 'maximized' : 'expanded'}.png`), animations: 'disabled' })
    const opening = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/traffic/replays'))
    await page.keyboard.press('Enter')
    const workspace = await (await opening).json() as TrafficReplayWorkspace
    expect(workspace.baseline).toMatchObject({ sequence: trace.rootSequence, source: 'external', target: 'checkout', requestTarget })
    await expect(editor.getByRole('textbox', { name: 'Replay path and query' })).toHaveValue(requestTarget)
    await expect(editor.getByRole('button', { name: 'SEND', exact: true })).toBeEnabled()
    await expect(page.getByRole('dialog', { name: /Traffic request and response/ })).toHaveCount(0)
    expect(runRequests).toBe(0)
    const closing = page.waitForResponse((response) => response.request().method() === 'DELETE' && response.url().endsWith(`/traffic/replays/${workspace.number}`))
    await page.keyboard.press('Escape')
    expect((await closing).status()).toBe(204)
    await expect(editor).toHaveCount(0)
    await expect(replay).toBeFocused()
    if (maximized) await expect(waterfall).toHaveClass(/trace-waterfall--maximized/)
  }
  const summary = waterfall.getByRole('group', { name: 'Trace summary', exact: true })
  await expect(summary.getByRole('heading')).toHaveText(`GET ${requestTarget}`)
  await expect(summary.getByRole('heading')).toHaveAttribute('title', `GET ${requestTarget}`)
  await expect(summary.getByTitle('Result', { exact: true })).toHaveText('200')
  await expect(summary.locator('time')).toHaveAttribute('datetime', trace.startedAt)
  for (const [colorScheme, width] of [['dark', 1280], ['light', 580]] as const) {
    await page.emulateMedia({ colorScheme })
    await page.setViewportSize({ width, height: 800 })
    await expect(replay).toBeInViewport()
    const layout = await waterfall.locator('.trace-waterfall__toolbar').evaluate((element) => {
      const heading = element.querySelector('h3')!.getBoundingClientRect()
      const summary = element.querySelector('.trace-waterfall__heading')!.getBoundingClientRect()
      const actions = element.querySelector('.trace-waterfall__actions')!.getBoundingClientRect()
      const toolbar = element.getBoundingClientRect()
      return { headingRight: heading.right, actionsLeft: actions.left, actionsRight: actions.right, toolbarRight: toolbar.right, summaryBottom: summary.bottom, toolbarBottom: toolbar.bottom }
    })
    expect(layout.headingRight).toBeLessThan(layout.actionsLeft)
    expect(layout.actionsRight).toBeLessThan(layout.toolbarRight)
    expect(layout.summaryBottom).toBeLessThan(layout.toolbarBottom)
    await page.screenshot({ path: testInfo.outputPath(`waterfall-summary-${colorScheme}.png`), animations: 'disabled' })
  }
  await page.keyboard.press('Escape')
  await expect(waterfall).not.toHaveClass(/trace-waterfall--maximized/)
  await page.setViewportSize({ width: 900, height: 800 })
  await page.emulateMedia({ colorScheme: 'light' })
  await expect(replay).toBeInViewport()
  await expect(waterfall.getByRole('button', { name: 'Maximize trace', exact: true })).toBeInViewport()
  await page.screenshot({ path: testInfo.outputPath('waterfall-replay-narrow-light.png'), animations: 'disabled' })
  const after = await controlAPI<{ traces: TrafficTrace[] }>(`${base}/traffic/traces?background=include&limit=1000`)
  expect(after.traces.filter((candidate) => candidate.requestTarget === requestTarget)).toHaveLength(1)
})

test('replays a waterfall HTTP span into a second isolated environment with a changed mock response', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  const state = readE2EState()
  const name = `replay-${randomUUID().slice(0, 8)}`
  const target = `/api/v1/environments/${state.project}/${name}`
  const scenario = 'replay-destination'
  const body = `{"checkout":"alternate","value":9007199254740993,"items":[${Array.from({ length: 80 }, (_, index) => `{"id":${index},"name":"item ${index}","price":1.00}`).join(',')}]}`
  await controlAPI('/api/v1/environments', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ project: state.project, name, from: state.environment }) })
  try {
    const environment = await controlAPI<Environment>(target)
    await controlAPI(`${target}/mocks`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: scenario }) })
    for (const service of environment.services) {
      await controlAPI(`${target}/mocks/${scenario}/routes/${service.name}-health`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: `${service.name}-health`, service: service.name, method: 'GET', path: '/health', status: 200, body: '{}', enabled: true }) })
    }
    await controlAPI(`${target}/mocks/${scenario}/routes/checkout`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'checkout', service: 'checkout', method: 'GET', path: '/checkout', status: 201, body, headers: { 'Content-Type': 'application/json' }, enabled: true }) })
    const activation = await controlAPI<Operation>(`${target}/mocks/${scenario}/activation`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() }, body: JSON.stringify({ enabled: true }) })
    await expect.poll(async () => (await controlAPI<Operation>(`${target}/operations/${activation.number}`)).state).toBe('succeeded')
    runCLI(state.binary, state.home, state.checkout, ['--env', `${state.project}/${name}`, 'up', '--no-open'])
    expect((await applicationRequest('/checkout?sku=coffee-mug&quantity=1')).status).toBe(200)
    await authenticate(page, environmentPath('traffic'))
    await page.locator('button.trace-row').filter({ hasText: '/checkout' }).first().click()
    await page.getByRole('region', { name: 'Trace waterfall' }).getByRole('button', { name: /Inspect external to checkout GET \/checkout/ }).click()
    const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
    await detail.getByRole('tab', { name: 'RESPONSE', exact: true }).click()
    const tracePalettes = new Map<string, Awaited<ReturnType<typeof responsePalette>>>()
    const trace = detail.locator('.traffic-message-workbench--response')
    for (const colorScheme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', colorScheme)
      tracePalettes.set(colorScheme, await responsePalette(trace.locator('.traffic-payload'), trace.locator('.traffic-payload-tabs')))
    }
    await detail.getByRole('button', { name: 'REPLAY', exact: true }).click()
    const editor = page.getByRole('dialog', { name: /Replay request/ })
    await editor.getByRole('combobox', { name: 'Replay destination environment' }).selectOption(name)
    const completed = await sendAndInspect(page)
    expect(completed.result?.exchange).toMatchObject({ environment: name, source: 'external', target: 'checkout', status: 201, targetProvider: 'mock', responseBody: body })
    expect(completed.result?.comparison.statusChanged).toBe(true)
    expect(completed.baseline?.environment).toBe(state.environment)
    await expect(editor).toContainText('200 → 201')
    await expect(editor.getByRole('table', { name: 'body changes' })).toContainText('9007199254740993')
    await editor.getByRole('tab', { name: 'Replayed', exact: true }).click()
    await expectDiffSummary(editor, false)
    const responseBody = editor.getByRole('region', { name: 'Replayed body', exact: true }).locator('.replay-response__body')
    await expect(responseBody.locator('.traffic-json__number').first()).toHaveText('9007199254740993')
    await expect(responseBody.locator('.traffic-json__number').filter({ hasText: /^1\.00$/ })).toHaveCount(80)
    await expectResponseFillsHeight(editor)
    expect(await responseBody.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true)
    await responseBody.focus()
    await page.keyboard.press('PageDown')
    await expect.poll(() => responseBody.evaluate((element) => element.scrollTop)).toBeGreaterThan(0)
    await responseBody.evaluate((element) => element.scrollTo(0, 0))
    for (const colorScheme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme })
      await expect(page.locator('html')).toHaveAttribute('data-theme', colorScheme)
      expect(await responsePalette(responseBody, editor.getByRole('tablist', { name: 'Replay response representation' }))).toEqual(tracePalettes.get(colorScheme))
      await page.screenshot({ path: testInfo.outputPath(`replay-body-${colorScheme}.png`), animations: 'disabled' })
    }
    await editor.getByRole('button', { name: 'Copy replayed response body' }).click()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(body)
    await page.setViewportSize({ width: 1280, height: 600 })
    // A short window scrolls the result controls while retaining room for the body.
    await editor.locator('.replay-result__content').evaluate((element) => element.scrollTo(0, element.scrollHeight))
    await expectResponseFillsHeight(editor)
    await page.emulateMedia({ colorScheme: 'light' })
    await page.setViewportSize({ width: 580, height: 900 })
    await expect(editor.getByRole('button', { name: 'SEND', exact: true })).toBeVisible()
    await expect(editor.getByRole('textbox', { name: 'Replay path and query' })).toBeVisible()
    await responseBody.scrollIntoViewIfNeeded()
    expect(await responseBody.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true)
    await editor.locator('.replay-workspace').evaluate((element) => element.scrollTo(0, element.scrollHeight))
    await expectResponseFillsHeight(editor)
    expect(await editor.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('replay-narrow-light.png'), animations: 'disabled' })
  } finally {
    runCLI(state.binary, state.home, state.checkout, ['--env', `${state.project}/${name}`, 'down'], true)
    runCLI(state.binary, state.home, state.checkout, ['--env', `${state.project}/${name}`, 'env', 'forget', '--yes'], true)
  }
})

test('reviews a real remote write and releases a closed session while its admitted run finishes', async ({ page }) => {
  test.setTimeout(90_000)
  const state = readE2EState()
  const base = `/api/v1/environments/${state.project}/${state.environment}`
  const original = (await controlAPI<Environment>(base)).bindings!.find((binding) => binding.service === 'checkout')!
  const marker = randomUUID().slice(0, 8)
  expect(await echoRequest(`/api/orders?remote-replay=${marker}`, '{"value":"original"}')).toBe(200)
  let writes = 0
  let pendingResponse: http.ServerResponse | undefined
  const server = http.createServer((request, response) => {
    request.resume()
    request.on('end', () => {
      if (request.method === 'POST') { writes++; pendingResponse = response; return }
      response.writeHead(200, { 'Content-Type': 'application/json' }).end('{}')
    })
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('Remote fixture has no loopback address')
  const bind = async (binding: unknown) => {
    const operation = await controlAPI<Operation>(`${base}/bindings/checkout`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() }, body: JSON.stringify(binding) })
    await expect.poll(async () => (await controlAPI<Operation>(`${base}/operations/${operation.number}`)).state, { timeout: 30_000 }).toBe('succeeded')
  }
  try {
    await bind({ service: 'checkout', provider: 'remote', remote: { url: `http://127.0.0.1:${address.port}`, classification: 'qa', writePolicy: 'read-write', healthPath: '/health' } })
    await authenticate(page, environmentPath('traffic'))
    await page.getByRole('tab', { name: 'EXCHANGES', exact: true }).click()
    await page.locator('button.traffic-row').filter({ hasText: marker }).first().click()
    const detail = page.getByRole('dialog', { name: /Traffic request and response/ })
    const preparedResponse = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/traffic/replays'))
    await detail.getByRole('button', { name: 'REPLAY', exact: true }).click()
    const opened = await (await preparedResponse).json() as TrafficReplayWorkspace
    const editor = page.getByRole('dialog', { name: /Replay request/ })
    await expect(editor.getByRole('button', { name: 'SEND', exact: true })).toBeEnabled()
    expect(writes).toBe(0)
    await editor.getByRole('button', { name: 'SEND', exact: true }).click()
    const confirmation = page.getByRole('alertdialog', { name: 'Confirm remote replay' })
    await expect(confirmation).toContainText('POST /api/orders')
    await expect(confirmation).toContainText('qa')
    expect(writes).toBe(0)
    await page.keyboard.press('Escape')
    await expect(confirmation).toHaveCount(0)
    await expect(editor).toBeVisible()
    expect(writes).toBe(0)
    await editor.getByRole('button', { name: 'SEND', exact: true }).click()
    await confirmation.getByRole('button', { name: 'SEND REMOTE REPLAY', exact: true }).click()
    await expect.poll(() => writes).toBe(1)
    await expect(editor.getByRole('button', { name: 'SENDING…', exact: true })).toBeDisabled()
    await expect(editor.getByRole('textbox', { name: 'Replay path and query' })).toBeDisabled()
    const closing = page.waitForResponse((response) => response.request().method() === 'DELETE' && response.url().endsWith(`/traffic/replays/${opened.number}`))
    await editor.getByRole('button', { name: 'Close replay' }).click()
    expect((await closing).status()).toBe(204)
    await expect(editor).toHaveCount(0)
    await expect(page.getByRole('button', { name: /RESUME REPLAY|VIEW PENDING REPLAY/ })).toHaveCount(0)
    const query = new URLSearchParams({ include: 'result', expectedCreatedAt: opened.createdAt, expectedDaemonStartedAt: opened.daemonStartedAt })
    const inspect = () => controlAPI<TrafficReplayWorkspace>(`${base}/traffic/replays/${opened.number}?${query}`)
    const released = await inspect()
    expect(released.baseline).toBeUndefined()
    expect(released.draft).toBeUndefined()
    expect(released.run?.state).toBe('running')
    pendingResponse!.writeHead(200, { 'Content-Type': 'application/json' }).end('{"writes":1}')
    pendingResponse = undefined
    await expect.poll(async () => (await inspect()).run?.state).toBe('completed')
    expect((await inspect()).result).toBeUndefined()
    await detail.getByRole('button', { name: 'REPLAY', exact: true }).click()
    await expect(editor.getByRole('button', { name: 'SEND', exact: true })).toBeEnabled()
    await expect(editor.getByRole('region', { name: 'Replayed body', exact: true })).toHaveCount(0)
    expect(writes).toBe(1)
  } finally {
    pendingResponse?.end('{}')
    await bind(original)
    server.closeAllConnections()
    await new Promise<void>((resolve) => server.close(() => resolve()))
  }
})


test('keeps the replay editor active without a countdown and releases it after one idle hour', async ({ page }) => {
  const marker = randomUUID().slice(0, 8)
  expect(await echoRequest(`/api/orders?idle-replay=${marker}`, '{"value":"original"}')).toBe(200)
  await authenticate(page, environmentPath('traffic'))
  await page.getByRole('tab', { name: 'EXCHANGES', exact: true }).click()
  await page.locator('button.traffic-row').filter({ hasText: marker }).first().click()
  const created = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/traffic/replays'))
  await page.getByRole('dialog', { name: /Traffic request and response/ }).getByRole('button', { name: 'REPLAY', exact: true }).click()
  const opened = await (await created).json() as TrafficReplayWorkspace
  const editor = page.getByRole('dialog', { name: /Replay request/ })
  await expect(editor.getByRole('textbox', { name: 'Replay path and query' })).toBeVisible()
  await expect(editor).not.toContainText(/Workspace \d+|expires \d/)
  await page.clock.install()
  let runs = 0
  page.on('request', (request) => { if (request.method() === 'POST' && request.url().endsWith('/runs')) runs++ })
  for (let iteration = 0; iteration < 3; iteration++) {
    await page.clock.fastForward(40 * 60_000)
    await expect(editor).toBeVisible()
    const touched = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith(`/traffic/replays/${opened.number}/activity`))
    await editor.getByRole('textbox', { name: 'Replay path and query' }).press('ArrowLeft')
    expect((await touched).status()).toBe(204)
  }
  const closed = page.waitForResponse((response) => response.request().method() === 'DELETE' && response.url().endsWith(`/traffic/replays/${opened.number}`))
  await page.clock.fastForward(60 * 60_000)
  expect((await closed).status()).toBe(204)
  await expect(editor).toHaveCount(0)
  expect(runs).toBe(0)
})
