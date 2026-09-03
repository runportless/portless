import { mkdirSync, realpathSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { authenticate, environmentHeader, controlAPI, environmentPath } from './helpers'
import { readE2EState } from './state'

test.describe.configure({ mode: 'serial' })

test('creates a cloned environment from the sidebar without duplicating sources', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  await page.getByRole('button', { name: `Create environment in ${state.project}` }).click()
  const dialog = page.getByRole('dialog', { name: 'Create Environment' })
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
  await expect(environmentHeader(page, state.project, 'qa-ui').getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
  const summary = page.getByRole('region', { name: `${state.project}/qa-ui overview summary` })
  await expect(summary.getByRole('heading', { name: 'qa-ui', level: 2, exact: true })).toBeVisible()
  const cloneOrigin = summary.locator('.environment-clone-origin')
  await expect(cloneOrigin).toHaveText('FROM local')
  await expect(cloneOrigin).toHaveAttribute('title', `Created by cloning ${state.project}/local; changes are independent.`)
  await expect(environmentHeader(page, state.project, 'qa-ui').getByRole('link', { name: /health: stopped/ })).toBeVisible()
  await expect(page.locator('.environment-notices')).toHaveCount(0)
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page.getByRole('button', { name: /qa-ui.*stopped/ })).toBeVisible()
  await expect(page.locator('.project-source-row:not(.table-row--header)')).toHaveCount(1)
  await expect(page.locator('.project-sources-panel')).not.toContainText('local, qa-ui')
})

test('forgets a stopped environment from its page header and blocks a running environment', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)

  const localActions = page.getByRole('button', { name: `Environment actions for ${state.project}/${state.environment}` })
  await localActions.click()
  await page.getByRole('menuitem', { name: 'FORGET ENVIRONMENT' }).click()
  let dialog = page.getByRole('alertdialog', { name: `Forget ${state.project}/${state.environment}?` })
  await expect(dialog).toContainText('Stop this environment before forgetting it.')
  await expect(dialog.getByRole('button', { name: 'FORGET ENVIRONMENT', exact: true })).toBeDisabled()
  await dialog.getByRole('button', { name: 'CANCEL' }).click()
  await expect(localActions).toBeFocused()

  const cloneName = 'qa-ui'
  const clonePath = `/environments/${state.project}/${cloneName}`
  await page.getByRole('navigation', { name: `${state.project} environments` }).getByRole('button', { name: new RegExp(`${cloneName}.*stopped`) }).click()
  await expect(page).toHaveURL(new RegExp(`${clonePath}$`))

  const cloneActions = page.getByRole('button', { name: `Environment actions for ${state.project}/${cloneName}` })
  await cloneActions.click()
  const actionMenu = page.getByRole('menu', { name: `${state.project}/${cloneName} actions` })
  await actionMenu.getByRole('menuitem', { name: 'FORGET ENVIRONMENT' }).press('Escape')
  await expect(actionMenu).toHaveCount(0)
  await expect(cloneActions).toBeFocused()

  await cloneActions.click()
  await page.getByRole('menuitem', { name: 'FORGET ENVIRONMENT' }).click()
  dialog = page.getByRole('alertdialog', { name: `Forget ${state.project}/${cloneName}?` })
  await expect(dialog).toContainText('Source files and checkouts on disk are not deleted.')
  await expect(dialog).toContainText('Source checkouts and managed data volumes')
  const confirm = dialog.getByRole('button', { name: 'FORGET ENVIRONMENT', exact: true })
  await expect(confirm).toBeEnabled()
  await confirm.click()

  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.getByRole('navigation', { name: `${state.project} environments` }).getByRole('button', { name: new RegExp(cloneName) })).toHaveCount(0)
  await expect(controlAPI(`/api/v1/environments/${state.project}/${cloneName}`)).rejects.toThrow('404')
})

test('stops one or every running environment from the project page', async ({ page }) => {
  const state = readE2EState()
  await authenticate(page)
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${state.project}$`))

  const environmentRow = () => page.locator('.environment-row-shell--interactive').filter({ has: page.locator(`.environment-row > strong[title="${state.environment}"]`) })
  const environmentActions = () => environmentRow().getByRole('button', { name: `Environment actions for ${state.project}/${state.environment}` })
  const actionMenu = () => page.getByRole('menu', { name: `${state.environment} environment actions` })

  await environmentRow().click()
  await expect(page).toHaveURL(new RegExp(`${environmentPath()}$`))
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: state.project }).click()

  await environmentActions().click()
  await expect(actionMenu()).toBeVisible()
  await expect(actionMenu().getByRole('menuitem')).toHaveText(['RESTART', 'STOP'])
  await actionMenu().getByRole('menuitem', { name: 'RESTART', exact: true }).press('Escape')
  await expect(actionMenu()).toHaveCount(0)
  await expect(environmentActions()).toBeFocused()

  await environmentActions().click()
  await actionMenu().getByRole('menuitem', { name: 'STOP', exact: true }).click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*stopped`) })).toBeVisible({ timeout: 30_000 })

  await environmentActions().click()
  await expect(actionMenu().getByRole('menuitem')).toHaveText(['START'])
  await actionMenu().getByRole('menuitem', { name: 'START', exact: true }).click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*healthy`) })).toBeVisible({ timeout: 30_000 })

  await environmentActions().click()
  await expect(actionMenu().getByRole('menuitem')).toHaveText(['RESTART', 'STOP'])
  await actionMenu().getByRole('menuitem', { name: 'RESTART', exact: true }).press('Escape')

  const stopAll = page.getByRole('button', { name: `Stop all ${state.project} environments` })
  await expect(stopAll).toBeEnabled()
  await stopAll.click()
  await expect(page.getByRole('button', { name: new RegExp(`${state.environment}.*stopped`) })).toBeVisible({ timeout: 30_000 })
  await expect(stopAll).toBeDisabled()

  await environmentActions().click()
  await actionMenu().getByRole('menuitem', { name: 'START', exact: true }).click()
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
  const sourceActions = retainedProjectSource.getByRole('button', { name: 'Source actions for catalog' })
  await sourceActions.click()
  const sourceMenu = page.getByRole('menu', { name: 'catalog source actions' })
  await expect(sourceMenu).toBeVisible()
  await sourceMenu.getByRole('menuitem', { name: 'DELETE' }).click()
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
  await expect(environmentHeader(page)).toContainText('healthy')
})

test('focuses the sidebar on one project while retaining searchable project history', async ({ page }) => {
  const state = readE2EState()
  const archivedProject = 'archive-ui'
  const archivedCheckout = join(state.root, archivedProject)
  mkdirSync(archivedCheckout, { recursive: true })
  writeFileSync(join(archivedCheckout, 'package.json'), JSON.stringify({
    name: archivedProject,
    scripts: { start: 'node server.js' },
    dependencies: { express: '1.0.0' },
  }))
  writeFileSync(join(archivedCheckout, 'server.js'), "require('http').createServer((_request, response) => response.end('archive')).listen(Number(process.env.PORT))\n")
  await controlAPI('/api/v1/projects', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: archivedProject, sources: [{ name: archivedProject, path: realpathSync(archivedCheckout) }] }),
  })

  await authenticate(page)
  await page.reload()
  const sidebar = page.locator('.sidebar')
  await expect(page.getByRole('button', { name: `Current project ${state.project}. Switch project` })).toBeVisible()
  await expect(page.getByRole('navigation', { name: `${state.project} environments` })).toBeVisible()
  await expect(sidebar).not.toContainText(archivedProject)
  await expect(page.getByRole('button', { name: /other project.*running/i })).toBeVisible()

  const currentProjectTrigger = page.getByRole('button', { name: `Current project ${state.project}. Switch project` })
  await currentProjectTrigger.click()
  let switcher = page.getByRole('dialog', { name: 'Switch project' })
  await expect(switcher.getByLabel('Search projects')).toHaveAttribute('placeholder', 'Search')
  await expect(switcher.getByText('RUNNING')).toBeVisible()
  await expect(switcher.getByRole('option', { name: new RegExp(state.debugProject) })).toBeVisible()
  await switcher.getByLabel('Search projects').press('Escape')
  await expect(currentProjectTrigger).toBeFocused()
  await currentProjectTrigger.click()
  switcher = page.getByRole('dialog', { name: 'Switch project' })
  await switcher.getByLabel('Search projects').fill(archivedProject)
  await switcher.getByRole('option', { name: new RegExp(archivedProject) }).click()
  await expect(page).toHaveURL(new RegExp(`/environments/${archivedProject}/local$`))
  await expect(page.getByRole('button', { name: `Current project ${archivedProject}. Switch project` })).toBeVisible()
  await expect(page.getByRole('navigation', { name: `${archivedProject} environments` })).toBeVisible()
  await expect(sidebar).not.toContainText(state.project)

  await page.getByRole('button', { name: `Current project ${archivedProject}. Switch project` }).click()
  switcher = page.getByRole('dialog', { name: 'Switch project' })
  await switcher.getByLabel('Search projects').fill(state.project)
  await switcher.getByRole('option', { name: new RegExp(state.project) }).click()
  await expect(page).toHaveURL(new RegExp(`${environmentPath()}$`))

  await page.getByRole('button', { name: `Current project ${state.project}. Switch project` }).click()
  switcher = page.getByRole('dialog', { name: 'Switch project' })
  await expect(switcher.getByRole('option', { name: new RegExp(archivedProject) })).toBeVisible()
  await switcher.getByRole('button', { name: 'Manage projects' }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.getByLabel('Search projects')).toHaveAttribute('placeholder', 'Search')

  const projectRow = (name: string) => page.locator('.project-registry-row:not(.table-row--header)').filter({ hasText: name })
  const projectNames = () => page.locator('.project-registry-row:not(.table-row--header) .project-registry-row__project strong').allTextContents()
  const defaultNames = await projectNames()
  expect(defaultNames).toEqual([state.project, archivedProject, state.debugProject])
  await expect(page.getByRole('columnheader', { name: /Last opened/ })).toHaveAttribute('aria-sort', 'descending')

  await page.getByRole('button', { name: 'Sort Project ascending' }).click()
  expect(await projectNames()).toEqual([...defaultNames].sort())
  await page.getByRole('button', { name: 'Sort Project descending' }).click()
  expect(await projectNames()).toEqual([...defaultNames].sort().reverse())
  await page.getByRole('button', { name: 'Sort Last opened ascending' }).click()
  expect(await projectNames()).toEqual([state.debugProject, archivedProject, state.project])
  await page.getByRole('button', { name: 'Sort Last opened descending' }).click()
  expect(await projectNames()).toEqual(defaultNames)

  await projectRow(state.project).locator('.project-registry-row__runtime').click()
  await expect(page).toHaveURL(new RegExp(`${environmentPath()}$`))
  await page.goto(`${state.baseURL}/projects`)
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible()

  await projectRow(state.project).getByRole('button', { name: `Project actions for ${state.project}` }).click()
  const projectActions = page.getByRole('menu', { name: `${state.project} actions` })
  await expect(projectActions).toBeVisible()
  await expect(projectActions.getByRole('menuitem')).toHaveText(['CONFIGURE', 'HIDE FROM RECENT', 'FORGET PROJECT'])
  await projectActions.getByRole('menuitem', { name: 'CONFIGURE' }).click()
  await expect(page).toHaveURL(`${state.baseURL}/projects/${encodeURIComponent(state.project)}`)
  await expect(page.getByRole('heading', { name: state.project, exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'CREATE ENVIRONMENT', exact: true })).toBeVisible()

  await page.goto(`${state.baseURL}/projects`)
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible()
  await projectRow(state.project).getByRole('button', { name: `Project actions for ${state.project}` }).click()
  await expect(projectActions).toBeVisible()
  await page.getByRole('heading', { name: 'Projects' }).click()
  await expect(projectActions).toHaveCount(0)

  await projectRow(state.project).getByRole('button', { name: `Project actions for ${state.project}` }).click()
  await page.getByRole('menuitem', { name: 'FORGET PROJECT' }).click()
  let forgetDialog = page.getByRole('alertdialog', { name: `Forget ${state.project}?` })
  await expect(forgetDialog).toContainText(`Stop every environment first: ${state.environment}.`)
  await expect(forgetDialog.getByRole('button', { name: 'FORGET PROJECT', exact: true })).toBeDisabled()
  await forgetDialog.getByRole('button', { name: 'CANCEL' }).click()

  await projectRow(archivedProject).getByRole('button', { name: `Project actions for ${archivedProject}` }).click()
  await page.getByRole('menuitem', { name: 'HIDE FROM RECENT' }).click()
  await page.getByRole('button', { name: /recent/i }).click()
  await expect(projectRow(archivedProject)).toHaveCount(0)

  await page.getByRole('button', { name: `Current project ${state.project}. Switch project` }).click()
  switcher = page.getByRole('dialog', { name: 'Switch project' })
  await switcher.getByLabel('Search projects').fill(archivedProject)
  await expect(switcher.getByRole('option', { name: new RegExp(archivedProject) })).toContainText('HIDDEN')
  await switcher.getByRole('button', { name: 'Close project switcher' }).click()

  await page.getByRole('button', { name: /all/i }).click()
  await page.getByLabel('Search projects').fill(archivedProject)
  await projectRow(archivedProject).getByRole('button', { name: `Project actions for ${archivedProject}` }).click()
  await page.getByRole('menuitem', { name: 'FORGET PROJECT' }).click()
  forgetDialog = page.getByRole('alertdialog', { name: `Forget ${archivedProject}?` })
  await expect(forgetDialog).toContainText('Source files and checkouts on disk are not deleted.')
  await expect(forgetDialog.getByRole('button', { name: 'FORGET PROJECT', exact: true })).toBeEnabled()
  await forgetDialog.getByRole('button', { name: 'FORGET PROJECT', exact: true }).click()
  await expect(forgetDialog).toHaveCount(0)
  await expect(projectRow(archivedProject)).toHaveCount(0)
})

test('paginates the project registry after ten rows and resets the page when controls change', async ({ page }) => {
  const state = readE2EState()
  const projectNames = Array.from({ length: 9 }, (_, index) => `pagination-ui-${String(index + 1).padStart(2, '0')}`)
  const createdProjects: string[] = []
  let cleanupFailure = ''

  try {
    for (const projectName of projectNames) {
      const checkout = join(state.root, projectName)
      mkdirSync(checkout, { recursive: true })
      writeFileSync(join(checkout, 'package.json'), JSON.stringify({
        name: projectName,
        scripts: { start: 'node server.js' },
        dependencies: { express: '1.0.0' },
      }))
      writeFileSync(join(checkout, 'server.js'), "require('http').createServer((_request, response) => response.end('pagination')).listen(Number(process.env.PORT))\n")
      await controlAPI('/api/v1/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: projectName, sources: [{ name: projectName, path: realpathSync(checkout) }] }),
      })
      createdProjects.push(projectName)
    }

    await authenticate(page, '/projects')
    const rows = page.locator('.project-registry-row:not(.table-row--header)')
    const pagination = page.getByLabel('projects pagination')

    await expect(rows).toHaveCount(10)
    await expect(pagination).toContainText('1–10 of 11')
    await expect(page.getByRole('button', { name: 'Previous projects page' })).toBeDisabled()

    await page.getByRole('button', { name: 'Next projects page' }).click()
    await expect(rows).toHaveCount(1)
    await expect(pagination).toContainText('11–11 of 11')
    await expect(page.getByRole('button', { name: 'Next projects page' })).toBeDisabled()

    await page.getByRole('button', { name: 'Sort Project ascending' }).click()
    await expect(rows).toHaveCount(10)
    await expect(pagination).toContainText('1–10 of 11')

    await page.getByRole('button', { name: 'Next projects page' }).click()
    await expect(pagination).toContainText('11–11 of 11')
    await page.getByLabel('Search projects').fill(projectNames[0])
    await expect(rows).toHaveCount(1)
    await expect(rows).toContainText(projectNames[0])
    await expect(pagination).toHaveCount(0)
  } finally {
    for (const projectName of createdProjects.reverse()) {
      const response = await fetch(`${state.baseURL}/api/v1/projects/${encodeURIComponent(projectName)}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${state.token}` },
      })
      if (!response.ok && !cleanupFailure) cleanupFailure = `DELETE ${projectName}: ${response.status} ${await response.text()}`
    }
  }
  expect(cleanupFailure).toBe('')
})
