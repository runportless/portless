import http from 'node:http'

const port = Number(process.env.PORT || 3000)
const orders = process.env.ORDERS_URL || 'http://127.0.0.1:3001'

const server = http.createServer(async (request, response) => {
  if (request.url === '/health') return response.end('ok')
  if (request.url === '/checkout') {
    try {
      const upstream = await fetch(`${orders}/orders`)
      const value = await upstream.json()
      response.setHeader('content-type', 'application/json')
      return response.end(JSON.stringify({ checkout: 'accepted', order: value }))
    } catch (error) {
      response.statusCode = 502
      return response.end(JSON.stringify({ error: String(error) }))
    }
  }
  response.setHeader('content-type', 'application/json')
  response.end(JSON.stringify({ service: 'gateway', routes: ['/checkout', '/health'] }))
})

server.listen(port, '127.0.0.1', () => console.log(`gateway ready on ${port}`))
