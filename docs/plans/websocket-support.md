# WebSocket forwarding

Status: implemented and validated on 2026-09-05.
The design below records the agreed implementation plan; section 2 describes
the pre-implementation baseline. Completion results are recorded in section 9.

Portless should forward an application's WebSocket connections through the same
stable application hostname and source-to-target HTTP dependency proxies that
already carry its HTTP requests. For example, an application serving `/ws`
should work at `ws://checkout.local.billing.localhost/ws` after `portless up`,
without another listener, a separate service declaration, or a new CLI command.

This adds a currently unsupported capability. The first version forwards the
opening handshake and subsequent bytes; it does not inspect WebSocket messages.

## 1. Scope and decisions

| Area | First-version contract |
| --- | --- |
| Client protocol | RFC 6455 WebSockets using HTTP/1.1 Upgrade, version 13. |
| Entry points | Application-host ingress and directed dependency edges whose declared protocol is HTTP. |
| Providers | Local processes, HTTP services exposed by containers, and explicitly read-write remote HTTP services. |
| Public address | Derive `ws://` from an existing `http://` application endpoint. Keep existing endpoint discovery and readable names. |
| Remote configuration | Keep existing `http://` and `https://` remote URLs. HTTPS upstreams use normal TLS verification; do not introduce `ws://` or `wss://` configuration variants. |
| Browser behavior | Preserve application Origin, cookies, authorization, subprotocol offers, extension negotiation, and application response headers on the wire. |
| Payload behavior | Forward both directions without interpreting frames, rewriting messages, or accumulating complete messages. |
| Traffic inspection | Record the opening HTTP exchange, including status 101. Messages, session byte totals, and session close codes are not captured. |
| Remote read-only policy | Reject upgrade attempts locally with HTTP 403, even though the opening request is GET. |
| Lifecycle | Close affected pending handshakes and active connections on service replacement, environment shutdown, or daemon shutdown. Clients reconnect after a daemon restart. |
| Initial bounds | At most 256 pending or active WebSocket connections per proxy manager; a 30-second upstream response-header timeout and a 5-second downstream handshake-write timeout. No fixed lifetime or idle timeout after acceptance. |

The protocol baseline is the HTTP opening handshake followed by a bidirectional
framed connection defined by [RFC 6455](https://www.rfc-editor.org/rfc/rfc6455).
Applications remain responsible for authentication, Origin validation, message
limits, heartbeat behavior, and reconnection.

Explicitly outside this change:

- WebSocket frame inspection, frame-level traffic events, message recording or
  replay, WebSocket mocks, or faults applied to individual messages.
- HTTP/2 extended CONNECT, which is a separate transport described by
  [RFC 8441](https://www.rfc-editor.org/rfc/rfc8441), and HTTP/3 WebSockets.
- Public HTTPS/WSS termination, certificates, new relay listeners, or relay
  installation changes. An HTTPS remote upstream does not add public WSS ingress.
- Transparent migration of open connections across daemon replacement.
- The separate HTTP streaming/SSE buffering finding and traffic-store scaling
  work. The control plane's existing SSE event transport stays in place.

## 2. Existing code and implementation choice

The forwarding owner is
[`portless-daemon/traffic/proxy`](../../portless-daemon/traffic/proxy/manager.go).
Both `ServeIngress` and HTTP dependency listeners call `forwardHTTP`. That method
currently removes `Connection` and `Upgrade`, performs one `RoundTrip`, and copies
only the response body to the client. It cannot complete a WebSocket upgrade or
forward client messages to the upstream.

Other boundaries already provide most of the required structure:

- [`api/server/server.go`](../../portless-daemon/api/server/server.go) dispatches
  application hosts before control routes and applies control security headers
  only to the control host. Preserve that separation.
- [`portless-relay/runtime/relay.go`](../../portless-relay/runtime/relay.go)
  already forwards raw TCP bytes to the daemon's private Unix ingress socket.
  Verify upgrades through it with isolated listener tests; it needs no WebSocket
  parser or new privileged behavior.
- `CloseEnvironment` and `Close` in the proxy manager currently close HTTP
  servers and listeners, but do not own hijacked connections. Manager shutdown
  also discovers scopes from dependency edges, which misses ingress-only sessions.
- [`daemon.go`](../../portless-daemon/daemon.go) supplies cancellable base
  contexts for ingress and control servers. HTTP dependency servers currently
  need their existing edge context connected through `BaseContext` too.
- [`controlplane/mocks.go`](../../portless-daemon/controlplane/mocks.go) derives
  mock routes from recorded HTTP exchanges. Its eligibility check currently lets
  status 101 reach route construction, although mocks require final HTTP statuses.

**Implementation choice:** retain the existing HTTP forwarding pipeline and add
a small, explicit upgrade branch around its existing `RoundTrip`. Use the
standard library's upgraded response body as an `io.ReadWriteCloser` and
`http.NewResponseController(writer).Hijack()` for the downstream connection.
Keep the transport and connection-ownership behavior in the proxy package; add
no production WebSocket dependency and no frame codec.

Go's [`httputil.ReverseProxy`](https://go.dev/src/net/http/httputil/reverseproxy.go)
is the reference for response validation, connection handoff, and duplex copying.
Replacing the entire HTTP forwarder with it would also change streaming,
request rewriting, and capture integration. A separate WebSocket-only
`ReverseProxy` would duplicate the current forwarding hooks and obscure the
exact point at which Portless completes its handshake traffic entry. The narrow
upgrade branch gives this change explicit capture and lifecycle ownership while
keeping one policy and routing pipeline. Do not expand it into a general-purpose
HTTP proxy or WebSocket endpoint implementation.

Proposed new implementation files:

- `portless-daemon/traffic/proxy/websocket.go`: upgrade classification,
  handshake checks, response handoff, and duplex copying.
- `portless-daemon/traffic/proxy/websocket_sessions.go`: bounded admission,
  cancellation, connection attachment, and targeted cleanup.
- Corresponding `_test.go` files beside each owner. Keep helpers unexported
  unless a real package boundary requires an export.

## 3. Forwarding algorithm

### Classify and prepare the request

1. Recognize upgrade attempts before stripping headers. Parse all `Connection`
   values as case-insensitive comma-separated tokens; do not compare one header
   value to the literal string `Upgrade`. Treat a nonempty `Upgrade` header or
   a `Connection: upgrade` token as an upgrade attempt.
2. Accept the HTTP/1.1 GET WebSocket handshake with version 13, one
   `Sec-WebSocket-Key` that decodes to 16 bytes, and no HTTP request body. Reject
   malformed combinations with 400; return 426 with `Sec-WebSocket-Version: 13` for an unsupported
   WebSocket version. Return 501 for other requested upgrade protocols. Do not
   silently turn an upgrade attempt into an ordinary application GET.
3. Keep existing directed-edge selection, trace context injection, and handshake
   fault lookup. Snapshot the selected target; a missing target remains 502.
   Apply any matched fault after the admission step below so injected delays
   also participate in cancellation and the pending-connection limit.
4. Extend the remote policy guard to reject every upgrade attempt to a read-only
   target before upstream I/O. Keep `X-Portless-Remote-Policy: read-only` and the
   current HTTP error convention. A GET handshake cannot establish that later
   WebSocket messages are read-only. Mock-provider upgrades return 501 with a
   clear unsupported-provider message; they never reach the real provider.
5. Reserve a pending session against the current target and manager state before
   starting upstream I/O or an injected delay. Check the snapshot, policy, and
   generation atomically with admission. Include the environment, source,
   target, target generation, and dependency-edge identity where applicable. Reject excess
   connections with 503 and release the reservation on every exit path.
6. Apply the matched synthetic delay, HTTP rejection, or abort before upgrading.
   Keep cancellation effective during the delay. Reuse current target URL,
   Host, base-path, and trace preparation. Preserve repeated query parameters
   and the exact query encoding; local targets keep
   the application Host and remote targets keep the existing remote Host rule.
   Cover escaped paths and remote base paths explicitly. Do not change discovery,
   public endpoint schemas, injected dependency URLs, or forwarded-host trust.

### Send and validate the opening handshake

7. Correct `removeHopHeaders` to remove headers nominated by every `Connection`
   value as well as the standard hop-by-hop list. For a validated WebSocket
   handshake, restore only `Connection: Upgrade` and `Upgrade: websocket` after
   that cleanup. Preserve end-to-end `Sec-WebSocket-*` headers. Reject conflicting
   nominations that would remove required handshake fields. Apply equivalent
   cleanup to the response; include ordinary HTTP regression tests for this
   shared helper.
8. Add a manager-owned WebSocket transport cloned from the current transport
   configuration. Explicitly enable HTTP/1.1 only using Go 1.26 transport
   protocol controls. Preserve normal proxy/TLS configuration, certificate
   verification, dial limits, and connection setup behavior. Set its
   `ResponseHeaderTimeout` to 30 seconds and cap upstream upgrade response
   headers at 64 KiB with `MaxResponseHeaderBytes`. Do not change the ordinary
   local transport's intentionally unbounded header wait used by debugger workflows.
   Do not put a timeout on the lifetime of an accepted connection.
9. Perform one upstream `RoundTrip` using the session's cancellable context.
   Non-101 responses, including application authentication errors, redirects,
   and refusal bodies, use the ordinary HTTP response/capture path. Do not follow
   redirects inside the proxy. Transport failure uses the existing 502 convention.
10. For status 101, verify the response actually upgrades to WebSocket, has the
    expected `Sec-WebSocket-Accept`, has a writable response body, and does not
    select an unoffered subprotocol. Reject malformed, unsolicited, or mismatched
    101 responses with 502 before hijacking. Extension negotiation stays between
    the application and its client; Portless does not decompress or renegotiate.

### Hand off the connection

11. Attach the upstream connection to its pending session and check that admission
    is still valid. Hijack through `ResponseController` so supported writer
    wrappers can expose `Unwrap`. If hijacking is unavailable, close the upstream
    and return an HTTP error before committing a downstream 101.
12. Attach the client connection using the same cancellation-safe ownership
    operation. Serialize the sanitized 101 headers without an HTTP body and flush
    the returned buffered writer. Use a 5-second write deadline for this step,
    then clear inherited deadlines before copying frames. Once hijacked, failures
    close both connections; never call `http.Error` on that writer afterward.
13. Preserve unread bytes on both sides. Read client bytes through the
    `bufio.Reader` returned by `Hijack`; use the transport's upgraded response
    body for upstream reads so already buffered server frames are retained.
    Never discard either reader or create a second upstream connection.
14. Run two bounded copy loops, one in each direction, using fixed-size buffers
    such as 32 KiB per direction. Do not use `io.ReadAll`, HTTP body capture
    wrappers, unbounded channels, or a message-sized allocation after the upgrade.
    TCP backpressure controls the flow. Forward text, binary, fragmentation,
    control frames, and negotiated compressed traffic as opaque bytes.
15. On EOF, I/O error, or cancellation, close both sides to unblock the other
    loop and wait for both loops to exit. Release the reservation exactly once.
    Forward peer close frames unchanged; a Portless-initiated shutdown is a
    transport disconnect, not a fabricated WebSocket close code. Never replay
    application bytes or reconnect upstream automatically.

## 4. Connection ownership and lifecycle

Go's [`http.Server.Shutdown`](https://pkg.go.dev/net/http#Server.Shutdown)
does not close or wait for hijacked connections. Add explicit manager ownership
for pending handshakes and accepted sessions across both ingress and dependency
listeners. A dependency edge's existing HTTP server is insufficient ownership
for an ingress connection.

Use one private session record from admission until final cleanup. It owns its
cancellation function, any attached connections, and a completion signal.
Admission, target generation checks, and invalidation must be synchronized with
target updates. A connection attached after cancellation is immediately closed;
it must never escape because shutdown happened between `RoundTrip` and `Hijack`.

| Trigger | Required behavior |
| --- | --- |
| Client disconnect or upstream failure | Cancel the session, close the other side, stop both copy loops, and release capacity. |
| Request/server context cancellation | Apply the same cleanup, including while waiting for upstream headers. |
| `RemoveTarget` | Invalidate pending and active sessions involving that service in the same environment, including sessions where it is the source. |
| Effective provider, address, or remote-policy change | Invalidate sessions involving the replaced service; new admissions use the new target generation. A read-only transition closes existing remote sessions before the operation reports completion. |
| Re-registering an unchanged target | Keep existing sessions. Routine reconciliation must not disconnect clients. Compare effective routing/policy values, not pointer identity. |
| `CloseEnvironment` | Stop admission to the retiring targets/edges, cancel their pending handshakes, and close every session in that environment, including ingress-only sessions. |
| `Manager.Close` | Mark the manager closed before enumeration, cancel all sessions independently of the edge map, and close idle connections on the new transport. |
| Daemon replacement | Cancel ingress and edge contexts, close owned connections promptly, and preserve the existing runtime-adoption behavior. Clients reconnect at the same stable address after replacement. |

Implementation constraints:

- Advance a private generation when a target is removed or effectively changed.
  Re-adding the same address after removal is a new generation. Never expose
  these ownership values through APIs, logs, or public URLs.
- Register pending work under the same synchronization that guards its target
  snapshot. Recheck cancellation on every connection attachment. A stale
  handshake must not become an untracked active connection after replacement.
- Invalidate records under the manager lock, then cancel/close outside that
  lock. Never hold the routing lock across network I/O or wait for a handler
  while holding a lock it needs for cleanup.
- Make close idempotent. Avoid `WaitGroup.Add` racing with shutdown `Wait`;
  publish completion before admitting work or wait on a detached set of records.
- Wire each HTTP dependency server's `BaseContext` to its existing edge context.
  Include pending requests in shutdown, not just connections that reached 101.
- Keep existing shutdown budgets in
  [`shutdown.go`](../../portless-daemon/shutdown.go): replacement currently
  permits 500 ms for HTTP draining and 500 ms for application cleanup. Do not
  extend those budgets to accommodate connections that may remain open forever.
- Test provider changes through their control-plane operations as well as
  direct manager methods. Confirm an unchanged reconciliation is a no-op and
  another service/environment remains connected.

## 5. Traffic, recordings, faults, and mocks

### Opening-handshake accounting

Reuse the existing `TrafficExchange` shape. A successful upgrade is one HTTP
exchange with status 101, original request target, source/target identity,
provider, trace context, and redacted handshake headers.

- Complete the exchange once the downstream 101 headers have been successfully
  flushed. Its duration measures the opening handshake, not the connection's
  lifetime. Release the `BeginHTTPRequest` entry at that point so a long-lived
  connection does not remain an active HTTP root for unrelated later traffic.
- Give every path exactly one accounting outcome. A failure before a complete
  101 records the appropriate HTTP failure or a transport error if the writer
  has already been hijacked. Later connection closure does not emit a second
  HTTP exchange or mutate the completed handshake entry.
- Snapshot handshake headers before destructive cleanup. Preserve application
  response headers and the selected upgrade metadata in the redacted inspection
  representation. Keep existing trace injection and secret-header redaction.
- Redact `Sec-WebSocket-Protocol` values in captured request and response headers:
  applications can carry credentials there. Forward the actual values unchanged.
  Do not include raw handshake headers or credential-bearing protocol values in
  new errors or logs. Add coverage for traffic details and recording exports.
- Successful handshakes have no captured HTTP body. Existing byte fields retain
  their HTTP body meaning and are zero for the upgrade; do not report them as
  WebSocket throughput. Frames never enter `bodyCapture`, even with recording
  body capture enabled.
- Decide recording membership at handshake completion using the existing
  recording rules. Starting or stopping a recording later does not capture
  messages from an already open connection. Keep persistence cancellable and
  bounded for this path; do not add detached background persistence that can
  outlive session or daemon cleanup.

In [`HttpTrafficDetail.tsx`](../../portless-web/src/features/traffic/protocols/HttpTrafficDetail.tsx),
recognize the HTTP 101 WebSocket upgrade from the existing status and handshake
headers. Display “WebSocket handshake — messages are not captured.” Keep headers
inspectable and make the body view explain this limitation instead of implying
that the session exchanged no messages. Do not add a misleading live connection
indicator or a new WebSocket message viewer.

### Fault and mock behavior

- Delay/jitter, status rejection, and connection abort apply to the opening
  handshake using the existing edge, method, and path filters. Applying a fault
  after acceptance does not alter an existing stream.
- The mock provider remains an HTTP-response provider. A WebSocket attempt
  returns 501; it must not fall back to a local or remote real service.
- Change `mockRoutesFromRecording` to exclude successful upgrades before
  duplicate selection and route construction. Return a specific warning that
  WebSocket handshakes cannot become HTTP mock routes. A recording containing
  both ordinary HTTP traffic and upgrades still imports eligible HTTP routes.
- Keep the existing rejection of status 101 in manually supplied mock routes.
  Cover mixed recordings, upgrades-only recordings, and the case where an
  upgrade and a normal response share a method/path/query.

### Contract and documentation changes

No new daemon API route, public protocol enum, session DTO, event type, recording
schema, CLI flag, or configuration field is required. Keep the current API
version 13.1.0, recording schema version 3, and lifecycle/supervisor versions;
this plan adds application forwarding while using existing wire fields.

Update the behavior descriptions in:

- `README.md` and `portless-daemon/README.md`: supported entry points, `ws://`
  example, remote policy, reconnection, and inspection limitations.
- `portless-daemon/api/openapi.yaml`: remote policy and status-101 traffic field
  semantics; the control API itself does not become a WebSocket API.
- `portless-daemon/api/events.md`: `traffic.exchange` reports the completed
  opening handshake; there are no frame, session-close, or WebSocket activity
  notifications. Existing control SSE remains unchanged.
- `docs/e2e-testing.md` and `tests/fixtures/store-lite/README.md`: fixture
  endpoints, real browser coverage, and the relay-test boundary.

If implementation introduces a wire field despite this design, update contract,
typed client, server, CLI/web consumers, schema tests, and versions coherently
instead of leaving undocumented JSON behind.

## 6. Implementation sequence

Each step depends on the previous step's relevant contracts. The feature is ready
to ship only after the lifecycle, policy, inspection, and E2E steps are complete.

### Step 1 — Establish failing protocol tests

Add `websocket_test.go` beside the proxy manager. Build a bounded HTTP/1.1 test
upstream and real network client; `httptest.ResponseRecorder` cannot prove
hijacking or bidirectional forwarding. Demonstrate failure through both
`ServeIngress` and an HTTP listener created by `EnsureEdge` before adding the
implementation.

Use standard-library-only test helpers for controlled handshakes and known frame
bytes. They are fixture code, not a reusable production WebSocket implementation.
An independent browser client in step 5 verifies interoperability rather than
having the same custom helper validate itself.

### Step 2 — Implement the shared upgrade branch

Add the request/response checks, hop-header correction, dedicated transport,
connection handoff, and duplex copy loops described above. Factor only the
preparation/capture helpers needed to keep one ordinary HTTP and upgrade policy
pipeline. Preserve existing ordinary HTTP, TCP, and remote target behavior.

Start the ownership registry in this step: even the first successful test must
have deterministic cleanup. Add table-driven negative tests and TLS tests with
explicit test trust roots rather than disabling certificate verification.

### Step 3 — Complete lifecycle and policy integration

Integrate generation-aware invalidation with `SetTargetProvider`,
`SetRemoteTarget`, `RemoveTarget`, `CloseEnvironment`, and `Close`. Wire edge base
contexts. Add tests around control-plane provider changes and environment stops,
and around daemon replacement with an open connection.

Use barriers/channels to place shutdown precisely during upstream connection,
response receipt, connection attachment, and active copying. Exercise the
connection cap and recovery after cancellation. Run the focused Go race tests.

### Step 4 — Complete inspection and documentation

Finish handshake-only capture, redaction, recording persistence, and mock import
filtering. Add the explanatory traffic-detail presentation and nearby Vitest
tests. Update the contract descriptions and product documentation in section 5.

Regenerate tracked web assets with `make web`; never edit the dist bundle by
hand. Preserve concurrent changes to README, E2E documentation, and web files
when applying this plan to the working tree.

### Step 5 — Prove the behavior through the compiled product

Extend the dependency-free `store-lite` fixture with a bounded WebSocket echo
endpoint on checkout and orders, plus a checkout HTTP endpoint that opens a
WebSocket to orders using the generated `ORDERS_URL`. Keep its protocol helpers
inside the fixture's own package, for example `tests/fixtures/store-lite/wstest`,
with explicit frame-size and operation-time limits. Do not add a required
Portless declaration or perform package installation during an E2E run.

Add `tests/e2e/websocket_test.go`:

- Start the copied fixture with `portless up` in an isolated home. Connect with
  the application Host through the real daemon listener; do not dial the
  fixture's process port directly.
- Exchange messages through application ingress and through the generated
  checkout-to-orders dependency URL. Verify captured `external:checkout` and
  `checkout:orders` handshake identities through the normal CLI/typed API.
- Keep a connection open while inspecting traffic to prove status 101 appears
  before disconnect. Record/export it and verify that frames and sensitive
  headers are absent.
- Stop/restart the isolated environment and normally restart its daemon while
  connections are open. Assert prompt closure, successful reconnection, stable
  endpoints, and the existing daemon-adoption promise that app processes survive
  daemon replacement. Do not use forced replacement.

Add `portless-web/e2e/websockets.spec.ts` using Chromium's native `WebSocket`:

- Follow `application-hosts.spec.ts`: replace the hostname of the isolated
  daemon URL with `state.applicationHost` while retaining its test port.
- Navigate to a fixture page on that application origin. Its application-owned
  CSP explicitly allows its script and WebSocket endpoint. Construct the socket
  using that page's actual host and port. Do not run the application connection
  from the control page or loosen the control CSP to make the test pass.
- Verify open, text echo, binary echo, a negotiated subprotocol, and close.
  Check the handshake appears with the explanatory text in Portless traffic.
- Use actual networking; do not stub or intercept WebSocket routing. The normal
  HTTP `applicationRequest` helper consumes response bodies and is not a
  WebSocket test client.

Finally, extend `portless-relay/runtime/relay_test.go` with a handshake and
bidirectional traffic test over a temporary TCP listener and private Unix socket.
Cover cancellation too. This proves the relay's byte transport without touching
the installed machine relay, port 80, DNS, or administrator permissions.

## 7. Required test matrix

| Area | Cases and required observation |
| --- | --- |
| Shared paths | Ingress and dependency edges both upgrade; `/api/ws` and `/auth/ws` stay with the application; readable source/target attribution is correct. |
| Wire preservation | Path/query, application Host or remote Host, Origin, auth, cookies, repeated headers, subprotocols, and application browser policies survive forwarding. |
| Header parsing | Mixed-case and repeated Connection tokens work; nominated hop headers are removed; required upgrade fields survive; malformed requests and unsupported protocols return defined errors. |
| Upstream responses | Valid 101 succeeds; wrong accept value/protocol, unsolicited 101, missing writable body, and unsupported hijacking fail without a leaked connection. Ordinary refusal responses retain status, headers, and body. |
| Duplex data | Text/binary bytes, fragmented frames, ping/pong/close, and precomputed compressed-frame exchanges remain intact. Exercise both directions concurrently and server data buffered with the 101. |
| Buffering/backpressure | Buffered client bytes are retained; a stream larger than the 64 KiB HTTP capture limit is neither truncated nor captured; a blocked writer can be cancelled without leaking a loop. |
| Remote policy/TLS | Read-only rejection causes zero upstream requests; read-write permits upgrade; HTTPS upstream trust succeeds and invalid certificates fail. Changing to read-only closes an existing connection. |
| Resource limits | Pending and accepted connections share the cap; failed/rejected/cancelled attempts release slots; oversized response headers and handshake timeouts release both sides; accepted sessions have no inherited deadline. |
| Lifecycle races | Stop during every admission/attachment phase; ingress-only manager close; service/provider changes; unchanged reconciliation; environment isolation; normal daemon restart and reconnect. |
| Capture | Exactly one entry, visible while connected; handshake duration/body-byte semantics; no frame bodies with any recording option; secret redaction in details and export; no persistent active HTTP request after acceptance. |
| Experiments | Handshake delay/status/abort; no retroactive frame faults; mock-provider rejection; mixed and upgrades-only recording imports report the right outcome. |
| Existing boundaries | Ordinary HTTP/TCP behavior, app route ownership, control auth/CSRF, control SSE, unknown-host rejection, and relay cancellation remain covered. |

Use event-driven synchronization and bounded test deadlines. Avoid fixed sleeps,
exact total traffic counts from a shared fixture, broad screenshot snapshots,
or machine-wide cleanup. Use unique request markers to select the exchange under
test. Add a bounded concurrency test and a targeted allocation benchmark for the
upgrade path; do not turn this into the separate traffic-store performance fix.

## 8. Validation and completion gates

While developing, run focused packages and race checks:

```bash
go test ./portless-daemon/traffic/proxy ./portless-daemon/controlplane ./portless-daemon/api/server ./portless-daemon/mocks ./portless-relay/runtime
go test -race ./portless-daemon/traffic/proxy ./portless-relay/runtime
go test ./tests/architecture
npm --prefix portless-web run typecheck
npm --prefix portless-web test -- src/features/traffic/TrafficDetail.test.tsx
make e2e-binary
PORTLESS_E2E_BINARY="$PWD/bin/portless-e2e" go test -count=1 -tags=e2e ./tests/e2e -run '^TestCLIWebSocket' -v
```

Before handoff, run the supported complete non-destructive checks:

```bash
make lint
make test
make test-e2e-cli
make test-e2e-ui
git diff --check
```

Verify the default E2E suites on macOS and in the same Linux environment used by
CI, especially browser hostname routing and daemon replacement. Ordinary tests
do not require a container engine. A container-backed WebSocket smoke test may
be added to the optional resource E2E suite; distinguish it from the default
loopback/container-provider adapter coverage when reporting results.

Do not run either machine-destructive relay E2E target as routine validation.
Those targets require separate explicit machine-level authorization under
`AGENTS.md` and `docs/e2e-testing.md`. The isolated relay test above is part of
the ordinary package suite.

For an implementation handed off for use in the current local installation,
build the complete executable with `make` and perform the normal
`./bin/portless daemon restart` so both the new proxy and tracked web assets are
running. Inspect daemon status and report a blocked normal handoff; do not use
`--force` as a fallback without the required authorization.

The change is complete when the real browser and compiled-product tests pass
through ingress and dependency proxies, remote policy cannot be bypassed, open
connections do not block lifecycle operations or leak resources, and inspection
accurately describes its handshake-only coverage. Report actual validation and
any skipped environment-specific check.

## 9. Completion results

Implemented the shared ingress/dependency upgrade path, bounded connection
ownership, lifecycle cancellation, remote policy enforcement, handshake-only
capture, subprotocol redaction, mock-import filtering, and the traffic-detail
explanation. No production WebSocket dependency or public API field was added.
The fixture includes real browser and dependency clients; its multi-source test
copies the helper into each independent source module.

Validation completed:

- `make lint` and `make test` passed, including the architecture checks and all
  296 web unit tests.
- Proxy and relay race tests passed. Coverage includes concurrent admission at
  the 256-session limit, late attachment and cancellation, TLS, opaque frame
  preservation, local/container provider adapters, and ordinary HTTP hop-header
  removal. A control-plane test verifies an active read-write remote WebSocket
  closes when its binding changes to read-only.
- The complete default CLI E2E suite and all 47 Chromium journeys passed on
  macOS arm64 and Ubuntu 24.04 arm64. Linux validation used a disposable
  Playwright container with Go 1.26.0 and Node 24; the hosted GitHub Actions x64
  runner was not invoked from this checkout.
- `make` produced the complete executable and regenerated the tracked web
  bundle. The normal daemon restart succeeded, and the active environment
  remained healthy with its application process IDs unchanged.
- `git diff --check` passed.

Machine-destructive relay suites and the optional container-engine resource
suites were not run. Relay forwarding was verified with isolated TCP/Unix
listeners; container-provider WebSockets were verified through the loopback
adapter rather than by launching an application container.
