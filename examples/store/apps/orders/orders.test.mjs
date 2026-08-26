import assert from 'node:assert/strict'
import test from 'node:test'
import { OrderCache, orderCacheKey } from './cache.mjs'
import { Orders } from './orders.mjs'

function memoryRedis() {
  const values = new Map()
  const calls = []
  return {
    values,
    calls,
    async get(key) {
      calls.push(['get', key])
      return values.get(key) ?? null
    },
    async set(key, value, options) {
      calls.push(['set', key, value, options])
      values.set(key, value)
    },
    async del(key) {
      calls.push(['del', key])
      return values.delete(key) ? 1 : 0
    },
  }
}

test('orders reads through PostgreSQL once and then serves the Redis cache', async () => {
  const order = { id: 9, sku: 'coffee-mug', quantity: 2, state: 'created', createdAt: '2026-08-25T12:00:00.000Z' }
  const redis = memoryRedis()
  let databaseReads = 0
  const orders = new Orders({
    async find(id) {
      databaseReads++
      assert.equal(id, 9)
      return order
    },
  }, new OrderCache(redis, 30))

  assert.deepEqual(await orders.find(9), { status: 'miss', order })
  assert.deepEqual(await orders.find(9), { status: 'hit', order })
  assert.equal(databaseReads, 1)
  assert.deepEqual(redis.calls[1], ['set', orderCacheKey(9), JSON.stringify(order), { EX: 30 }])
})

test('orders falls back to PostgreSQL when Redis is unavailable', async () => {
  const order = { id: 4, sku: 'keyboard', quantity: 1, state: 'created', createdAt: '2026-08-25T12:00:00.000Z' }
  const cache = {
    read: async () => ({ status: 'unavailable', order: null }),
    write: async () => false,
  }
  const orders = new Orders({ find: async () => order }, cache)
  assert.deepEqual(await orders.find(4), { status: 'unavailable', order })
})

test('creating an order persists it before invalidating its cache key', async () => {
  const calls = []
  const order = { id: 12, sku: 'coffee-mug', quantity: 1, state: 'created', createdAt: '2026-08-25T12:00:00.000Z' }
  const orders = new Orders({
    async create(input) {
      calls.push(['create', input])
      return order
    },
  }, {
    async remove(id) {
      calls.push(['remove', id])
    },
  })
  assert.deepEqual(await orders.create({ sku: 'coffee-mug', quantity: 1 }), order)
  assert.deepEqual(calls, [['create', { sku: 'coffee-mug', quantity: 1 }], ['remove', 12]])
})
