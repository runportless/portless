import { createApp } from './app.mjs';

const port = Number(process.env.PORT);
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  console.error('chat requires a valid PORT supplied by Portless.');
  process.exit(1);
}
let app;
try {
  app = await createApp({ roomsURL: process.env.ROOMS_URL });
  for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => {
    app.close().catch(() => { console.error('Could not close chat cleanly.'); process.exitCode = 1; });
  });
  await app.listen({ port, host: '127.0.0.1' });
  console.log('Chat ready. Open the chat service in Portless.');
} catch {
  console.error('Could not start chat. Check PORT, ROOMS_URL, and its Portless service status.');
  if (app) await app.close();
  process.exitCode = 1;
}
