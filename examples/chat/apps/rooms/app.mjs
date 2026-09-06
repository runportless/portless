import Fastify from 'fastify';
import { WebSocket, WebSocketServer } from 'ws';
import { createRoom } from './room.mjs';

const protocol = 'portless-chat.v1';
const outputLimit = 1024 * 1024;

export function createApp() {
  const app = Fastify({ logger: false, requestTimeout: 5000, connectionTimeout: 10_000 });
  const room = createRoom();
  const sockets = new WebSocketServer({ noServer: true, maxPayload: 8192, perMessageDeflate: false, closeTimeout: 1000, handleProtocols: () => protocol });
  let stopping = false;
  let admitted = 0;

  app.get('/health', async () => ({ status: 'ok' }));
  app.server.on('upgrade', (request, socket, head) => {
    const offered = (request.headers['sec-websocket-protocol'] || '').split(',').map((value) => value.trim());
    const status = request.url?.split('?')[0] !== '/ws' ? 404
      : request.headers.origin !== undefined ? 403
        : !offered.includes(protocol) ? 400
          : stopping || admitted >= 32 ? 503 : 0;
    if (status) {
      socket.end('HTTP/1.1 ' + status + ' Rejected\r\nConnection: close\r\nContent-Length: 0\r\n\r\n');
      socket.setTimeout(1000, () => socket.destroy());
      return;
    }
    admitted++;
    socket.once('close', () => admitted--);
    sockets.handleUpgrade(request, socket, head, (ws) => {
      ws.alive = true;
      ws.on('pong', () => { ws.alive = true; });
      const connection = room.connect({
        send(value) {
          if (ws.readyState !== WebSocket.OPEN) return false;
          const data = JSON.stringify(value);
          if (ws.bufferedAmount + Buffer.byteLength(data) > outputLimit) {
            ws.close(1013, 'Slow client');
            return false;
          }
          ws.send(data, { binary: false }, (error) => { if (error) ws.terminate(); });
          return true;
        },
        close: (code) => ws.close(code, 'Chat request rejected'),
      });
      ws.on('message', (data, binary) => {
        if (binary) {
          connection.leave();
          ws.close(1003, 'Text messages required');
          return;
        }
        let command;
        try { command = JSON.parse(data.toString()); } catch { command = null; }
        connection.receive(command);
      });
      ws.on('close', connection.leave);
      ws.on('error', connection.leave);
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
    room.dispose();
    await new Promise((resolve) => {
      const deadline = setTimeout(() => { for (const ws of sockets.clients) ws.terminate(); }, 1000);
      for (const ws of sockets.clients) ws.close(1001, 'Room restarting');
      sockets.close(() => { clearTimeout(deadline); resolve(); });
    });
  });
  return app;
}
