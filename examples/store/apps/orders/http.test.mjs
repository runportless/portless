import assert from 'node:assert/strict'
import test from 'node:test'
import { createOrdersServer } from './http.mjs'

async function withServer(orders, run) {
  const server = createOrdersServer({ orders, logger: { error() {} } })
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  try {
    await run(`http://127.0.0.1:${port}`)
  } finally {
    await new Promise((resolve) => server.close(resolve))
  }
}

test('orders HTTP API creates and retrieves durable-shaped orders', async () => {
  const created = { id: 3, sku: 'coffee-mug', quantity: 2, state: 'created', createdAt: '2026-08-25T12:00:00.000Z' }
  const calls = []
  await withServer({
    async create(input) {
      calls.push(['create', input])
      return created
    },
    async find(id) {
      calls.push(['find', id])
      return { status: 'miss', order: created }
    },
  }, async (origin) => {
    const create = await fetch(`${origin}/orders`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sku: 'coffee-mug', quantity: 2 }),
    })
    assert.equal(create.status, 201)
    assert.deepEqual(await create.json(), created)

    const find = await fetch(`${origin}/orders/3`)
    assert.equal(find.status, 200)
    assert.deepEqual(await find.json(), { cache: 'miss', order: created })
  })
  assert.deepEqual(calls, [['create', { sku: 'coffee-mug', quantity: 2 }], ['find', 3]])
})

test('orders HTTP API validates writes and reports missing orders', async () => {
  await withServer({
    create: async () => assert.fail('invalid input reached the order service'),
    find: async () => ({ status: 'miss', order: null }),
  }, async (origin) => {
    const invalid = await fetch(`${origin}/orders`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sku: '', quantity: 0 }),
    })
    assert.equal(invalid.status, 400)

    const missing = await fetch(`${origin}/orders/404`)
    assert.equal(missing.status, 404)
    assert.deepEqual(await missing.json(), { error: 'order not found', cache: 'miss' })
  })
})
