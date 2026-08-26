import assert from 'node:assert/strict'
import test from 'node:test'
import { createCheckoutServer } from './http.mjs'

async function withServer(options, run) {
  const server = createCheckoutServer({ ...options, logger: { error() {} } })
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  try {
    await run(`http://127.0.0.1:${port}`)
  } finally {
    await new Promise((resolve) => server.close(resolve))
  }
}

test('checkout serves a browser page that exercises the JSON POST endpoint', async () => {
  await withServer({
    inventoryURL: 'http://inventory',
    ordersURL: 'http://orders',
    requestJSON: async () => assert.fail('browser assets must not call dependencies'),
  }, async (origin) => {
    const pageResponse = await fetch(`${origin}/`)
    assert.equal(pageResponse.status, 200)
    assert.match(pageResponse.headers.get('content-type') || '', /^text\/html;/)
    assert.match(pageResponse.headers.get('content-security-policy') || '', /default-src 'none'/)
    const page = await pageResponse.text()
    assert.match(page, /<form id="checkout-form">/)
    assert.match(page, /value="coffee-mug"/)
    assert.match(page, /value="usb-c-cable"/)
    assert.match(page, /id="theme-toggle"/)
    assert.match(page, /class="brand__signal"/)
    assert.match(page, /id="request-preview"/)
    assert.match(page, /src="\/checkout\.js"/)
    assert.doesNotMatch(page, /PRIMARY SERVICE/)
    assert.doesNotMatch(page, /POST \/checkout/)
    assert.doesNotMatch(page, /aria-label="Breadcrumb"/)
    assert.doesNotMatch(page, /checkout\.local\.store\.localhost/)
    assert.doesNotMatch(page, /class="ready-state"/)
    assert.match(page, /Create an order and view the response\./)

    const scriptResponse = await fetch(`${origin}/checkout.js`)
    assert.equal(scriptResponse.status, 200)
    assert.match(scriptResponse.headers.get('content-type') || '', /^text\/javascript;/)
    const script = await scriptResponse.text()
    assert.match(script, /fetch\('\/checkout'/)
    assert.match(script, /method: 'POST'/)
    assert.match(script, /'content-type': 'application\/json'/)
    assert.match(script, /body: JSON\.stringify\(payload\)/)
    assert.match(script, /output\.textContent/)
    assert.match(script, /window\.localStorage\.setItem\(themeStorageKey, next\)/)
    assert.match(script, /root\.dataset\.theme = theme/)
    assert.match(script, /themeToggle\.addEventListener\('click'/)
  })
})

test('checkout validates inventory and creates an order with propagated trace context', async () => {
  const calls = []
  const created = { id: 6, sku: 'coffee-mug', quantity: 2, state: 'created', createdAt: '2026-08-25T12:00:00.000Z' }
  const reservation = { id: 11, sku: 'coffee-mug', quantity: 2, state: 'reserved', createdAt: '2026-08-25T11:59:59.000Z' }
  const inventory = { sku: 'coffee-mug', name: 'Ceramic Coffee Mug', onHand: 22, warehouse: 'ord-01' }
  await withServer({
    inventoryURL: 'http://inventory',
    ordersURL: 'http://orders',
    async requestJSON(url, options) {
      calls.push({ url, options })
      if (url.startsWith('http://inventory')) return { status: 201, value: { reservation, inventory } }
      return { status: 201, value: created }
    },
  }, async (origin) => {
    const response = await fetch(`${origin}/checkout`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        traceparent: '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01',
      },
      body: JSON.stringify({ sku: 'coffee-mug', quantity: 2 }),
    })
    assert.equal(response.status, 201)
    assert.deepEqual(await response.json(), {
      checkout: 'accepted',
      inventory,
      reservation,
      order: created,
    })
  })
  assert.equal(calls.length, 2)
  assert.equal(calls[0].url, 'http://inventory/inventory/coffee-mug/reservations')
  assert.equal(calls[0].options.method, 'POST')
  assert.deepEqual(calls[0].options.body, { quantity: 2 })
  assert.equal(calls[1].url, 'http://orders/orders')
  assert.equal(calls[1].options.method, 'POST')
  assert.deepEqual(calls[1].options.body, { sku: 'coffee-mug', quantity: 2 })
  assert.equal(calls[1].options.headers.traceparent, '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01')
})

test('checkout rejects unavailable inventory without creating an order', async () => {
  let requests = 0
  await withServer({
    inventoryURL: 'http://inventory',
    ordersURL: 'http://orders',
    async requestJSON() {
      requests++
      return { status: 409, value: { sku: 'usb-c-cable', available: false, onHand: 0 } }
    },
  }, async (origin) => {
    const response = await fetch(`${origin}/checkout`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sku: 'usb-c-cable', quantity: 1 }),
    })
    assert.equal(response.status, 409)
  })
  assert.equal(requests, 1)
})

test('checkout releases reserved inventory when order creation fails', async () => {
  const calls = []
  await withServer({
    inventoryURL: 'http://inventory',
    ordersURL: 'http://orders',
    async requestJSON(url, options) {
      calls.push({ url, options })
      if (url.endsWith('/reservations')) {
        return {
          status: 201,
          value: {
            reservation: { id: 17, sku: 'coffee-mug', quantity: 1, state: 'reserved' },
            inventory: { sku: 'coffee-mug', onHand: 23 },
          },
        }
      }
      if (url === 'http://orders/orders') return { status: 503, value: { error: 'unavailable' } }
      return { status: 200, value: { id: 17, state: 'released' } }
    },
  }, async (origin) => {
    const response = await fetch(`${origin}/checkout`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sku: 'coffee-mug', quantity: 1 }),
    })
    assert.equal(response.status, 502)
  })
  assert.equal(calls.length, 3)
  assert.equal(calls[2].url, 'http://inventory/inventory/reservations/17/release')
  assert.equal(calls[2].options.method, 'POST')
})

test('checkout exposes persisted order lookups through the primary service', async () => {
  await withServer({
    inventoryURL: 'http://inventory',
    ordersURL: 'http://orders',
    requestJSON: async (url) => ({ status: 200, value: { order: { id: Number(url.split('/').pop()) }, cache: 'hit' } }),
  }, async (origin) => {
    const response = await fetch(`${origin}/orders/8`)
    assert.equal(response.status, 200)
    assert.deepEqual(await response.json(), { order: { id: 8 }, cache: 'hit' })
  })
})
