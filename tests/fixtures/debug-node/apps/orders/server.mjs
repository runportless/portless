import http from 'node:http'

const port = Number(process.env.PORT || 3001)
const server = http.createServer((request, response) => {
  if (request.url === '/health') {
    response.writeHead(204).end()
    return
  }
  if (request.url === '/orders') {
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ number: 42, state: 'created' }))
    return
  }
  response.writeHead(404).end()
})

server.listen(port, '127.0.0.1', () => console.log(`orders ready on ${port}`))
