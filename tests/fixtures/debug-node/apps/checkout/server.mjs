import http from 'node:http'

const port = Number(process.env.PORT || 3000)
const orders = (process.env.ORDERS_URL || 'http://127.0.0.1:3001').replace(/\/$/, '')

const server = http.createServer(async (request, response) => {
  if (request.url === '/health') {
    response.writeHead(204).end()
    return
  }
  if (request.url === '/checkout') {
    try {
      const upstream = await fetch(`${orders}/orders`)
      const order = await upstream.json()
      response.writeHead(upstream.ok ? 200 : 502, { 'content-type': 'application/json' })
      response.end(JSON.stringify({ checkout: 'accepted', order }))
    } catch (error) {
      response.writeHead(502, { 'content-type': 'application/json' })
      response.end(JSON.stringify({ error: String(error) }))
    }
    return
  }
  response.writeHead(404).end()
})

server.listen(port, '127.0.0.1', () => console.log(`checkout ready on ${port}`))
