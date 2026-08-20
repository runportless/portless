import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import test from 'node:test';

import {primaryCallToAction} from '../src/data/site.ts';

const siteRoot = fileURLToPath(new URL('../', import.meta.url));

function sha256(bytes: Buffer) {
  return createHash('sha256').update(bytes).digest('hex');
}

test('the primary call to action falls back to the on-page demo', () => {
  assert.deepEqual(primaryCallToAction(), {
    href: '#demo',
    label: 'Watch the demo',
    external: false,
  });
});

test('the primary call to action accepts a configured early-access URL', () => {
  assert.deepEqual(primaryCallToAction(' https://example.com/access '), {
    href: 'https://example.com/access',
    label: 'Get early access',
    external: true,
  });
});

test('all marketing product media are live-app captures', async () => {
  const captures = [
    'faults.jpg',
    'projects.jpg',
    'recordings.jpg',
    'traffic-detail.jpg',
    'traffic.jpg',
  ];

  for (const capture of captures) {
    const bytes = await readFile(`${siteRoot}src/assets/product/${capture}`);
    assert.deepEqual([...bytes.subarray(0, 3)], [0xff, 0xd8, 0xff], `${capture} must be a JPEG capture`);
  }

  const topologyAnimation = await readFile(`${siteRoot}src/assets/product/topology-live.gif`);
  assert.equal(topologyAnimation.subarray(0, 6).toString('ascii'), 'GIF89a');
  assert.match(topologyAnimation.toString('latin1'), /NETSCAPE2\.0/, 'topology capture must loop as an animated GIF');

  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  assert.doesNotMatch(page, /brand\/video-preview|explainer-poster/i);
  assert.doesNotMatch(page, /flow-dot/, 'live topology motion must come from the captured application');
});

test('the published explainer uses the neural-voice master', async () => {
  const [published, neuralMaster, naturalMaster] = await Promise.all([
    readFile(`${siteRoot}public/demo/portless-explainer.mp4`),
    readFile(`${siteRoot}../brand/video-preview/out/portless-explainer-neural-voice.mp4`),
    readFile(`${siteRoot}../brand/video-preview/out/portless-explainer-natural-voice.mp4`),
  ]);

  assert.equal(sha256(published), sha256(neuralMaster));
  assert.notEqual(sha256(published), sha256(naturalMaster));
});

test('the explainer plays inline with captions initially disabled', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');

  assert.match(page, /<video[^>]*data-demo-video[^>]*>/);
  assert.doesNotMatch(page, /<video[^>]*\bcontrols\b[^>]*>/, 'browser-native controls must not handle responsive playback');
  assert.match(page, /data-demo-play[^>]*aria-label="Play demo"/);
  assert.doesNotMatch(page, /VideoDialog|data-open-video|<dialog/);
  assert.doesNotMatch(page, /<track[^>]*\bdefault\b[^>]*>/);
  assert.match(page, /track\.mode = 'disabled'/);
  assert.match(page, /demoVideo\.pause\(\)/);
});

test('the Docker Compose comparison states the product boundary', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');

  assert.match(page, /id="docker-compose"/);
  assert.match(page, /Compose is container-first/);
  assert.match(page, /Portless is application-first/);
  assert.match(page, /Portless can use Docker Engine or Podman/);
  assert.match(page, /runtime stays a runtime choice—not the shape of your application/);
});

test('the page moves from the problem and solution into the product pillars', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  const orderedMarkers = [
    'id="problem"',
    'id="solution"',
    'id="how-it-works"',
    'id="control-plane"',
    'id="experiments"',
    'id="docker-compose"',
  ];
  const positions = orderedMarkers.map((marker) => page.indexOf(marker));

  assert.ok(positions.every((position) => position >= 0), 'every narrative section must be present');
  assert.deepEqual([...positions].sort((left, right) => left - right), positions);
  assert.doesNotMatch(page, /How it works/);
});

test('the solution resolves collisions before Run it explains the command', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  const runStart = page.indexOf('id="how-it-works"');
  const runEnd = page.indexOf('class="section product-section"');
  const runSection = page.slice(runStart, runEnd);

  assert.match(page, /id="solution"/);
  assert.match(page, /Every service gets/);
  assert.match(page, /<strong>orders<\/strong><b>HTTP<\/b><span>localhost:8080/);
  assert.match(page, /<strong>inventory<\/strong><b>HTTP<\/b><span>localhost:8080/);
  assert.match(page, /<strong>orders-db<\/strong><b>Postgres<\/b><span>localhost:5432/);
  assert.match(page, /<strong>inventory-db<\/strong><b>Postgres<\/b><span>localhost:5432/);
  assert.match(page, /orders<\/strong>\.local\.store\.localhost/);
  assert.match(page, /inventory<\/strong>\.local\.store\.localhost/);
  assert.match(page, /orders-db<\/strong>\.local\.store\.portless\.test<b>:5432/);
  assert.match(page, /inventory-db<\/strong>\.local\.store\.portless\.test<b>:5432/);
  assert.match(runSection, /portless up/);
  assert.doesNotMatch(runSection, /portless setup|portless ui/);
});
