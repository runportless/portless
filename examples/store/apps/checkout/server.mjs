import http from 'node:http'

const port = Number(process.env.PORT || 3000)
const orders = process.env.ORDERS_URL || 'http://127.0.0.1:3001'
const inventory = process.env.INVENTORY_URL || 'http://127.0.0.1:8080'

async function fetchJSON(name, url) {
  const upstream = await fetch(url)
  const value = await upstream.json()
  if (!upstream.ok) throw new Error(`${name} returned ${upstream.status}: ${JSON.stringify(value)}`)
  return value
}

const server = http.createServer(async (request, response) => {
  const url = new URL(request.url || '/', `http://${request.headers.host || 'localhost'}`)
  if (url.pathname === '/health') return response.end('ok')
  if (url.pathname === '/checkout') {
    response.setHeader('content-type', 'application/json')
    const sku = url.searchParams.get('sku') || 'coffee-mug'
    const quantity = Number(url.searchParams.get('quantity') || 1)
    if (!Number.isInteger(quantity) || quantity < 1) {
      response.statusCode = 400
      return response.end(JSON.stringify({ error: 'quantity must be a positive integer' }))
    }
    try {
      const stock = await fetchJSON('inventory', `${inventory}/inventory/${encodeURIComponent(sku)}?quantity=${quantity}`)
      if (!stock.available) {
        response.statusCode = 409
        return response.end(JSON.stringify({ checkout: 'rejected', reason: 'insufficient inventory', inventory: stock }))
      }
      const order = await fetchJSON('orders', `${orders}/orders?sku=${encodeURIComponent(sku)}&quantity=${quantity}`)
      return response.end(JSON.stringify({ checkout: 'accepted', inventory: stock, order }))
    } catch (error) {
      response.statusCode = 502
      return response.end(JSON.stringify({ error: String(error) }))
    }
  }
  response.setHeader('content-type', 'application/json')
  response.end(JSON.stringify({ service: 'checkout', routes: ['/checkout?sku=coffee-mug&quantity=1', '/health'] }))
})

server.listen(port, '127.0.0.1', () => console.log(`checkout ready on ${port}`))
