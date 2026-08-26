import assert from 'node:assert/strict'
import test from 'node:test'
import { OrderRepository, createOrdersTableSQL, insertOrderSQL, selectOrderSQL } from './repository.mjs'

test('repository migrates and uses parameterized order queries', async () => {
  const calls = []
  const pool = {
    async query(sql, parameters) {
      calls.push({ sql, parameters })
      if (sql === insertOrderSQL) {
        return { rows: [{ id: 7, sku: 'coffee-mug', quantity: 2, state: 'created', created_at: '2026-08-25T12:00:00Z' }] }
      }
      if (sql === selectOrderSQL) {
        return { rows: [{ id: 7, sku: 'coffee-mug', quantity: 2, state: 'created', created_at: '2026-08-25T12:00:00Z' }] }
      }
      return { rows: [] }
    },
  }
  const repository = new OrderRepository(pool)

  await repository.migrate()
  const created = await repository.create({ sku: 'coffee-mug', quantity: 2 })
  const found = await repository.find(7)

  assert.equal(calls[0].sql, createOrdersTableSQL)
  assert.deepEqual(calls[1], { sql: insertOrderSQL, parameters: ['coffee-mug', 2] })
  assert.deepEqual(calls[2], { sql: selectOrderSQL, parameters: [7] })
  assert.deepEqual(created, {
    id: 7,
    sku: 'coffee-mug',
    quantity: 2,
    state: 'created',
    createdAt: '2026-08-25T12:00:00.000Z',
  })
  assert.deepEqual(found, created)
})

test('repository returns null when an order does not exist', async () => {
  const repository = new OrderRepository({ query: async () => ({ rows: [] }) })
  assert.equal(await repository.find(99), null)
})
