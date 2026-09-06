import { createApp } from './app.mjs';

const port = Number(process.env.PORT);
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  console.error('rooms requires a valid PORT supplied by Portless.');
  process.exit(1);
}
const app = createApp();
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => {
  app.close().catch(() => { console.error('Could not close rooms cleanly.'); process.exitCode = 1; });
});
try {
  await app.listen({ port, host: '127.0.0.1' });
  console.log('Chat room ready. History is kept in memory.');
} catch {
  console.error('Could not start rooms. Check its Portless service status.');
  await app.close();
  process.exitCode = 1;
}
