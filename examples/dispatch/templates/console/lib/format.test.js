import assert from 'node:assert/strict'
import test from 'node:test'
import { formatMoney, nextStatus, statusLabel } from './format.js'

test('formats delivery values for the dashboard', () => {
  assert.equal(formatMoney(1875), '$18.75')
  assert.equal(nextStatus('scheduled'), 'assigned')
  assert.equal(nextStatus('delivered'), '')
  assert.equal(statusLabel('picked_up'), 'picked up')
})

