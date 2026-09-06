import { WebSocket } from 'ws';

export async function listen(app) {
  await app.listen({ port: 0, host: '127.0.0.1' });
  return 'http://127.0.0.1:' + app.server.address().port;
}

export function connect(url, options = {}, protocols = ['portless-chat.v1']) {
  const ws = new WebSocket(url.replace(/^http/, 'ws'), protocols, options);
  const queue = [];
  const waiting = new Set();
  let closed;
  function deliver(value) {
    for (const waiter of waiting) {
      if (waiter.match(value)) {
        waiting.delete(waiter);
        clearTimeout(waiter.timer);
        waiter.resolve(value);
        return;
      }
    }
    queue.push(value);
  }
  ws.on('message', (data, binary) => {
    let value;
    try { value = JSON.parse(data.toString()); } catch { value = {}; }
    deliver({ ...value, binary });
  });
  ws.on('error', () => {});
  ws.on('close', (code) => {
    closed = code;
    for (const waiter of waiting) { clearTimeout(waiter.timer); waiter.reject(new Error('Socket closed: ' + code)); }
    waiting.clear();
  });
  return {
    ws, queue,
    open: new Promise((resolve, reject) => { ws.once('open', resolve); ws.once('error', reject); }),
    send: (value) => ws.send(JSON.stringify(value)),
    next(type, predicate = () => true) {
      const match = (value) => value.type === type && predicate(value);
      const index = queue.findIndex(match);
      if (index >= 0) return Promise.resolve(queue.splice(index, 1)[0]);
      if (closed !== undefined) return Promise.reject(new Error('Socket closed: ' + closed));
      return new Promise((resolve, reject) => {
        const waiter = { match, resolve, reject };
        waiter.timer = setTimeout(() => { waiting.delete(waiter); reject(new Error('Timed out waiting for ' + type)); }, 3000);
        waiting.add(waiter);
      });
    },
    closeCode() {
      if (closed !== undefined) return Promise.resolve(closed);
      return new Promise((resolve) => ws.once('close', resolve));
    },
  };
}

export function fakeClock() {
  let time = 0;
  let serial = 0;
  const timers = new Map();
  return {
    now: () => time,
    clock: {
      setTimeout(callback, delay) { const id = ++serial; timers.set(id, { callback, at: time + delay }); return id; },
      clearTimeout(id) { timers.delete(id); },
    },
    advance(amount) {
      const target = time + amount;
      for (;;) {
        const next = [...timers].filter(([, timer]) => timer.at <= target).sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        timers.delete(next[0]); time = next[1].at; next[1].callback();
      }
      time = target;
    },
    count: () => timers.size,
  };
}
