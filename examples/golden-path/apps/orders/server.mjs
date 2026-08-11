import http from 'node:http'

const port = Number(process.env.PORT || 3001)
const server = http.createServer((request, response) => {
  response.setHeader('content-type', 'application/json')
  if (request.url === '/orders') return response.end(JSON.stringify({ number: 42, state: 'created' }))
  response.end(JSON.stringify({ service: 'orders', databaseBound: Boolean(process.env.DATABASE_URL), cacheBound: Boolean(process.env.REDIS_URL) }))
})

server.listen(port, '127.0.0.1', () => console.log(`orders ready on ${port}`))
