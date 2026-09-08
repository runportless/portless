import { expect, test } from '@playwright/test'
import { authenticate, environmentHeader, environmentPath } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('toggles settings back to the exact environment view', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  const topologyPath = environmentPath('topology')
  await page.goto(`${state.baseURL}${topologyPath}`)

  const settings = page.getByRole('button', { name: 'Settings' })
  await settings.click()
  await expect(page).toHaveURL(/\/settings$/)
  await expect(settings).toHaveAttribute('aria-current', 'page')

  await settings.click()
  await expect(page).toHaveURL(new RegExp(`${topologyPath.replace('?', '\\?')}$`))
  await expect(page.getByRole('navigation', { name: `${state.project}/${state.environment} views` }).getByRole('button', { name: 'Topology' })).toHaveAttribute('aria-current', 'page')
})

test('persists the selected browser theme', async ({ page }) => {
  await authenticate(page)
  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible()

  const theme = page.getByRole('radiogroup', { name: 'Theme' })
  await theme.getByRole('radio', { name: /Light/ }).click()
  await expect(theme.getByRole('radio', { name: /Light/ })).toHaveAttribute('aria-checked', 'true')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  expect(await page.evaluate(() => localStorage.getItem('portless.theme'))).toBe('light')

  await page.reload()
  await expect(page.getByRole('radiogroup', { name: 'Theme' }).getByRole('radio', { name: /Light/ })).toHaveAttribute('aria-checked', 'true')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
})

test('generates scoped MCP client configuration without persisting elevated access', async ({ page }) => {
  const state = readE2EState()
  const selector = `${state.project}/${state.environment}`
  await authenticate(page)

  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('tab', { name: 'MCP' }).click()
  await expect(page).toHaveURL(/\/settings\?tab=mcp$/)
  await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible()
  await expect(page.getByText('CONFIGURE CLIENT')).toBeVisible()

  await page.getByLabel('MCP environment').selectOption(selector)
  const preview = page.getByLabel('Generated MCP configuration')
  await expect(preview).toContainText(selector)
  expect(JSON.parse(await preview.textContent() || '')).toEqual({
    mcpServers: {
      [`portless-${state.project}-${state.environment}`]: {
        command: 'portless',
        args: ['--env', selector, 'mcp', 'serve'],
      },
    },
  })

  await page.getByRole('checkbox', { name: /^Replay/ }).check()
  await expect(page.getByRole('button', { name: 'COPY' })).toBeDisabled()
  await expect(page.getByRole('checkbox', { name: /^Sensitive traffic/ })).not.toBeChecked()
  await page.getByRole('checkbox', { name: /^Replay/ }).uncheck()
  await page.getByRole('checkbox', { name: /^Lifecycle/ }).check()
  await page.getByRole('checkbox', { name: /^Sensitive traffic/ }).check()
  await expect(preview).toContainText('--allow-lifecycle')
  await expect(preview).toContainText('--allow-sensitive-traffic')
  await expect(page.locator('.mcp-preview')).toContainText('SENSITIVE · 29 TOOLS')

  await page.getByRole('button', { name: 'COPY' }).click()
  await expect(page.getByRole('button', { name: 'COPIED' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(await preview.textContent())

  await page.getByRole('radio', { name: /^Project/ }).check()
  await page.getByLabel('MCP project').fill(state.project)
  await page.getByRole('checkbox', { name: /^Configuration/ }).check()
  await page.getByRole('checkbox', { name: /^Traffic control/ }).check()
  await page.getByRole('checkbox', { name: /^Replay/ }).check()
  await page.getByLabel('MCP source roots').fill('/tmp/store checkout\n/tmp/inventory')
  await expect(preview).toContainText('--project')
  await expect(preview).toContainText('--source-root')
  await expect(preview).toContainText('--allow-configuration')
  await expect(page.locator('.mcp-preview')).toContainText('SENSITIVE · 64 TOOLS')

  await page.goto(`${state.baseURL}${environmentPath()}`)
  await expect(environmentHeader(page).getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
  await page.keyboard.press('Control+K')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  const input = palette.getByPlaceholder('Search')
  await input.fill('Configure MCP')
  await input.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/settings\\?tab=mcp&env=${state.project}%2F${state.environment}$`))
  await expect(page.getByLabel('MCP environment')).toHaveValue(selector)

  await page.reload()
  await expect(page.getByRole('checkbox', { name: /^Lifecycle/ })).not.toBeChecked()
  await expect(page.getByRole('checkbox', { name: /^Sensitive traffic/ })).not.toBeChecked()
})

test('keeps conditional MCP fields consistent with the app in both themes and narrow layouts', async ({ page }, testInfo) => {
  const state = readE2EState()
  await authenticate(page)
  await page.goto(`${state.baseURL}/settings?tab=mcp`)
  await page.getByRole('radio', { name: /^Project/ }).check()
  await page.getByLabel('MCP project').fill(state.project)
  await page.getByRole('checkbox', { name: /^Configuration/ }).check()
  const roots = page.getByLabel('MCP source roots')
  await roots.fill('/tmp/store checkout\n/tmp/inventory')

  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    for (const width of [1680, 1280, 760, 390]) {
      await page.setViewportSize({ width, height: 1000 })
      const reference = await page.locator('input[name="portless-mcp-executable"]').evaluate((element) => {
        const style = getComputedStyle(element)
        return { background: style.backgroundColor, color: style.color, font: style.fontFamily, size: style.fontSize, border: style.borderColor, height: element.getBoundingClientRect().height }
      })
      for (const field of [page.getByLabel('MCP project'), roots]) {
        await expect(field).toHaveCSS('background-color', reference.background)
        await expect(field).toHaveCSS('color', reference.color)
        await expect(field).toHaveCSS('font-family', reference.font)
        await expect(field).toHaveCSS('font-size', reference.size)
        await expect(field).toHaveCSS('border-radius', '0px')
        const alignment = await field.evaluate((element) => {
          const bounds = element.getBoundingClientRect()
          const parent = element.parentElement!.getBoundingClientRect()
          return { left: bounds.left - parent.left, width: bounds.width - parent.width }
        })
        expect(alignment.left).toBeCloseTo(0)
        expect(alignment.width).toBeCloseTo(0)
        await field.focus()
        const focusBorder = await field.evaluate((element) => getComputedStyle(element).borderColor)
        expect(focusBorder).not.toBe(reference.border)
      }
      expect((await page.getByLabel('MCP project').boundingBox())!.height).toBe(reference.height)
      await expect(roots).toHaveCSS('resize', 'vertical')
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
      await page.locator('.mcp-form').screenshot({ path: testInfo.outputPath(`mcp-fields-${theme}-${width}.png`), animations: 'disabled' })
    }
  }
  await expect(page.getByLabel('Generated MCP configuration')).toContainText('--source-root')
  await page.getByRole('checkbox', { name: /^Configuration/ }).uncheck()
  await expect(roots).toHaveCount(0)
  await page.getByRole('checkbox', { name: /^Configuration/ }).check()
  await expect(roots).toHaveValue('/tmp/store checkout\n/tmp/inventory')
})
