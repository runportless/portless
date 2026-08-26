import http from 'node:http'
import { checkoutPageHTML, checkoutPageJavaScript } from './page.mjs'
import { forwardedTraceHeaders } from './trace.mjs'

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

function sendText(response, contentType, value) {
  response.statusCode = 200
  response.setHeader('cache-control', 'no-store')
  response.setHeader('content-type', contentType)
  response.setHeader('x-content-type-options', 'nosniff')
  response.end(value)
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

function checkoutInput(value) {
  const sku = typeof value?.sku === 'string' ? value.sku.trim() : ''
  const quantity = value?.quantity
  if (!sku || sku.length > 80) throw new RequestError(400, 'sku must contain between 1 and 80 characters')
  if (!Number.isInteger(quantity) || quantity < 1 || quantity > 100) {
    throw new RequestError(400, 'quantity must be an integer between 1 and 100')
  }
  return { sku, quantity }
}

export async function fetchJSON(url, { method = 'GET', headers = {}, body } = {}) {
  const outgoingHeaders = { ...headers }
  let encodedBody
  if (body !== undefined) {
    outgoingHeaders['content-type'] = 'application/json'
    encodedBody = JSON.stringify(body)
  }
  const response = await fetch(url, {
    method,
    headers: outgoingHeaders,
    body: encodedBody,
    signal: AbortSignal.timeout(3_000),
  })
  const encoded = await response.text()
  let value = {}
  if (encoded) {
    try {
      value = JSON.parse(encoded)
    } catch {
      value = { message: encoded }
    }
  }
  return { status: response.status, value }
}

export function createCheckoutServer({ inventoryURL, ordersURL, requestJSON = fetchJSON, logger = console }) {
  return http.createServer((request, response) => {
    void handleRequest(request, response, { inventoryURL, ordersURL, requestJSON, logger }).catch((error) => {
      if (error instanceof RequestError) {
        sendJSON(response, error.status, { error: error.message })
        return
      }
      logger.error(`checkout request failed: ${error?.message || error}`)
      sendJSON(response, 502, { error: 'checkout dependency is temporarily unavailable' })
    })
  })
}

async function handleRequest(request, response, dependencies) {
  const url = new URL(request.url || '/', `http://${request.headers.host || 'localhost'}`)
  if (request.method === 'GET' && url.pathname === '/') {
    response.setHeader('content-security-policy', "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
    sendText(response, 'text/html; charset=utf-8', checkoutPageHTML)
    return
  }
  if (request.method === 'GET' && url.pathname === '/checkout.js') {
    sendText(response, 'text/javascript; charset=utf-8', checkoutPageJavaScript)
    return
  }
  if (request.method === 'GET' && url.pathname === '/favicon.ico') {
    response.statusCode = 204
    response.end()
    return
  }
  if (request.method === 'GET' && url.pathname === '/health') {
    sendJSON(response, 200, { service: 'checkout', ready: true })
    return
  }
  if (request.method === 'POST' && url.pathname === '/checkout') {
    const input = checkoutInput(await readJSON(request))
    const traceHeaders = forwardedTraceHeaders(request.headers)
    const inventory = await dependencies.requestJSON(
      `${dependencies.inventoryURL}/inventory/${encodeURIComponent(input.sku)}/reservations`,
      { method: 'POST', headers: traceHeaders, body: { quantity: input.quantity } },
    )
    if (inventory.status === 404) {
      sendJSON(response, 404, { checkout: 'rejected', reason: 'unknown sku', inventory: inventory.value })
      return
    }
    if (inventory.status === 409) {
      sendJSON(response, 409, { checkout: 'rejected', reason: 'insufficient inventory', inventory: inventory.value })
      return
    }
    if (inventory.status !== 201) throw new Error(`inventory returned ${inventory.status}`)

    let order
    try {
      order = await dependencies.requestJSON(`${dependencies.ordersURL}/orders`, {
        method: 'POST',
        headers: traceHeaders,
        body: input,
      })
      if (order.status !== 201) throw new Error(`orders returned ${order.status}`)
    } catch (error) {
      await releaseInventoryReservation(dependencies, inventory.value.reservation?.id, traceHeaders)
      throw error
    }
    sendJSON(response, 201, {
      checkout: 'accepted',
      inventory: inventory.value.inventory,
      reservation: inventory.value.reservation,
      order: order.value,
    })
    return
  }
  const orderMatch = /^\/orders\/(\d+)$/.exec(url.pathname)
  if (request.method === 'GET' && orderMatch) {
    const result = await dependencies.requestJSON(`${dependencies.ordersURL}${url.pathname}`, {
      headers: forwardedTraceHeaders(request.headers),
    })
    if (result.status === 404) {
      sendJSON(response, 404, result.value)
      return
    }
    if (result.status !== 200) throw new Error(`orders returned ${result.status}`)
    sendJSON(response, 200, result.value)
    return
  }
  if (url.pathname === '/checkout' || orderMatch) {
    sendJSON(response, 405, { error: 'method not allowed' })
    return
  }
  sendJSON(response, 200, { service: 'checkout', routes: ['GET /', 'POST /checkout', 'GET /orders/:id', 'GET /health'] })
}

async function releaseInventoryReservation(dependencies, reservationID, traceHeaders) {
  if (!Number.isInteger(reservationID)) return
  try {
    const released = await dependencies.requestJSON(
      `${dependencies.inventoryURL}/inventory/reservations/${reservationID}/release`,
      { method: 'POST', headers: traceHeaders },
    )
    if (released.status !== 200) {
      dependencies.logger.error(`inventory reservation ${reservationID} release returned ${released.status}`)
    }
  } catch (error) {
    dependencies.logger.error(`inventory reservation ${reservationID} release failed: ${error?.message || error}`)
  }
}
