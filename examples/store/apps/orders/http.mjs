import http from 'node:http'

const maxRequestBytes = 16 * 1024

class RequestError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

function sendJSON(response, status, value) {
  response.statusCode = status
  response.setHeader('content-type', 'application/json')
  response.end(JSON.stringify(value))
}

async function readJSON(request) {
  if (!String(request.headers['content-type'] || '').toLowerCase().startsWith('application/json')) {
    throw new RequestError(415, 'content-type must be application/json')
  }
  const chunks = []
  let size = 0
  for await (const chunk of request) {
    size += chunk.length
    if (size > maxRequestBytes) throw new RequestError(413, 'request body is too large')
    chunks.push(chunk)
  }
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'))
  } catch {
    throw new RequestError(400, 'request body must be valid JSON')
  }
}

function orderInput(value) {
  const sku = typeof value?.sku === 'string' ? value.sku.trim() : ''
  const quantity = value?.quantity
  if (!sku || sku.length > 80) throw new RequestError(400, 'sku must contain between 1 and 80 characters')
  if (!Number.isInteger(quantity) || quantity < 1 || quantity > 100) {
    throw new RequestError(400, 'quantity must be an integer between 1 and 100')
  }
  return { sku, quantity }
}

export function createOrdersServer({ orders, logger = console }) {
  return http.createServer((request, response) => {
    void handleRequest(request, response, orders).catch((error) => {
      if (error instanceof RequestError) {
        sendJSON(response, error.status, { error: error.message })
        return
      }
      logger.error(`orders request failed: ${error?.message || error}`)
      sendJSON(response, 503, { error: 'orders service is temporarily unavailable' })
    })
  })
}

async function handleRequest(request, response, orders) {
  const url = new URL(request.url || '/', `http://${request.headers.host || 'localhost'}`)
  if (request.method === 'GET' && url.pathname === '/health') {
    sendJSON(response, 200, { service: 'orders', ready: true })
    return
  }
  if (request.method === 'POST' && url.pathname === '/orders') {
    const created = await orders.create(orderInput(await readJSON(request)))
    sendJSON(response, 201, created)
    return
  }
  const match = /^\/orders\/(\d+)$/.exec(url.pathname)
  if (request.method === 'GET' && match) {
    const result = await orders.find(Number(match[1]))
    if (!result.order) {
      sendJSON(response, 404, { error: 'order not found', cache: result.status })
      return
    }
    sendJSON(response, 200, { order: result.order, cache: result.status })
    return
  }
  if (url.pathname === '/orders' || match) {
    sendJSON(response, 405, { error: 'method not allowed' })
    return
  }
  sendJSON(response, 200, { service: 'orders', routes: ['POST /orders', 'GET /orders/:id', 'GET /health'] })
}
