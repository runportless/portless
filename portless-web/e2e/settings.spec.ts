import { expect, test } from '@playwright/test'
import { authenticate, environmentPath } from './helpers'
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

  await page.getByRole('checkbox', { name: /^Lifecycle/ }).check()
  await page.getByRole('checkbox', { name: /^Sensitive traffic/ }).check()
  await expect(preview).toContainText('--allow-lifecycle')
  await expect(preview).toContainText('--allow-sensitive-traffic')
  await expect(page.locator('.mcp-preview')).toContainText('SENSITIVE · 19 TOOLS')

  await page.getByRole('button', { name: 'COPY' }).click()
  await expect(page.getByRole('button', { name: 'COPIED' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(await preview.textContent())

  await page.goto(`${state.baseURL}${environmentPath()}`)
  await expect(page.getByRole('heading', { name: state.environment, exact: true })).toBeVisible()
  await page.keyboard.press('Control+K')
  const palette = page.getByRole('dialog', { name: 'Command palette' })
  const input = palette.getByPlaceholder('jump to a project or environment')
  await input.fill('Configure MCP')
  await input.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/settings\\?tab=mcp&env=${state.project}%2F${state.environment}$`))
  await expect(page.getByLabel('MCP environment')).toHaveValue(selector)

  await page.reload()
  await expect(page.getByRole('checkbox', { name: /^Lifecycle/ })).not.toBeChecked()
  await expect(page.getByRole('checkbox', { name: /^Sensitive traffic/ })).not.toBeChecked()
})

