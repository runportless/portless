package runtime

import "html"

const unavailablePageStart = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="dark light">
  <meta http-equiv="refresh" content="2">
  <title>Portless unavailable</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #071012;
      --text: #d7e1e2;
      --muted: #72858a;
      --line: #223337;
      --teal: #31c3b5;
      --glow: rgba(49, 195, 181, .2);
    }
    @media (prefers-color-scheme: light) {
      :root {
        color-scheme: light;
        --bg: #f4f7f7;
        --text: #172527;
        --muted: #52676b;
        --line: #d6e0e1;
        --teal: #0f766e;
        --glow: rgba(15, 118, 110, .16);
      }
    }
    * { box-sizing: border-box; }
    html, body { min-width: 320px; min-height: 100%; margin: 0; }
    body {
      min-height: 100vh;
      min-height: 100dvh;
      background: radial-gradient(circle at 50% 42%, var(--glow), transparent 24%), var(--bg);
      color: var(--text);
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      font-synthesis: none;
      text-rendering: optimizeLegibility;
    }
    main { min-height: 100vh; min-height: 100dvh; padding: 32px; display: grid; place-items: center; }
    .status { display: flex; flex-direction: column; align-items: center; text-align: center; }
    .brand { display: flex; align-items: center; gap: 12px; color: var(--text); letter-spacing: -.04em; }
    .brand strong { font-size: 26px; font-weight: 800; }
    .signal { height: 24px; display: flex; align-items: flex-end; gap: 3px; }
    .signal i { width: 4px; background: var(--teal); box-shadow: 0 0 12px var(--glow); }
    .signal i:nth-child(1) { height: 10px; opacity: .6; }
    .signal i:nth-child(2) { height: 17px; opacity: .8; }
    .signal i:nth-child(3) { height: 24px; }
    .spinner {
      width: 28px;
      height: 28px;
      margin: 28px 0 20px;
      border: 2px solid var(--line);
      border-top-color: var(--teal);
      border-radius: 50%;
      animation: spin .9s linear infinite;
    }
    p { max-width: 560px; margin: 0; color: var(--muted); font-size: 13px; line-height: 1.7; }
    @keyframes spin { to { transform: rotate(360deg); } }
    @media (prefers-reduced-motion: reduce) { .spinner { animation: none; } }
  </style>
</head>
<body>
  <main>
    <section class="status" role="status" aria-live="polite">
      <div class="brand" aria-label="Portless">
        <span class="signal" aria-hidden="true"><i></i><i></i><i></i></span>
        <strong>portless</strong>
      </div>
      <div class="spinner" aria-hidden="true"></div>
      <p>`

const unavailablePageEnd = `</p>
    </section>
  </main>
</body>
</html>`

func renderUnavailablePage(message string) string {
	return unavailablePageStart + html.EscapeString(message) + unavailablePageEnd
}
