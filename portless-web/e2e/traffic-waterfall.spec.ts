import { expect, test } from '@playwright/test'
import { applicationRequest, authenticate, environmentPath } from './helpers'

test.describe.configure({ mode: 'serial' })

test('renders database transactions as aggregate waterfall spans with command details in the drawer', async ({ page }) => {
  await authenticate(page, environmentPath('traffic'))
  const marker = `/transaction-waterfall-${Date.now()}`
  expect((await applicationRequest(marker)).status).toBe(404)

  const filter = page.getByPlaceholder('filter path, service, edge, status…')
  await filter.fill(marker)
  const row = page.locator('button.trace-row').filter({ hasText: marker }).first()
  await expect(row).toBeVisible()
  await expect(page.getByRole('button', { name: 'SHOW TCP ROOTS' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'SHOW BACKGROUND' })).toHaveCount(0)

  const syntheticExchanges = new Map<number, Record<string, unknown>>()
  const selectRows = Array.from({ length: 12 }, (_, index) => ({
    id: String(42 + index),
    state: index === 0 ? 'created' : index === 1 ? 'paid' : 'queued',
    note: index === 0 ? null : index === 1 ? 'priority' : `note-${42 + index}`,
  }))
  await page.route('**/traffic/exchanges/*', async (route) => {
    const sequence = Number(new URL(route.request().url()).pathname.split('/').pop())
    const exchange = syntheticExchanges.get(sequence)
    if (!exchange) { await route.continue(); return }
    await route.fulfill({ json: exchange })
  })

  await page.route('**/traffic/traces/*', async (route) => {
    const response = await route.fetch()
    const trace = await response.json() as {
      lastSequence: number
      spans: Array<{ exchange: Record<string, unknown>; depth: number; startOffsetMs: number; correlation: string }>
    }
    const root = trace.spans[0]
    const rootExchange = root.exchange as { sequence: number; project: string; environment: string; startedAt: string; completedAt: string }
    const tcpSpan = (offset: number, operation: string, background = false, transactionGroup?: number, source = 'inventory', target = 'inventory-postgres') => {
      const sequence = rootExchange.sequence + offset
      const query = operation === 'UPDATE'
        ? 'UPDATE store_inventory SET on_hand = on_hand - $1 WHERE sku = $2'
        : operation === 'SELECT' ? 'SELECT id, state, note FROM orders ORDER BY id' : operation
      const requestMessages = [
        { type: operation === 'UPDATE' ? 'parse' : 'query', offsetMs: 0, summary: operation === 'UPDATE' ? `Parse ${query}` : operation, wireBytes: 6, content: query, contentType: 'text/x-sql', encoding: 'utf8' },
        ...(operation === 'UPDATE' ? [{ type: 'bind', offsetMs: 0, summary: 'Bind parameters', wireBytes: 20, content: '[1,"coffee-mug"]', contentType: 'application/json', encoding: 'utf8' }] : []),
      ]
      const responseMessages = operation === 'SELECT' ? [
        { type: 'row-description', offsetMs: 0, summary: '3 columns', wireBytes: 24, fields: [{ name: 'column', value: 'id' }, { name: 'column', value: 'state' }, { name: 'column', value: 'note' }] },
        ...selectRows.map((row, index) => ({ type: 'data-row', offsetMs: index + 1, summary: 'Data row', wireBytes: 24, content: JSON.stringify(row), contentType: 'application/json', encoding: 'utf8' })),
        { type: 'command-complete', offsetMs: selectRows.length + 1, summary: 'SELECT 12', wireBytes: 6, fields: [{ name: 'command', value: 'SELECT 12' }] },
      ] : [{ type: 'command-complete', offsetMs: 1, summary: operation === 'UPDATE' ? 'UPDATE 1' : operation, wireBytes: 6, fields: [{ name: 'command', value: operation === 'UPDATE' ? 'UPDATE 1' : operation }] }]
      const exchange = {
        project: rootExchange.project, environment: rootExchange.environment, sequence: rootExchange.sequence + offset,
        protocol: 'tcp', source, target, background,
        startedAt: rootExchange.startedAt, completedAt: rootExchange.completedAt, durationMs: 2,
        requestBytes: 6, responseBytes: 6,
        tcp: {
          kind: 'operation', applicationProtocol: 'postgresql', operation, inspection: 'decoded', outcome: 'success',
          requestMessageCount: requestMessages.length, responseMessageCount: responseMessages.length,
          requestMessages,
          responseMessages,
        },
      }
      syntheticExchanges.set(sequence, exchange)
      return { exchange, parentSequence: rootExchange.sequence, depth: 1, startOffsetMs: offset * 2, correlation: 'inferred', transactionGroup }
    }
    const redisSpan = (offset: number) => {
      const sequence = rootExchange.sequence + offset
      const cachedOrder = JSON.stringify({ id: 56, sku: 'coffee-mug', quantity: 1, state: 'created' })
      const exchange = {
        project: rootExchange.project, environment: rootExchange.environment, sequence,
        protocol: 'tcp', source: 'orders', target: 'orders-redis', background: false,
        startedAt: rootExchange.startedAt, completedAt: rootExchange.completedAt, durationMs: 1,
        requestBytes: 34, responseBytes: cachedOrder.length + 7,
        tcp: {
          kind: 'operation', applicationProtocol: 'redis', operation: 'GET', inspection: 'decoded', outcome: 'success',
          requestMessageCount: 1, responseMessageCount: 1,
          requestMessages: [{ type: 'command', offsetMs: 0, summary: 'GET store:order:56', wireBytes: 34, contentType: 'application/json', encoding: 'utf8', content: JSON.stringify(['GET', 'store:order:56'], null, 2) }],
          responseMessages: [{ type: 'response', offsetMs: 1, summary: `${cachedOrder.length} byte value`, wireBytes: cachedOrder.length + 7, contentType: 'application/json', encoding: 'utf8', content: JSON.stringify(cachedOrder) }],
        },
      }
      syntheticExchanges.set(sequence, exchange)
      return { exchange, parentSequence: rootExchange.sequence, depth: 1, startOffsetMs: offset * 2, correlation: 'inferred' }
    }
    const spans = [
      root,
      tcpSpan(1, 'QUERY', true),
      tcpSpan(2, 'BEGIN', false, 1),
      tcpSpan(3, 'UPDATE', false, 1),
      tcpSpan(4, 'COMMIT', false, 1),
      tcpSpan(5, 'SELECT', false, undefined, 'orders', 'orders-postgres'),
      redisSpan(6),
    ]
    await route.fulfill({ response, json: { ...trace, lastSequence: rootExchange.sequence + 6, spanCount: spans.length, spans } })
  })

  await row.click()
  const waterfall = page.getByRole('region', { name: 'Trace waterfall' })
  const transaction = waterfall.locator('.trace-span--transaction')
  await expect(transaction).toBeVisible()
  const standaloneOperation = waterfall.getByRole('button', { name: /Inspect orders to orders-postgres POSTGRESQL · SELECT/ })
  const redisOperation = waterfall.getByRole('button', { name: /Inspect orders to orders-redis REDIS · GET/ })
  await expect(standaloneOperation).toBeVisible()
  await expect(redisOperation).toBeVisible()
  await expect(transaction).toHaveClass(/trace-span--dependency-summary/)
  await expect(transaction).toContainText('POSTGRESQL · TRANSACTION')
  await expect(transaction.locator('.trace-span__track small')).toHaveText('6ms')
  await expect(waterfall.locator('.trace-span__disclosure')).toHaveCount(0)
  await expect(standaloneOperation).toHaveClass(/trace-span--dependency-summary/)
  const [transactionBackground, standaloneBackground] = await Promise.all([
    transaction.evaluate((element) => getComputedStyle(element).backgroundColor),
    standaloneOperation.evaluate((element) => getComputedStyle(element).backgroundColor),
  ])
  expect(standaloneBackground).toBe(transactionBackground)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · QUERY/ })).toHaveCount(0)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · BEGIN/ })).toHaveCount(0)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · UPDATE/ })).toHaveCount(0)
  await expect(waterfall.getByRole('button', { name: /POSTGRESQL · COMMIT/ })).toHaveCount(0)

  await waterfall.getByRole('button', { name: /Inspect inventory to inventory-postgres POSTGRESQL transaction/ }).click()
  let detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail.locator('.traffic-detail__protocol-badge')).toHaveText('TCP')
  await expect(detail.locator('.traffic-detail__heading .eyebrow')).toHaveCount(0)
  await expect(detail.locator('.traffic-detail__heading h3 > span')).toHaveText('POSTGRESQL')
  await expect(detail.locator('.traffic-detail__heading h3 code')).toHaveText('TRANSACTION')
  await expect(detail.locator('.traffic-detail__transaction-count')).toHaveText('1 command')
  const transactionOverview = detail.getByRole('region', { name: 'Exchange overview' })
  await expect(transactionOverview).toContainText('ENVIRONMENT')
  await expect(transactionOverview).toContainText('TARGET BINDING')
  await expect(transactionOverview).toContainText('STARTED')
  await expect(transactionOverview).toContainText('COMPLETED')
  const transactionCommandTab = detail.getByRole('tab', { name: 'COMMAND', exact: true })
  const transactionResultTab = detail.getByRole('tab', { name: 'RESULT', exact: true })
  await expect(transactionCommandTab).toHaveAttribute('aria-selected', 'true')
  await expect(detail.getByRole('tablist', { name: 'Exchange payload' }).getByRole('tab')).toHaveCount(2)
  await expect(detail.getByRole('tab', { name: 'TCP DETAILS' })).toHaveCount(0)
  const transactionCommand = detail.getByRole('region', { name: 'Command', exact: true })
  await expect(transactionCommand).toContainText("UPDATE store_inventory SET on_hand = on_hand - 1 WHERE sku = 'coffee-mug'")
  await expect(transactionCommand.getByRole('region', { name: 'Bound parameters' })).toHaveCount(0)
  await expect(transactionCommand).not.toContainText('$1')
  await expect(transactionCommand).not.toContainText('$2')
  await expect(transactionCommand).not.toContainText('UPDATE 1')
  await expect(transactionCommand).not.toContainText('BEGIN')
  await expect(transactionCommand).not.toContainText('COMMIT')
  await transactionResultTab.click()
  const transactionResult = detail.getByRole('region', { name: 'Result', exact: true })
  await expect(transactionResult).toContainText('UPDATE 1')
  await expect(transactionResult).not.toContainText('UPDATE store_inventory SET')
  await transactionCommandTab.click()
  const transactionNavigation = detail.getByRole('navigation', { name: 'Trace span navigation' })
  await expect(transactionNavigation.getByRole('button', { name: 'HTTP' })).toHaveAttribute('aria-pressed', 'true')
  await expect(transactionNavigation.locator('output')).toHaveAttribute('aria-label', 'Current span is outside HTTP navigation; 1 HTTP span available')
  await detail.getByRole('button', { name: 'Close traffic details' }).click()

  await standaloneOperation.click()
  detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail.locator('.traffic-detail__protocol-badge')).toHaveText('TCP')
  await expect(detail.locator('.traffic-detail__heading')).toContainText('SELECT')
  await expect(detail.getByRole('region', { name: 'Exchange overview' })).toBeVisible()
  const standaloneCommand = detail.getByRole('region', { name: 'Command', exact: true })
  await expect(standaloneCommand.getByText('COMMAND', { exact: true })).toBeVisible()
  await expect(standaloneCommand).toContainText('SELECT id, state, note FROM orders ORDER BY id')
  await detail.getByRole('tab', { name: 'RESULT', exact: true }).click()
  const standaloneResult = detail.getByRole('region', { name: 'Result', exact: true })
  await expect(standaloneResult.getByText('RESULT', { exact: true })).toBeVisible()
  await expect(standaloneResult).toContainText('12 rows · 3 columns')
  const resultRows = standaloneResult.getByRole('table', { name: 'Database result rows' })
  await expect(resultRows.getByRole('columnheader')).toHaveText(['id', 'state', 'note'])
  const databaseRows = resultRows.getByRole('row')
  await expect(databaseRows).toHaveCount(11)
  await expect(databaseRows.nth(1)).toContainText('42')
  await expect(databaseRows.nth(1)).toContainText('created')
  await expect(databaseRows.nth(1)).toContainText('NULL')
  await expect(databaseRows.nth(2)).toContainText('43')
  await expect(databaseRows.nth(2)).toContainText('paid')
  await expect(databaseRows.nth(2)).toContainText('priority')
  const resultPagination = standaloneResult.getByLabel('database result rows pagination')
  await expect(resultPagination).toContainText('1–10 of 12')
  await resultPagination.getByRole('button', { name: 'Next database result rows page' }).click()
  await expect(resultPagination).toContainText('11–12 of 12')
  await expect(databaseRows).toHaveCount(3)
  await expect(databaseRows.nth(1)).toContainText('52')
  await expect(databaseRows.nth(2)).toContainText('53')
  await expect(resultPagination.getByRole('button', { name: 'Next database result rows page' })).toBeDisabled()
  await expect(standaloneResult.locator('.traffic-json')).toHaveCount(0)
  await expect(standaloneResult.locator('.traffic-semantic-card__body--table')).toHaveCSS('padding', '0px')
  await expect(resultRows.locator('..')).toHaveCSS('border-top-width', '0px')
  await expect(databaseRows.last().locator('td').first()).toHaveCSS('border-bottom-width', '1px')
  const copyCSV = standaloneResult.getByRole('button', { name: 'Copy database results as CSV' })
  await expect(copyCSV).toHaveText('COPY')
  await copyCSV.click()
  const expectedCSV = ['id,state,note', ...selectRows.map((row) => `${row.id},${row.state},${row.note || ''}`)].join('\n')
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(expectedCSV)

  await detail.getByRole('button', { name: 'Close traffic details' }).click()
  await redisOperation.click()
  detail = page.getByRole('dialog', { name: /Traffic request and response/ })
  await expect(detail.locator('.traffic-detail__heading h3 > span')).toHaveText('REDIS')
  await expect(detail.locator('.traffic-detail__heading h3 code')).toHaveText('GET')
  const redisCommand = detail.getByRole('region', { name: 'Command', exact: true })
  await expect(redisCommand.locator('.traffic-redis-command')).toHaveText('GET store:order:56')
  await expect(redisCommand).toContainText('1 argument')
  await expect(redisCommand).not.toContainText('[\n  "GET"')
  await expect(redisCommand.locator('dl')).toHaveCount(0)
  await detail.getByRole('tab', { name: 'RESULT', exact: true }).click()
  const redisResult = detail.getByRole('region', { name: 'Result', exact: true })
  await expect(redisResult.getByText('string', { exact: true })).toBeVisible()
  await expect(redisResult.locator('.traffic-json')).toBeVisible()
  await expect(redisResult).toContainText('coffee-mug')
  await expect(redisResult).not.toContainText('\\"id\\"')
})
