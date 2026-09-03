import { expect, test } from '@playwright/test'
import type { Environment } from '../src/api/contracts/environments'
import { authenticate, controlAPI, environmentHeader } from './helpers'
import { readE2EState } from './state'

test('shows one red failure across views, dismissal, reload, and a repeated failed start', async ({ page }, testInfo) => {
  const state = readE2EState()
  const name = 'qa-errors'
  const apiPath = `/api/v1/environments/${state.project}/${name}`
  const stop = async () => {
    await controlAPI(`${apiPath}/down`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ removeVolumes: false }) })
    await expect.poll(async () => (await controlAPI<Environment>(apiPath)).status).toBe('stopped')
  }
  await controlAPI('/api/v1/environments', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ project: state.project, name, from: state.environment }) })
  try {
    await authenticate(page, `/environments/${state.project}/${name}`)
    const header = environmentHeader(page, state.project, name)
    const notices = page.locator('.environment-notices')
    const error = notices.getByRole('alert')
    const views = page.getByRole('navigation', { name: `${state.project}/${name} views` })

    await header.getByRole('button', { name: 'Start', exact: true }).click()
    await expect(header.getByRole('link', { name: /health: failed/ })).toBeVisible()
    await expect(error).toHaveCount(1)
    await expect(error).toContainText('Environment startup failed')
    const failed = await controlAPI<Environment>(apiPath)
    const reason = failed.reason!
    expect(reason).toContain(`already running in ${state.project}/${state.environment}`)

    for (const view of ['Overview', 'Topology', 'Traffic', 'Mocks', 'Recordings', 'Faults', 'Bindings', 'Timeline']) {
      await views.getByRole('button', { name: view, exact: true }).click()
      await expect(error).toHaveCount(1)
      await expect(error).toHaveClass('action-error')
      await expect(error.locator('.action-error__body > p')).toHaveText(reason)
      await expect(page.locator('.environment-status-reason, .alert')).toHaveCount(0)
    }
    await views.getByRole('button', { name: 'Overview', exact: true }).click()
    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme })
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 844 })
        await expect(error).toBeVisible()
        const colors = await error.evaluate((element) => ({
          marker: getComputedStyle(element.querySelector('.action-error__mark')!).color,
          shadow: getComputedStyle(element).boxShadow,
        }))
        expect(colors.shadow).toContain(colors.marker)
        const layout = await error.evaluate((element) => ({
          left: element.getBoundingClientRect().left,
          right: element.getBoundingClientRect().right,
          width: element.clientWidth,
          contentWidth: element.scrollWidth,
        }))
        expect(layout.left).toBeGreaterThanOrEqual(0)
        expect(layout.right).toBeLessThanOrEqual(width)
        expect(layout.contentWidth).toBeLessThanOrEqual(layout.width)
      }
      await notices.screenshot({ path: testInfo.outputPath(`environment-error-${theme}.png`) })
    }
    await page.setViewportSize({ width: 1280, height: 844 })

    await error.getByRole('button', { name: 'Dismiss error' }).click()
    await expect(notices).toHaveCount(0)
    await views.getByRole('button', { name: 'Timeline', exact: true }).click()
    await expect(notices).toHaveCount(0)
    await views.getByRole('button', { name: 'Overview', exact: true }).click()
    await expect(notices).toHaveCount(0)

    await page.reload()
    await expect(error).toHaveCount(1)
    await expect(error).toContainText('Environment failed')
    await expect(error.locator('.action-error__body > p')).toHaveText(reason)
    await expect(page.locator('.environment-status-reason, .alert')).toHaveCount(0)
    await error.getByRole('button', { name: 'Dismiss error' }).click()
    await expect(notices).toHaveCount(0)

    await stop()
    await header.getByRole('button', { name: 'Start', exact: true }).click()
    await expect(error).toHaveCount(1)
    await expect(error).toContainText('Environment startup failed')
    await expect(error.locator('.action-error__body > p')).toHaveText(reason)
    await expect(header.locator('.environment-clone-origin')).toHaveCount(0)
    await expect(page.locator('.environment-overview-heading .environment-clone-origin')).toHaveText(`FROM ${state.environment}`)
    expect((await controlAPI<Environment>(`/api/v1/environments/${state.project}/${state.environment}`)).status).toBe('healthy')
  } finally {
    await stop()
    await controlAPI(apiPath, { method: 'DELETE' })
  }
})
