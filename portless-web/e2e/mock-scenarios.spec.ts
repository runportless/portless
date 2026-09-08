import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { authenticate, controlAPI } from './helpers'
import { readE2EState } from './state'

test('keeps scenario columns, sorting, and row menus reachable without page overflow', async ({ page }, testInfo) => {
  const state = readE2EState()
  const environment = `mock-list-${randomUUID().slice(0, 8)}`
  const base = `/api/v1/environments/${state.project}/${environment}`
  const names = ['a-scenario-with-a-long-readable-name', 'z-another-scenario-with-a-long-readable-name']
  await controlAPI('/api/v1/environments', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ project: state.project, name: environment, from: state.environment }) })
  try {
    await authenticate(page, `/environments/${state.project}/${environment}?tab=mocks`)
    const panel = page.locator('.mock-scenarios-panel')
    const scroll = panel.getByRole('region', { name: 'Mock scenarios', exact: true })
    const rows = panel.locator('.mock-scenario-row:not(.mock-scenario-row--header)')
    const header = panel.locator('.mock-scenario-row--header')

    for (const count of [0, 1, 2]) {
      if (count > 0) {
        await controlAPI(`${base}/mocks`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: names[count - 1], unmatchedRequests: 'reject' }) })
      }
      await expect(rows).toHaveCount(count)
      await expect(header.getByRole('button')).toHaveCount(count > 1 ? 6 : 0)
      if (count === 0) await expect(panel.getByText('No mock scenarios.', { exact: false })).toBeVisible()

      for (const theme of ['light', 'dark'] as const) {
        await page.emulateMedia({ colorScheme: theme })
        for (const width of [1440, 1024, 760, 390, 320]) {
          await page.setViewportSize({ width, height: 844 })
          await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
          await expect(panel.getByRole('button', { name: 'CREATE SCENARIO', exact: true })).toBeInViewport({ ratio: 1 })
          await scroll.evaluate((element) => { element.scrollLeft = 0 })
          await expect(async () => {
            const columns = await panel.locator('.mock-scenario-row').evaluateAll((elements) => elements.map((row) => Array.from(row.children, (cell) => {
              const bounds = cell.getBoundingClientRect()
              return { left: Math.round(bounds.left), right: Math.round(bounds.right) }
            })))
            expect(columns[0]).toHaveLength(7)
            for (let index = 1; index < 7; index++) expect(columns[0][index].left).toBeGreaterThan(columns[0][index - 1].right)
            for (const row of columns.slice(1)) expect(row).toEqual(columns[0])
          }).toPass()

          if (width > 390) continue
          await scroll.focus()
          await expect(scroll).toBeFocused()
          await expect(scroll).toHaveCSS('outline-style', 'solid')
          await scroll.press('ArrowRight')
          await expect.poll(() => scroll.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0)

          if (count > 1) {
            const nameHeader = header.getByRole('columnheader').nth(1)
            const descending = await nameHeader.getAttribute('aria-sort') === 'ascending'
            const sort = nameHeader.getByRole('button')
            await sort.focus()
            await expect(sort).toBeInViewport({ ratio: 1 })
            await sort.press('Enter')
            await expect(nameHeader).toHaveAttribute('aria-sort', descending ? 'descending' : 'ascending')
            await expect(rows.locator('.mock-scenario-row__name button')).toHaveText(descending ? [...names].reverse() : names)
          }

          for (const name of names.slice(0, count)) {
            const row = rows.filter({ has: page.getByRole('button', { name: `Open ${name} mock scenario`, exact: true }) })
            const trigger = row.getByRole('button', { name: `Mock scenario actions for ${name}`, exact: true })
            await trigger.focus()
            await expect(trigger).toBeInViewport({ ratio: 1 })
            await expect(row.getByRole('switch', { name: `${name} enabled`, exact: true })).toBeInViewport({ ratio: 1 })
            await trigger.press('Enter')
            const menu = row.getByRole('menu', { name: `${name} mock scenario actions`, exact: true })
            const remove = menu.getByRole('menuitem', { name: `Delete ${name}`, exact: true })
            await expect(remove).toBeInViewport({ ratio: 1 })
            await remove.click()
            await expect(menu.getByRole('menuitem', { name: `Confirm delete ${name}`, exact: true })).toBeInViewport({ ratio: 1 })
            await page.keyboard.press('Escape')
            await expect(menu).toHaveCount(0)
            await expect(trigger).toBeFocused()
          }
          await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
          if (count === 2) await panel.screenshot({ path: testInfo.outputPath(`mock-scenarios-${theme}-${width}.png`) })
        }
      }
    }
  } finally {
    await controlAPI(base, { method: 'DELETE' })
  }
})
