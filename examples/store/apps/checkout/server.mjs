import { createCheckoutServer } from './http.mjs'

const port = Number(process.env.PORT || 3000)
const ordersURL = process.env.ORDERS_URL || 'http://127.0.0.1:3001'
const inventoryURL = process.env.INVENTORY_URL || 'http://127.0.0.1:8080'

const server = createCheckoutServer({ inventoryURL, ordersURL })
server.listen(port, '127.0.0.1', () => console.log(`checkout ready on ${port}`))

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => server.close(() => process.exit(0)))
}
