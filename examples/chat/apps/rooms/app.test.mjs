import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createApp } from './app.mjs';
import { connect, listen } from '../../test-support.mjs';

test('rooms accepts only non-browser protocol connections and health probes create no members', async (t) => {
  const app = createApp(); t.after(() => app.close());
  const address = await listen(app);
  assert.equal((await fetch(address + '/health')).status, 200);
  for (const [path, options, protocols, expected] of [
    ['/ws', { origin: 'http://foreign.localhost' }, ['portless-chat.v1'], 403],
    ['/ws', { headers: { Origin: '' } }, ['portless-chat.v1'], 403],
    ['/wrong', {}, ['portless-chat.v1'], 404],
    ['/ws', {}, [], 400], ['/ws', {}, ['another'], 400],
  ]) {
    const client = connect(address + path, options, protocols);
    await assert.rejects(client.open, new RegExp(String(expected)));
  }
  const client = connect(address + '/ws'); t.after(() => client.ws.terminate()); await client.open;
  client.send({ type: 'join', name: 'Alex' });
  assert.equal((await client.next('welcome')).members.length, 1);
});

test('rooms rejects binary, oversized, and malformed commands with defined close codes', async (t) => {
  const app = createApp(); t.after(() => app.close());
  const address = await listen(app);
  for (const [data, expected] of [[Buffer.from('binary'), 1003], ['x'.repeat(8193), 1009], ['{', 1008]]) {
    const client = connect(address + '/ws'); await client.open;
    const closing = client.closeCode();
    client.ws.send(data);
    assert.equal(await closing, expected);
  }
});

test('rooms bounds pending clients, releases slots, and closes all sockets on shutdown', async (t) => {
  const app = createApp(); t.after(() => app.close());
  const address = await listen(app);
  const clients = [];
  t.after(() => { for (const client of clients) client.ws.terminate(); });
  for (let i = 0; i < 32; i++) { const client = connect(address + '/ws'); clients.push(client); await client.open; }
  const excess = connect(address + '/ws');
  await assert.rejects(excess.open, /503/);
  clients[0].ws.close();
  await clients[0].closeCode();
  const replacement = connect(address + '/ws'); clients.push(replacement); await replacement.open;
  const closing = replacement.closeCode();
  await app.close();
  assert.equal(await closing, 1001);
});
