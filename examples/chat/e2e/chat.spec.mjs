import { test, expect } from './fixtures.mjs';
import { readFile } from 'node:fs/promises';
import { join as joinPath } from 'node:path';

async function join(page, portless, name) {
  await page.goto(portless.url);
  await page.getByLabel('Your display name').fill(name);
  await page.getByRole('button', { name: 'Join room' }).click();
  await expect(page.locator('#status')).toHaveText('Connected');
}
async function send(page, text) {
  await page.getByLabel('Message', { exact: true }).fill(text);
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.locator('#pending')).toBeHidden();
  await expect(page.locator('.message-text').last()).toHaveText(text);
}
async function pair(browser, page, portless, testInfo) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, recordVideo: { dir: testInfo.outputPath('second-tab-video') } });
  const other = await context.newPage();
  await join(page, portless, 'Alex');
  await join(other, portless, 'Sam');
  await expect(page.locator('#member-count')).toHaveText('2');
  return { other, close: () => context.close() };
}
const pids = (state) => Object.fromEntries(state.services.map((service) => [service.name, service.pid]));

test('labels WebSocket services and topology edges from live and retained handshakes', async ({ browser, page, portless }, testInfo) => {
  await join(page, portless, 'Alex');
  const { port } = JSON.parse(await readFile(joinPath(portless.home, 'control.json'), 'utf8'));
  const baseURL = `http://127.0.0.1:${port}`;
  const token = (await readFile(joinPath(portless.home, 'install.key'), 'utf8')).trim();
  const claim = await fetch(`${baseURL}/api/v1/browser-claims`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ next: '/environments/chat/local?tab=topology' }),
  });
  expect(claim.ok).toBe(true);
  const { url } = await claim.json();
  const controlContext = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  try {
    const control = await controlContext.newPage();
    await control.clock.install();
    await control.goto(new URL(new URL(url).pathname, baseURL).href);
    const topology = control.getByRole('region', { name: 'Service topology' });
    const ingress = topology.locator('.topology-edge[data-source="external"][data-target="chat"]');
    const dependency = topology.locator('.topology-edge[data-source="chat"][data-target="rooms"]');
    const badges = topology.locator('.topology-node__websocket');
    await expect(ingress.locator('text')).toHaveText('HTTP + WS');
    await expect(dependency.locator('text')).toHaveText('WEBSOCKET');
    await expect(badges).toHaveCount(2);

    const cleared = await fetch(`${baseURL}/api/v1/environments/chat/local/traffic`, {
      method: 'DELETE', headers: { Authorization: `Bearer ${token}` },
    });
    expect(cleared.ok).toBe(true);
    await expect(badges).toHaveCount(0);
    await expect(dependency.locator('text')).toHaveText('HTTP');
    await page.getByRole('button', { name: 'Leave room' }).click();
    await page.getByLabel('Your display name').fill('Alex');
    await page.getByRole('button', { name: 'Join room' }).click();
    await expect(page.locator('#status')).toHaveText('Connected');
    for (const edge of [ingress, dependency]) await expect(edge.locator('text')).toHaveText('WEBSOCKET');
    await expect(badges).toHaveCount(2);

    await control.clock.fastForward(31_000);
    for (const edge of [ingress, dependency]) {
      await expect(edge.locator('text')).toHaveText('WEBSOCKET');
      await expect(edge).toHaveClass(/topology-edge--idle/);
      await expect(edge.locator('.topology-edge__pulse')).toHaveCount(0);
    }
    await expect(page.locator('#status')).toHaveText('Connected');
    await control.reload();
    for (const edge of [ingress, dependency]) await expect(edge.locator('text')).toHaveText('WEBSOCKET');
    await expect(badges).toHaveCount(2);
    await topology.locator('.topology-node[data-service="rooms"]').hover();
    await expect(topology.getByRole('tooltip')).toContainText('Observed traffic: WebSocket');
    await control.getByRole('button', { name: 'Center topology' }).click();
    await control.screenshot({ path: testInfo.outputPath('chat-websocket-topology.png'), animations: 'disabled' });

    await page.reload();
    await expect(ingress.locator('text')).toHaveText('HTTP + WS');
    await expect(dependency.locator('text')).toHaveText('WEBSOCKET');
  } finally { await controlContext.close(); }
});

test('discovers Chat and carries real messages, typing, presence and inspectable handshakes', async ({ browser, page, portless }, testInfo) => {
  const state = await portless.status();
  expect(state.status).toBe('healthy');
  expect(state.primaryService).toBe('chat');
  expect(state.services.map((service) => service.name).sort()).toEqual(['chat', 'rooms']);
  expect(state.connections.map(({ source, target, protocol }) => ({ source, target, protocol }))).toEqual([{ source: 'chat', target: 'rooms', protocol: 'http' }]);
  const watermark = Math.max(0, ...(await portless.json(['traffic', 'list', '--limit', '100'])).exchanges.map((exchange) => exchange.sequence));
  const peers = await pair(browser, page, portless, testInfo);
  try {
    await page.getByLabel('Message', { exact: true }).fill('A live thought');
    await expect(peers.other.locator('#typing')).toHaveText('Alex is typing…');
    await page.getByRole('button', { name: 'Send message' }).click();
    await expect(peers.other.locator('.message-text')).toHaveText(['A live thought']);
    await expect(peers.other.locator('#typing')).toBeEmpty();
    await send(peers.other, 'Hello from another tab');
    await expect(page.locator('.message-text')).toHaveText(['A live thought', 'Hello from another tab']);
    for (const edge of ['external:chat', 'chat:rooms']) {
      let handshake;
      await expect.poll(async () => {
        const traffic = await portless.json(['traffic', 'list', '--edge', edge, '--limit', '100']);
        handshake = traffic.exchanges.find((exchange) => exchange.sequence > watermark && exchange.status === 101 && exchange.requestTarget === '/ws');
        return Boolean(handshake);
      }).toBe(true);
      const details = await portless.json(['traffic', 'show', String(handshake.sequence)]);
      expect(JSON.stringify(details)).not.toContain('A live thought');
      expect(handshake.responseBody || '').toBe('');
    }
    const popupPromise = page.context().waitForEvent('page');
    await page.getByRole('link', { name: 'Open another tab' }).click();
    const popup = await popupPromise;
    await popup.waitForLoadState();
    await expect(popup.getByLabel('Your display name')).toBeEmpty();
    expect(await popup.evaluate(() => window.opener === null)).toBe(true);
    await popup.close();
    await peers.other.getByRole('button', { name: 'Leave room' }).click();
    await expect(page.locator('#member-count')).toHaveText('1');
    await expect(peers.other.locator('#status')).toHaveText('Disconnected');
  } finally { await peers.close(); }
});

test('rooms restart clears history and reconnects both tabs', async ({ browser, page, portless }, testInfo) => {
  const peers = await pair(browser, page, portless, testInfo);
  try {
    await send(page, 'Before room restart');
    const before = pids(await portless.status());
    await portless.cli(['service', 'restart', 'rooms', '--timeout', '45s']);
    const after = pids(await portless.status());
    expect(after.chat).toBe(before.chat); expect(after.rooms).not.toBe(before.rooms);
    for (const tab of [page, peers.other]) {
      await expect(tab.locator('#status')).toHaveText('Connected');
      await expect(tab.locator('#notice')).toHaveText('Room restarted; history cleared.');
      await expect(tab.locator('.message-text')).toHaveCount(0);
      await expect(tab.locator('#member-count')).toHaveText('2');
    }
    await send(peers.other, 'After room restart');
    await expect(page.locator('.message-text')).toHaveText(['After room restart']);
  } finally { await peers.close(); }
});

test('chat and daemon restarts preserve history and daemon adoption preserves application PIDs', async ({ browser, page, portless }, testInfo) => {
  const peers = await pair(browser, page, portless, testInfo);
  try {
    await send(page, 'Keep this conversation');
    const initial = pids(await portless.status());
    await portless.cli(['service', 'restart', 'chat', '--timeout', '45s']);
    const afterChat = pids(await portless.status());
    expect(afterChat.rooms).toBe(initial.rooms); expect(afterChat.chat).not.toBe(initial.chat);
    for (const tab of [page, peers.other]) {
      await expect(tab.locator('#status')).toHaveText('Connected');
      await expect(tab.locator('.message-text')).toHaveText(['Keep this conversation']);
      await expect(tab.locator('#member-count')).toHaveText('2');
    }
    await portless.cli(['daemon', 'restart']);
    expect(pids(await portless.status())).toEqual(afterChat);
    for (const tab of [page, peers.other]) {
      await expect(tab.locator('#status')).toHaveText('Connected');
      await expect(tab.locator('.message-text')).toHaveText(['Keep this conversation']);
    }
    await send(peers.other, 'Still here');
    await expect(page.locator('.message-text')).toHaveText(['Keep this conversation', 'Still here']);
  } finally { await peers.close(); }
});

test('unavailability, Leave during retries, and environment down/up recover without resending drafts', async ({ browser, page, portless }, testInfo) => {
  const peers = await pair(browser, page, portless, testInfo);
  try {
    await send(page, 'Before shutdown');
    await page.getByLabel('Message', { exact: true }).fill('Keep my draft');
    await portless.cli(['service', 'stop', 'rooms', '--timeout', '45s']);
    await expect(page.locator('#status')).toHaveText('Reconnecting');
    await expect(page.getByRole('button', { name: 'Send message' })).toBeDisabled();
    await peers.other.getByRole('button', { name: 'Leave room' }).click();
    await expect(peers.other.locator('#status')).toHaveText('Disconnected');
    await portless.cli(['service', 'start', 'rooms', '--timeout', '45s']);
    await expect(page.locator('#status')).toHaveText('Connected');
    await expect(page.locator('#member-count')).toHaveText('1');
    await expect(page.getByLabel('Message', { exact: true })).toHaveValue('Keep my draft');
    await expect(peers.other.locator('#status')).toHaveText('Disconnected');
    await portless.cli(['down', '--timeout', '45s']);
    await expect(page.locator('#status')).toHaveText('Reconnecting');
    await portless.cli(['up', '--managed', '--no-open', '--timeout', '60s']);
    await expect(page.locator('#status')).toHaveText('Connected');
    await expect(page.locator('.message-text')).toHaveCount(0);
    await expect(page.getByLabel('Message', { exact: true })).toHaveValue('Keep my draft');
    await page.getByRole('button', { name: 'Send message' }).click();
    await expect(page.locator('.message-text')).toHaveText(['Keep my draft']);
  } finally { await peers.close(); }
});

test('keyboard, long names, literal text, scroll preservation, and both responsive themes', async ({ browser, page, portless }, testInfo) => {
  await page.goto(portless.url);
  await page.getByLabel('Your display name').focus();
  await page.keyboard.type('A'.repeat(24));
  await page.keyboard.press('Enter');
  await expect(page.locator('#status')).toHaveText('Connected');
  await expect(page.getByLabel('Message', { exact: true })).toBeFocused();
  await page.keyboard.type('<b>Hello</b>');
  await page.keyboard.press('Shift+Enter');
  await page.keyboard.type('Second line');
  await page.keyboard.press('Enter');
  await expect(page.locator('.message-text')).toHaveText(['<b>Hello</b>\nSecond line']);
  expect(await page.locator('.message-text b').count()).toBe(0);
  await page.getByLabel('Message', { exact: true }).fill('An unfinished composition');
  await page.getByLabel('Message', { exact: true }).dispatchEvent('keydown', { key: 'Enter', isComposing: true });
  await expect(page.getByLabel('Message', { exact: true })).toHaveValue('An unfinished composition');
  await expect(page.locator('.message-text')).toHaveCount(1);
  await page.getByLabel('Message', { exact: true }).fill('');
  const context = await browser.newContext();
  const other = await context.newPage();
  try {
    await join(other, portless, 'A'.repeat(24));
    await expect(page.locator('.member-name')).toHaveText(['A'.repeat(24) + ' · #1', 'A'.repeat(24) + ' · #2']);
    for (let i = 0; i < 12; i++) await send(other, 'Message ' + i + '\nA little more to read.');
    await page.locator('#transcript').evaluate((node) => { node.scrollTop = 0; });
    const observer = await context.newPage();
    await join(observer, portless, 'Observer');
    await expect(page.locator('#member-count')).toHaveText('3');
    await expect(page.getByRole('button', { name: 'New messages' })).toBeHidden();
    await observer.close();
    await expect(page.locator('#member-count')).toHaveText('2');
    await send(other, 'One more message');
    await expect(page.getByRole('button', { name: 'New messages' })).toBeVisible();
    expect(await page.locator('#transcript').evaluate((node) => node.scrollTop)).toBeLessThan(20);
    await page.getByRole('button', { name: 'New messages' }).click();
    await expect(page.getByRole('button', { name: 'New messages' })).toBeHidden();
    for (const width of [1280, 700, 390]) {
      await page.setViewportSize({ width, height: 900 });
      for (const theme of ['light', 'dark']) {
        if (await page.locator('html').getAttribute('data-theme') !== theme) await page.getByRole('button', { name: 'Switch to ' + theme + ' theme' }).click();
        await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await expect(page.getByRole('button', { name: 'Send message' })).toBeInViewport();
        await page.screenshot({ path: testInfo.outputPath('chat-' + width + '-' + theme + '.png'), fullPage: true });
      }
    }
    await page.getByRole('button', { name: 'Leave room' }).focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#status')).toHaveText('Disconnected');
    await expect(page.getByLabel('Your display name')).toBeFocused();
    await page.reload();
    await expect(page.getByLabel('Your display name')).toBeEmpty();
  } finally { await context.close(); }
});
