import pg from 'pg'
import { createClient } from 'redis'
import { OrderCache } from './cache.mjs'
import { createOrdersServer } from './http.mjs'
import { Orders } from './orders.mjs'
import { OrderRepository } from './repository.mjs'

const port = Number(process.env.PORT || 3001)
const databaseURL = process.env.DATABASE_URL
const redisURL = process.env.REDIS_URL

if (!databaseURL) throw new Error('DATABASE_URL is required')
if (!redisURL) throw new Error('REDIS_URL is required')

const pool = new pg.Pool({
  connectionString: databaseURL,
  application_name: 'portless-store-orders',
  max: 4,
  connectionTimeoutMillis: 2_000,
  idleTimeoutMillis: 30_000,
})
const redis = createClient({
  url: redisURL,
  disableOfflineQueue: true,
  maintNotifications: 'disabled',
  socket: { connectTimeout: 2_000 },
})
redis.on('error', (error) => console.error(`orders cache connection: ${error.message}`))

const repository = new OrderRepository(pool)
const cache = new OrderCache(redis)
const server = createOrdersServer({ orders: new Orders(repository, cache) })

let stopping = false
async function stop() {
  if (stopping) return
  stopping = true
  await new Promise((resolve) => server.close(resolve))
  await Promise.allSettled([
    pool.end(),
    redis.isOpen ? redis.close() : Promise.resolve(),
  ])
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => {
    void stop().finally(() => process.exit(0))
  })
}

try {
  await Promise.all([repository.migrate(), redis.connect()])
  server.listen(port, '127.0.0.1', () => console.log(`orders ready on ${port}`))
} catch (error) {
  console.error(`orders failed to start: ${error?.message || error}`)
  await Promise.allSettled([pool.end(), redis.isOpen ? redis.close() : Promise.resolve()])
  process.exitCode = 1
}
