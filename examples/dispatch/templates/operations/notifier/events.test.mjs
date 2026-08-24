import assert from 'node:assert/strict'
import test from 'node:test'
import { EventBuffer } from './events.mjs'

test('event buffer retains newest events within its bound', () => {
  const events = new EventBuffer(2)
  events.add({ eventId: 'one' })
  events.add({ eventId: 'two' })
  events.add({ eventId: 'three' })
  assert.deepEqual(events.list(), [{ eventId: 'three' }, { eventId: 'two' }])
  assert.deepEqual(events.list(1), [{ eventId: 'three' }])
})

