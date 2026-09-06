# HTTP request replay and response comparison

Status: proposed; not implemented.

Created: 2026-09-05.

## 1. Outcome and scope

Let a developer open an observed HTTP request in Traffic, edit its inputs,
send it through the same logical dependency edge in the current or another
environment, and compare the new response with the captured response.

The browser journey is:

```text
Traffic → Exchanges or an HTTP trace span → Replay…
        → Edit request and select environment → Send replay
        → Original response / Replay response / Response diff
```

The first release includes:

- One ordinary HTTP request selected from the live traffic window.
- The original environment or another environment in the same project.
- The original source and target identities, including `external` ingress.
- Editable method, escaped path/query, repeated headers, and bounded text body.
- An immutable captured baseline, repeated explicit runs, and the latest result.
- Status, response-header, text, and structured JSON comparison.
- Local, HTTP container, mock, and eligible remote target providers.
- The same daemon execution and comparison contract for browser and CLI.

Defer whole-trace/recording replay, recording exchange browsing, saved replay
collections, arbitrary pairs of historical responses, cross-project remapping,
arbitrary URLs, binary/multipart uploads, streaming/SSE, WebSockets, gRPC/TCP,
load generation, assertions, data restoration, and scenario execution.
Selecting a span replays only that HTTP request; it does not execute its parent
or resubmit every captured child. Downstream calls made by the real service
happen normally. This is request reproduction, not deterministic application
or database-state reproduction.

Keep the feature within Traffic. Do not add a sidebar destination. Recordings
can reuse the workspace later, after named-recording exchange inspection exists.
Opening, preparing, editing, comparing, and resetting do not send application
requests. Only **Send replay** does.

## 2. Current implementation and integration points

Read the current versions of these files before implementing; this plan records
the inspected checkout, not a replacement for its contracts.

| Current owner | Existing behavior and required extension |
| --- | --- |
| `portless-web/src/features/traffic/TrafficPanel.tsx` | Owns exchange/trace selection, detail loads, navigation, and Clear reactions. Own replay state above the selected drawer so list changes cannot substitute its baseline. |
| `ExchangeTraceDrawer.tsx`, `WaterfallTraceDrawer.tsx`, `detail/TrafficDrawerShell.tsx` | Both inspection paths share one shell. Add the HTTP Replay entry and a replay presentation mode here. |
| `protocols/HttpTrafficDetail.tsx` | Request/response inspectors preserve repeated headers. Current COMPARE means displaying request beside response. Rename that existing presentation to SIDE BY SIDE. |
| `portless-web/src/api.ts` | Existing authenticated, CSRF-protected daemon requests and structured errors. Use it for every replay API call. |
| `portless-daemon/api/contract/traffic.go`, `api/client/traffic.go`, `api/server/traffic.go` | Traffic contracts, typed client, and routing. Existing traffic routing has a GET-only guard apart from Clear; dispatch replay subresources before it. |
| `portless-daemon/controlplane/observability.go` | `TrafficExchange` falls back to a bounded recording scan. Replay preparation must explicitly query live traffic instead. |
| `portless-daemon/traffic/proxy/manager.go` | `forwardHTTP` applies edge faults, provider routing, remote policy, trace propagation, recording, and capture. Reuse this pipeline through an internal replay entry. |
| `portless-daemon/traffic/store.go`, `traces.go` | Own retention and trace projection. Add explicit replay-root provenance so temporal inference cannot attach a replay to an unrelated in-flight request. |
| `portless-cli/traffic` | Own the new `traffic replay` and result-inspection commands. |
| `portless-mcp/traffic_tools.go`, `results.go` | Preserve safe metadata mapping and sensitive-detail gating. Do not expose replay execution through existing MCP capabilities. |

Important constraints found in the current implementation:

- HTTP bodies are bounded text prefixes, normally 64 KiB, and invalid UTF-8 is
  converted for display. An empty string does not prove an empty original body.
- Header redaction replaces known credentials with `[REDACTED]`. Those values
  cannot be replayed or recovered from provider secrets or browser sessions.
- `finishHTTP` currently returns no exchange. A run needs its exact completed
  exchange directly, even if live traffic is cleared or evicted immediately.
- Normal HTTP forwarding selects a target after fault delay; replay must also
  verify that the reviewed provider generation has not changed.
- Trace inference can use overlapping source/target timing even when trace IDs
  differ. New trace headers alone do not establish an independent replay root.
- Recordings currently show metadata and Repeat/Export/Delete actions, without
  an exchange-detail view. Their bounded fallback lookup is not a replay API.

## 3. Browser behavior

### Entry and layout

Add a labelled **Replay…** action beside the size/close actions in the shared
HTTP drawer. Show it for ordinary HTTP exchanges, with an explanatory disabled
state when capture metadata proves the exchange cannot yet be prepared. TCP
operations/transactions and upgrade handshakes do not offer replay.

Opening Replay creates a bounded, non-executing daemon workspace and expands
the drawer. Retain the inspection selection and scroll position for **Back to
exchange**. Use this layout:

```text
Replay request #142                     Back to exchange       Close
checkout → orders     Destination: store / local
Provider: Local       http://orders.local.store.localhost

Method  Path and query                                      Send replay
GET     /orders?limit=10&tag=new&tag=priority

Request editor                   Response comparison
Headers | Body                   Original | Replayed | Response diff
                                 Status / duration / captured size
                                 Body | Headers
```

The destination picker changes only the environment. Source and target remain
visible and fixed. Use the environment inventory already loaded by `App.tsx`,
passed through `EnvironmentPage` to Traffic. Show stopped/missing/incompatible
destinations with reasons; server validation remains authoritative. Never start
an environment or change a binding implicitly. Keep public endpoint URLs visible
and copyable. Runtime ports and listener addresses are not editor fields.

Show request and results side by side on wide screens, stacked or through
accessible panel tabs on narrow screens. Request controls and Send remain
reachable while bodies scroll independently. Before the first run the result
area shows the captured original and a concise empty replay state.

### Request editing

- Initialize path/query from `requestTarget`, preserving original escaping,
  parameter order, repeated names, empty values, and `+` versus `%20`.
- Use one raw path-and-query input in v1. A separate decoded query table can
  follow later; do not accidentally normalize the captured target on open.
- Allowed methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS. CONNECT, TRACE,
  upgrades, and protocol tunnelling are unsupported.
- Header drafts use ordered rows with stable row IDs; serialize to arrays of
  values, not a single-value map. Show which headers are excluded or regenerated.
- Body mode is explicit: captured complete text, replacement text, or empty.
  JSON formatting is an optional editor action and never changes bytes on open.
- **Reset request** restores the prepared original draft and its unresolved
  omissions. It does not send. It clears manually supplied credential values.
- Missing/truncated bodies require a full replacement or an explicit empty-body
  choice. Present a captured prefix as reference, never as a complete sendable body.
- Redacted credential headers are unresolved entries: provide a value or choose
  to omit that header. Never send a placeholder. Keep manually supplied values
  in browser memory only, mask them by default, and clear them when changing
  destination or disposing the workspace.

Use a traffic-owned header editor with the existing name/value interaction
patterns. Do not reuse the mock response-header serializer, which does not
model repeated values.

### Runs, drafts, and navigation

Use one `useTrafficReplay` owner in TrafficPanel, not component-local state that
disappears when the drawer selection changes. Keep one browser workspace active
at a time and retain it while returning temporarily to ordinary inspection.
Replay on its original exchange resumes that workspace. Replay on another
exchange resumes the existing pending run if one is active; otherwise, explicitly
offer to keep the existing draft or replace it, dispose the old workspace, and
only then prepare the new one. Do not accumulate hidden abandoned workspaces.

On Send: validate and prepare the complete draft with the daemon, show the
specific remote-write confirmation when required, then submit the reviewed
revision once. Preparing the draft does not contact the application or remote
health endpoint. While pending, disable Send, editor/destination changes, and
previous/next exchange navigation. The drawer may be closed; closing changes
presentation and does not claim to cancel the request. Keep an in-memory
pending indicator and a way to reopen it while remaining in Traffic.
Presentation close clears browser credential fields immediately. Any unused
daemon-side credential preparation expires within 60 seconds; closing the UI
does not itself guarantee immediate server-side erasure. Reopening requires
re-entering credentials and preparing again before another run.

After completion, preserve the editor and the result associated with the exact
submitted draft. Editing marks that result **From previous request**. Each
subsequent Send gets a new run number and compares with the same original
baseline. Retain only the latest full result in v1.

Scope transitions, stale detail requests, and SSE must never replace a replay's
baseline or result. Normal live eviction leaves the frozen workspace intact.
Clear has explicit behavior: it discards non-running workspace payloads and
drafts in that origin environment; an already admitted run is allowed to finish
but its cleared workspace cannot republish its baseline/result into the UI.
Its newly completed traffic follows the ordinary post-Clear capture rules.
Keep a small run receipt until expiry so a delayed duplicate cannot resend.

Browser reload/navigation away discards draft secrets. Store no request body or
headers in URLs, localStorage, sessionStorage, or durable replay history. A
same-page API reconnect reconciles an existing run by GET, without another Send.
Daemon replacement invalidates ephemeral workspaces; explain that a request
which had begun may have reached the application. Never resume execution
automatically. Full browser reload recovery is not a v1 promise.

### Existing comparison name and accessibility

Rename the existing COMPARE label and internal `compare` presentation value to
**SIDE BY SIDE** / `side-by-side` across HTTP, TCP, transaction layouts, icon
names, CSS, and tests. This retains its existing request/response or
command/result layout. **Response diff** names the new comparison result.

Use labelled inputs, visible focus, keyboard-operable tabs with arrows/Home/End,
correct tab/tabpanel associations, and a polite sending/completion announcement.
Disable global drawer navigation while editing. Integrate the shared overlay
focus/dismiss behavior and remove conflicting Escape handlers. Escape from a
confirmation returns to the editor; Escape from Replay returns to inspection;
neither sends or claims cancellation. Restore focus to the triggering action.
Use the existing structured error notice and both theme-variable palettes.

## 4. Ownership and execution boundary

Add `portless-daemon/traffic/replay` for bounded workspace state, request
validation, comparison, and typed run results. Give it injected resolver and
executor capabilities. It must not import controlplane, API server, CLI, relay,
or daemon composition, and it must not open sockets or read arbitrary files.

`controlplane/replay.go` coordinates live baseline lookup, environment/topology
validation, and the injected proxy executor. `traffic/proxy/replay.go` owns the
network integration. Proxy code can use domain provenance types without
depending on the replay manager. Wire request/response types belong in
`api/contract`; use adapters at the server/control-plane boundary. Follow the
existing model aliases for additions to TrafficExchange without broad model
reorganization in this feature.

Document these dependency directions in the architecture plan/guards. No generic
HTTP client service, second proxy server, executable, privileged listener, or
raw HTTP transport exposed to CLI is needed.

### Source-aware dispatch

Create an internal `ReplayHTTP` entry into the shared proxy pipeline. It receives
an already validated scope, source, target, admitted target snapshot, request,
run provenance, and bounded context. Construct a fresh application request;
never clone the control API request or call the application from the browser.

For a dependency replay, validate that the exact `source:target` HTTP edge
exists in the selected environment. For `external`, validate the target's actual
public HTTP ingress eligibility, including non-primary services. Do not reuse
the experiment scope validator's primary-service restriction blindly.

Do not route a dependency replay through `ServeIngress`, which changes its
source to `external`. Do not dial a private dependency listener supplied by a
client. Resolve the current provider internally and preserve remote base paths,
mock attribution, and the configured TLS verification.

Current destination faults, mocks, and active recording policies apply normally.
Preparation must not increment fault matches, publish traffic, record payloads,
or probe remote services. Historical faults/providers are shown as baseline
context, not restored. A replay emulates the captured caller's routing identity;
it does not execute that caller's code or recover its private authentication state.

### Admission and remote writes

Draft preparation records the destination topology/binding revision and private
proxy target generation, including classification and write policy. Run
admission verifies this snapshot, then checks it again after any fault delay
immediately before dispatch. Use the admitted immutable target; do not perform
a fresh target-only lookup that can silently redirect the reviewed request.
Treat route/provider removal as a stale destination. Synchronize the admission
and replacement boundary without holding a global lock during network I/O.
Register admitted runs against their generation. Replacement/removal invalidates
and cancels those admissions; reject connection attachment after invalidation
before writing request bytes. Cover replacement during dialing as well as during
fault delay. Once request bytes have been sent, cancellation cannot roll back
application effects; preserve that uncertainty in the outcome.

A read-only remote rejects methods disallowed by the existing local policy
before outbound I/O. No confirmation can override it. A mutating request to a
read-write remote needs explicit confirmation naming project/environment,
service, classification, method, and path. Bind confirmation to the prepared
draft revision and destination snapshot; changed inputs or provider state need
new preparation. The ordinary local/mock Send action is sufficient to send.

Do not imply that method-based read-only enforcement proves application-level
absence of side effects. This feature preserves the current remote policy; it
does not reinterpret application semantics.

### Request normalization and transport

- Accept only an origin-form escaped path/query beginning with `/`. Reject
  absolute/network-path URLs, authority, fragments, control characters, invalid
  escapes, and host/scheme overrides. Preserve valid raw escaping through local,
  mock, and remote base-path composition, including `%2F` and repeated query names.
- Derive Host from the destination's public application identity; the existing
  remote provider adapter selects its upstream authority. Exclude captured Host,
  forwarding/routing overrides, proxy-auth headers, hop-by-hop headers and
  Connection-nominated fields. Recompute Content-Length; do not accept framing
  headers, trailers, Upgrade, or Expect from the draft.
- Clear old W3C/B3/Datadog trace carriers, tracestate, and baggage. Create fresh
  trace/span IDs for the replay. Never copy Portless API bearer credentials,
  CSRF tokens, control cookies, or browser credentials into the application call.
- Remove browser Origin/Referer/fetch metadata from the default draft and
  disclose this normalization. Origin/Referer may be entered intentionally for
  application testing; they cannot affect daemon target resolution.
- Default response negotiation to identity encoding and disable implicit
  decompression in the replay transport. Unsupported encoded responses remain
  explicitly unavailable for text/JSON comparison; do not interpret compressed
  bytes as text. Do not render application HTML or execute returned scripts.
- Make one explicit transport attempt and return redirects without following
  them. No cookie jar, authentication retry, upstream retry, or automatic resend.
  Audit the Go transport's internal retries: an ordinary RoundTrip alone does
  not establish this guarantee. Use an owned fresh HTTP/1.1 connection for the
  bounded v1 run, with no reused connection or replayable GetBody, and test
  connection failure and idempotency-header cases. Retain TLS verification.

Go documents retries for certain failures on reused connections; HTTP also
distinguishes retryable semantics from proving that a previous write did not
occur. The implementation must test its one-dispatch property rather than
assuming it from a button guard. [Go 1.26 transport behavior](https://pkg.go.dev/net/http@go1.26.4#Transport),
[HTTP retry semantics](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2).

Refactor the shared completion path to return/callback the exact redacted
completed exchange and dispatch outcome, not a later search of the live ring.
Preserve application abort faults as aborts in replay results; a response sink
without Hijacker must not convert them into misleading ordinary 503 responses.
Complete each admitted replay once on every path, including cancellation during
fault delay. Bound recording persistence and fault-accounting I/O reached by
replay; do not leave its completion dependent on `context.Background()` writes.

## 5. Capture fidelity and traffic provenance

Add explicit HTTP request/response capture metadata at the capture boundary,
before lossy text conversion. The state must distinguish `empty`, `complete`,
`truncated`, `omitted`, `unsupported`, and `incomplete`, with observed/captured
byte counts, content encoding, and whether the retained text preserves the
original UTF-8 bytes. Use conservative classification whenever EOF, valid
encoding, or full byte availability is unknown.

A Content-Length estimate, no body string, or a false truncation flag cannot
alone prove completeness. Count bytes and observe EOF/read failure even when
content is not retained. Account for chunked bodies, aborted upload, rejected
requests whose bodies were never read, HEAD/204/304 body semantics, and an
invalid UTF-8 boundary at the retention limit. Do not increase ordinary body
capture limits to make replay convenient.

Auto-prefill a sendable captured body only for proven empty bodies or complete,
identity-encoded UTF-8 text within the request limit. Unknown or incompatible
captures need an explicit user-authored replacement/empty decision. The output
then identifies the request as edited. Comparison remains useful for headers
and status when body comparison is unavailable.

Add safe optional replay provenance to TrafficExchange: origin project and
environment, original exchange sequence/time, replay workspace number, run
number, and an explicit replay-root marker. A destination exchange keeps its
actual destination scope and original logical source/target. It gets a new
sequence and trace ID; the original exchange is never overwritten.

Pass the root marker into trace projection and skip inferred/exact incoming
parent assignment for that explicit root. Its downstream observed calls may
still attach normally, with existing confidence labels. Force explicit replays
foreground even for normally hidden housekeeping paths. Label their summaries
**Replay**; provide **View replayed request in Traffic** for the destination.
Do not claim an environment-local trace number until projection establishes it.
Add explicit destination Traffic exchange-selection navigation, carrying only
public project/environment names, exchange sequence, and daemon/window identity.
Selecting the link opens Exchanges and that exact live detail. Validate the
identity on arrival; if the exchange expired or its daemon changed, show an
unavailable selection notice and retain the comparison in its workspace. Never
silently open a different exchange with a reused sequence. Wire this through the
existing environment navigation/route selection and TrafficPanel, with tests for
cross-environment navigation and missing detail.

Update defensive copies, byte accounting, Clear handling, summary redaction,
recording serialization, and MCP safe mapping together. SSE summaries carry only
safe capture/provenance metadata, never headers, bodies, or draft credentials.
Metadata-only recordings must mark bodies omitted after stripping them; they
must not retain a `complete` classification copied from live capture.

## 6. Bounded workspace and run lifecycle

Use ephemeral daemon state, separate from SQLite environment operations. A
workspace freezes the original redacted exchange when Replay opens. It remains
valid after ordinary live eviction and has one prepared draft revision and one
latest full result. This small store exists to preserve the baseline, bind
remote review, and suppress duplicate execution; it is not a saved collection.

Use readable numeric workspace/run numbers scoped to the origin environment.
Also require the workspace creation timestamp and daemon start timestamp in
mutation preconditions. Numeric IDs can repeat after a restart; an old delayed
POST must never address a newly created workspace with the same number.
These timestamps are version expectations, not ownership keys or URL identities.
Every read response includes them too. Polling and result retrieval for a known
workspace send expected identity or compare it before accepting the response;
a mismatch expires the old UI session rather than replacing its baseline.

| Budget | Initial enforced limit |
| --- | --- |
| Workspace lifetime | 15 minutes from creation; expired sessions cannot admit runs |
| Workspaces | 32 per daemon, 8 per origin environment |
| Aggregate replay state | 64 MiB including baselines, prepared drafts, results, comparison structures, and in-flight buffers |
| Concurrent runs | 4 per daemon, 1 per workspace; reject excess admission instead of queueing |
| Runs per workspace | 32; retain compact terminal receipts for all admitted run numbers |
| Request body | 64 KiB of UTF-8 bytes, including replacements |
| Control request envelope | 1 MiB of JSON, enforced before decoding; individual field limits still apply |
| Request/header input | 128 rows and 32 KiB total header bytes; path/query 8 KiB |
| Upstream response headers | 64 KiB; reject over-limit headers through bounded transport parsing |
| Retained response body | 64 KiB prefix with explicit completeness metadata |
| Response bytes consumed | 1 MiB; stop and close when exceeded, reporting an incomplete result |
| Run deadline | 30 seconds total, including fault delay, dialing, body read, and completion bookkeeping |
| Prepared secret lifetime | At most 60 seconds; release unused preparation on use, revision change, explicit disposal, expiry, or shutdown |
| UI/server comparison | 10,000 JSON nodes, depth 64, 1,000 displayed changes, bounded text-diff work |

Treat these as starting product budgets to verify with tests and measurements.
Reject allocations/admission over budget; never evict a running request or a
duplicate-suppression receipt to make room. Expiry may let an admitted run
finish within its existing deadline but must not extend it. Use bounded workers,
no per-poll goroutine or unconstrained response accumulation.
Reject preparation when original header sets exceed the corresponding request
or response header budget, before copying them into workspace state. Report the
specific capture limit; do not truncate headers and then claim a complete diff.

Keep safe prepared data separate from ephemeral credential-bearing runtime
values. GETs, logs, comparisons, timeline and errors expose only safe data.
Discard references promptly; Go memory management does not imply guaranteed
cryptographic erasure. The browser resubmits manually entered credentials for
each preparation from its own memory. Expired secret preparation requires a
new revision before Send.
An admitted run owns the credential values it needs until dispatch/completion
cleanup; preparation expiry must not corrupt an in-flight request. Apply known
credential-value redaction if an upstream echoes those supplied values, before
replay result/live/recorded retention. Arbitrary application payloads still follow
the existing local capture policy; do not claim general secret detection.

Run states are `running`, `completed`, `failed`, and `interrupted`. Record a
separate dispatch outcome: `not-sent`, `response-received`, or `unknown`.
HTTP 4xx/5xx completes a run with an application response, not an API transport
failure. Response truncation/limits are explicit result limitations. A timeout
or connection loss after sending but before an application response may mean
the application committed a write: label delivery unknown, with no automatic
retry. Once response headers arrive, preserve `response-received`, status and
headers even if its body subsequently fails; mark that body incomplete. Client
disconnect does not cancel an admitted worker.

Shutdown cancels and drains replay workers before closing proxy, traffic or
database dependencies. Its deadline is the smaller of each run's remaining
deadline and the enclosing daemon shutdown budget. Do not extend normal daemon
restart to 30 seconds or publish into a disposed traffic store. Reserve bounded
completion time within the shutdown budget, and classify interrupted work safely.

The server atomically admits the next run number for a prepared revision.
Duplicate submission of that same run returns its current/terminal receipt.
Conflicting revision/arguments for an admitted number return conflict. Older
run numbers can never execute again after the latest result is replaced.
A lost POST response is resolved by GET; another API Send is unnecessary.
This suppresses duplicate Portless dispatch within the workspace lifetime;
it does not promise exactly-once application effects across daemon failure.

## 7. API and typed client contract

Proposed resource prefix:

```text
/api/v1/environments/{projectName}/{environmentName}/traffic/replays
```

The path names the baseline/origin environment. Destination is a same-project
environment name in the draft. Require normal authenticated mutation/CSRF
checks, explicit body-size limits, strict JSON decoding, and no-store responses.

| Method and suffix | Behavior |
| --- | --- |
| `POST /` | Prepare a workspace from a live sequence plus expected exchange/daemon timestamps. Return 201 with frozen baseline, initial safe draft, unresolved omissions, number, creation time and expiry. No application I/O. |
| `PUT /{number}/draft` | Accept expected workspace identity/revision, complete edited request and destination. Validate, normalize and prepare a new revision with current policy and destination generation; return safe data and whether remote-write confirmation is required. Reject while a run is active. |
| `POST /{number}/runs` | Accept expected workspace identity, prepared revision, next run number, and confirmation when required. Atomically admit once and return 202/receipt; a duplicate returns its existing receipt. |
| `GET /{number}` | Return identity timestamps, workspace/run metadata, compact admitted-run receipts, prepared revision, deadline, limitations and next run number. Support expected identity for reconciliation. `include=result` adds the frozen baseline, latest redacted submitted request/result, and bounded comparison; no prepared credential values. |
| `DELETE /{number}` | Release a non-running workspace's payloads and draft secrets. Retain compact receipts until expiry. Return conflict while a run is active; browser closing is not DELETE/cancellation. |

Expected wire types in `api/contract/traffic_replay.go`:
`PrepareTrafficReplayRequest`, `TrafficReplayWorkspace`, `TrafficReplayDraft`,
`UpdateTrafficReplayDraftRequest`, `RunTrafficReplayRequest`,
`TrafficReplayRun`, `TrafficReplayResult`, `TrafficResponseComparison`, and
typed capture/limitation/change values. Each exported declaration needs GoDoc.
Use timestamp/revision fields consistently for all client preconditions.

The draft includes method, raw request target, repeated headers, body mode/text,
destination environment, and explicit omitted-header/body resolutions. It does
not accept a URL, source/target override, provider credentials, or runtime address.
The result includes the normalized submitted request with secrets redacted,
actual destination provider/policy snapshot, observed exchange reference,
dispatch outcome, timings, limitations, and structured diff.

Classify invalid draft as 400, oversized control input as 413;
policy/auth denial as 403; unavailable baseline/workspace as 404; expired
workspace as 410 where identifiable; stale revision/destination or concurrent
run as 409; capacity as 429; and unavailable daemon/runtime as 503. Use stable
structured error codes and remediation. Runtime application failures belong
in the run result. Errors must not contain secret header values, request bodies,
private ports, or raw dial URLs.
Do not echo raw decoder/validation values in errors: malformed header/body input
can itself contain credentials. Return the field name and a fixed reason.

Add typed client methods for each endpoint. Poll metadata at a bounded interval
(initially 500 ms while running, backing off on disconnection); request full
result only at completion or explicit reopening. No replay-specific SSE topic
is required initially. Real executed exchanges continue through the existing
destination traffic/trace stream. Document reconciliation in `events.md`.

The inspected API version is 16.0.0. Plan an additive 16.1.0 version for the new
resources and optional capture/provenance fields, rechecking the current version
before implementation. Daemon lifecycle and supervisor protocols do not change.
Recording export advances from schema 3 to schema 4 to define body availability
and replay provenance consistently; update the current import/export consumers
and fixtures together. Do not add retired-format compatibility adapters.

## 8. Comparison semantics

Compute the canonical comparison in `traffic/replay`, using only redacted,
bounded baseline/result values. The browser renders this result; CLI does not
implement a competing comparator. Comparison has per-section states `equal`,
`different`, `partial`, and `unavailable`; a global summary must not claim full
equality when any relevant section is incomplete or redacted.

| Section | Rules |
| --- | --- |
| Status/outcome | Show original and replay HTTP status separately from transport/dispatch failure. A missing response is not HTTP status zero or a matched failure. |
| Duration | Show original/replay observed duration and signed delta. Show percent only when the original duration is nonzero. Label single-run timing; do not imply a benchmark or statistically established regression. |
| Size | Distinguish observed bytes from retained bytes. Stopping after the consumption cap does not establish the response's total size. |
| Headers | Compare names case-insensitively and value arrays in order; do not comma-fold repeated headers. Report added/removed/changed names. Redacted values are unknown, not equal because both contain a placeholder. |
| Complete JSON | Ignore object-key ordering and insignificant whitespace. Preserve array order, missing versus null, and type differences. Use Go `json.Decoder.UseNumber`; do not round large integers through float64/JavaScript Number. Compare numeric token spellings conservatively, so `1` and `1.0` may differ. |
| JSON ambiguities | Duplicate object keys, trailing tokens, excessive depth/node count, invalid UTF-8, or invalid JSON fall back to bounded text inspection with a reason; do not silently collapse ambiguous objects. |
| Complete text | Compare exact text, including meaningful whitespace/newlines, with bounded line-diff work. If work/line/change limits are exceeded, show changed state and raw bounded panes instead of an unbounded diff. |
| Partial or unsupported body | Label captured-prefix differences only. Never claim complete equality or parse a truncated JSON prefix as a document. Status/header comparisons still work. |

Keep number comparisons exact across API rendering by returning scalar display
values as typed strings in change records. Escape JSON Pointer paths and sort
object traversal for stable output. Suggested text budget: 2,000 lines and
1,000,000 comparison steps, then an explicit limited result. Cap both computation
and mounted change rows. No hidden default ignore rules for Date, request IDs,
timestamps, or array order. User-defined ignore rules are a later feature.

JSON's number range and interoperability constraints support preserving number
tokens rather than silently accepting binary64 rounding.
[RFC 8259, numbers](https://www.rfc-editor.org/rfc/rfc8259.html#section-6).

The result UI provides Original, Replayed, and Response diff views, each with
Body/Headers. Wide diff shows original and replay values beside one another;
narrow diff stacks labelled sides. Reuse/factor the existing traffic message
inspector, copy controls, capture notices, and literal content rendering.
The current `trafficBodyPresentation` uses `JSON.parse`/`JSON.stringify`, which
can round large numbers and collapse duplicate keys. Do not pass replay baselines
or results through that formatter. Provide a lossless token formatter or a raw
fallback for Original/Replayed panes, Copy and the optional editor Format action.
Copy raw content by default; any separately offered formatted copy must preserve
number tokens and ambiguity. Test displayed values as well as diff records.

## 9. CLI and MCP

Add the following under the existing `traffic` command group:

```bash
portless traffic replay 142
portless traffic replay 142 --against store/fix
portless traffic replay 142 --method POST --path '/orders?dryRun=true' \
  --header 'Content-Type: application/json' --body-file request.json
portless traffic replay 142 --empty-body
portless traffic replay show 3
portless --json traffic replay show 3
```

Global `--env` selects the origin. `--against` must name the same project's
destination. Repeatable `--header` replaces the selected name's original values
with the provided ordered values; `--remove-header` explicitly omits a captured
name. `--body-file` (including `-`) reads bounded content in the CLI and sends
bytes, never a path for the daemon to open. Add a bounded `--headers-file` option
for values that should not be entered in shell history. Reject conflicting
body/header options. Header-file entries preserve value arrays.

The command prepares, renders any missing-input requirements, obtains only the
required remote-write confirmation, submits once, waits, and prints status,
duration and change summary plus the workspace number. A `--yes` option can
confirm the specifically reviewed remote write in noninteractive use; it cannot
override read-only or unresolved body/credential omissions. `--json` emits one
bounded machine-readable result and never includes plaintext runtime secrets.
Ctrl-C stops waiting and reports the receipt if known; it does not promise to
cancel an already admitted request. `traffic replay show` resumes inspection.
Automatic recovery always matches the receipt's daemon/workspace timestamps.
A fresh explicit `traffic replay show 3` denotes workspace 3 in the currently
running daemon, and prints its creation/daemon times and original exchange
identity prominently. For recovery from an earlier receipt, support
`--expected-created-at` and `--expected-daemon-started-at` together, fail on a
mismatch, and print a copyable identity-bound show command when waiting fails.

Define exit behavior: completed HTTP responses, including 4xx/5xx and detected
differences, are successful inspection results; validation, execution, timeout,
or unavailable-result failures are nonzero. Assertion-style exit codes are
deferred to scenarios. Keep useful parent help, completion, output/color policy,
typed API calls, and generated command-reference tests.

Do not add MCP replay mutation tools in v1. Current traffic-control capability
permits bounded faults/recordings, not arbitrary application HTTP writes. Safe
traffic inspection can show replay provenance; detailed body/header access
continues to require the sensitive-traffic capability. Future execution needs
a separately designed immutable capability and consent contract.

## 10. File-level work packages

| Work package | Files to add or update |
| --- | --- |
| Contract/client | `portless-daemon/api/contract/traffic_replay.go`, `traffic.go`, `contract.go`; `api/client/traffic_replay.go` and client tests; `portless-web/src/api/contracts/traffic.ts` or a traffic-owned replay contract module. |
| Capture/provenance | `portless-daemon/model/model.go`; `traffic/proxy/manager.go`; capture helpers; `traffic/store.go`, `traces.go`, projection/copy/retention tests; recording serialization and mock recording importer. |
| Replay domain | New `portless-daemon/traffic/replay/{manager,validation,comparison}.go` and focused tests/fuzz cases. Keep buffers and receipts in one explicitly bounded manager. |
| Dispatch | New `traffic/proxy/replay.go`; narrowly refactor shared forwarding/completion, target admission, raw-path handling and trace creation; real isolated HTTP/TLS tests. |
| Control/API | `portless-daemon/controlplane/replay.go`, service construction/shutdown; `api/server/traffic_replay.go`, `traffic.go`, error mapping and security tests; daemon composition injection as needed. |
| Browser | New `features/traffic/replay/{useTrafficReplay,trafficReplayDraft,TrafficReplayEditor,TrafficReplayResult,ReplayHeaderEditor}`; TrafficPanel, both drawer wrappers, TrafficDrawerShell, inventory props, shared overlay integration, lossless/raw body presentation, destination exchange-selection navigation, `app.css`. |
| Existing split view | `detail/trafficDetailTypes.ts`, `TrafficDrawerShell.tsx`, `CommandResultLayout.tsx`, `TrafficFormatting.tsx`, HTTP/TCP detail components, matching CSS/tests. |
| CLI/MCP | `portless-cli/traffic` replay commands/options/output/tests; command-tree reference; MCP safe-result mapping/tests only. |
| Product proof/docs | New `portless-web/e2e/traffic-replay.spec.ts`, traffic inspection expectations, isolated CLI E2E; README, implementation status, command reference, OpenAPI/events, E2E guide and architecture guards. |

The current working tree contains unrelated mock-editor/README and generated
web changes. Preserve them. At implementation time inspect the tree again,
coordinate overlapping `app.css`/README edits, and regenerate tracked web assets
through Make instead of copying or hand-editing bundles.

## 11. Delivery milestones and exit criteria

| Milestone | Deliverable | Exit criterion |
| --- | --- | --- |
| A — Fidelity and contracts | Capture states, provenance, request/result types, limits, API/version decisions and comparison semantics. | Tests distinguish empty/omitted/truncated/incomplete bodies, repeated headers and large JSON numbers; architecture remains coherent. |
| B — Bounded replay executor | Domain manager, frozen live baseline, source-aware dispatch, generation checks, one-send receipts, result comparison. | Real local/mock/remote fixture tests prove edge attribution, fresh trace roots, policy enforcement and no duplicate dispatch. |
| C — API and CLI | Authenticated replay routes, typed client, terminal receipts, CLI send/show/output/completion. | Compiled CLI can replay a real request, inspect its diff and recover a lost response through GET without another send. |
| D — Browser workflow | Shared Replay action, editor, destination picker, result panels, pending/error/focus handling, SIDE BY SIDE rename. | One complete browser journey works from both Exchanges and a waterfall span without altering the original. |
| E — Product validation | Full boundary matrix, performance/race checks, docs and regenerated embedded assets. | Required suites pass; normal built-checkout daemon restart succeeds and refreshed UI serves the new workflow. |

Do not expose a browser Send action backed by a temporary generic HTTP fetch.
Each milestone should be independently reviewable, but the user-facing feature
is complete only after the full single-request journey and its limits ship.

## 12. Validation matrix

| Area | Required evidence |
| --- | --- |
| Preparation | Opening/editing/resetting sends zero upstream requests, makes no health probe, changes no provider/fault counter/recording, and rejects stale or recorded-only baselines. |
| Fidelity | Proven empty, complete text, unknown-length/chunked, truncated, omitted, binary, compressed, invalid UTF-8, aborted upload, explicit replacement and explicit empty choice. No placeholder is transmitted. |
| Request semantics | Repeated headers/query names, empty values, raw escaping, encoded slashes, remote base paths, HEAD, all allowed methods, computed length, forbidden URL/header/method forms. |
| Edge and provider | Both external and internal edges; non-primary ingress; alternate environment with matching edge; stopped/missing/incompatible destination; local/mock/eligible container/remote targets; current faults and recording scope. |
| Policy and staleness | Read-only remote mutation causes zero remote calls; required read-write confirmation; changed method/destination/revision; provider generation change before/during fault delay and dialing; connection attachment rejects invalidated admission; no silent replacement target. |
| Trace/capture | New sequence/trace/root, no temporal attachment to an unrelated active caller, downstream correlation retained, explicit replay remains foreground, metadata-only SSE/MCP and recording capture policy. |
| Delivery | Double click, concurrent same-run POST, conflicting reuse, response lost after admission, repeated/stale GET, old run tombstone, expiry, Clear, daemon restart and timestamp collision, ordered shutdown; exactly one Portless dispatch per admitted run. |
| Transport | Redirect not followed, no control cookies/token leakage, no automatic retry on failed/reused-connection paths, valid/untrusted TLS, timeout before versus after send, abort fault preserved, unexpected 101, SSE/stream consumption cap. |
| Comparison | Status/errors, zero-duration baseline, headers by case/array order, unknown redacted values, object order, arrays, null/missing, duplicate JSON keys, big numbers, invalid JSON/text, partial captures, depth/node/diff limits. |
| Browser | Entry from both inspection paths, immutable baseline, repeated edits/runs, stale-result label, pending controls, close/reopen and credential clearing, workspace replacement, late responses, Clear/navigation, exact destination exchange selection/expiry, lossless display/copy, focus restoration, keyboard tabs, input arrows, dark/light, narrow/focus/fullscreen layouts. |
| Capacity | Maximum sessions/runs/header/body/diff sizes, full live ring, slow upstream, dropped polls, four simultaneous runs, session expiry and shutdown. Verify bounded memory, cleanup, no proxy-wide lock during I/O and no starvation of ordinary traffic. |

Use real local HTTP fixtures and upstream call counters for side-effect,
authentication and policy assertions. Use a loopback TLS fixture configured as
a remote provider; never send test mutations to a real remote service. Use Go
fuzzing for path/header/draft validation and structured JSON comparison limits.
Mocking a browser API response alone does not prove replay execution.

The primary acceptance journey: capture a request whose baseline returns 200
with a known JSON value; open Replay without sending; select a second isolated
environment whose corresponding service returns a changed field; send once;
see both responses and the exact field/status/timing comparison; edit and send
again; verify original traffic unchanged and new traffic correctly attributed
to the selected environment/edge. Repeat from a waterfall span. Separately
exercise a mutating fixture with a counter to prove duplicate suppression and
remote-policy refusal.

Run focused tests as each owner changes, then:

```bash
go test ./portless-daemon/traffic/... ./portless-daemon/controlplane
go test ./portless-daemon/api/... ./portless-cli/traffic ./portless-mcp
go test -race ./portless-daemon/traffic/... ./portless-daemon/controlplane
go test ./tests/architecture
npm --prefix portless-web run typecheck
npm --prefix portless-web test
make lint
make test
make test-e2e
git diff --check
```

Update `docs/e2e-testing.md` and use its isolated compiled-product harness. The
normal end-to-end tests use the test-only private ingress, preserving real
application routing without changing the installed relay. No destructive relay,
reset/uninstall, or incidental developer-service shutdown is validation here.
Run container-resource E2E only if changes reach that runtime boundary.

After implementation validation, build the exact checkout with `make`, inspect
`./bin/portless daemon status`, and complete the normal
`./bin/portless daemon restart` before handing off the refreshable local UI.
Investigate a blocked handoff; forced restart requires separate authorization.
Verify the refreshed page, healthy adopted environments, and loaded asset hash.

## 13. Definition of done and follow-on work

The feature is done when a developer can replay one captured HTTP request from
either Traffic entry, see an accurate bounded comparison, repeat deliberately,
and trust that Portless preserves the selected edge and current provider policy.
Missing capture, redaction, stale destination, unknown delivery outcome, and
comparison limits must be visible rather than hidden behind a successful-looking
result. Required unit, integration, architecture, CLI and browser checks pass.

Follow-on work can add named-recording exchange browsing with exact indexed
lookups, durable saved runs, arbitrary response-pair comparison, ignore rules,
binary/streaming support, whole-trace scenarios, and reproduction-bundle
integration. Those extensions should reuse the execution and comparison
contracts after defining their additional identity, persistence, and consent
requirements. See [future features](future-features.md) and
[reproduction bundles](reproduction-bundles.md).
