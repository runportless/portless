import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSession } from './public/session.js';
import { fakeClock } from '../../test-support.mjs';

const self = { id: '1', name: 'Alex' };
const uuid = '12345678-1234-4123-8123-123456789abc';
const accepted = (text = 'Hello', overrides = {}) => ({ type: 'message', generation: 'generation-a', id: 1, sender: self, at: '2026-09-05T10:00:00.000Z', text, requestId: uuid, ...overrides });

function fixture(t) {
  const timer = fakeClock();
  const sockets = [];
  const session = createSession({
    url: 'ws://chat.localhost/ws', clock: timer.clock, now: timer.now, random: () => 1, requestId: () => uuid,
    createSocket() {
      const listeners = {};
      const socket = {
        readyState: 0, protocol: 'portless-chat.v1', sent: [],
        addEventListener(type, callback) { (listeners[type] ||= []).push(callback); },
        emit(type, value) { for (const callback of listeners[type] || []) callback(value); },
        send(data) { socket.sent.push(JSON.parse(data)); },
        open() { socket.readyState = 1; socket.emit('open', {}); },
        receive(data) { socket.emit('message', { data: JSON.stringify(data) }); },
        close(code = 1006) { socket.readyState = 3; socket.emit('close', { code }); },
      };
      sockets.push(socket);
      return socket;
    },
  });
  t.after(() => { session.leave(); assert.equal(timer.count(), 0); });
  function welcome(overrides = {}) {
    const socket = sockets.at(-1);
    socket.open(); socket.receive({ type: 'ready' });
    socket.receive({ type: 'welcome', room: 'lobby', generation: 'generation-a', self, members: [self], typing: [], history: [], ...overrides });
    return socket;
  }
  return { session, sockets, welcome, ...timer };
}

test('joins only after ready and waits for welcome before sending', (t) => {
  const { session, sockets } = fixture(t);
  session.join('Alex');
  const socket = sockets[0];
  assert.equal(session.snapshot().status, 'Connecting');
  socket.open();
  assert.deepEqual(socket.sent, []);
  session.setDraft('Hello');
  assert.equal(session.send(), false);
  socket.receive({ type: 'ready' });
  assert.deepEqual(socket.sent, [{ type: 'join', name: 'Alex' }]);
  assert.equal(session.snapshot().status, 'Joining');
});

test('confirmation never overwrites a newer draft and duplicate broadcasts do not duplicate history', (t) => {
  const { session, welcome } = fixture(t);
  session.join('Alex'); const socket = welcome();
  session.setDraft('Hello'); session.send();
  session.setDraft('The next draft');
  assert.equal(session.send(), false);
  socket.receive(accepted());
  socket.receive(accepted());
  assert.equal(session.snapshot().draft, 'The next draft');
  assert.equal(session.snapshot().pending, null);
  assert.equal(session.snapshot().history.length, 1);
});

test('confirmation timeout reconnects and resolves matching history without replaying', (t) => {
  const { session, sockets, welcome, advance } = fixture(t);
  session.join('Alex'); welcome();
  session.setDraft('Hello'); session.send();
  advance(5000);
  assert.equal(session.snapshot().pending.status, 'unconfirmed');
  assert.equal(session.snapshot().status, 'Reconnecting');
  advance(500);
  const socket = welcome({ self: { id: '2', name: 'Alex' }, history: [accepted()] });
  assert.equal(sockets.length, 2);
  assert.equal(session.snapshot().pending, null);
  assert.deepEqual(socket.sent.map((item) => item.type), ['join']);
});

test('history eviction and room restart preserve uncertain text without claiming it was lost', (t) => {
  const { session, welcome, advance } = fixture(t);
  session.join('Alex'); let socket = welcome({ history: [accepted('Earlier', { requestId: 'earlier' })] });
  session.setDraft('Hello'); session.send();
  socket.close(1006); advance(500);
  socket = welcome({ history: [accepted('Later', { id: 55, sender: { id: '2', name: 'Sam' }, requestId: 'later' })] });
  assert.equal(session.snapshot().pending.status, 'unconfirmed');
  assert.match(session.snapshot().notice, /no longer available/);
  socket.close(1001); advance(500);
  welcome({ generation: 'generation-b' });
  assert.equal(session.snapshot().pending.text, 'Hello');
  assert.equal(session.snapshot().notice, 'Room restarted; history cleared.');
  session.setDraft('A different draft'); session.dismiss();
  assert.equal(session.snapshot().pending, null);
  assert.equal(session.snapshot().draft, 'A different draft');
});

test('explicit text rejection retains the connection and rejected text for correction', (t) => {
  const { session, welcome } = fixture(t);
  session.join('Alex'); const socket = welcome();
  session.setDraft('Hello'); session.send(); session.setDraft('New');
  socket.receive({ type: 'error', code: 'INVALID_TEXT', message: 'Please revise it.', requestId: uuid });
  assert.equal(session.snapshot().pending.status, 'rejected');
  assert.equal(session.snapshot().status, 'Connected');
  assert.equal(session.snapshot().draft, 'New');
});

test('retry backoff caps at five seconds, shows guidance, and ignores obsolete callbacks', (t) => {
  const { session, sockets, advance } = fixture(t);
  session.join('Alex');
  for (const delay of [500, 1000, 2000, 4000, 5000, 5000]) {
    const old = sockets.at(-1);
    old.close(1006);
    const count = sockets.length;
    advance(delay - 1); assert.equal(sockets.length, count);
    advance(1); assert.equal(sockets.length, count + 1);
    old.open(); old.receive({ type: 'ready' }); old.close(1008);
    assert.equal(session.snapshot().wanted, true);
  }
  assert.equal(session.snapshot().guidance, true);
  sockets.at(-1).close(1009);
  assert.equal(session.snapshot().guidance, false);
  assert.match(session.snapshot().error, /exceeded/);
});

test('opening, ready, and welcome watchdogs each end stalled attempts', (t) => {
  for (const stage of ['opening', 'ready', 'welcome']) {
    const { session, sockets, advance } = fixture(t);
    session.join('Alex');
    const socket = sockets[0];
    if (stage !== 'opening') socket.open();
    if (stage === 'welcome') socket.receive({ type: 'ready' });
    advance(10_000);
    assert.equal(session.snapshot().status, 'Reconnecting');
    session.leave();
  }
});

test('terminal codes stop retries; Leave cancels an opening attempt and a scheduled retry', (t) => {
  for (const code of [1002, 1003, 1007, 1008, 1009]) {
    const { session, sockets, advance } = fixture(t);
    session.join('Alex'); sockets[0].close(code); advance(30_000);
    assert.equal(sockets.length, 1); assert.equal(session.snapshot().wanted, false);
  }
  for (const failFirst of [false, true]) {
    const { session, sockets, advance } = fixture(t);
    session.join('Alex');
    if (failFirst) sockets[0].close(1006);
    session.leave(); sockets[0].open(); advance(30_000);
    assert.equal(sockets.length, 1);
  }
});

test('back/forward restoration reconnects desired sessions but never revives an explicit leave', (t) => {
  const { session, sockets, welcome } = fixture(t);
  session.join('Alex'); welcome(); session.setDraft('Keep this');
  session.suspend(); session.resume();
  assert.equal(sockets.length, 2);
  assert.equal(session.snapshot().draft, 'Keep this');
  session.leave(); session.suspend(); session.resume();
  assert.equal(sockets.length, 2);
});

test('typing is throttled, expires and cancels on send or leave', (t) => {
  const { session, welcome, advance } = fixture(t);
  session.join('Alex'); const socket = welcome();
  session.setDraft('H'); session.setDraft('He'); session.setDraft('Hel');
  assert.equal(socket.sent.filter((item) => item.type === 'typing').length, 1);
  advance(500);
  assert.equal(socket.sent.filter((item) => item.type === 'typing').length, 2);
  advance(2500); assert.equal(socket.sent.at(-1).active, false);
  session.send();
  session.leave();
});

test('UTF-16 and encoded limits reject invalid submissions before sending', (t) => {
  const { session, welcome } = fixture(t);
  session.join('Alex'); const socket = welcome();
  for (const value of ['  \n', 'a'.repeat(2001), '\u0000'.repeat(2000)]) {
    session.setDraft(value); assert.equal(session.send(), false);
  }
  assert.equal(socket.sent.filter((item) => item.type === 'message').length, 0);
});
