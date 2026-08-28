import { mkdirSync, realpathSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { authenticate, controlAPI, environmentPath } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('creates a cloned environment from the project modal without duplicating sources', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()

  await page.getByRole('button', { name: 'CREATE ENVIRONMENT' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create environment' })
  const name = dialog.getByLabel('NAME')
  await expect(name).toBeFocused()
  await expect(name).toHaveValue('')
  await expect(name).toHaveAttribute('placeholder', 'qa-local')
  await expect(name).toHaveAttribute('autocomplete', 'off')
  const create = dialog.getByRole('button', { name: 'CREATE ENVIRONMENT', exact: true })
  await expect(create).toBeDisabled()
  await name.fill('qa-ui')
  await create.click()

  await expect(page).toHaveURL(new RegExp(`/environments/${state.project}/qa-ui$`))
  await expect(page.getByRole('heading', { name: 'qa-ui', exact: true })).toBeVisible()
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page.getByRole('button', { name: /qa-ui.*stopped/ })).toBeVisible()
  await expect(page.locator('.project-source-row:not(.table-row--header)')).toHaveCount(1)
  await expect(page.locator('.project-sources-panel')).not.toContainText('local, qa-ui')
})

test('stops one or every running environment from the project page', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${state.project}$`))

  const stopEnvironment = page.getByRole('button', { name: `Stop ${state.environment}`, exact: true })
  await expect(stopEnvironment).toBeEnabled()
  await stopEnvironment.click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*stopped`) })).toBeVisible({ timeout: 30_000 })
  const startEnvironment = () => page.getByRole('button', { name: `Start ${state.environment}`, exact: true })
  await expect(startEnvironment()).toBeEnabled()

  await startEnvironment().click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*healthy`) })).toBeVisible({ timeout: 30_000 })

  const stopAll = page.getByRole('button', { name: `Stop all ${state.project} environments` })
  await expect(stopAll).toBeEnabled()
  await stopAll.click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*stopped`) })).toBeVisible({ timeout: 30_000 })
  await expect(stopAll).toBeDisabled()
  await expect(startEnvironment()).toBeEnabled()

  await startEnvironment().click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*healthy`) })).toBeVisible({ timeout: 30_000 })
})

test('manages project sources separately from environment checkouts', async ({ page }) => {
  const state = readE2EState()
  const catalogSource = join(state.root, 'catalog-source')
  const catalogWorktree = join(state.root, 'catalog-worktree')
  mkdirSync(catalogSource, { recursive: true })
  mkdirSync(catalogWorktree, { recursive: true })
  writeFileSync(join(catalogSource, 'package.json'), JSON.stringify({
    name: 'catalog',
    scripts: { start: 'node server.js' },
    dependencies: { express: '1.0.0' },
  }))
  writeFileSync(join(catalogSource, 'server.js'), "require('http').createServer((_request, response) => response.end('catalog')).listen(Number(process.env.PORT))\n")
  writeFileSync(join(catalogWorktree, 'package.json'), JSON.stringify({
    name: 'catalog',
    scripts: { start: 'node server.js' },
    dependencies: { express: '1.0.0' },
  }))
  writeFileSync(join(catalogWorktree, 'server.js'), "require('http').createServer((_request, response) => response.end('catalog worktree')).listen(Number(process.env.PORT))\n")

  const pickerInitialPaths: string[] = []
  await page.route('**/api/v1/system/directories/select', async (route) => {
    const input = route.request().postDataJSON() as { initialPath?: string }
    pickerInitialPaths.push(input.initialPath || '')
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ path: realpathSync(catalogWorktree) }),
    })
  })

  await authenticate(page, environmentPath('bindings'))
  await page.getByRole('button', { name: 'STOP ALL' }).click()
  await expect(page.getByRole('button', { name: 'START ALL' })).toBeVisible({ timeout: 30_000 })

  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${state.project}$`))

  await page.getByRole('button', { name: 'ADD SOURCE', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Add source' })
  await expect(dialog.getByLabel('NAME', { exact: true })).toBeFocused()
  await expect(dialog.getByRole('button', { name: 'BROWSE…' })).toBeVisible()
  await dialog.getByLabel('NAME', { exact: true }).fill('catalog')
  await dialog.getByLabel('Initial environment').selectOption(state.environment)
  await dialog.getByLabel('INITIAL CHECKOUT PATH', { exact: true }).fill(catalogSource)
  await dialog.getByRole('button', { name: 'ADD SOURCE', exact: true }).click()
  await expect(dialog).toHaveCount(0, { timeout: 30_000 })

  const projectSource = page.locator('.project-source-row:not(.table-row--header)').filter({ hasText: 'catalog' })
  await expect(projectSource).toBeVisible()
  await expect(projectSource.locator('code')).toHaveText(realpathSync(catalogSource))
  await expect(projectSource.locator('small')).toHaveCount(0)

  await page.goto(`${state.baseURL}${environmentPath('bindings')}`)
  const checkout = page.locator('.source-checkouts-panel .source-table tbody tr').filter({ hasText: 'catalog' })
  await expect(checkout).toBeVisible()
  await expect(checkout.locator('code')).toHaveText(realpathSync(catalogSource))
  await expect(checkout.locator('time')).toHaveAttribute('datetime', /^\d{4}-\d{2}-\d{2}T/)
  const createdAt = await checkout.locator('time').getAttribute('datetime')
  await checkout.getByRole('button', { name: 'EDIT', exact: true }).click()
  const editDialog = page.getByRole('dialog', { name: 'Edit catalog' })
  await expect(editDialog.getByLabel('CHECKOUT PATH')).toHaveValue(realpathSync(catalogSource))
  await editDialog.getByRole('button', { name: 'BROWSE…' }).click()
  await expect(editDialog.getByLabel('CHECKOUT PATH')).toHaveValue(realpathSync(catalogWorktree))
  expect(pickerInitialPaths).toEqual([realpathSync(catalogSource)])
  await editDialog.getByRole('button', { name: 'SAVE CHANGES' }).click()
  await expect(editDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(checkout.locator('code')).toHaveText(realpathSync(catalogWorktree))
  await expect(checkout.locator('time')).toHaveAttribute('datetime', createdAt || '')

  const catalogProvider = page.locator('.configured-providers-panel .provider-row:not(.provider-row--header)').filter({ hasText: 'catalog' })
  await catalogProvider.getByRole('button', { name: 'EDIT', exact: true }).click()
  const providerDialog = page.getByRole('dialog', { name: 'Configure Provider' })
  await providerDialog.getByLabel('Provider', { exact: true }).selectOption('remote')
  await providerDialog.getByLabel('Remote URL', { exact: true }).fill('https://catalog.qa.example.com')
  await providerDialog.getByLabel('Classification', { exact: true }).selectOption('qa')
  await providerDialog.getByLabel('Write policy', { exact: true }).selectOption('read-only')
  await providerDialog.getByRole('button', { name: 'SAVE CHANGES', exact: true }).click()
  await expect(providerDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(catalogProvider.locator('.provider-kind')).toHaveText('Remote')

  await checkout.getByRole('button', { name: 'REMOVE', exact: true }).click()
  const removeDialog = page.getByRole('alertdialog', { name: 'Remove catalog checkout?' })
  await expect(removeDialog).toContainText(`The project source, its services, and every other environment stay unchanged.`)
  await removeDialog.getByRole('button', { name: 'REMOVE CHECKOUT', exact: true }).click()
  await expect(removeDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(checkout).toContainText('Not configured')
  await expect(checkout.getByRole('button', { name: 'CONFIGURE', exact: true })).toBeVisible()
  await expect(checkout.locator('code')).toHaveCount(0)

  let project = await controlAPI<{ sources: Array<{ name: string }> }>(`/api/v1/projects/${state.project}`)
  expect(project.sources.map((item) => item.name)).toContain('catalog')

  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  const retainedProjectSource = page.locator('.project-source-row:not(.table-row--header)').filter({ hasText: 'catalog' })
  await expect(retainedProjectSource).toContainText('not bound locally')
  await retainedProjectSource.getByRole('button', { name: 'DELETE' }).click()
  const deleteDialog = page.getByRole('alertdialog', { name: 'Delete catalog?' })
  await expect(deleteDialog).toContainText('catalog')
  await deleteDialog.getByRole('button', { name: 'DELETE SOURCE', exact: true }).click()
  await expect(deleteDialog).toHaveCount(0, { timeout: 30_000 })
  await expect(retainedProjectSource).toHaveCount(0)

  project = await controlAPI<{ sources: Array<{ name: string }> }>(`/api/v1/projects/${state.project}`)
  expect(project.sources.map((item) => item.name)).not.toContain('catalog')
  expect(project.sources.length).toBeGreaterThan(0)

  await page.goto(`${state.baseURL}${environmentPath()}`)
  await page.getByRole('button', { name: 'START ALL' }).click()
  await expect(page.getByRole('button', { name: 'STOP ALL' })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByRole('heading', { name: state.environment, exact: true }).locator('..')).toContainText('healthy')
})
