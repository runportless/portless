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

test('the hero explains automatic discovery without required config', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  const hero = page.slice(page.indexOf('class="hero"'), page.indexOf('id="problem"'));

  assert.match(hero, /automatically discovers and runs your services<br \/>/);
  assert.match(hero, /and dependencies — no config required — so you can trace<br \/>/);
  assert.match(hero, /without leaving your<br \/>local development workflow/);
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
  const frameDelays: number[] = [];
  for (let offset = 0; offset + 7 < topologyAnimation.length; offset += 1) {
    if (topologyAnimation[offset] === 0x21 && topologyAnimation[offset + 1] === 0xf9 && topologyAnimation[offset + 2] === 0x04) {
      frameDelays.push(topologyAnimation.readUInt16LE(offset + 4));
      offset += 7;
    }
  }
  assert.equal(frameDelays.length, 48);
  assert.ok(frameDelays.every((delay) => delay === 20), 'topology capture must play at the slower five-frames-per-second pace');

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
  assert.match(page, /<span class="demo-title__line">One system\.<\/span>/);
  assert.match(page, /<span class="demo-title__line demo-title__accent">Ready to debug\.<\/span>/);
  assert.doesNotMatch(page, /VideoDialog|data-open-video|<dialog/);
  assert.doesNotMatch(page, /Read the transcript|class="transcript"/);
  assert.doesNotMatch(page, /<track[^>]*\bdefault\b[^>]*>/);
  assert.match(page, /track\.mode = 'disabled'/);
  assert.match(page, /demoVideo\.pause\(\)/);
});

test('the page moves from the problem and solution into the product pillars', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  const footerStart = page.indexOf('class="site-footer"');
  const footerEnd = page.indexOf('</footer>', footerStart);
  const footer = page.slice(footerStart, footerEnd);
  const projectsStart = page.indexOf('class="section product-section"');
  const projectsEnd = page.indexOf('id="control-plane"');
  const projectsSection = page.slice(projectsStart, projectsEnd);
  const orderedMarkers = [
    'id="problem"',
    'id="why-portless"',
    'id="how-it-works"',
    'id="control-plane"',
    'id="experiments"',
  ];
  const positions = orderedMarkers.map((marker) => page.indexOf(marker));

  assert.ok(positions.every((position) => position >= 0), 'every narrative section must be present');
  assert.deepEqual([...positions].sort((left, right) => left - right), positions);
  assert.doesNotMatch(footer, /href="#(?:how-it-works|control-plane|experiments|demo)"/);
  assert.match(footer, />GitHub<\/a>/);
  assert.match(page, /<a href="#why-portless">Why Portless<\/a>/);
  assert.match(page, /eyebrow eyebrow--danger eyebrow--pillar">The problem/);
  assert.match(page, /eyebrow eyebrow--pillar">Why Portless/);
  assert.match(page, /eyebrow eyebrow--pillar">Locally</);
  assert.match(page, /<span class="principles-title__line">Your application\.<\/span>/);
  assert.match(page, /<span class="principles-title__line principles-title__accent">Your machine\.<\/span>/);
  assert.match(page, /Portless runs locally with no account or hosted service required\. Environment state, traffic, recordings, mocks, and faults stay on your machine\./);
  assert.match(page, /<PrincipleIcon name={principle\.icon} \/>/);
  assert.doesNotMatch(page, /principle\.index/);
  assert.match(page, /topology-title__line">Every service\.<\/span>/);
  assert.match(page, /topology-title__line">Every interaction\.<\/span>/);
  assert.match(page, /topology-title__line topology-title__line--accent">One live view\.<\/span>/);
  assert.match(page, /<span class="experiments-title__accent">Beyond<\/span> the happy path\./);
  assert.match(page, /Capture a workflow, replace one dependency with a deterministic mock, or inject latency and failures between services\. No application code changes required\./);
  assert.match(page, /<h3>Record Traffic<\/h3>/);
  assert.match(page, /Capture a failing workflow as it happens, then inspect the requests, responses, and timing that led to it\./);
  assert.match(page, /<h3>Mock Dependencies<\/h3>/);
  assert.match(page, /<h3>Inject Faults<\/h3>/);
  assert.match(page, /Target one service connection with latency, error responses, or dropped connections and see how the system reacts\./);
  assert.doesNotMatch(page, /<span>(Record|Mock|Fault)<\/span><h3>/);
  assert.doesNotMatch(page, /See the system as it runs/);
  assert.match(page, /gets <span>messy<\/span> fast/);
  assert.doesNotMatch(projectsSection, /Projects \+ environments/);
  assert.match(projectsSection, /<span>One application\.<\/span><br \/><span class="product-copy__accent">Many shapes\.<\/span>/);
  assert.match(projectsSection, /Mix local, container, remote, and mock providers/);
  assert.doesNotMatch(projectsSection, /Keep remote targets behind explicit write policies/);
  assert.ok(projectsSection.indexOf('product-window--projects') < projectsSection.indexOf('product-copy'));
  assert.doesNotMatch(page, /How it works/);
});

test('the solution resolves collisions before Run it explains the command', async () => {
  const page = await readFile(`${siteRoot}src/pages/index.astro`, 'utf8');
  const runStart = page.indexOf('id="how-it-works"');
  const runEnd = page.indexOf('class="section product-section"');
  const runSection = page.slice(runStart, runEnd);
  const trafficStart = page.indexOf('class="section traffic-section"');
  const trafficEnd = page.indexOf('class="section experiments narrative-section"');
  const trafficSection = page.slice(trafficStart, trafficEnd);

  assert.match(page, /id="why-portless"/);
  assert.match(page, /Every service gets/);
  assert.match(page, /<strong>orders<\/strong><code>localhost:8080/);
  assert.match(page, /<strong>inventory<\/strong><code>localhost:8080/);
  assert.match(page, /<strong>orders-db<\/strong><code>localhost:5432/);
  assert.match(page, /<strong>inventory-db<\/strong><code>localhost:5432/);
  assert.doesNotMatch(page, /<b>HTTP<\/b>|<b>Postgres<\/b>/);
  assert.match(page, /<strong>orders<\/strong><code><strong>orders<\/strong>\.local\.store\.localhost/);
  assert.match(page, /<strong>inventory<\/strong><code><strong>inventory<\/strong>\.local\.store\.localhost/);
  assert.match(page, /<strong>orders-db<\/strong><code><strong>orders-db<\/strong>\.local\.store\.portless\.test<\/code>/);
  assert.match(page, /<strong>inventory-db<\/strong><code><strong>inventory-db<\/strong>\.local\.store\.portless\.test<\/code>/);
  assert.match(runSection, /<p class="eyebrow eyebrow--pillar">Run it<\/p>/);
  assert.doesNotMatch(page, /eyebrow eyebrow--pillar">See it · live topology/);
  assert.match(page, /<p class="eyebrow eyebrow--pillar">See it<\/p>/);
  assert.doesNotMatch(trafficSection, /eyebrow eyebrow--pillar">See it/);
  assert.match(trafficSection, /Follow the request\. <span class="traffic-title__accent">Inspect<\/span> the exchange\./);
  assert.match(trafficSection, /Trace requests across services, then inspect timing, headers, and captured request and response bodies\./);
  assert.match(trafficSection, />Trace Waterfall<\/button>/);
  assert.match(trafficSection, />Exchange Detail<\/button>/);
  assert.doesNotMatch(trafficSection, /correlation confidence/i);
  assert.match(page, /<p class="eyebrow eyebrow--pillar">Break it<\/p>/);
  assert.doesNotMatch(page, /reproduce the failure/);
  assert.match(page, /<strong>Locally\.<\/strong>/);
  assert.doesNotMatch(page, /traffic inspector<\/p>/);
  assert.match(runSection, /<span>One command<\/span> starts/);
  assert.match(runSection, /Starting work shouldn't mean rebuilding your local environment by hand/);
  assert.match(runSection, /Run <code>portless up<\/code> from any checkout/);
  assert.match(runSection, /class="command-story"/);
  assert.match(runSection, /<span>Discovers<\/span> the application around you/);
  assert.match(runSection, /Related checkouts, services, and dependencies are discovered automatically/);
  assert.match(runSection, /<span>Launches<\/span> in dependency order/);
  assert.match(runSection, /Local processes and managed resources launch in the order they are needed/);
  assert.match(runSection, /<span>Publishes<\/span> stable service endpoints/);
  assert.match(runSection, /The control plane opens with addresses and health visible for every service/);
  assert.doesNotMatch(runSection, /class="command-flow"|class="run-command"|class="command-journey"/);
  assert.doesNotMatch(runSection, /Run it · one command/);
  assert.match(runSection, /portless up/);
  assert.doesNotMatch(runSection, /portless setup|portless ui/);
});
