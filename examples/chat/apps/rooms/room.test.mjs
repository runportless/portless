import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createRoom } from './room.mjs';

function participant(room, name = 'Alex') {
  const events = [];
  const peer = { send: (event) => { events.push(event); return true; }, close: (code) => { peer.code = code; } };
  const connection = room.connect(peer);
  if (name !== null) connection.receive({ type: 'join', name });
  return { ...connection, events, peer, welcome: () => events.find((event) => event.type === 'welcome') };
}
const submit = (client, text) => client.receive({ type: 'message', text, requestId: randomUUID() });

test('orders joins, broadcasts and bounded history; empty room retains generation and history', (t) => {
  const room = createRoom(); t.after(() => room.dispose());
  const alex = participant(room);
  submit(alex, 'Before join');
  const duplicate = participant(room);
  assert.notEqual(alex.welcome().self.id, duplicate.welcome().self.id);
  assert.equal(duplicate.welcome().history.length, 1);
  submit(alex, 'After join');
  assert.deepEqual(duplicate.events.filter((event) => ['welcome', 'message'].includes(event.type)).map((event) => event.type), ['welcome', 'message']);
  for (let i = 0; i < 55; i++) submit(alex, 'Message ' + i);
  alex.leave(); duplicate.leave();
  const last = participant(room, 'Taylor').welcome();
  assert.equal(last.history.length, 50);
  assert.equal(last.history[0].id, 8);
  assert.equal(last.generation, alex.welcome().generation);
  assert.equal(last.members.length, 1);
});

test('typing expires, clears on send and departure, and never leaks into history', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const room = createRoom(); t.after(() => room.dispose());
  const alex = participant(room);
  const sam = participant(room, 'Sam');
  alex.receive({ type: 'typing', active: true });
  assert.deepEqual(sam.events.at(-1).memberIds, [alex.welcome().self.id]);
  t.mock.timers.tick(3000);
  assert.deepEqual(sam.events.at(-1).memberIds, []);
  alex.receive({ type: 'typing', active: true });
  submit(alex, 'Sent');
  assert.deepEqual(sam.events.at(-1).memberIds, []);
  alex.receive({ type: 'typing', active: true });
  alex.leave();
  assert.deepEqual(sam.events.at(-1).memberIds, []);
});

test('unjoined sockets expire and malformed, early and identity-spoofing commands close', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const room = createRoom(); t.after(() => room.dispose());
  const slow = participant(room, null);
  t.mock.timers.tick(5000);
  assert.equal(slow.peer.code, 1013);
  for (const command of [null, [], {}, { type: 'typing', active: true }, { type: 'join', name: 'A', sender: 'B' }]) {
    const client = participant(room, null);
    client.receive(command);
    assert.equal(client.peer.code, 1008);
  }
  const joined = participant(room);
  joined.receive({ type: 'join', name: 'Another' });
  assert.equal(joined.peer.code, 1008);
});

test('validates names and text while preserving meaningful Unicode and line breaks', (t) => {
  const room = createRoom(); t.after(() => room.dispose());
  for (const name of ['', ' '.repeat(4), 'a'.repeat(25), 'A\u0000B']) {
    const client = participant(room, name);
    assert.equal(client.peer.code, 1008);
    assert.equal(client.events[0].code, 'INVALID_NAME');
  }
  const alex = participant(room, '  Ame\u0301lie  ');
  assert.equal(alex.welcome().self.name, 'Amélie');
  for (const text of ['', ' \n ', 'a'.repeat(2001), 5]) submit(alex, text);
  assert.equal(alex.peer.code, undefined);
  assert.equal(alex.events.at(-1).code, 'INVALID_TEXT');
  submit(alex, '  hello\n世界  ');
  assert.equal(alex.events.at(-1).text, '  hello\n世界  ');
});

test('failed output removes that member while healthy members still receive the message', (t) => {
  const room = createRoom(); t.after(() => room.dispose());
  const alex = participant(room);
  const slow = participant(room, 'Slow');
  const healthy = participant(room, 'Healthy');
  slow.peer.send = () => false;
  submit(alex, 'Delivered');
  assert.equal(healthy.events.filter((event) => event.type === 'message').at(-1).text, 'Delivered');
  assert.equal(healthy.events.filter((event) => event.type === 'presence').at(-1).members.length, 2);
});
