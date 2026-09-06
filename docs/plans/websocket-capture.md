# WebSocket traffic capture

Status: drafted for review, then deferred on 2026-09-05 while
[tracing performance](tracing-performance.md) is addressed. No implementation
changes are included.

After joining Chat, sending a message should make that message inspectable in
Portless immediately, while both sockets remain open. Traffic should show the
direction, content, time, and connection; topology should react to actual
message activity. The opening handshake remains available as a finite HTTP
trace with a link to the connection's ongoing message timeline.

This extends the implemented [forwarding plan](websocket-support.md). It uses
the existing application ingress, directed dependency proxies, live traffic
buffer, and opt-in recordings. Chat continues to keep its room history in
memory; this does not add a database to the example.

1. **Agree on the behavior delivered by this change.**

   Capture logical text and binary messages in both directions on accepted
   WebSockets, including fragmented messages and `permessage-deflate` compression.
   Retain bounded content previews, message sizes, connection state, and close
   information. Make these available through the browser, typed API, CLI, and
   the existing MCP traffic capabilities.

   Live previews follow the existing HTTP/TCP policy: automatic, local, and
   bounded. Durable payloads require an active recording with
   `capturePayloads: true`. A metadata-only recording must not retain message
   content or application-supplied close reasons.

   Keep forwarding behavior and supported entry points unchanged. HTTP/1.1
   upgrades work through ingress and directed HTTP dependencies, including
   verified TLS to eligible remote upstreams. Read-only remote providers still
   reject upgrades. No application configuration or new service declaration is
   required.

   Message replay, WebSocket mocks, message faults, application-specific codecs,
   HTTP/2 or HTTP/3 WebSockets, and public WSS termination are outside this plan.
   Handshake faults and the existing reconnect behavior remain in place.

2. **Define separate connection and message contracts first.**

   The current `switchWebSocket` finishes one HTTP exchange and then runs two
   opaque copy loops. Its ownership registry already tracks pending and active
   connections. Extend that path rather than introducing a second proxy.

   Use the accepted handshake's environment-local exchange sequence as the
   public session number. A reconnect creates a new handshake and session.
   Private ownership keys and target generations remain internal.

   Proposed data model:

   | Record | Required information |
   | --- | --- |
   | Handshake exchange | Existing HTTP method, target, status 101, duration, redacted headers, and trace context; add an explicit session reference. Failed upgrades have no session. |
   | WebSocket session | Number, project/environment, source/target, provider, original endpoint, opened/closed times, open/closing/closed state, revision, handshake reference, per-direction wire totals and observed message counts, retained counts, inspection state, capture gaps, and transport termination reason. |
   | WebSocket message exchange | Normal exchange sequence plus session number, direction, opcode, first/last observation times, wire bytes, decoded size when known, fragment count, compression flag, inspection state, bounded content, encoding, captured bytes, and truncation/incomplete flags. |
   | Close information | Separately observed close code from each peer, whether the close exchange was observed in both directions, and bounded reason text available only through sensitive detail. Transport EOF/reset and Portless shutdown are separate facts. |

   A logical message is one immutable traffic exchange. Ping, pong, and close
   frames are inspectable control entries, separately identified from data
   messages. A fragmented data message produces one entry when complete; a
   transport ending midway produces one explicitly incomplete entry.

   Introduce a traffic-specific protocol value `websocket`, separate from the
   topology's HTTP/TCP transport declaration. Keep handshake exchanges HTTP.
   Message bytes live in a WebSocket detail field, not HTTP request/response
   bodies. Do not invent HTTP status codes or request/response pairings for
   individual messages. Duration means time observing a message, not server
   response latency. Use explicit source-to-target / target-to-source direction;
   keep the underlying directed edge unchanged in both cases.

   Preserve wire order within each direction. A combined timeline uses observer
   timestamps and exchange sequence for stable ordering; it does not claim an
   exact causal order between simultaneous sends in opposite directions.

   Define wire types, query types, and versions in `api/contract` first, then
   update the typed client and the existing model aliases coherently. Proposed
   API version: **14.0.0**, because protocol enums and capture semantics change.
   Proposed recording export schema: **4**, with session context alongside
   exchanges. Lifecycle and supervisor protocols do not change. Do not add
   parallel legacy formats or compatibility endpoints.

3. **Add passive, bounded observation to the existing copy loops.**

   Create a narrowly owned decoder under
   `portless-daemon/traffic/protocol/websocket`. It observes copied bytes and
   emits message records; it does not own sockets. `traffic/proxy` owns network
   integration, lifecycle, recording-policy selection, and emission to the
   traffic store. The decoder must not import the API server, database,
   control plane, or proxy manager. Document this dependency direction and
   cover it with the architecture guards.

   Retain the current handshake validation and buffered-reader handoff. Return
   the assigned handshake exchange from capture so session registration
   completes before either copy loop can emit a message. Include any frame
   bytes already buffered with the upgrade response or downstream request.

   Forward each chunk first, count the bytes successfully written, then offer
   an owned copy to a bounded observation queue. Neither socket loop performs
   decoding, JSON serialization, trace construction, disk I/O, or subscriber
   delivery. A slow observer or storage writer cannot block application
   forwarding. Handle short writes and simultaneous directions explicitly.

   Decode frame headers incrementally across arbitrary chunk boundaries;
   handle masking offsets, extended lengths, continuations, interleaved
   control frames, zero-length messages, and incremental UTF-8 validation.
   Unknown framing semantics or malformed input disable affected inspection
   with a reason, while the original bytes continue to flow. Never rewrite
   framing or synthesize protocol replies. The framing reference is
   [RFC 6455, sections 5–7](https://www.rfc-editor.org/rfc/rfc6455.html#section-5).

   Inspect negotiated compression without modifying the negotiation. Support
   per-direction context takeover and negotiated window parameters; preserve
   the appropriate dictionary between compressed messages and reset it when
   required. Control frames stay independent of fragmented compressed data.
   Use Go's `compress/flate` with bounded input/output handling. These rules
   follow [RFC 7692, section 7](https://www.rfc-editor.org/rfc/rfc7692.html#section-7)
   and the [Go decompressor API](https://pkg.go.dev/compress/flate#NewReaderDict).

   After a decompression limit, stop retaining/inflating that message. Continue
   frame-length accounting where framing remains trustworthy. With context
   takeover, losing dictionary state makes later compressed content unavailable
   for that direction until reconnect; report that limitation rather than
   showing corrupted text. Without takeover, inspection can resume at the next
   complete message. Unknown extensions produce an explicit unsupported state.

4. **Set concrete capture limits and failure behavior.**

   Start with these bounds; benchmark them before release and document any
   adjustment. They limit observation, not application message sizes.

   | Resource | Proposed bound / behavior |
   | --- | --- |
   | Pending or active sockets | Preserve the existing 256 per daemon. |
   | Live content | 64 KiB per logical message; count the full message when framing permits. |
   | Durable content | Existing recording payload limit, at most 1 MiB per message; only when payload capture is enabled. |
   | Copied bytes waiting for observation | 256 KiB per connection and 8 MiB across the daemon. |
   | Decoder working memory | 32 MiB daemon-wide budget covering prefixes, compressed input, dictionaries, and pending results; reserve before allocation. |
   | Inflation work | At most 1 MiB compressed input and 8 MiB decoded output per message, plus a 4,096-fragment inspection bound; exceeding either retains a bounded partial result with a limitation. |
   | Completed work waiting for storage | At most 1,024 records and 16 MiB across the daemon, including payload representations. |
   | Live retention | Share the existing 5,000-exchange / 64 MiB per-environment window; account for encoded binary content and metadata copies. |
   | Session summaries | Keep all active sessions within the socket cap and at most 1,000 closed summaries per environment; include their retained metadata in memory accounting. |
   | Activity notifications | Coalesce data updates to at most four per second per active edge; publish open/close transitions promptly. |
   | Shutdown work | Bounded cancellation/drain within the existing daemon restart deadline; forwarding shutdown never waits indefinitely for capture. |

   A preview limit only truncates the preview; it should not prevent later
   messages from being captured. Queue overflow is different: missing raw
   bytes can destroy parser alignment, so stop decoding that direction and
   keep transport counters. Never resume at an arbitrary byte boundary. Expose
   `limited` state and whether missing message counts are unknown.

   If decoded records cannot be retained or persisted, preserve a bounded
   metadata indication of the loss. A recording must report incomplete/failed
   capture rather than silently claim a complete conversation. Keep lifecycle
   finalization available even when the data queue is full.

5. **Extend retention without rebuilding traces for every message.**

   `traffic.Store.addExchange` currently copies the retained slice and rebuilds
   all traces on every insertion. WebSocket message insertion must bypass trace
   projection. Use bounded, indexed retention for messages and session lookup,
   with a shared eviction budget and near-constant-time append/eviction. Keep
   the refactor limited to the traffic store; do not move product boundaries.

   Maintain session summaries separately from message payloads. A long-lived
   connection must remain inspectable even after its handshake or oldest
   messages have been evicted. Retain only the minimal redacted handshake
   context needed to identify it. Report the oldest retained sequence and
   omitted history; lifetime observed counts are not retained row counts.

   Exclude message/control entries from HTTP/TCP trace inference. Preserve
   existing handshake trace correlation, and attach session references to its
   presentation. Message insertion does not extend the handshake duration or
   keep the HTTP request active. If eviction removes HTTP/TCP inputs, invalidate
   the corresponding trace projection and reconcile it through snapshots.

   Do not match sends to replies or correlate messages across service edges
   based on matching text or timing. A broadcast may appear on several
   connections, and each is a valid observation. Any later application-level
   tracing feature would need its own explicit propagation contract.

6. **Make snapshots and live notifications work together.**

   Add the following authenticated control API resources, scoped to the
   existing readable project/environment route:

   | Resource | Purpose |
   | --- | --- |
   | `GET .../traffic/websockets` | Bounded session summaries; filter by service, directed edge, and connection state. |
   | `GET .../traffic/websockets/{number}` | One session and its current capture/lifecycle state. |
   | `GET .../traffic/websockets/{number}/messages` | Bounded message summaries with direction/opcode filters and before/after cursors. |
   | `GET .../traffic/exchanges?protocol=websocket` | WebSocket message/control exchanges in the existing raw traffic collection. |
   | `GET .../traffic/exchanges/{sequence}` | Full bounded content for one selected message, using existing detail authorization. |

   Default message page size is 100; maximum is 1,000. Before and after cursors
   are mutually exclusive. Return explicit snapshot watermarks, oldest
   retained sequence, and capture/retention gap information. Apply filters
   before the page limit so an unrelated busy edge cannot hide matching data.

   Continue `traffic.exchange` for message summaries. Add
   `traffic.websocket.session` for revisioned session state and
   `traffic.websocket.activity` for per-edge live counts and byte/message
   activity. Notifications and summary endpoints must omit content, close
   reason text, and sensitive handshake values before entering the broker.
   Keep the control transport as SSE.

   Merge immutable entries by exchange sequence and session updates by revision.
   Subscribe before snapshot reconciliation; retain updates newer than the
   snapshot watermark. On stream reconnect or suspected loss, fetch authoritative
   snapshots and page retained messages after the last cursor. Activity updates
   carry cumulative counters/revisions so dropped or repeated events cannot
   double count. Clear and daemon replacement invalidate stale responses.

   Add aggregate active-session/activity state to the authoritative session
   snapshot so topology does not reset open counts to zero on page reload.

7. **Define recording, clear, and shutdown boundaries.**

   Starting a recording must work with already-open sockets. Select policy per
   logical message, using observed times and recording policy epochs; do not
   keep the policy from the opening handshake or query SQLite per frame.
   Record only messages whose observed beginning and completion are inside
   the recording interval. Explicitly count messages skipped because they
   straddle its start/stop/expiry boundary.

   Preserve existing scope, event-count, payload-size, and expiry limits. Each
   retained data/control entry consumes one event. Persist the session context
   with its first eligible event in the same transaction, without fabricating
   a new handshake inside the recording interval. Store a bounded per-recording
   session snapshot and capture range for export.

   Stop/expiry seals admission, drains eligible queued records within a bounded
   deadline, and freezes the recording's session snapshot. A socket can remain
   open after capture ends; label that fact instead of recording a false close.
   Report dropped work or persistence failure in recording metadata and
   `recording.state`. Subsequent messages cannot enter a finished recording.

   Export schema 4 includes recorded messages and their session context. Add
   indexed retrieval/pagination so a configured recording above 10,000 events
   is not silently cut off by the current export read limit. Recording-scoped
   session/message inspection must work after live eviction and daemon restart.
   HTTP mock import skips WebSocket messages as well as upgrade handshakes,
   retaining its existing explicit warning.

   Clear removes live message history, closed summaries, previews, and pending
   capture from the previous clear generation. Keep minimal active-connection
   state, because Clear does not close application sockets. Discard a message
   that began before Clear; capture resumes at a trustworthy subsequent message
   boundary. Separate lifetime counters from retained/post-clear history.
   Old asynchronous work must never repopulate cleared content. Recordings
   remain independent of live Clear.

   On normal close, reset, service/provider replacement, environment stop, and
   daemon shutdown, finalize each session exactly once, release decoder state,
   and cancel workers. Preserve observed peer close codes separately from the
   transport termination cause. Live memory disappears across daemon replacement;
   retained recordings remain available and new sockets get new sessions within
   the new live window. Do not promise to migrate open connections.

8. **Expose messages through Traffic and the existing trace journey.**

   Add a **WebSockets** view beside **Traces** and **Exchanges**. Session rows
   show endpoint, edge, state, sent/received counts, last activity, and capture
   limitations. Opening one shows a chronological message list with explicit
   direction, type, time, size, and truncation indicators. Fetch content only for
   the selected message.

   The detail drawer offers readable text, formatted complete JSON, or a bounded
   binary hex/base64 view and Copy. Preserve literal text rendering. Never parse
   truncated JSON as a complete document or render application HTML. Hide ping
   and pong entries by default behind **Show control frames**; keep abnormal
   closure and capture gaps visible. Close reason text belongs in detail.

   Update Traces summary rows and waterfall labels to **WS /ws**. Expanding a
   handshake trace exposes links to each observed session and its live message
   counts. Selecting that link opens the session timeline directly. The trace's
   duration and span count continue to describe the opening request. Counts
   update from session events without fetching or rebuilding the trace for
   every message. If a handshake trace has expired, the WebSockets view still
   provides the active/retained session.

   Add a **WS** protocol filter to Exchanges and a protocol-specific message
   drawer. For the WS filter, include linked handshakes as context and clearly
   distinguish them from messages; define the same inclusion rule in API, CLI,
   and MCP. HTTP filtering continues to include HTTP 101 exchanges; TCP must
   explicitly mean TCP rather than the current non-HTTP catch-all.

   Keep pause/resume buffers, mounted rows, detail caches, and history paging
   bounded. Pause freezes presentation only; forwarding and capture continue.
   Resume reconciles snapshots and reports omitted history rather than storing
   unlimited browser-side events. Preserve scroll position while inspecting
   older messages. Search/filter text must name its actual scope: session
   metadata and loaded messages, not unseen historical payloads.

   Replace HTTP RPS/latency statistics with message rate, wire throughput, and
   active connections in the WebSockets view. In mixed Exchanges, do not count
   WebSocket transfer duration as HTTP latency or data messages as HTTP requests.
   Maintain the existing dense styling, both themes, keyboard interaction,
   accessible names, and visible focus.

9. **Drive topology from message activity and live connection state.**

   Keep one node per service and one line per directed dependency. Protocol
   labels remain **WEBSOCKET** or **HTTP + WS** based on observed traffic.
   Mixed-service cards should show **HTTP + WS** explicitly; a WS-only badge
   should no longer be the only visible indication for a mixed service.

   Show open WebSocket counts independently of activity. Completed data messages
   drive message-rate metrics; forwarded data bytes drive activity during a
   large or fragmented message. Where framing is unavailable, label byte
   activity as unclassified rather than inventing message counts. Exclude
   recognizable ping/pong heartbeats from application activity.

   Use the coalesced activity stream as the sole source of WebSocket animation
   and counters; do not count the same message again from `traffic.exchange`.
   Keep HTTP request rate and WebSocket message rate separately labeled in mixed
   traffic. Hover/detail can show the fuller breakdown without overloading the
   line label. Open but idle sockets stop animating after the activity window,
   while retaining their open count and protocol label.

   Reload and resume restore active counts from the snapshot. Clear removes
   historical HTTP observations, but an active socket still establishes current
   WebSocket use. This deliberately updates the previous handshake-only rule
   that clearing history erased all WS classification.

10. **Complete CLI, MCP, redaction, and documentation together.**

    Extend `traffic list --protocol websocket`, tailing, `traffic show`, and
    trace rendering with explicit WS session/direction/type information. Add
    session listing/detail commands beneath the existing `traffic` group,
    with normal JSON output and bounded session-message queries. Use only the
    typed daemon client; update generated command documentation and completion.

    Extend the existing MCP traffic query/detail tools to classify WS entries.
    Default tools return metadata only. Message content and close reason text
    require the existing sensitive-traffic capability and its output limits.
    Do not expose payload-derived previews in a metadata field to bypass that
    boundary.

    Preserve existing credential-header and provider-secret redaction before
    retention. Application message bodies follow the same local-data policy as
    HTTP/TCP application bodies; generic WebSocket framing cannot identify every
    secret inside an arbitrary application payload. The UI, exports, and docs
    must accurately describe that boundary. Keep protocol/parser errors free
    of raw payloads and never log message content as a side effect of capture.

    Update README WebSockets/local-data sections, daemon README, OpenAPI,
    events, CLI/MCP references, Chat instructions, and E2E documentation. Mark
    the forwarding plan's handshake-only limitation as superseded by this plan
    only when implementation ships. Regenerate tracked web assets through Make.

11. **Implement and validate in this order.**

    | Milestone | Deliverable and exit condition |
    | --- | --- |
    | A — Contracts and retention | Types, typed queries, versions, bounded session/message store, snapshot watermarks, clear semantics, and unit/contract tests. No new message path invokes full trace reconstruction. |
    | B — Observation | Incremental parser, compression, bounded queues, lifecycle integration, and real ingress/dependency tests proving unchanged delivery. |
    | C — Durable capture | Policy boundaries, persistence, metadata-only/payload-enabled recordings, session context, export completeness, and restart retrieval tests. |
    | D — Consumers | API/SSE, Traffic session/message UI, trace entry points, topology, CLI, MCP, and nearby component/security tests. |
    | E — Product proof | Compiled-product CLI/browser journeys, real Chat validation, performance/race checks, docs, regenerated assets, and normal daemon restart. |

    Required test cases:

    | Area | Evidence required |
    | --- | --- |
    | Framing | Every split position in headers/masks/payloads, multiple frames per read, fragments, interleaved controls, empty messages, Unicode across fragments, binary, 64-bit lengths, malformed/reserved bits/opcodes, and mid-message EOF. Fuzz bounded parsing with no panic or unbounded allocation. |
    | Compression | Real independent client/server interoperability in both directions, takeover on/off, window parameters, fragmented compressed messages, uncompressed messages interspersed, compressed bombs/limits, and unsupported extensions. |
    | Forwarding isolation | Partial writes, blocked observer/storage, slow subscribers, queue exhaustion, many empty frames, cancellation, and unchanged original bytes. Capture failure cannot disconnect an otherwise working app. |
    | Identity and lifecycle | Ingress and dependency attribution, simultaneous connections on the same edge, reconnection, close versus reset, provider changes, environment stop, shutdown races, and exactly-once cleanup. |
    | Retention and recordings | Evicted handshake with live messages, paged history and gaps, recording start/stop during an open or fragmented message, expiry/event limits, metadata-only exports, payload limits, persistence failure, more than 10,000 export events, Clear races, and restart retrieval. |
    | API and privacy | Exact enum filtering, bounded pages, edge/environment isolation, redacted summaries/SSE, sensitive MCP gating, no payloads in parser errors, and no HTTP mock conversion of WS entries. |
    | UI and topology | Trace-to-session navigation, live content before socket close, binary/truncated views, reconnect/pause/clear reconciliation, stable reading position, mixed HTTP + WS, idle sockets, multiple open counts, and no double-counted activity. |

    Extend the isolated Go proxy and CLI tests, root
    `portless-web/e2e/websockets.spec.ts`, and the real `examples/chat` suite.
    Keep synthetic compression/protocol cases in test fixtures; Chat's user
    experience does not need new test-only endpoints or persistence.

    The primary browser acceptance journey is: open two Chat tabs, join, send
    a unique message, and find its content and broadcast on the correct ingress
    and `chat:rooms` sessions before closing either socket. Follow a WS handshake
    trace directly to those messages; then verify topology activity and mixed
    page-load traffic. Start a payload recording after joining, send another
    message, stop capture, and verify it survives an isolated normal daemon
    restart. Repeat with metadata-only capture to prove content is absent.

    Benchmark capture-disabled versus capture-enabled forwarding, including a
    sustained 1,000-small-messages/second workload across eight sessions and the
    256-session admission boundary. Measure CPU, allocations, retained memory,
    application delivery, and UI response. CI should assert enforced budgets,
    bounded queues, no deadlock, and no application-message loss; report any
    capture gaps explicitly. Also benchmark a full retention window to prove
    message cost does not grow with handshake trace count.

    Run focused tests and `go test -race` on the changed traffic/proxy/store
    packages, then `go test ./tests/architecture`, `make lint`, `make test`,
    `make test-e2e`, the Chat checks/browser suite, and `git diff --check`.
    Ordinary suites use isolated Portless homes. Do not run destructive relay
    suites or stop the developer's services as validation.

    After validation, build the complete checkout with `make`, inspect daemon
    status, and perform the normal `./bin/portless daemon restart` so the current
    control page can load the new bundle. Verify healthy adopted environments
    and refreshed assets. A blocked handoff requires diagnosis; forced restart
    is not part of this plan's routine completion path.

The feature is complete when actual messages are visible while sockets remain
open, trace navigation reaches them, mixed topology remains accurate, recordings
respect their boundaries, and capture load or malformed traffic cannot disrupt
application forwarding. A label-only update or uncompressed Chat-only success
does not satisfy this plan.
