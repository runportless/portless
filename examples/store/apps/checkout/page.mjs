export const checkoutPageHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="dark light">
    <title>Store checkout</title>
    <style>
      :root {
        color-scheme: dark;
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: #07110f;
        color: #e6f4ef;
        --surface: rgba(12, 30, 26, 0.92);
        --surface-raised: #102a25;
        --border: #285146;
        --muted: #91aaa2;
        --accent: #55ddb1;
        --accent-strong: #85f0cd;
        --danger: #ff9a9a;
      }

      * { box-sizing: border-box; }

      body {
        min-height: 100vh;
        margin: 0;
        display: grid;
        place-items: center;
        padding: 32px 20px;
        background:
          radial-gradient(circle at 15% 10%, rgba(52, 211, 153, 0.14), transparent 32rem),
          linear-gradient(145deg, #07110f, #091714 55%, #07110f);
      }

      main {
        width: min(880px, 100%);
        display: grid;
        grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.1fr);
        border: 1px solid var(--border);
        border-radius: 18px;
        overflow: hidden;
        background: var(--surface);
        box-shadow: 0 28px 80px rgba(0, 0, 0, 0.34);
      }

      section { padding: clamp(28px, 5vw, 48px); }
      section + section { border-left: 1px solid var(--border); }

      .eyebrow {
        margin: 0 0 12px;
        color: var(--accent);
        font: 700 0.72rem/1.2 ui-monospace, SFMono-Regular, Menlo, monospace;
        letter-spacing: 0.16em;
      }

      h1, h2 { margin: 0; letter-spacing: -0.03em; }
      h1 { font-size: clamp(2rem, 6vw, 3.5rem); line-height: 0.98; }
      h2 { font-size: 1.1rem; }

      .lede {
        margin: 18px 0 30px;
        color: var(--muted);
        line-height: 1.65;
      }

      code {
        padding: 2px 6px;
        border: 1px solid var(--border);
        border-radius: 5px;
        color: var(--accent-strong);
        font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
        font-size: 0.86em;
      }

      form { display: grid; gap: 18px; }
      label { display: grid; gap: 8px; font-size: 0.83rem; font-weight: 700; }

      select, input, button {
        width: 100%;
        min-height: 46px;
        border: 1px solid var(--border);
        border-radius: 9px;
        font: inherit;
      }

      select, input {
        padding: 0 12px;
        background: var(--surface-raised);
        color: inherit;
      }

      select:focus-visible, input:focus-visible, button:focus-visible {
        outline: 3px solid color-mix(in srgb, var(--accent) 38%, transparent);
        outline-offset: 2px;
      }

      button {
        margin-top: 4px;
        border-color: var(--accent);
        background: var(--accent);
        color: #03231a;
        font-weight: 800;
        cursor: pointer;
      }

      button:hover { background: var(--accent-strong); }
      button:disabled { cursor: wait; opacity: 0.62; }

      .response-heading {
        display: flex;
        align-items: baseline;
        justify-content: space-between;
        gap: 16px;
        margin-bottom: 18px;
      }

      #response-status {
        color: var(--muted);
        font: 700 0.74rem/1.2 ui-monospace, SFMono-Regular, Menlo, monospace;
      }

      #response-status[data-tone="success"] { color: var(--accent-strong); }
      #response-status[data-tone="error"] { color: var(--danger); }

      pre {
        min-height: 330px;
        max-height: 520px;
        margin: 0;
        padding: 18px;
        overflow: auto;
        border: 1px solid var(--border);
        border-radius: 10px;
        background: #06100e;
        color: #cce9df;
        font: 0.78rem/1.6 ui-monospace, SFMono-Regular, Menlo, monospace;
        white-space: pre-wrap;
        overflow-wrap: anywhere;
      }

      noscript { color: var(--danger); }

      @media (max-width: 720px) {
        main { grid-template-columns: 1fr; }
        section + section { border-top: 1px solid var(--border); border-left: 0; }
        pre { min-height: 220px; }
      }

      @media (prefers-color-scheme: light) {
        :root {
          color-scheme: light;
          background: #eef8f4;
          color: #10231e;
          --surface: rgba(250, 255, 253, 0.94);
          --surface-raised: #f3faf7;
          --border: #b9d8cd;
          --muted: #526c63;
          --accent: #087d5c;
          --accent-strong: #05684c;
          --danger: #b42318;
        }

        body {
          background:
            radial-gradient(circle at 15% 10%, rgba(5, 150, 105, 0.12), transparent 32rem),
            linear-gradient(145deg, #eef8f4, #f8fcfa 55%, #eef8f4);
        }

        main { box-shadow: 0 28px 80px rgba(22, 62, 49, 0.16); }
        pre { background: #10231e; color: #e1f4ed; }
        button { color: #ffffff; }
      }
    </style>
  </head>
  <body>
    <main>
      <section aria-labelledby="checkout-title">
        <p class="eyebrow">STORE / CHECKOUT</p>
        <h1 id="checkout-title">Create an order</h1>
        <p class="lede">Choose an item and submit the same JSON request a client sends to <code>POST /checkout</code>.</p>

        <form id="checkout-form">
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

          <button type="submit">Create order</button>
        </form>

        <noscript><p>JavaScript is required to submit the JSON request from this page.</p></noscript>
      </section>

      <section aria-labelledby="response-title">
        <div class="response-heading">
          <div>
            <p class="eyebrow">RESPONSE</p>
            <h2 id="response-title">Checkout result</h2>
          </div>
          <span id="response-status" data-tone="idle">NOT SENT</span>
        </div>
        <pre id="response-output" aria-live="polite">Submit an order to see the HTTP response.</pre>
      </section>
    </main>
    <script type="module" src="/checkout.js"></script>
  </body>
</html>
`

export const checkoutPageJavaScript = `const form = document.querySelector('#checkout-form')
const button = form.querySelector('button[type="submit"]')
const status = document.querySelector('#response-status')
const output = document.querySelector('#response-output')

function renderResponse(label, value, tone) {
  status.textContent = label
  status.dataset.tone = tone
  output.textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2)
}

form.addEventListener('submit', async (event) => {
  event.preventDefault()
  button.disabled = true
  button.textContent = 'Creating order...'
  renderResponse('SENDING', 'Waiting for checkout...', 'idle')

  const payload = {
    sku: form.elements.sku.value,
    quantity: Number(form.elements.quantity.value),
  }

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
    button.textContent = 'Create order'
  }
})
`
