# Chat

A live chat room with presence, typing indicators, and reconnection. Its two
Fastify services demonstrate WebSockets through Portless application ingress
and a source-aware dependency proxy.

The latest 50 messages stay in memory. Chat needs no database, container
engine, account, frontend build, or Portless configuration file.

## Run

Install Node.js 22.12 or newer, npm, and a configured Portless installation
with WebSocket support. See the [root README](../../README.md#install) for
building Portless and its one-time machine setup.

From the repository root:

```bash
make example-chat-dependencies
cd examples/chat
portless up
```

If using this checkout's executable, substitute `../../bin/portless up` while
inside `examples/chat`.

Portless discovers `chat/local`, starts both services, and opens its control
plane. Open the `chat` service:

[Open Chat](http://chat.local.chat.localhost)

Choose a display name and select **Join room**. Use **Open another tab** to
join independently. Names can repeat; a numbered badge distinguishes members
with the same name. Each tab owns its connection and draft. Enter sends;
Shift+Enter adds a new line.

## Try reconnection

Run these commands from this example directory, or use Portless's service
controls:

```bash
portless service restart rooms
portless service restart chat
```

Restarting `rooms` clears membership and history. Both tabs reconnect and join
the fresh room. Restarting only `chat` preserves history in `rooms`. A normal
`portless daemon restart` also preserves application processes and history.

For a longer interruption:

```bash
portless service stop rooms
portless service start rooms
```

The page remains available and retries while `rooms` is stopped. Your draft
stays in the tab; **Leave room** cancels retries. Reloading the page starts
with a fresh join form.

Stop this example with `portless down`. Starting it again with `portless up`
creates a fresh in-memory conversation.

## What Portless sees

```text
browser → Portless application ingress → chat
chat    → Portless dependency proxy    → rooms
```

Each browser WebSocket has a corresponding connection from `chat` to `rooms`.
The browser uses its own origin. The `chat` service uses the injected
`ROOMS_URL`; `.env.example` supplies static discovery evidence and is not loaded
over the injected runtime configuration.

While tabs remain connected:

```bash
portless traffic list --edge external:chat
portless traffic list --edge chat:rooms
portless traffic show <sequence>
```

Traffic shows the opening HTTP 101 handshakes. Portless does not capture or
replay the chat messages. Both application endpoints enforce their own
WebSocket Origin and subprotocol rules.

The topology identifies observed upgrades with WEBSOCKET edge labels and WS
service badges. The browser-to-chat edge shows HTTP + WS when page requests are
also present. These protocol labels persist after the activity animation stops
and are restored from retained handshakes when you reload the control plane.

## Deliberate limits

- One room, `lobby`, with up to 32 simultaneous connections.
- Names have at most 24 characters; messages have at most 2,000 characters and
  8 KiB of encoded JSON. Character limits use the browser's UTF-16 input length.
- Both server and browser retain the latest 50 confirmed messages. Reconnecting
  restores that snapshot; older messages may no longer be available.
- Restarting `rooms` loses history. There is no persistent storage or account
  authentication.
- The composer permits one outstanding submission. If its confirmation is
  lost, **Delivery unconfirmed** retains the text. A reconnect may confirm it
  from recent history. Chat never resends automatically; copying and sending
  again could create a duplicate.
- Text is displayed literally. There are no attachments, rich text, read
  receipts, or additional rooms.

## Validate

From the repository root:

```bash
make test-example-chat

# One-time browser installation, only for E2E tests:
make -C examples/chat install-browser

make test-e2e-chat
```

Node tests cover room behavior, real paired connections, and deterministic
browser-session recovery. The browser suite drives this actual example through
a compiled Portless E2E binary with a fresh temporary installation per test.
It exercises discovery, both handshake paths, service/daemon/environment
restarts, keyboard use, and mobile/desktop layouts in both themes. It does not
use the machine relay or touch other running Portless environments.

Reports and screenshots are ignored under `playwright-report/` and
`test-results/`. See [E2E testing](../../docs/e2e-testing.md#chat-example-integration)
for diagnostics and isolation details.
