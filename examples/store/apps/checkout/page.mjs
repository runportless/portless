export const checkoutPageHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="dark light">
    <meta name="theme-color" content="#071012">
    <title>Store checkout</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #071012;
        --surface: #0d171a;
        --surface-2: #111d20;
        --surface-3: #172529;
        --line: #223337;
        --line-bright: #31484d;
        --text: #d7e1e2;
        --muted: #72858a;
        --faint: #43565b;
        --heading: #e3ebec;
        --teal: #31c3b5;
        --teal-dim: #1a766f;
        --green: #59c69b;
        --red: #f05a67;
        --canvas-glow: rgba(36, 112, 107, 0.09);
        --selection-bg: rgba(49, 195, 181, 0.25);
        --selection-text: #ffffff;
        --topbar-bg: rgba(10, 19, 21, 0.94);
        --panel-bg: rgba(14, 24, 27, 0.74);
        --panel-highlight: rgba(255, 255, 255, 0.012);
        --accent-button-bg: rgba(21, 107, 100, 0.18);
        --accent-glow: rgba(49, 195, 181, 0.25);
        --danger-bg: rgba(240, 90, 103, 0.06);
        --danger-border: #6c3239;
        --log-surface: #050a0b;
        --log-text: #aebfc2;
        --grid-accent: rgba(49, 195, 181, 0.045);
        background: var(--bg);
        color: var(--text);
        font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
        font-synthesis: none;
        text-rendering: optimizeLegibility;
      }

      html[data-theme="light"] {
        color-scheme: light;
        --bg: #f4f7f7;
        --surface: #ffffff;
        --surface-2: #edf2f2;
        --surface-3: #e4ecec;
        --line: #d6e0e1;
        --line-bright: #b7c6c8;
        --text: #172527;
        --muted: #52676b;
        --faint: #6d7f83;
        --heading: #111e20;
        --teal: #0f766e;
        --teal-dim: #4c9c95;
        --green: #16815a;
        --red: #c9364a;
        --canvas-glow: rgba(15, 118, 110, 0.07);
        --selection-bg: rgba(15, 118, 110, 0.2);
        --selection-text: #111e20;
        --topbar-bg: rgba(249, 251, 251, 0.95);
        --panel-bg: rgba(255, 255, 255, 0.92);
        --panel-highlight: rgba(255, 255, 255, 0.8);
        --accent-button-bg: rgba(15, 118, 110, 0.09);
        --accent-glow: rgba(15, 118, 110, 0.18);
        --danger-bg: rgba(201, 54, 74, 0.07);
        --danger-border: #d8a2aa;
        --log-surface: #edf2f2;
        --log-text: #31464a;
        --grid-accent: rgba(15, 118, 110, 0.07);
      }

      * { box-sizing: border-box; }

      html, body {
        min-width: 320px;
        min-height: 100%;
        margin: 0;
      }

      body {
        min-height: 100vh;
        background:
          linear-gradient(var(--grid-accent) 1px, transparent 1px),
          linear-gradient(90deg, var(--grid-accent) 1px, transparent 1px),
          radial-gradient(circle at 70% -20%, var(--canvas-glow), transparent 34%),
          var(--bg);
        background-size: 56px 56px, 56px 56px, auto, auto;
      }

      button, input, select { font: inherit; }
      button { color: inherit; }
      code, pre { font: inherit; }
      ::selection { background: var(--selection-bg); color: var(--selection-text); }

      .topbar {
        height: 64px;
        padding: 0 24px;
        border-bottom: 1px solid var(--line);
        background: var(--topbar-bg);
        backdrop-filter: blur(14px);
        display: grid;
        grid-template-columns: 1fr auto;
        align-items: center;
      }

      .brand {
        color: var(--text);
        display: flex;
        align-items: center;
        gap: 10px;
        font-size: 17px;
        font-weight: 800;
        letter-spacing: -0.04em;
      }

      .brand__signal {
        height: 16px;
        display: flex;
        gap: 2px;
        align-items: end;
      }

      .brand__signal i {
        width: 3px;
        display: block;
        background: var(--teal);
        box-shadow: 0 0 10px var(--accent-glow);
      }

      .brand__signal i:nth-child(1) { height: 7px; opacity: 0.6; }
      .brand__signal i:nth-child(2) { height: 12px; opacity: 0.8; }
      .brand__signal i:nth-child(3) { height: 16px; }
      .brand__context { color: var(--faint); font-size: 10px; font-weight: 600; letter-spacing: 0.08em; }

      .topbar__tools { justify-self: end; }

      .theme-toggle {
        height: 36px;
        padding: 0 10px;
        border: 1px solid var(--line);
        background: var(--surface);
        color: var(--muted);
        display: inline-flex;
        align-items: center;
        gap: 8px;
        cursor: pointer;
        font-size: 9px;
        font-weight: 800;
        letter-spacing: 0.08em;
      }

      .theme-toggle:hover { border-color: var(--line-bright); color: var(--text); }
      .theme-toggle:focus-visible { outline: 1px solid var(--teal); outline-offset: 3px; }

      .theme-toggle__glyph {
        width: 16px;
        height: 16px;
        border: 1px solid currentColor;
        border-radius: 50%;
        background: linear-gradient(90deg, currentColor 0 50%, transparent 50% 100%);
      }

      .page {
        width: min(1180px, 100%);
        margin: 0 auto;
        padding: 36px 28px 70px;
      }

      .page-heading {
        min-height: 66px;
        margin-bottom: 22px;
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 28px;
      }

      h1, h2, p { margin-top: 0; }
      h1 { margin-bottom: 0; color: var(--heading); font-size: 24px; letter-spacing: -0.04em; }
      h2 { margin: 0; color: var(--text); font-size: 11px; letter-spacing: 0; }

      .page-heading__copy {
        max-width: 540px;
        margin: 12px 0 0;
        color: var(--muted);
        font-size: 10px;
        line-height: 1.6;
      }

      .workspace {
        display: grid;
        grid-template-columns: minmax(330px, 0.88fr) minmax(420px, 1.12fr);
        gap: 20px;
        align-items: stretch;
      }

      .panel {
        min-width: 0;
        border: 1px solid var(--line);
        background: var(--panel-bg);
        box-shadow: inset 0 1px var(--panel-highlight);
      }

      .panel-title {
        min-height: 43px;
        padding: 0 13px;
        border-bottom: 1px solid var(--line);
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 14px;
      }

      .panel-title > span:first-child {
        color: var(--faint);
        font-size: 9px;
        font-weight: 800;
        letter-spacing: 0.1em;
      }

      .panel-body { padding: 22px; }
      form { display: grid; gap: 16px; }
      .field-grid { display: grid; grid-template-columns: minmax(0, 1fr) 120px; gap: 12px; }

      label {
        display: grid;
        gap: 7px;
        color: var(--faint);
        font-size: 8px;
        font-weight: 800;
        letter-spacing: 0.09em;
        text-transform: uppercase;
      }

      select, input {
        width: 100%;
        height: 38px;
        padding: 0 10px;
        border: 1px solid var(--line);
        border-radius: 0;
        outline: 0;
        background: var(--bg);
        color: var(--text);
        font-size: 11px;
      }

      select:hover, input:hover { border-color: var(--line-bright); }
      select:focus, input:focus { border-color: var(--teal-dim); box-shadow: inset 2px 0 var(--teal); }

      .request-preview { margin-top: 4px; border: 1px solid var(--line); }

      .request-preview__title {
        min-height: 34px;
        padding: 0 11px;
        border-bottom: 1px solid var(--line);
        color: var(--faint);
        display: flex;
        align-items: center;
        justify-content: space-between;
        font-size: 8px;
        font-weight: 800;
        letter-spacing: 0.08em;
      }

      .request-preview__title code { color: var(--teal); font-weight: 500; letter-spacing: 0; }

      #request-preview {
        min-height: 98px;
        margin: 0;
        padding: 13px;
        background: var(--log-surface);
        color: var(--log-text);
        font-size: 10px;
        line-height: 1.6;
        white-space: pre-wrap;
      }

      .submit-button {
        min-height: 35px;
        padding: 0 13px;
        border: 1px solid var(--teal-dim);
        background: var(--accent-button-bg);
        color: var(--teal);
        display: inline-flex;
        align-items: center;
        justify-content: center;
        font-size: 9px;
        font-weight: 800;
        letter-spacing: 0.08em;
        cursor: pointer;
      }

      .submit-button:hover:not(:disabled) { border-color: var(--teal); background: var(--surface-3); color: var(--text); }
      .submit-button:focus-visible { outline: 1px solid var(--teal); outline-offset: 3px; }
      .submit-button:disabled { cursor: wait; opacity: 0.45; }

      #response-status {
        color: var(--faint);
        font-size: 8px;
        font-weight: 800;
        letter-spacing: 0.08em;
      }

      #response-status[data-tone="success"] { color: var(--green); }
      #response-status[data-tone="error"] { color: var(--red); }

      .response-viewport { position: relative; height: calc(100% - 43px); min-height: 414px; }

      #response-output {
        width: 100%;
        min-height: 100%;
        margin: 0;
        padding: 16px;
        border: 0;
        background: var(--log-surface);
        color: var(--log-text);
        font-size: 10px;
        line-height: 1.7;
        white-space: pre-wrap;
        overflow-wrap: anywhere;
        overflow: auto;
        scrollbar-color: var(--line-bright) transparent;
        scrollbar-width: thin;
      }

      .response-panel:has(#response-status[data-tone="error"]) { border-color: var(--danger-border); }
      .response-panel:has(#response-status[data-tone="error"]) .panel-title { background: var(--danger-bg); }
      noscript { color: var(--red); font-size: 10px; }

      @media (max-width: 820px) {
        .workspace { grid-template-columns: 1fr; }
        .response-viewport { min-height: 320px; }
      }

      @media (max-width: 560px) {
        .topbar { padding: 0 16px; }
        .brand__context { display: none; }
        .page { padding: 28px 16px 48px; }
        .page-heading { display: block; }
        .field-grid { grid-template-columns: 1fr; }
        .theme-toggle__label { display: none; }
      }
    </style>
  </head>
  <body>
    <header class="topbar">
      <div class="brand" aria-label="Portless Store">
        <span class="brand__signal" aria-hidden="true"><i></i><i></i><i></i></span>
        <span>portless</span>
        <span class="brand__context">/ STORE</span>
      </div>
      <div class="topbar__tools">
        <button class="theme-toggle" id="theme-toggle" type="button" aria-label="Switch to light theme">
          <span class="theme-toggle__glyph" aria-hidden="true"></span>
          <span class="theme-toggle__label" id="theme-toggle-label">LIGHT</span>
        </button>
      </div>
    </header>

    <main class="page">
      <header class="page-heading">
        <div>
          <h1>Checkout</h1>
          <p class="page-heading__copy">Create an order and view the response.</p>
        </div>
      </header>

      <div class="workspace">
        <section class="panel" aria-labelledby="request-title">
          <header class="panel-title">
            <span id="request-title">REQUEST BUILDER</span>
          </header>
          <div class="panel-body">
            <form id="checkout-form">
              <div class="field-grid">
                <label for="sku">
                  Item
                  <select id="sku" name="sku">
                    <option value="coffee-mug">Ceramic Coffee Mug</option>
                    <option value="mechanical-keyboard">Mechanical Keyboard</option>
                    <option value="usb-c-cable">USB-C Cable (out of stock)</option>
                  </select>
                </label>

                <label for="quantity">
                  Quantity
                  <input id="quantity" name="quantity" type="number" min="1" max="100" step="1" value="1" required>
                </label>
              </div>

              <div class="request-preview">
                <div class="request-preview__title"><span>JSON BODY</span><code>application/json</code></div>
                <pre id="request-preview" aria-label="Request body"></pre>
              </div>

              <button class="submit-button" type="submit">CREATE ORDER</button>
            </form>
            <noscript><p>JavaScript is required to submit the JSON request from this page.</p></noscript>
          </div>
        </section>

        <section class="panel response-panel" aria-labelledby="response-title">
          <header class="panel-title">
            <span id="response-title">RESPONSE</span>
            <span id="response-status" data-tone="idle">NOT SENT</span>
          </header>
          <div class="response-viewport">
            <pre id="response-output" aria-live="polite">Submit an order to see the HTTP response.</pre>
          </div>
        </section>
      </div>
    </main>
    <script type="module" src="/checkout.js"></script>
  </body>
</html>
`

export const checkoutPageJavaScript = `const themeStorageKey = 'portless.theme'
const root = document.documentElement
const themeColor = document.querySelector('meta[name="theme-color"]')
const themeToggle = document.querySelector('#theme-toggle')
const themeToggleLabel = document.querySelector('#theme-toggle-label')
const form = document.querySelector('#checkout-form')
const button = form.querySelector('button[type="submit"]')
const requestPreview = document.querySelector('#request-preview')
const status = document.querySelector('#response-status')
const output = document.querySelector('#response-output')

function initialTheme() {
  try {
    const stored = window.localStorage.getItem(themeStorageKey)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {}
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function applyTheme(theme) {
  const next = theme === 'dark' ? 'light' : 'dark'
  root.dataset.theme = theme
  root.style.colorScheme = theme
  themeColor.setAttribute('content', theme === 'dark' ? '#071012' : '#f4f7f7')
  themeToggleLabel.textContent = next.toUpperCase()
  themeToggle.setAttribute('aria-label', 'Switch to ' + next + ' theme')
}

function checkoutPayload() {
  return {
    sku: form.elements.sku.value,
    quantity: Number(form.elements.quantity.value),
  }
}

function updateRequestPreview() {
  requestPreview.textContent = JSON.stringify(checkoutPayload(), null, 2)
}

function renderResponse(label, value, tone) {
  status.textContent = label
  status.dataset.tone = tone
  output.textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2)
}

applyTheme(initialTheme())
updateRequestPreview()

themeToggle.addEventListener('click', () => {
  const next = root.dataset.theme === 'dark' ? 'light' : 'dark'
  try { window.localStorage.setItem(themeStorageKey, next) } catch {}
  applyTheme(next)
})

form.addEventListener('input', updateRequestPreview)
form.addEventListener('submit', async (event) => {
  event.preventDefault()
  button.disabled = true
  button.textContent = 'CREATING ORDER...'
  renderResponse('SENDING', 'Waiting for checkout...', 'idle')

  const payload = checkoutPayload()
  try {
    const response = await fetch('/checkout', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(payload),
    })
    const encoded = await response.text()
    let value = encoded
    try {
      value = encoded ? JSON.parse(encoded) : {}
    } catch {}
    renderResponse('HTTP ' + response.status, value, response.ok ? 'success' : 'error')
  } catch (error) {
    renderResponse('REQUEST FAILED', error instanceof Error ? error.message : String(error), 'error')
  } finally {
    button.disabled = false
    button.textContent = 'CREATE ORDER'
  }
})
`
