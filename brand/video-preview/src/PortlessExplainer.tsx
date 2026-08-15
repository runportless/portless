import React from 'react';
import {
  AbsoluteFill,
  Easing,
  Img,
  Sequence,
  interpolate,
  spring,
  staticFile,
  useCurrentFrame,
  useVideoConfig,
} from 'remotion';

const colors = {
  bg: '#071013',
  surface: '#0d171a',
  surfaceBright: '#122125',
  line: '#203238',
  lineBright: '#33494f',
  text: '#e5ecee',
  muted: '#819197',
  faint: '#52636a',
  teal: '#31c3b5',
  green: '#5cc99b',
  amber: '#e2ad39',
  red: '#ed5565',
};

const mono = 'SFMono-Regular, Menlo, Monaco, Consolas, monospace';
const sans = '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
const totalFrames = 1395;

function fade(frame: number, duration: number, edge = 14) {
  return interpolate(frame, [0, edge, duration - edge, duration], [0, 1, 1, 0], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });
}

function Logo({size = 30}: {size?: number}) {
  const unit = size / 3.25;
  return (
    <div style={{display: 'flex', alignItems: 'flex-end', gap: unit * 0.48, height: size}}>
      {[0.44, 0.75, 1].map((height, index) => (
        <div
          key={height}
          style={{
            width: unit * 0.72,
            height: size * height,
            background: colors.teal,
            opacity: 0.62 + index * 0.18,
          }}
        />
      ))}
    </div>
  );
}

function Grid() {
  return (
    <AbsoluteFill
      style={{
        opacity: 0.24,
        backgroundImage:
          'linear-gradient(rgba(49,195,181,.08) 1px,transparent 1px),linear-gradient(90deg,rgba(49,195,181,.08) 1px,transparent 1px)',
        backgroundSize: '32px 32px',
        maskImage: 'radial-gradient(circle at 50% 45%, black, transparent 78%)',
      }}
    />
  );
}

function Chrome({children}: {children: React.ReactNode}) {
  return (
    <AbsoluteFill style={{background: colors.bg, color: colors.text, fontFamily: sans}}>
      <Grid />
      {children}
    </AbsoluteFill>
  );
}

function Progress() {
  const frame = useCurrentFrame();
  const progress = interpolate(frame, [0, totalFrames], [0, 100], {extrapolateRight: 'clamp'});
  return (
    <div style={{position: 'absolute', zIndex: 20, top: 0, left: 0, width: `${progress}%`, height: 3, background: colors.teal}} />
  );
}

function CornerBrand() {
  return (
    <div style={{position: 'absolute', zIndex: 15, right: 38, top: 30, display: 'flex', alignItems: 'center', gap: 10, opacity: 0.75}}>
      <Logo size={19} />
      <span style={{fontFamily: mono, fontWeight: 700, fontSize: 14, letterSpacing: -0.4}}>portless</span>
    </div>
  );
}

function Intro() {
  const frame = useCurrentFrame();
  const enter = spring({frame, fps: 30, config: {damping: 18, stiffness: 90}});
  const line = interpolate(frame, [42, 88], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
  return (
    <Chrome>
      <div style={{position: 'absolute', left: 112, top: 180, opacity: enter, transform: `translateY(${(1 - enter) * 32}px)`}}>
        <div style={{display: 'flex', alignItems: 'center', gap: 20}}>
          <Logo size={54} />
          <div style={{fontFamily: mono, fontWeight: 800, fontSize: 54, letterSpacing: -3}}>portless</div>
        </div>
        <h1 style={{margin: '44px 0 0', maxWidth: 920, fontFamily: mono, fontSize: 48, lineHeight: 1.12, letterSpacing: -2.3, fontWeight: 600}}>
          Own your entire<br />local environment.
        </h1>
        <div style={{marginTop: 32, width: 510 * line, height: 2, background: colors.teal, boxShadow: `0 0 24px ${colors.teal}`}} />
      </div>
      <div style={{position: 'absolute', bottom: 46, left: 112, color: colors.muted, fontFamily: mono, fontSize: 15, letterSpacing: 0.5}}>
        RUN IT · SEE IT · BREAK IT
      </div>
    </Chrome>
  );
}

const conflictCards = [
  {name: 'payments', service: 'postgres', port: ':5432', color: colors.red},
  {name: 'billing', service: 'postgres', port: ':5432', color: colors.red},
  {name: 'checkout', service: 'redis', port: ':6379', color: colors.amber},
  {name: 'orders', service: 'redis', port: ':6379', color: colors.amber},
];

function Problem() {
  const frame = useCurrentFrame();
  const duration = 180;
  return (
    <Chrome>
      <CornerBrand />
      <div style={{opacity: fade(frame, duration), position: 'absolute', inset: 0}}>
        <div style={{position: 'absolute', left: 76, top: 92}}>
          <div style={{fontFamily: mono, color: colors.red, fontSize: 13, letterSpacing: 2}}>THE PROBLEM</div>
          <h2 style={{margin: '16px 0 0', fontFamily: mono, fontSize: 40, lineHeight: 1.16, letterSpacing: -1.5}}>Local development<br />gets messy fast.</h2>
          <p style={{width: 420, marginTop: 24, color: colors.muted, fontSize: 19, lineHeight: 1.55}}>Ports collide. Services scatter across terminals. The environment becomes harder to understand than the application.</p>
        </div>
        <div style={{position: 'absolute', right: 72, top: 100, width: 560, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14}}>
          {conflictCards.map((item, index) => {
            const show = spring({frame: frame - 12 - index * 9, fps: 30, config: {damping: 17, stiffness: 120}});
            return (
              <div key={`${item.name}-${item.service}`} style={{height: 154, padding: 20, border: `1px solid ${colors.lineBright}`, background: colors.surface, opacity: show, transform: `translateY(${(1 - show) * 24}px)`}}>
                <div style={{fontFamily: mono, color: colors.faint, fontSize: 11, letterSpacing: 1.3}}>PROJECT / {item.name}</div>
                <div style={{marginTop: 24, display: 'flex', justifyContent: 'space-between', alignItems: 'center'}}>
                  <strong style={{fontFamily: mono, fontSize: 19}}>{item.service}</strong>
                  <code style={{fontFamily: mono, color: item.color, fontSize: 20}}>{item.port}</code>
                </div>
                <div style={{marginTop: 22, color: item.color, fontFamily: mono, fontSize: 11}}>▲ ADDRESS ALREADY IN USE</div>
              </div>
            );
          })}
        </div>
      </div>
    </Chrome>
  );
}

function Terminal() {
  const frame = useCurrentFrame();
  const duration = 180;
  const command = 'portless up';
  const typed = command.slice(0, Math.floor(interpolate(frame, [18, 70], [0, command.length], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'})));
  const lines = [
    {at: 73, text: 'discovered checkout, inventory, orders', color: colors.green},
    {at: 88, text: 'started postgres and redis with docker', color: colors.green},
    {at: 103, text: 'store/local  healthy  5/5 ready', color: colors.teal},
    {at: 122, text: 'http://checkout.local.store.localhost', color: colors.text},
  ];
  return (
    <Chrome>
      <CornerBrand />
      <div style={{opacity: fade(frame, duration), position: 'absolute', left: 84, right: 84, top: 96, bottom: 72}}>
        <div style={{fontFamily: mono, color: colors.teal, fontSize: 13, letterSpacing: 2}}>ONE COMMAND</div>
        <h2 style={{margin: '13px 0 26px', fontFamily: mono, fontSize: 38, letterSpacing: -1.4}}>Stand up the whole environment.</h2>
        <div style={{height: 420, border: `1px solid ${colors.lineBright}`, background: '#04090b', boxShadow: '0 26px 90px rgba(0,0,0,.38)'}}>
          <div style={{height: 46, borderBottom: `1px solid ${colors.line}`, display: 'flex', alignItems: 'center', gap: 8, paddingLeft: 18}}>
            {[colors.red, colors.amber, colors.green].map((color) => <i key={color} style={{display: 'block', width: 9, height: 9, borderRadius: '50%', background: color, opacity: 0.75}} />)}
            <span style={{marginLeft: 12, color: colors.faint, fontFamily: mono, fontSize: 11}}>~/workspace/store</span>
          </div>
          <div style={{padding: '30px 34px', fontFamily: mono, fontSize: 19, lineHeight: 2.15}}>
            <div><span style={{color: colors.teal}}>$</span> {typed}<span style={{opacity: frame % 20 < 11 ? 1 : 0, color: colors.teal}}>▍</span></div>
            {lines.map((line) => (
              <div key={line.text} style={{opacity: interpolate(frame, [line.at, line.at + 8], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'}), color: line.color}}>
                <span style={{color: colors.green}}>✓</span> {line.text}
              </div>
            ))}
          </div>
        </div>
      </div>
    </Chrome>
  );
}

function ProductFrame({image, zoom = 1.035, x = 0, y = 0}: {image: string; zoom?: number; x?: number; y?: number}) {
  const frame = useCurrentFrame();
  const drift = interpolate(frame, [0, 220], [0, 1], {extrapolateRight: 'clamp'});
  return (
    <div style={{position: 'absolute', inset: '46px 58px 54px', overflow: 'hidden', border: `1px solid ${colors.lineBright}`, background: colors.surface, boxShadow: '0 28px 100px rgba(0,0,0,.5)'}}>
      <Img
        src={staticFile(`captures/${image}`)}
        style={{width: '100%', height: '100%', objectFit: 'cover', transform: `translate(${x * drift}px, ${y * drift}px) scale(${1 + (zoom - 1) * drift})`}}
      />
      <div style={{position: 'absolute', inset: 0, boxShadow: 'inset 0 0 70px rgba(0,0,0,.18)', pointerEvents: 'none'}} />
    </div>
  );
}

function Callout({eyebrow, title, copy, align = 'left'}: {eyebrow: string; title: string; copy: string; align?: 'left' | 'right'}) {
  const frame = useCurrentFrame();
  const show = spring({frame: frame - 12, fps: 30, config: {damping: 18, stiffness: 110}});
  return (
    <div style={{position: 'absolute', zIndex: 10, bottom: 72, [align]: 82, width: 465, padding: '20px 22px', border: `1px solid ${colors.lineBright}`, background: 'rgba(7,16,19,.93)', boxShadow: '0 18px 50px rgba(0,0,0,.48)', opacity: show, transform: `translateY(${(1 - show) * 18}px)`}}>
      <div style={{fontFamily: mono, color: colors.teal, fontSize: 10, letterSpacing: 1.7}}>{eyebrow}</div>
      <div style={{marginTop: 8, fontFamily: mono, fontWeight: 700, fontSize: 23}}>{title}</div>
      <div style={{marginTop: 9, color: colors.muted, fontSize: 14, lineHeight: 1.45}}>{copy}</div>
    </div>
  );
}

function Projects() {
  const frame = useCurrentFrame();
  return (
    <Chrome>
      <div style={{opacity: fade(frame, 120)}}>
        <ProductFrame image="projects.png" zoom={1.045} x={-10} y={-5} />
        <Callout eyebrow="PROJECTS + ENVIRONMENTS" title="One application. Many shapes." copy="Reuse the project model, then choose what runs locally, in a container, or against a remote environment." />
      </div>
    </Chrome>
  );
}

function FlowDots() {
  const frame = useCurrentFrame();
  return (
    <>
      {[0, 22, 44].map((offset) => {
        const phase = ((frame + offset) % 70) / 70;
        return <i key={offset} style={{position: 'absolute', zIndex: 8, left: 455 + phase * 370, top: 400, width: 7, height: 7, borderRadius: '50%', background: colors.teal, boxShadow: `0 0 15px ${colors.teal}`, opacity: Math.sin(phase * Math.PI)}} />;
      })}
    </>
  );
}

function Topology() {
  const frame = useCurrentFrame();
  return (
    <Chrome>
      <div style={{opacity: fade(frame, 225)}}>
        <ProductFrame image="topology-live.png" zoom={1.075} x={-14} y={-8} />
        <FlowDots />
        <Callout eyebrow="LIVE TOPOLOGY" title="See the system as it runs." copy="Watch real traffic move from checkout to orders, Postgres, and Redis—with rate and latency on every HTTP edge." align="right" />
      </div>
    </Chrome>
  );
}

function Traffic() {
  const frame = useCurrentFrame();
  const duration = 225;
  const detail = interpolate(frame, [48, 75], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp', easing: Easing.out(Easing.cubic)});
  return (
    <Chrome>
      <div style={{opacity: fade(frame, duration)}}>
        <div style={{position: 'absolute', inset: '46px 58px 54px', overflow: 'hidden', border: `1px solid ${colors.lineBright}`, background: colors.surface, boxShadow: '0 28px 100px rgba(0,0,0,.5)'}}>
          <Img src={staticFile('captures/traffic.png')} style={{position: 'absolute', width: '100%', height: '100%', objectFit: 'cover', transform: `scale(${1 + frame / 9000})`}} />
          <Img src={staticFile('captures/traffic-detail.png')} style={{position: 'absolute', width: '100%', height: '100%', objectFit: 'cover', opacity: detail, transform: `translateX(${(1 - detail) * 34}px)`}} />
        </div>
        <Callout eyebrow="TRAFFIC INSPECTOR" title="Open the complete exchange." copy="Inspect the edge, timing, redacted headers, request body, status, and formatted response body." />
      </div>
    </Chrome>
  );
}

function ExperimentCard({image, label, title, copy, delay}: {image: string; label: string; title: string; copy: string; delay: number}) {
  const frame = useCurrentFrame();
  const show = spring({frame: frame - delay, fps: 30, config: {damping: 20, stiffness: 100}});
  return (
    <div style={{height: 505, minWidth: 0, border: `1px solid ${colors.lineBright}`, background: colors.surface, overflow: 'hidden', opacity: show, transform: `translateY(${(1 - show) * 28}px)`}}>
      <div style={{height: 310, overflow: 'hidden', borderBottom: `1px solid ${colors.line}`}}>
        <Img src={staticFile(`captures/${image}`)} style={{width: '100%', height: '100%', objectFit: 'cover', objectPosition: 'left center'}} />
      </div>
      <div style={{padding: 22}}>
        <div style={{fontFamily: mono, color: colors.teal, fontSize: 10, letterSpacing: 1.6}}>{label}</div>
        <div style={{marginTop: 9, fontFamily: mono, fontSize: 22, fontWeight: 700}}>{title}</div>
        <div style={{marginTop: 10, color: colors.muted, fontSize: 14, lineHeight: 1.45}}>{copy}</div>
      </div>
    </div>
  );
}

function Experiments() {
  const frame = useCurrentFrame();
  return (
    <Chrome>
      <CornerBrand />
      <div style={{position: 'absolute', left: 66, right: 66, top: 86, opacity: fade(frame, 195)}}>
        <div style={{fontFamily: mono, fontSize: 31, fontWeight: 700, letterSpacing: -1}}>Reproduce the failure—not just the happy path.</div>
        <div style={{display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 18, marginTop: 28}}>
          <ExperimentCard image="recordings.png" label="RECORD" title="Keep the evidence." copy="Capture a bounded workflow and retain the requests that matter." delay={12} />
          <ExperimentCard image="faults.png" label="FAULT" title="Break one edge." copy="Inject latency, HTTP status codes, or aborted connections without changing application code." delay={25} />
        </div>
      </div>
    </Chrome>
  );
}

function Outro() {
  const frame = useCurrentFrame();
  const duration = 135;
  const show = spring({frame: frame - 5, fps: 30, config: {damping: 18, stiffness: 85}});
  return (
    <Chrome>
      <div style={{opacity: fade(frame, duration, 12), position: 'absolute', inset: 0, display: 'grid', placeItems: 'center'}}>
        <div style={{textAlign: 'center', transform: `scale(${0.94 + show * 0.06})`, opacity: show}}>
          <div style={{display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 18}}><Logo size={48} /><span style={{fontFamily: mono, fontSize: 49, fontWeight: 800, letterSpacing: -2.5}}>portless</span></div>
          <div style={{marginTop: 42, fontFamily: mono, fontSize: 38, fontWeight: 600, letterSpacing: -1.4}}>Run it. See it. Break it.</div>
          <div style={{marginTop: 18, color: colors.teal, fontFamily: mono, fontSize: 22}}>locally.</div>
          <div style={{marginTop: 50, display: 'inline-flex', alignItems: 'center', padding: '14px 22px', border: `1px solid ${colors.lineBright}`, background: colors.surface}}>
            <code style={{fontFamily: mono, fontSize: 17}}><span style={{color: colors.teal}}>$</span> portless up</code>
          </div>
        </div>
      </div>
    </Chrome>
  );
}

export const PortlessExplainer: React.FC = () => (
  <AbsoluteFill style={{background: colors.bg}}>
    <Sequence from={0} durationInFrames={135}><Intro /></Sequence>
    <Sequence from={135} durationInFrames={180}><Problem /></Sequence>
    <Sequence from={315} durationInFrames={180}><Terminal /></Sequence>
    <Sequence from={495} durationInFrames={120}><Projects /></Sequence>
    <Sequence from={615} durationInFrames={225}><Topology /></Sequence>
    <Sequence from={840} durationInFrames={225}><Traffic /></Sequence>
    <Sequence from={1065} durationInFrames={195}><Experiments /></Sequence>
    <Sequence from={1260} durationInFrames={135}><Outro /></Sequence>
    <Progress />
  </AbsoluteFill>
);
