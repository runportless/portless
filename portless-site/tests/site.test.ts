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

test('all marketing product images are live-app JPEG captures', async () => {
  const captures = [
    'faults.jpg',
    'projects.jpg',
    'recordings.jpg',
    'topology-live.jpg',
    'traffic-detail.jpg',
    'traffic.jpg',
  ];

  for (const capture of captures) {
    const bytes = await readFile(`${siteRoot}src/assets/product/${capture}`);
    assert.deepEqual([...bytes.subarray(0, 3)], [0xff, 0xd8, 0xff], `${capture} must be a JPEG capture`);
  }

  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  assert.doesNotMatch(page, /brand\/video-preview|explainer-poster/i);
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
