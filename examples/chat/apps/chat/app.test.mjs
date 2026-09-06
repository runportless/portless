import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { createApp, roomSocketURL } from './app.mjs';
import { createApp as createRooms } from '../rooms/app.mjs';
import { connect, listen } from '../../test-support.mjs';

test('dependency conversion preserves the source-aware URL and rejects unsafe settings', () => {
  assert.equal(roomSocketURL('https://rooms.localhost:8443/base%20path/?route=1'), 'wss://rooms.localhost:8443/base%20path/ws?route=1');
  assert.equal(roomSocketURL('http://127.0.0.1:1234'), 'ws://127.0.0.1:1234/ws');
  for (const value of [undefined, '', 'ws://rooms', 'ftp://rooms', 'http://user:secret@rooms', 'http://rooms/#fragment']) assert.throws(() => roomSocketURL(value));
});

test('chat validates Origin and protocol before dialing; serves explicit assets and its own CSP', async (t) => {
  const backend = createServer(); let attempts = 0;
  backend.on('upgrade', (_request, socket) => { attempts++; socket.destroy(); });
  await new Promise((resolve) => backend.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => backend.close(resolve)));
  const app = await createApp({ roomsURL: 'http://127.0.0.1:' + backend.address().port }); t.after(() => app.close());
  const address = await listen(app);
  const response = await fetch(address);
  assert.equal(response.status, 200);
  assert.match(response.headers.get('content-security-policy'), new RegExp('connect-src ws://127.0.0.1:' + app.server.address().port));
  assert.equal((await fetch(address + '/health')).status, 200);
  assert.equal((await fetch(address + '/server.mjs')).status, 404);
  for (const [path, options, protocols, status] of [
    ['/ws', {}, ['portless-chat.v1'], 403],
    ['/ws', { origin: 'null' }, ['portless-chat.v1'], 403],
    ['/ws', { origin: 'http://elsewhere.localhost', headers: { 'X-Forwarded-Host': 'elsewhere.localhost' } }, ['portless-chat.v1'], 403],
    ['/ws', { origin: address }, [], 400],
    ['/ws', { origin: address }, ['unsupported'], 400],
    ['/missing', { origin: address }, ['portless-chat.v1'], 404],
  ]) {
    const client = connect(address + path, options, protocols);
    await assert.rejects(client.open, new RegExp(String(status)));
  }
  assert.equal(attempts, 0);
});

test('two browser peers exchange text, presence, and worst-case bounded history through both services', async (t) => {
  const rooms = createRooms(); t.after(() => rooms.close());
  const upstreamHeaders = [];
  rooms.server.on('upgrade', (request) => upstreamHeaders.push(request.headers));
  const roomsAddress = await listen(rooms);
  const app = await createApp({ roomsURL: roomsAddress }); t.after(() => app.close());
  const address = await listen(app);
  async function join(name) {
    const client = connect(address + '/ws', {
      origin: address,
      headers: { cookie: 'session=test-only', authorization: 'Bearer test-only', traceparent: '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' },
    }); t.after(() => client.ws.terminate());
    await client.open; await client.next('ready');
    client.send({ type: 'join', name });
    const welcome = await client.next('welcome');
    return { client, welcome };
  }
  const { client: alex } = await join('Alex');
  const { client: sam } = await join('Sam');
  assert.equal(upstreamHeaders.length, 2);
  for (const headers of upstreamHeaders) {
    assert.equal(headers.origin, undefined);
    assert.equal(headers.cookie, undefined);
    assert.equal(headers.authorization, undefined);
    assert.equal(headers.traceparent, '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01');
  }
  assert.equal((await alex.next('presence', (value) => value.members.length === 2)).members[1].name, 'Sam');
  for (let i = 0; i < 51; i++) {
    const text = i % 2 ? '世'.repeat(2000) : '\u0000'.repeat(1300);
    const requestId = randomUUID();
    alex.send({ type: 'message', text, requestId });
    const received = await sam.next('message');
    assert.equal(received.text, text);
    assert.equal(received.binary, false);
    assert.equal((await alex.next('message')).requestId, requestId);
  }
  const { welcome } = await join('History');
  assert.equal(welcome.history.length, 50);
  assert.ok(Buffer.byteLength(JSON.stringify(welcome)) < 512 * 1024);
  sam.ws.close(); await sam.closeCode();
  assert.equal((await alex.next('presence', (value) => value.members.length === 2 && !value.members.some((person) => person.name === 'Sam'))).members.length, 2);
});

test('upstream failures recover as retryable closes and shutdown closes pairs promptly', async (t) => {
  const rooms = createRooms(); t.after(() => rooms.close());
  const addressRooms = await listen(rooms);
  const app = await createApp({ roomsURL: addressRooms }); t.after(() => app.close());
  const address = await listen(app);
  const client = connect(address + '/ws', { origin: address }); await client.open; await client.next('ready');
  client.send({ type: 'join', name: 'Alex' }); await client.next('welcome');
  const closing = client.closeCode();
  await rooms.close();
  assert.equal(await closing, 1001);
  const failed = connect(address + '/ws', { origin: address }); await failed.open;
  assert.equal((await failed.next('error')).code, 'ROOM_UNAVAILABLE');
  assert.equal(await failed.closeCode(), 1013);
  assert.equal((await fetch(address + '/health')).status, 200);
});

test('leaving during upstream setup cancels its pending connection and does not leak on shutdown', async (t) => {
  const backend = createServer();
  const raw = new Set();
  backend.on('connection', (socket) => { raw.add(socket); socket.on('close', () => raw.delete(socket)); });
  backend.on('upgrade', (_request, socket) => {
    // Consume EOF even though this peer intentionally never answers the upgrade.
    socket.on('end', () => socket.end());
    socket.resume();
  });
  await new Promise((resolve) => backend.listen(0, '127.0.0.1', resolve));
  t.after(() => { for (const socket of raw) socket.destroy(); backend.close(); });
  const app = await createApp({ roomsURL: 'http://127.0.0.1:' + backend.address().port }); t.after(() => app.close());
  const address = await listen(app);
  const client = connect(address + '/ws', { origin: address }); await client.open;
  client.ws.close(); await client.closeCode();
  const early = connect(address + '/ws', { origin: address }); await early.open;
  early.send({ type: 'join', name: 'Too early' });
  assert.equal((await early.next('error')).code, 'INVALID_COMMAND');
  assert.equal(await early.closeCode(), 1008);
  await app.close();
  for (let i = 0; raw.size && i < 100; i++) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(raw.size, 0);
});

test('chat caps paired connections and closes every browser when stopped', async (t) => {
  const rooms = createRooms(); t.after(() => rooms.close());
  const app = await createApp({ roomsURL: await listen(rooms) }); t.after(() => app.close());
  const address = await listen(app);
  const clients = [];
  t.after(() => { for (const client of clients) client.ws.terminate(); });
  for (let i = 0; i < 32; i++) {
    const client = connect(address + '/ws', { origin: address });
    clients.push(client); await client.open; await client.next('ready');
    client.send({ type: 'join', name: 'Member ' + i }); await client.next('welcome');
  }
  await assert.rejects(connect(address + '/ws', { origin: address }).open, /503/);
  const closing = clients.map((client) => client.closeCode());
  await app.close();
  assert.deepEqual(await Promise.all(closing), Array(32).fill(1001));
});
