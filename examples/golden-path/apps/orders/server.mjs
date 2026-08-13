import http from 'node:http'
import net from 'node:net'

const port = Number(process.env.PORT || 3001)
const database = process.env.DATABASE_URL
const cache = process.env.REDIS_URL

function exchange(name, rawURL, defaultPort, payload) {
  if (!rawURL) return Promise.resolve(Buffer.from('not configured'))
  const target = new URL(rawURL)
  const targetPort = Number(target.port || defaultPort)
  return new Promise((resolve, reject) => {
    let settled = false
    const socket = net.createConnection({ host: target.hostname, port: targetPort })
    const fail = (error) => {
      if (settled) return
      settled = true
      socket.destroy()
      reject(new Error(`${name}: ${error.message}`))
    }
    socket.setTimeout(2_000)
    socket.once('connect', () => socket.write(payload))
    socket.once('data', (reply) => {
      if (settled) return
      settled = true
      socket.end()
      resolve(reply)
    })
    socket.once('timeout', () => fail(new Error('timed out')))
    socket.once('error', fail)
    socket.once('close', () => {
      if (!settled) fail(new Error('closed without a response'))
    })
  })
}

async function checkPostgres() {
  if (!database) return 'not configured'
  const sslRequest = Buffer.alloc(8)
  sslRequest.writeInt32BE(8, 0)
  sslRequest.writeInt32BE(80877103, 4)
  const reply = await exchange('postgres', database, 5432, sslRequest)
  const mode = reply.toString('ascii', 0, 1)
  if (mode !== 'S' && mode !== 'N') throw new Error('postgres: unexpected handshake response')
  return 'reachable'
}

async function checkRedis() {
  if (!cache) return 'not configured'
  const reply = await exchange('redis', cache, 6379, Buffer.from('*1\r\n$4\r\nPING\r\n'))
  if (!reply.toString().startsWith('+PONG')) throw new Error('redis: unexpected PING response')
  return 'PONG'
}

const server = http.createServer(async (request, response) => {
  response.setHeader('content-type', 'application/json')
  if (request.url === '/orders') {
    try {
      const [postgres, redis] = await Promise.all([checkPostgres(), checkRedis()])
      return response.end(JSON.stringify({ number: 42, state: 'created', dependencies: { postgres, redis } }))
    } catch (error) {
      response.statusCode = 503
      return response.end(JSON.stringify({ error: String(error) }))
    }
  }
  response.end(JSON.stringify({ service: 'orders', databaseBound: Boolean(process.env.DATABASE_URL), cacheBound: Boolean(process.env.REDIS_URL) }))
})

server.listen(port, '127.0.0.1', () => console.log(`orders ready on ${port}`))
