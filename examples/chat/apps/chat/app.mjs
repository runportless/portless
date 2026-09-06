import { readFile } from 'node:fs/promises';
import Fastify from 'fastify';
import { WebSocket, WebSocketServer } from 'ws';

const protocol = 'portless-chat.v1';
const outputLimit = 1024 * 1024;
const traceHeaders = ['traceparent', 'tracestate', 'baggage'];

export function roomSocketURL(value) {
  let url;
  try { url = new URL(value); } catch { throw new Error('chat requires a valid ROOMS_URL supplied by Portless.'); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) {
    throw new Error('ROOMS_URL must be HTTP or HTTPS without credentials or a fragment.');
  }
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.pathname = url.pathname.replace(/\/$/, '') + '/ws';
  return url.href;
}

function pageOrigin(request) {
  const host = request.headers.host;
  if (typeof host !== 'string' || !/^(?:[a-z0-9.-]+|\[[a-f0-9:]+\])(?::[0-9]{1,5})?$/i.test(host)) return null;
  try { return new URL((request.socket.encrypted ? 'https://' : 'http://') + host).origin; } catch { return null; }
}

function closePeer(ws, code) {
  if (ws.readyState === WebSocket.CONNECTING) ws.terminate();
  else if (ws.readyState === WebSocket.OPEN) {
    const valid = (code >= 1000 && code <= 1014 && ![1004, 1005, 1006].includes(code)) || (code >= 3000 && code <= 4999);
    ws.close(valid ? code : 1011, 'Connection ended');
  }
}

export async function createApp({ roomsURL }) {
  const upstreamURL = roomSocketURL(roomsURL);
  const app = Fastify({ logger: false, requestTimeout: 5000, connectionTimeout: 10_000 });
  const sockets = new WebSocketServer({ noServer: true, maxPayload: 8192, perMessageDeflate: false, closeTimeout: 1000, handleProtocols: () => protocol });
  const pairs = new Map();
  let stopping = false;
  let admitted = 0;

  app.addHook('onRequest', async (request, reply) => {
    const origin = pageOrigin(request.raw);
    if (!origin) return reply.code(400).send({ error: 'Invalid application host.' });
    const socketOrigin = origin.replace(/^http/, 'ws');
    reply.header('Content-Security-Policy', "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src " + socketOrigin + "; base-uri 'none'; form-action 'none'; frame-ancestors 'none'");
    reply.header('X-Content-Type-Options', 'nosniff');
    reply.header('Referrer-Policy', 'no-referrer');
    reply.header('Cache-Control', 'no-store');
  });
  app.get('/health', async () => ({ status: 'ok' }));
  for (const [path, file, type] of [
    ['/', 'index.html', 'text/html; charset=utf-8'],
    ['/app.js', 'app.js', 'text/javascript; charset=utf-8'],
    ['/session.js', 'session.js', 'text/javascript; charset=utf-8'],
    ['/styles.css', 'styles.css', 'text/css; charset=utf-8'],
  ]) {
    const body = await readFile(new URL('./public/' + file, import.meta.url));
    app.get(path, async (_request, reply) => reply.type(type).send(body));
  }
  app.get('/favicon.ico', async (_request, reply) => reply.code(204).send());

  function send(ws, data) {
    if (ws.readyState !== WebSocket.OPEN) return;
    if (ws.bufferedAmount + Buffer.byteLength(data) > outputLimit) {
      ws.close(1013, 'Slow client');
      return;
    }
    ws.send(data, { binary: false }, (error) => { if (error) ws.terminate(); });
  }
  function fail(ws, code, message, closeCode = 1013) {
    send(ws, JSON.stringify({ type: 'error', code, message }));
    closePeer(ws, closeCode);
  }
  app.server.on('upgrade', (request, socket, head) => {
    const origin = pageOrigin(request);
    const offered = (request.headers['sec-websocket-protocol'] || '').split(',').map((value) => value.trim());
    const status = request.url?.split('?')[0] !== '/ws' ? 404
      : !origin || request.headers.origin !== origin ? 403
        : !offered.includes(protocol) ? 400
          : stopping || admitted >= 32 ? 503 : 0;
    if (status) {
      socket.end('HTTP/1.1 ' + status + ' Rejected\r\nConnection: close\r\nContent-Length: 0\r\n\r\n');
      socket.setTimeout(1000, () => socket.destroy());
      return;
    }
    admitted++;
    socket.once('close', () => admitted--);
    sockets.handleUpgrade(request, socket, head, (browser) => {
      const headers = Object.fromEntries(traceHeaders.filter((name) => typeof request.headers[name] === 'string').map((name) => [name, request.headers[name]]));
      const upstream = new WebSocket(upstreamURL, protocol, {
        headers, handshakeTimeout: 5000, maxPayload: 512 * 1024,
        perMessageDeflate: false, followRedirects: false, closeTimeout: 1000,
      });
      pairs.set(browser, upstream);
      browser.alive = true;
      browser.on('pong', () => { browser.alive = true; });
      let ready = false;
      upstream.on('open', () => {
        if (browser.readyState !== WebSocket.OPEN) { upstream.terminate(); return; }
        if (upstream.protocol !== protocol) {
          fail(browser, 'INVALID_COMMAND', 'The room protocol was not accepted.', 1008);
          upstream.terminate();
          return;
        }
        ready = true;
        send(browser, JSON.stringify({ type: 'ready' }));
      });
      browser.on('message', (data, binary) => {
        if (binary) { closePeer(browser, 1003); return; }
        if (!ready) { fail(browser, 'INVALID_COMMAND', 'Wait for the room before joining.', 1008); return; }
        send(upstream, data);
      });
      upstream.on('message', (data, binary) => {
        if (binary) { closePeer(browser, 1003); closePeer(upstream, 1003); return; }
        send(browser, data);
      });
      browser.on('error', () => closePeer(upstream, 1011));
      upstream.on('error', (error) => {
        const code = error.code === 'WS_ERR_UNSUPPORTED_MESSAGE_LENGTH' ? 1009 : ready ? 1011 : 1013;
        fail(browser, 'ROOM_UNAVAILABLE', 'The room is unavailable. Reconnecting…', code);
      });
      browser.on('close', (code) => { pairs.delete(browser); closePeer(upstream, code); });
      upstream.on('close', (code) => closePeer(browser, code));
    });
  });

  const heartbeat = setInterval(() => {
    for (const ws of sockets.clients) {
      if (!ws.alive) ws.terminate();
      else { ws.alive = false; ws.ping(); }
    }
  }, 15_000);
  heartbeat.unref();
  app.addHook('preClose', async () => {
    stopping = true;
    clearInterval(heartbeat);
    await new Promise((resolve) => {
      const deadline = setTimeout(() => {
        for (const [browser, upstream] of pairs) { browser.terminate(); upstream.terminate(); }
      }, 1000);
      for (const [browser, upstream] of pairs) { closePeer(browser, 1001); closePeer(upstream, 1001); }
      sockets.close(() => { clearTimeout(deadline); resolve(); });
    });
  });
  return app;
}
