import process from 'node:process'
import { connect } from '@nats-io/transport-node'
import Fastify from 'fastify'
import { EventBuffer } from './events.mjs'

const port = Number(process.env.PORT || 4202)
const natsURL = process.env.NATS_URL
if (!natsURL) throw new Error('NATS_URL is required')

const app = Fastify({ logger: { level: 'info' } })
const events = new EventBuffer(50)
const nats = await connect({ servers: natsURL, timeout: 5_000, reconnectTimeWait: 1_000, maxReconnectAttempts: 10 })
const subscription = nats.subscribe('dispatch.delivery.*')

void (async () => {
  for await (const message of subscription) {
    try {
      const event = message.json()
      events.add(event)
      app.log.info({ event: 'received', type: event.type, deliveryId: event.deliveryId, traceId: event.traceId })
    } catch (error) {
      app.log.warn({ event: 'invalid_message', error: String(error) })
    }
  }
})()

app.addHook('onSend', async (_request, reply) => {
  reply.header('X-Dispatch-Service', 'notifier')
})

app.get('/health', async () => ({ service: 'notifier', ready: !nats.isClosed() }))
app.get('/events', async (request) => {
  const requested = Number(request.query?.limit || 20)
  return { events: events.list(requested) }
})

const stop = async () => {
  subscription.unsubscribe()
  await nats.drain().catch(() => undefined)
  await app.close()
}
process.once('SIGTERM', () => void stop())
process.once('SIGINT', () => void stop())

await app.listen({ host: '127.0.0.1', port })
