# Traffic transforms implementation plan

Status: proposed; not implemented.

Created: 2026-08-24.

## Goal

Let a developer select an observed HTTP route or an explicit directed edge,
attach a bounded TypeScript transform, preview the result, and temporarily
rewrite request or response headers and inspectable bodies without modifying
application source code.

Traffic transforms fill the gap between Portless's existing interventions:

- a mock replaces the selected upstream provider;
- a fault changes timing, availability, or failure behavior; and
- a transform keeps the real provider while adapting the request or response.

The primary use case is temporary API compatibility across independently
changing repositories or providers. Other intended uses include injecting a
development identity or feature header, reshaping a QA response, reproducing a
legacy payload, removing a field, and testing a consumer against an upcoming
schema.

The feature must preserve Portless's zero-configuration entry point,
source-aware routing, single distributed executable, remote write-policy
boundary, bounded traffic handling, and explicit local safety controls.

## Prerequisites

Implementation should begin only after the current trace-first traffic contract
and UI changes are complete. The implementation must use the then-current
traffic detail shape as authoritative rather than assuming the exact API
version or flattened fields described in this plan remain unchanged.

Before implementation:

1. Re-read `README.md`, `docs/e2e-testing.md`, this plan, and the current
   `docs/plans/traffic-tracing.md` status.
2. Read `portless-daemon/api/openapi.yaml` and
   `portless-daemon/api/events.md` before changing the HTTP or event boundary.
3. Inspect the current working tree and preserve unrelated work.
4. Complete the runtime-selection spike and record the selected engine and
   compiler in an ADR. No public transform contract should merge before that
   gate passes.

## Product decisions

The first release makes the following decisions deliberately:

- The public name is **traffic transform**. `converter` may appear in
  explanatory copy, but not as a second command, API resource, or wire name.
- A transform belongs to one project/environment and one explicit
  `source -> target` HTTP edge. It is never target-only.
- Method and path matching are declarative and evaluated before TypeScript is
  invoked. Code does not choose its own blast radius.
- TypeScript may return request header/body patches and response
  status/header/body patches. It cannot change the source, target, upstream
  host, URL scheme, HTTP method, or request path in the first release.
- Transform source is stateless, synchronous, import-free, and capability
  isolated. It cannot access files, environment variables, processes, the
  network, Portless private keys, runtime ports, or arbitrary clocks and
  randomness.
- No Node.js installation is required at runtime. Compilation and execution
  ship inside the existing Portless executable.
- Every activation is temporary. It defaults to 15 minutes and cannot exceed
  one hour in the first release. The saved definition remains after expiry and
  can be enabled again with a new expiry.
- Matching execution fails closed. A compile or preview failure prevents
  activation; an invocation failure returns a visible Portless-generated
  `502` rather than silently forwarding unmodified traffic.
- TCP, WebSocket, server-sent-event, gRPC, streaming, and arbitrary binary-body
  transformation are out of scope initially.
- MCP does not receive transform source or transform mutation tools in the
  first release. Existing MCP traffic-control permission is not sufficient to
  authorize arbitrary code.

## User experience

### Create from observed traffic

The preferred browser workflow starts in the Traffic view:

1. Select an HTTP exchange or span.
2. Choose **Create transform** in the exchange drawer.
3. Pre-fill project, environment, source, target, method, and the exact
   normalized path from the exchange.
4. Let the developer keep the exact path, choose a suggested path glob, or
   enter a custom valid glob.
5. Select request and response body modes independently: none, text, or JSON.
6. Generate a typed starter function from the selected modes.
7. Preview the candidate against the selected exchange without saving,
   incrementing counters, publishing events, or producing timeline history.
8. Show compile diagnostics and structured request/response before-and-after
   differences.
9. Enable the valid transform for 15 minutes by default.

Active transforms appear in all of these places:

- an environment-level indicator with the remaining lifetime;
- an edge badge in topology and traffic filters;
- the exchange intervention summary;
- a dedicated Transforms view with disable-all;
- environment timeline entries for lifecycle changes; and
- concise CLI list and show output.

An active indicator always offers an immediate disable action. Disabling is
not destructive and does not require confirmation. Deletion is permanent,
requires the transform to be disabled, and is preview/confirmation guarded in
the CLI and browser.

### CLI workflow

The owning package remains `portless-cli/traffic`, but the artifact receives a
top-level command alongside `traffic`, `record`, and `fault`:

```text
portless transform list
portless transform preview --file adapt.ts --exchange 142
portless transform apply api-v2 checkout:payments \
  --file adapt.ts \
  --method POST \
  --path '/api/charges/*' \
  --request-body json \
  --response-body json \
  --duration 15m
portless transform show api-v2
portless transform show api-v2 --source
portless transform enable api-v2 --duration 15m
portless transform disable api-v2
portless transform clear
portless transform delete api-v2 --yes
```

`apply` creates and enables a new transform. If the name already exists it
fails with its current revision and requires `--replace`; replacement sends
that revision so a concurrent edit produces a conflict instead of overwriting
work. `--file -` reads source from standard input. The daemon receives source
content and never receives or reads a client filesystem path.

Default output is concise human text. All data-bearing commands support global
`--json`; streaming is not part of the first command surface. List and show
without `--source` omit source code. Shell completion covers transform names
and configured HTTP edges.

## Transform identity and lifecycle

A transform is a named environment artifact with this logical state:

```text
project / environment / name
  matcher: source, target, method?, path?
  order: integer
  requestBodyMode: none | text | json
  responseBodyMode: none | text | json
  sourceCode: TypeScript
  scriptApiVersion: semantic version
  maxBodyBytes: bounded integer
  readSensitiveHeaders: boolean
  remoteMode: deny | response-only | request-response
  enabled: boolean
  expiresAt: timestamp when enabled
  revision: monotonic integer
  counters and last safe runtime outcome
```

Public identity uses the existing artifact-name validation and is
case-insensitively unique within an environment. Private environment keys stay
inside persistence and never appear in source, APIs, commands, URLs, events,
errors, or worker messages.

The lifecycle is:

```text
preview candidate -> create enabled -> update atomically -> expire/disable
                  -> enable with new expiry -> disable -> delete
```

Only source that compiles and passes static contract validation may be stored.
Updating an enabled transform compiles and prepares the new revision first,
then atomically makes it visible to new requests. In-flight requests retain
the revision snapshot they began with. Disable and disable-all prevent matches
for requests beginning after the mutation response; already-running requests
finish with their captured revision.

Transforms are not copied when an environment is cloned. They are diagnostic
experiments like faults, not provider topology like mock profiles. Forgetting
an environment deletes its transforms through the environment foreign key.
Removing a service or connection disables affected transforms and retains
their audit history; it never retargets a transform by name.

## Matching and composition

The matcher is evaluated against the request as received by Portless:

- `source` and `target` are required exact service identities;
- the connection must exist and use HTTP;
- `external -> primary-service` is a valid ingress edge;
- `method`, when present, is one uppercase HTTP token;
- `path`, when present, uses the same validated Go `path.Match` glob semantics
  as faults; and
- query values, fragments, headers, and bodies do not affect matching in the
  first release.

Matching remains based on the original method and normalized path even after
an earlier transform modifies headers or a body. This makes the selected route
stable and prevents one script from expanding another script's scope.

More than one transform may match. Composition is explicit and deterministic:

1. Sort by ascending `order`.
2. Break equal-order ties by case-insensitive artifact name.
3. Run request hooks in that order.
4. Send the result upstream.
5. Run response hooks in the same order.

Each hook sees the output of earlier hooks. Exchange detail records the exact
ordered names and revisions. Preview shows the complete active chain when the
candidate would compose with another transform. No more than eight transforms
may match one exchange, and no more than 32 may be enabled in one environment.
Activation that would violate either bound fails before state changes.

`order` defaults to `100`. The browser explains ordering only when more than
one rule overlaps; it does not require developers to understand middleware
ordering for the common one-transform case.

## TypeScript authoring contract

The daemon embeds the authoritative ambient declarations. Source files have
no imports and may export either or both synchronous hooks:

```ts
export function onRequest(
  request: HttpRequest,
  context: TransformContext,
): RequestPatch | void

export function onResponse(
  response: HttpResponse,
  context: TransformContext,
): ResponsePatch | void
```

At least one hook is required. Returning `undefined` means no change. Returning
`null`, a Promise, a class instance, a function, a symbol, a cyclic value, or
any value outside the patch schema is a runtime contract error.

The version-one ambient surface is conceptually:

```ts
type JsonPrimitive = string | number | boolean | null
type JsonValue = JsonPrimitive | JsonValue[] | { [name: string]: JsonValue }
type HeaderValues = Readonly<Record<string, readonly string[]>>

interface TransformContext {
  readonly project: string
  readonly environment: string
  readonly source: string
  readonly target: string
  readonly provider: 'local' | 'container' | 'mock' | 'remote'
  readonly remoteClassification?: 'development' | 'qa' | 'staging' | 'unknown'
  readonly requestStartedAt: string
  readonly traceId?: string
  readonly request: Readonly<{
    method: string
    path: string
    requestTarget: string
    headers: HeaderValues
    body?: string
    json?: JsonValue
  }>
}

interface HttpRequest {
  readonly method: string
  readonly path: string
  readonly requestTarget: string
  readonly headers: HeaderValues
  readonly body?: string
  readonly json?: JsonValue
}

interface HttpResponse {
  readonly status: number
  readonly headers: HeaderValues
  readonly body?: string
  readonly json?: JsonValue
}

interface HeaderPatch {
  readonly set?: Record<string, string | readonly string[]>
  readonly append?: Record<string, string | readonly string[]>
  readonly remove?: readonly string[]
}

interface MessagePatch {
  readonly headers?: HeaderPatch
  readonly body?: string
  readonly json?: JsonValue
}

type RequestPatch = MessagePatch
type ResponsePatch = MessagePatch & { readonly status?: number }
```

The actual `types.d.ts` shipped by the implementation is the contract and must
be tested as an embedded asset. Input objects are recursively frozen. Header
names are case-insensitive, input values retain repeated lines, and patch
operations are applied case-insensitively without collapsing unrelated values.
For a request hook, `context.request` is the same current request passed as the
first argument. For a response hook, it is the final effective request sent
upstream after every request hook, subject to that transform's sensitive-header
capability.

Example:

```ts
type Charge = {
  customerId: string
  amount: number
  currency?: string
}

export function onRequest(request: HttpRequest): RequestPatch {
  const charge = request.json as Charge
  return {
    headers: {
      set: { 'x-portless-scenario': 'api-v2' },
      remove: ['x-legacy-client'],
    },
    json: {
      customer_id: charge.customerId,
      amount_cents: charge.amount,
      currency: charge.currency ?? 'USD',
    },
  }
}

export function onResponse(response: HttpResponse): ResponsePatch {
  const result = response.json as { id: string; approved: boolean }
  return {
    headers: { set: { 'cache-control': 'no-store' } },
    json: { chargeId: result.id, status: result.approved ? 'approved' : 'declined' },
  }
}
```

The script API exposes plain ECMAScript data operations, JSON, arrays, maps,
sets, and regular expressions. It does not expose imports, `require`, dynamic
import, `eval`, `Function`, WebAssembly, console output, fetch, sockets, files,
environment variables, processes, timers, or crypto. Host time and randomness
are not available; reproducible request time is supplied through context.
Global state is discarded between invocations.

## Mutation rules

### Allowed request mutations

- Set, append, or remove end-to-end request headers.
- Replace a bounded text body.
- Replace a bounded JSON body with JSON-serializable data.

### Allowed response mutations

- Change the final status to an integer from 200 through 599.
- Set, append, or remove end-to-end response headers.
- Replace a bounded text body.
- Replace a bounded JSON body with JSON-serializable data.

### Immutable or reserved values

The first release does not permit code to modify:

- source, target, provider, upstream URL, host, scheme, method, path, query, or
  trace identity;
- hop-by-hop headers, `Host`, `Content-Length`, `Transfer-Encoding`, or
  connection-upgrade headers;
- W3C, B3, or Datadog trace-propagation headers managed by Portless; or
- method-override headers that could weaken a remote read-only policy.

Attempts to return a reserved mutation fail the invocation. Portless owns
`Content-Length`, transfer framing, trace injection, and hop-header removal.

Transforms do not delay, abort, synthesize an upstream-free response, reroute,
or probabilistically match. Faults, mocks, and provider bindings continue to
own those behaviors.

## Body and HTTP semantics

Body handling is declared outside the source independently for each direction:

- `none`: the hook receives no body and the proxy preserves streaming for that
  direction;
- `text`: the proxy buffers a bounded UTF-8-compatible inspectable body and
  supplies `body`; and
- `json`: the proxy buffers a bounded JSON media type, parses it, and supplies
  `json`.

`none` is the default. A hook may return a body only when its direction's body
mode is `text` or `json`. Text mode uses the existing inspectable-body media
types. JSON mode accepts `application/json` and structured `+json` types.

Initial hard limits are:

- TypeScript source: 64 KiB;
- aggregate headers supplied to or returned by one hook: 64 KiB;
- body per direction: 256 KiB by default and 1 MiB maximum;
- serialized patch: the configured body cap plus 64 KiB of headers; and
- eight matching transforms per exchange.

Unknown-length bodies are read only through a bounded reader. Crossing the
limit fails before the hook receives a partial value. A request-body failure
occurs before contacting the upstream. A response-body failure occurs after
the upstream replied but before Portless sends response headers to the caller.

Body-aware request transforms initially reject non-identity
`Content-Encoding`. Body-aware response transforms request identity encoding
upstream and reject a compressed response that still arrives. Transparent gzip
decode/re-encode can be added later; the first implementation must not hand
compressed bytes to a text hook.

After a body mutation, Portless:

- recomputes `Content-Length` and removes stale transfer framing;
- removes stale `Digest`, `Content-MD5`, and strong entity tags;
- preserves an existing compatible JSON content type or supplies
  `application/json` when the transform creates JSON without one;
- rejects a body for `HEAD`, `204`, or `304` response semantics; and
- closes/drains upstream bodies using bounded normal proxy cleanup.

Patch validation happens before mutating the live message, so a failed patch
never leaves a partially modified request or response.

## Sensitive values

Scripts receive the raw request or response only at the live proxy boundary.
Known credential-bearing header values are replaced with `[REDACTED]` in hook
input by default. The patch API means an omitted sensitive header remains
unchanged on the real message even though the script saw a redacted view.
Scripts may explicitly set or remove a sensitive header without reading its
old value.

`readSensitiveHeaders` is a separately confirmed transform capability. When
enabled, the hook may inspect live credential-bearing headers. The browser and
CLI warn that source can intentionally reflect those values into an allowed
body or header. Preview against retained traffic still receives redacted data
because Portless never retains the original credential values.

Regardless of this capability:

- traffic retention redacts known credentials before storing original or
  effective headers;
- worker errors, timeline events, daemon logs, counters, and API diagnostics do
  not contain runtime header or body values;
- list responses and events never contain source code; and
- transform source is never added to a recording export automatically.

The source itself may contain literal secrets. The editor and CLI warn against
this, source is stored only in the ownership-protected local database, and no
heuristic scanner is treated as a secret-safety boundary.

## Execution architecture and runtime gate

Arbitrary TypeScript in the proxy path is the highest-risk part of this
feature. It must not run inside the daemon process and must not invoke a
project's Node.js, package manager, source tree, or dependencies.

The selected architecture is a private, long-lived transform worker mode in
the same distributed `portless` executable:

```text
daemon proxy
  -> bounded transform engine interface
  -> authenticated parent/child pipe protocol
  -> private transform worker process
  -> embedded TypeScript compiler and JavaScript runtime
```

The executable entry point only dispatches the hidden mode. Worker lifecycle,
protocol, compiler assets, runtime limits, and patch mapping belong under
`portless-daemon/traffic`, not the CLI composition root. The worker has no
network listener and no daemon HTTP route.

The parent starts a small bounded worker pool. Scripts are compiled on create,
update, enable, preview, and daemon reconciliation, then cached in memory by
source hash plus script API version. Compiled artifacts are not persisted,
because they are engine-version-specific; source remains authoritative and is
recompiled after an executable change.

Every invocation uses a fresh global scope. The parent sends only public scope
names, safe provider classification, bounded message data, deterministic
request context, script hash/revision, and the hook name. It never sends
private database keys, authentication material, environment variables,
runtime process data, or upstream private addresses.

The pipe protocol uses length-prefixed frames, a fixed maximum frame size,
request identifiers, a private startup nonce, and an internal protocol version.
Malformed, oversized, unsolicited, duplicated, or late frames terminate and
replace the worker. Standard output is reserved for the protocol; diagnostic
text is bounded and sanitized on standard error.

Initial runtime budgets are:

- compile deadline: 2 seconds;
- hook CPU budget: 20 milliseconds;
- hook wall deadline including queueing: 50 milliseconds;
- worker memory target: 128 MiB with a hard process/runtime limit where the
  selected engine supports it;
- bounded pending queue with backpressure; and
- at most four workers by default, capped by available CPUs.

These numbers are starting contract limits, not tuning guesses. The runtime
spike must benchmark 1 KiB, 64 KiB, 256 KiB, and 1 MiB JSON transformations and
may adjust the values once before the public contract is documented. User
configuration cannot relax CPU, wall, source, header, chain, or global memory
limits in the first release.

### Phase-zero runtime selection spike

Before product implementation, build a disposable benchmark harness that
compares viable embedded compiler/runtime combinations inside the worker
process. The ADR must select a combination that:

- builds for supported macOS/Linux AMD64 and ARM64 release targets;
- does not require C toolchains or a separately installed runtime unless a
  product-level dependency review explicitly accepts that cost;
- supports in-memory TypeScript parsing, type-checking against the embedded
  declarations, transpilation, and source-location diagnostics;
- supports deterministic interruption of infinite loops and deep recursion;
- bounds or externally contains allocation growth;
- exposes no host capabilities unless Portless explicitly binds them;
- can remove dynamic code-loading primitives;
- resets state between calls;
- has license and maintenance characteristics acceptable for distribution;
- survives malformed scripts, thrown values, cyclic returns, worker crashes,
  cancellation, and concurrent load; and
- keeps the daemon healthy when the worker is killed forcibly.

The spike must attempt infinite loops, allocation bombs, catastrophic regular
expressions, enormous return values, deep recursion, syntax/type failures,
forged protocol frames, and repeated worker crashes. It must also prove that a
script cannot reach files, network APIs, environment variables, process APIs,
or Portless internals through the exposed global object.

Spawning `node`, running `tsx`/`ts-node`, executing project code, accepting npm
imports, or running the interpreter solely in-process are rejected approaches.
The subprocess protects daemon availability and resource cleanup. It should
not be described as a machine-level security sandbox unless the selected
runtime and operating-system containment actually justify that claim.

## Hot-path registry

The proxy must not query SQLite, compile source, or start a process for each
request. Add a transform registry owned by the daemon traffic subsystem:

- one immutable compiled snapshot per environment;
- atomic snapshot lookup in the no-match path;
- pre-indexing by exact `source -> target` edge;
- method/path matching and expiry checks in memory;
- prepared ordered hook handles with revision and source hash; and
- an explicit close path that cancels calls and stops workers.

Control-plane mutations validate and prepare a replacement snapshot before
committing visible state. After persistence succeeds, the environment snapshot
is swapped atomically before the API returns. Daemon reconciliation compiles
enabled, unexpired transforms before recovered traffic is advertised ready.

Invocation counters are aggregated in memory and flushed to SQLite in bounded
batches outside the request latency path. Shutdown performs one bounded flush.
Counters are diagnostic rather than an exactly-once event log; the API should
label them as observed counts if a crash can lose the most recent batch.

## Proxy pipeline

For an HTTP exchange the intended order is:

```text
receive request
  -> begin live exchange and capture original request prefix
  -> match recording, fault, and transform snapshots on original edge/method/path
  -> apply fault delay
  -> terminate on synthetic-status or abort fault
  -> resolve current provider
  -> enforce transform/provider compatibility
  -> enforce remote write policy on the immutable original method
  -> buffer body only when a matching request hook requires it
  -> execute and apply ordered request hooks
  -> inject/normalize Portless trace context and remove hop headers
  -> round trip to the current provider
  -> retain mock attribution and remove private mock headers
  -> buffer body only when a matching response hook requires it
  -> execute and apply ordered response hooks
  -> write the final response
  -> retain redacted original/effective phases and transform attribution
  -> publish exchange/trace updates and persist a matching recording
```

Fault matching remains first. A synthetic or aborted fault does not invoke a
transform because no real upstream exchange exists. A latency-only fault delays
the same transformed request. Mock responses may be transformed because they
are real responses in the normal edge path.

Trace propagation stays owned by Portless. A request transform sees the
incoming trace headers only according to sensitive-header rules, cannot patch
them, and runs before Portless injects the final upstream trace context.

Cancellation propagates through body reads, worker calls, upstream I/O, and
response writes. No transform goroutine or worker request may outlive the
exchange context without a bounded cleanup path.

## Remote-provider policy

Remote transforms require an explicit artifact policy because a provider can
handoff while an edge listener remains stable:

- `deny` is the default. An active transform blocks changing its target to a
  remote provider until it is disabled or updated.
- `response-only` allows response hooks for a remote provider but never runs a
  request hook against that provider.
- `request-response` permits both directions only when the remote binding is
  explicitly read-write.

Creating or enabling a transform against a current remote provider requires a
browser confirmation or CLI `--allow-remote` option that maps to the selected
remote mode. `request-response` is rejected for a read-only binding. A provider
handoff validates all enabled transforms before changing runtime state and
returns structured remediation naming the incompatible transforms. It never
silently skips an active transform or weakens the remote write policy.

Method, path, host, target, trace headers, and method-override headers remain
immutable for remote traffic. The daemon rechecks provider kind and write
policy at request time so a stale registry or concurrent handoff cannot bypass
the local policy.

## Failures and circuit breaking

Activation fails without changing state for compiler, type-contract, topology,
remote-policy, limit, or worker-preparation errors.

After activation, a matching request fails with a generic Portless-generated
`502 Bad Gateway` when a hook:

- times out or exhausts its budget;
- throws;
- returns an invalid or oversized patch;
- attempts a reserved mutation;
- encounters an unsupported, malformed, encoded, or oversized required body;
- loses its worker; or
- cannot be applied without violating HTTP semantics.

The synthetic response includes a stable non-sensitive error header such as
`X-Portless-Transform-Error: runtime-timeout`. It never includes source,
thrown values, headers, bodies, private addresses, or stack traces. Exchange
detail contains the transform name, revision, safe error code, and source
line/column when available.

Five consecutive runtime failures for one revision within 30 seconds trip that
revision. A tripped transform remains logically active but matching traffic
receives an immediate `502` without invoking the worker. This avoids both
silent pass-through and repeated resource exhaustion. The UI and CLI show
`tripped`; a successful update or an explicit disable/enable resets the
circuit. One `transform.state` event and one timeline event are emitted when
the circuit trips, not one event per failed request.

If an enabled transform cannot be recompiled after a daemon executable change,
reconciliation marks it unavailable and trips it rather than crashing the
daemon or silently applying no transform. The environment remains inspectable,
and remediation directs the developer to preview/update or disable the named
transform.

## Traffic capture and recordings

Transforms make a two-message exchange into four observable phases:

```text
caller request -> effective upstream request
upstream response -> effective caller response
```

The traffic contract must make those phases unambiguous. Preserve the current
meaning of the original request fields as received by Portless. Define the
ordinary response fields as the final response delivered to the caller. When a
transform changes a phase, add bounded effective/original fields rather than
silently replacing evidence:

- forwarded request status line metadata, headers, body prefix, byte counts,
  and truncation;
- upstream response status, headers, body prefix, byte counts, and truncation;
- ordered transform applications with name, revision, order, direction,
  duration, safe outcome, changed-header names, body/status change flags, and
  safe error code; and
- summary booleans/names sufficient for list rows and trace aggregation.

Do not duplicate an unchanged body merely because a header changed. Capture
helpers should retain the smallest bounded before/after evidence needed to
render an accurate diff. All four header phases pass through the same
credential redaction before entering the live traffic store.

The existing live traffic capture cap remains independent of the larger body
execution cap. A transform may process a 256 KiB body while live detail retains
only the configured 64 KiB diagnostic prefix with explicit truncation.

Recordings persist transform attribution even when payload capture is disabled.
When bounded payload capture is enabled, they persist the changed before/after
body evidence under the recording's own limit. Recording export increments its
schema version and includes transform name, revision, source hash, and script
API version, but not TypeScript source. A future reproduction bundle may add
source only through a separate reviewed export contract.

Trace summaries add `transformed` and `transformFailed` aggregate flags derived
from spans. Trace projection remains rebuildable from authoritative exchanges.

## Persistence

Add an environment-owned table along these lines, adapting names to the schema
conventions current at implementation time:

```sql
CREATE TABLE traffic_transforms (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  name TEXT NOT NULL COLLATE NOCASE,
  source TEXT NOT NULL,
  target TEXT NOT NULL,
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  order_index INTEGER NOT NULL DEFAULT 100,
  request_body_mode TEXT NOT NULL DEFAULT 'none',
  response_body_mode TEXT NOT NULL DEFAULT 'none',
  source_code BLOB NOT NULL,
  source_hash TEXT NOT NULL,
  script_api_version TEXT NOT NULL,
  max_body_bytes INTEGER NOT NULL,
  read_sensitive_headers INTEGER NOT NULL DEFAULT 0,
  remote_mode TEXT NOT NULL DEFAULT 'deny',
  enabled INTEGER NOT NULL DEFAULT 1,
  runtime_status TEXT NOT NULL DEFAULT 'ready',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  expires_at TEXT,
  match_count INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_match_at TEXT,
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_at TEXT,
  revision INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(environment_key, name)
);
```

Add an index supporting enabled edge lookup during reconciliation. Do not
persist compiled JavaScript, runtime bytecode, raw thrown messages, input or
output values, or private worker data.

Database methods own create, optimistic update, enable, disable, disable-all,
delete, list/detail, expiry transition, batched result counters, and affected-
edge disable. Tests cover case-insensitive names, revision conflicts, expiry,
foreign-key cleanup, topology changes, and the decision not to clone.

An expired rule must stop matching based on the in-memory timestamp even if no
client lists it. A bounded reconciliation path advances persisted state and
publishes one expiry state/timeline change. Do not introduce an unbounded
background goroutine in an HTTP handler.

## Daemon API contract

Change contract types first, then client, server, control plane, CLI/web, and
OpenAPI/events in that order. The API server only translates and authenticates;
it does not compile, match, or execute source itself.

Proposed environment routes are:

```text
GET    /environments/{project}/{environment}/transforms
POST   /environments/{project}/{environment}/transforms
POST   /environments/{project}/{environment}/transforms/preview
POST   /environments/{project}/{environment}/transforms/disable-all
GET    /environments/{project}/{environment}/transforms/{name}
PUT    /environments/{project}/{environment}/transforms/{name}
DELETE /environments/{project}/{environment}/transforms/{name}
POST   /environments/{project}/{environment}/transforms/{name}/enable
POST   /environments/{project}/{environment}/transforms/{name}/disable
```

Collection responses return summaries and never source. Item detail returns
source because the authenticated CLI and same-origin UI need to edit it.
Create validates, compiles, prepares, and enables a new transform with a
bounded expiry. Update requires `expectedRevision`, compiles first, and
preserves enabled/expiry state unless it includes a new activation expiry; CLI
`apply --replace` includes that expiry so replacement and reactivation are one
atomic mutation. A scope change that is incompatible with the current provider
requires explicit reactivation. Enable requires a new expiry no more than one
hour away. Delete rejects an enabled transform.

Preview accepts an unsaved candidate definition plus one retained HTTP
exchange sequence and an `includeActiveChain` boolean. It returns:

- `matched` and ordered candidate/chain metadata;
- compile diagnostics with stable code, severity, line, and column;
- bounded redacted original and transformed request/response phases;
- structured header/body/status differences;
- truncation or unsupported-sample warnings; and
- runtime duration and safe failure information.

Preview rejects TCP samples, missing/evicted exchanges, truncated input needed
by a body-aware hook, and credential access that the retained sample cannot
provide. It performs no upstream request and does not change counters, state,
events, timeline, or recordings.

Add typed contract values for:

- transform body and remote modes;
- summary and detail;
- create/update/enable inputs;
- list and disable-all responses;
- compiler diagnostics;
- preview input/result and message diffs;
- per-exchange transform applications; and
- trace transformation aggregates.

Use structured API errors and remediation for at least:

```text
INVALID_TRANSFORM_SCOPE
INVALID_TRANSFORM_MATCHER
INVALID_TRANSFORM_SOURCE
TRANSFORM_COMPILE_FAILED
TRANSFORM_REVISION_CONFLICT
TRANSFORM_NOT_FOUND
TRANSFORM_NOT_DISABLED
TRANSFORM_EXPIRED
TRANSFORM_REMOTE_FORBIDDEN
TRANSFORM_LIMIT_REACHED
TRANSFORM_SAMPLE_NOT_FOUND
TRANSFORM_SAMPLE_TRUNCATED
TRANSFORM_SAMPLE_UNSUPPORTED
TRANSFORM_RUNTIME_UNAVAILABLE
```

Increment the semantic daemon API version deliberately based on the contract
current at implementation time. Increment the recording export schema when
adding transform evidence. Because this is greenfield, update all consumers
and generated/test assets coherently; do not add an old route, alias, dual wire
shape, or compatibility facade.

## Events and timeline

Document and publish `transform.state` for create, update, enable, disable,
disable-all, expiry, trip, recovery, and deletion. State events contain only a
summary or a small deletion marker; they never contain source or compiler
output.

`traffic.exchange` summaries include transform names and success/failure
classification. `traffic.trace` includes aggregate transform flags through its
normal projection. There is no per-invocation transform event topic; traffic
already carries that information and a second high-volume stream would drift.

Timeline types are:

```text
transform.created
transform.updated
transform.enabled
transform.disabled
transforms.disabled_all
transform.expired
transform.tripped
transform.recovered
transform.deleted
```

Timeline details may include public scope, matcher, revision, order, expiry,
remote mode, body modes, actor, and safe failure code. They do not include
source, header values, bodies, thrown strings, stack traces, private keys, or
worker identifiers.

## Package ownership

The daemon traffic subsystem owns the feature. A likely layout is:

```text
portless-daemon/traffic/
  transforms/
    contract.go       internal compiler/runtime input and patch types
    compiler.go       source validation and compiler diagnostics
    engine.go         bounded worker-facing execution interface
    matcher.go        edge/method/path indexing and ordering
    registry.go       immutable per-environment active snapshots
    patch.go          HTTP-safe header/status/body patch application
    worker.go         process pool and framed protocol
    types.d.ts        embedded public TypeScript declarations
  proxy/
    manager.go        pipeline integration only
```

Exact filenames may change to keep cohesive packages, but do not place this in
a generic `internal`, `common`, `plugins`, or standalone API package. The
transform package may depend on model-level traffic values and injected
interfaces; it must not import the CLI, API server, relay, discovery, or web.

`portless-daemon/controlplane` owns use cases, actor attribution, topology and
remote-policy validation, database/registry coordination, timeline, and state
events. `portless-daemon/database` owns durable rows and counters.
`portless-daemon/api/contract` owns wire types, `api/client` owns every path,
and `api/server` adapts requests to injected control-plane methods.

The proxy should receive a narrow transform engine/registry dependency. It
must not learn API DTOs or worker protocol details. The CLI uses only the typed
daemon client and never constructs routes or imports implementation packages.

All new exported Go declarations under the product roots require meaningful
GoDoc beginning with the exact identifier. Run architecture tests after
adding packages or imports.

## Control-plane behavior

Add use cases equivalent to:

```text
ListTransforms
Transform
CreateTransform
UpdateTransform
PreviewTransform
EnableTransform
DisableTransform
DisableAllTransforms
DeleteTransform
```

Each mutation uses the existing per-project/environment lock so provider
handoffs, topology changes, expiry transitions, and transform registry swaps
cannot race into an invalid public state.

Validation includes:

- project/environment and artifact name;
- exact configured HTTP edge, including valid external ingress;
- method token and path glob;
- order, source, body, header, active-count, and chain limits;
- at least one supported hook and consistency with body modes;
- script API version and compilation;
- expiry from one second through one hour;
- sensitive-header acknowledgement;
- current provider and remote mode/write policy; and
- optimistic revision for updates.

Provider change planning must call the same compatibility validation and return
specific remediation before touching the running provider. Source/service
deletion atomically disables affected transforms and records what changed.

## Web implementation

Keep React/TypeScript and the existing CSS/theme architecture. Do not add a UI
framework. The first editor can be an accessible monospace textarea with line
numbers, server-returned diagnostics, focusable error links, and generated
starter source. A heavyweight editor dependency is follow-on work unless a
separate product-level review proves its value and bundle impact.

Create a focused feature boundary rather than expanding `ProjectPage.tsx`:

```text
portless-web/src/features/transforms/
  TransformsPanel.tsx
  TransformEditor.tsx
  TransformPreview.tsx
  TransformRow.tsx
  transformDraft.ts
  transformDiff.ts
```

The implementation should include:

- a URL-addressable, maximizable editor drawer;
- create-from-exchange in `TrafficDetail`;
- exact edge/method/path and duration controls outside the source editor;
- independent request/response body-mode controls;
- generated typed request/response skeletons;
- compile diagnostics linked to source lines;
- structured header changes and pretty JSON/text before/after views;
- a warning when retained preview data is truncated or redacted;
- active-chain order preview;
- explicit sensitive-header and remote confirmations;
- active, expiring, expired, tripped, and disabled states;
- pending-state disabling for mutually exclusive actions;
- structured shared error presentation;
- visible focus, keyboard operation, and accessible names; and
- an environment-level disable-all action.

Traffic rows and trace spans gain a transform intervention chip without hiding
fault or recording attribution. The existing intervention section gains
ordered transform applications and links to definitions. Active transform
state is reconciled from a snapshot plus `transform.state` events; reconnects
must not duplicate or resurrect stale revisions.

Use shared theme variables and the dense control-plane visual language. After
source changes, run web typecheck/unit/build, regenerate tracked
`portless-web/dist` through `make web` or `make`, build the full executable,
inspect daemon status, and restart the normal daemon with that exact checkout
before handing off a refreshable local UI change.

## MCP boundary

The first release adds no transform-specific MCP tools. Traffic summaries and
details may expose safe transform names, revisions, outcomes, and changed-field
metadata because those are part of traffic inspection. They do not expose
source or unredacted before/after values beyond the existing sensitive-traffic
capability.

Do not put transform mutation behind existing `--allow-traffic-control`; that
flag was designed for bounded typed recording/fault operations, not arbitrary
source execution. A future MCP design would require a separately named
immutable capability, bounded duration, host confirmation guidance, no source
readback by default, and explicit prompt-injection treatment.

## Delivery sequence

Keep the public feature hidden until the runtime, API, CLI, and disable path are
coherent. Suggested implementation slices are:

### Slice 0: runtime and threat-model gate

1. Build the disposable worker/compiler/runtime harness.
2. Exercise interruption, allocation, protocol, capability, and portability
   cases.
3. Benchmark representative header/text/JSON hooks and the no-op IPC path.
4. Record the dependency, license, containment claims, limits, and rejected
   alternatives in an ADR.
5. Stop if deterministic interruption or bounded recovery cannot be proven.

Ship criterion: the daemon remains healthy and resource-bounded under every
malicious-script fixture, and supported release targets build without an
external runtime.

### Slice 1: artifact model and compile/preview core

1. Add model values and database table/CRUD/revision/expiry tests.
2. Add embedded `types.d.ts`, source validation, compilation, and worker
   lifecycle behind Go interfaces.
3. Add header/body patch validation independent of `http.ResponseWriter`.
4. Add immutable registry snapshots and reconciliation without proxy use.
5. Add control-plane create/update/preview/enable/disable/delete methods and
   timeline/state events.

Ship criterion: a candidate can compile and preview against a synthetic
bounded message, persisted enabled state reconciles after daemon restart, and
no application traffic is changed yet.

### Slice 2: API and typed client

1. Add contract DTOs and semantic API version change.
2. Add typed client methods before CLI callers.
3. Add authenticated server routing and structured errors.
4. Update OpenAPI and events documentation in the same change.
5. Add server contract tests for source omission, diagnostics, revisions,
   expiry, CSRF/same-origin browser mutations, limits, and disable-all.

Ship criterion: the API is complete, typed, documented, bounded, and has no raw
transport/path construction outside the client.

### Slice 3: local header transforms

1. Integrate matching snapshots into the HTTP proxy.
2. Implement ordered request/response header patches with reserved-header
   enforcement.
3. Add runtime failures, trip behavior, cancellation, worker restart, and
   result aggregation.
4. Add transform attribution to traffic exchanges/traces/events.
5. Verify faults, mocks, trace injection, recordings, and provider handoffs
   retain their established semantics.

Ship criterion: bounded local/container/mock header transforms work on explicit
edges with a reliable disable path and no measurable material regression when
no transform is active.

### Slice 4: bounded text and JSON bodies

1. Add declared body modes and bounded request/response buffering.
2. Add JSON parse/serialize mapping and content-type checks.
3. Implement framing, validators, no-body status, encoding, cancellation, and
   upstream cleanup rules.
4. Capture redacted original/effective phases and update recording export
   schema.
5. Add traffic detail diff DTOs and unit/contract tests.

Ship criterion: a route can reshape request and response JSON without partial
forwarding, stale framing, hidden truncation, or ambiguous retained evidence.

### Slice 5: CLI and browser workflow

1. Add the top-level transform command tree in `portless-cli/traffic` with
   human/JSON rendering, stdin/file input, revision conflicts, completion, and
   destructive confirmation.
2. Add the Transforms view, editor, preview/diff, create-from-exchange action,
   active indicator, and intervention detail.
3. Add Vitest coverage for draft generation, diffing, diagnostics, state/event
   reconciliation, confirmations, pending controls, and accessibility.
4. Regenerate embedded web assets only through the supported build.

Ship criterion: a developer can complete the primary workflow from a captured
exchange and can immediately see and disable the active blast radius.

### Slice 6: remote policy and provider handoff

1. Add remote mode validation and confirmations.
2. Reject incompatible create/enable and provider handoff operations before
   runtime changes.
3. Recheck provider/write policy in the proxy hot path.
4. Cover response-only and explicit read-write request/response behavior.
5. Verify no transform bypasses a read-only remote policy.

Ship criterion: remote behavior is explicit, locally enforced, auditable, and
cannot silently skip or broaden an active transform.

### Slice 7: complete validation and documentation

1. Add isolated CLI and Playwright E2E journeys.
2. Add performance, worker crash/restart, and security regression suites.
3. Update README, CLI reference, daemon README, implementation status, local
   data/safety documentation, and relevant future-feature links.
4. Run formatting, architecture, focused, full non-destructive, and diff
   checks.
5. Build and restart the developer daemon normally before UI handoff.

Ship criterion: all release gates below pass, documentation states exact
limits and sensitive-data behavior, and no destructive relay suite was needed.

## Verification plan

### Compiler and runtime unit tests

- Valid request-only, response-only, and combined modules compile.
- Missing hooks, imports, async hooks, dynamic code loading, invalid syntax,
  invalid contract types, and oversized source fail with line/column diagnostics.
- Input objects are frozen and global mutations do not survive an invocation.
- Infinite loops, deep recursion, catastrophic regular expressions, allocation
  pressure, huge/cyclic returns, thrown primitives, and worker termination stay
  within bounds and do not crash the daemon.
- Files, network, environment, process, timer, console, crypto, and host-private
  values are absent.
- Cancellation and deadline paths leave no queued or orphan calls.
- Concurrent calls cannot mix source hashes, revisions, frames, or results.

### Matcher, patch, and registry tests

- Source/target identity is exact and caller-aware.
- External ingress and configured HTTP connections validate; TCP edges reject.
- Method comparison is case-insensitive after canonicalization and path matching
  uses normalized path without query values.
- Ordering is stable across restarts and equal-order names.
- Chain and active-environment limits fail before mutation.
- Registry swaps are atomic and in-flight requests retain their revision.
- Expired transforms stop matching without a list request.
- Header set/append/remove preserves repeated values and casing semantics.
- Reserved, hop, trace, host, framing, and method-override headers reject.
- Sensitive input is redacted by default and available only with capability.
- Patch validation is all-or-nothing.

### Body and HTTP tests

- Empty, text, JSON, structured `+json`, malformed JSON, UTF-8 replacement,
  known length, chunked, exact-limit, over-limit, and canceled bodies behave as
  documented.
- Request failures make no upstream request.
- Response failures send no original response headers before the synthetic
  `502`.
- `Content-Length`, transfer framing, digest, entity tag, content type, HEAD,
  `204`, and `304` semantics remain valid.
- Encoded bodies reject clearly; header-only transforms preserve streaming.
- Upstream bodies and idle connections are cleaned up after success/failure.

### Persistence and control-plane tests

- CRUD, case-insensitive names, revisions, enable expiry, disable-all, trip,
  recovery, counters, and foreign-key deletion persist.
- Environment clone does not copy transforms.
- Removing a service/connection disables affected definitions without deleting
  them.
- Provider plans reject incompatible remote transforms before handoff.
- Reconciliation restores valid enabled rules and trips invalid ones visibly.
- Timeline/events omit source and runtime data values.

### Proxy integration tests

- Request and response header/body transforms reach the correct directed edge.
- Two callers to one target can have different transforms.
- Ordered chains see previous output and record exact revisions.
- Latency faults compose; synthetic/abort faults bypass transforms.
- Mock responses can be transformed with mock attribution retained.
- Trace context remains coherent and cannot be patched.
- Recordings retain transform attribution and bounded changed evidence.
- Worker crash, timeout, trip, update recovery, disable, and expiry are visible.
- A remote read-only target never receives a transformed request.

### API and CLI tests

- Every route has method, auth, same-origin/CSRF, not-found, validation, limit,
  conflict, and stable structured-error coverage.
- Lists and events omit source; authenticated item detail returns it.
- Preview is side-effect-free and rejects evicted/truncated/unsupported samples.
- CLI parents show useful help and do not make accidental API calls.
- Human output is concise; `--json` is valid and complete.
- File/stdin source, `--replace`, durations, remote confirmation, completion,
  disable-all, and deletion confirmation work.

### Web unit and browser tests

- Create-from-exchange pre-fills the exact edge/method/path.
- Generated source matches selected body modes.
- Diagnostics, active-chain ordering, structured diffs, truncation, redaction,
  and runtime failures render accessibly in light and dark themes.
- Pending actions disable incompatible controls and errors use the shared
  structured presentation.
- Keyboard users can open, edit, preview, enable, disable, maximize, and close
  the drawer with visible focus.
- Snapshot/event reconnect retains newest revisions without duplicates.
- Active indicators, topology/traffic chips, intervention detail, expiry,
  trip, and disable-all remain accurate.

### Isolated E2E journeys

Extend the normal isolated fixtures after reading `docs/e2e-testing.md`:

1. Start the store fixture and capture a checkout-to-inventory JSON exchange.
2. Create a response transform from that exchange.
3. Preview a changed field and header, enable it, repeat the request, and verify
   the caller received the transformed response.
4. Verify traffic detail shows upstream and delivered response differences,
   transform name/revision, and redacted credential headers.
5. Disable it and prove the next request is unmodified.
6. Exercise a thrown hook and assert visible `502`, traffic attribution, trip,
   update recovery, and one-click disable.
7. Verify a second source-to-same-target edge is unaffected.
8. Bind the target to read-only remote in the existing isolated remote fixture
   and prove incompatible request transformation is rejected locally.

No relay-destructive suite, reset, uninstall, or machine networking mutation is
required for this feature.

### Performance and resource tests

The runtime spike establishes a checked-in benchmark baseline. Final gates
cover:

- inactive registry lookup versus the current proxy path;
- header-only request and response hooks;
- 1 KiB, 64 KiB, 256 KiB, and maximum JSON bodies;
- one and eight-transform chains;
- concurrent traffic at representative local-development rates;
- queue saturation and deadline behavior; and
- worker RSS, restart frequency, goroutine/fd stability, and shutdown cleanup.

The inactive path should add only an atomic/index lookup and must not perform
SQLite, allocation-heavy compilation, IPC, or body buffering. Set final
latency/throughput budgets from the phase-zero measurements and record them in
the ADR rather than weakening limits after implementation.

## Documentation changes at implementation time

Update these in the same coherent change:

- `README.md`: observe/experiment workflow and local-data safety;
- `portless-cli/COMMANDS.md`: complete transform command hierarchy and examples;
- `portless-cli/README.md`: command ownership if needed;
- `portless-daemon/README.md`: worker lifecycle, limits, and troubleshooting;
- `portless-daemon/api/openapi.yaml`: routes and schemas;
- `portless-daemon/api/events.md`: transform state and traffic attribution;
- `docs/e2e-testing.md`: CLI/UI journey inventory;
- `docs/implementation-status.md`: implemented boundary and deferred protocols;
- `docs/plans/future-features.md`: link or reprioritize only when the feature is
  actually scheduled; and
- the runtime-selection ADR.

Document that TypeScript source and body data remain local but can contain
secrets, that transform code is explicitly enabled user code rather than
discovery, and that the containment boundary is capability-limited rather than
claiming stronger OS sandboxing than the implementation provides.

## Rejected alternatives

### Run project Node.js

Rejected because it violates the single executable and zero-runtime-entry
properties, makes behavior depend on project dependencies, and executes code
inside a workspace Portless otherwise discovers statically and read-only.

### Put matching inside TypeScript

Rejected because code could silently expand its blast radius and the UI/CLI
could not explain scope without executing it. Edge/method/path selection stays
typed and declarative.

### Use only a declarative rewrite DSL

Rejected as the primary feature because schema adaptation quickly becomes an
awkward programming language. Declarative matching, activation, limits, and
header patch representation still constrain the code boundary. Simple no-code
header controls may be added later as another authoring surface over the same
artifact.

### Execute JavaScript in the daemon process

Rejected as the sole containment mechanism because an infinite loop, allocator
failure, runtime bug, or interpreter panic would share the traffic/control
plane's availability boundary.

### Silently pass through on errors

Rejected because a developer could believe a compatibility or test transform
ran when the upstream actually received original traffic. Matching failures
are explicit `502` outcomes with an immediate disable path.

### Store only transformed traffic

Rejected because it would be impossible to tell what the caller sent, what the
upstream received, what the upstream returned, and what Portless delivered.
Bounded changed-phase evidence is required.

### Allow routing or method changes initially

Rejected because those changes interact with source-aware ownership, trace
matching, remote write policy, mocks, and provider handoff. They can be
designed later as routing/intercept behavior instead of leaking into body
conversion.

## Non-goals for the first release

- TCP, database-protocol, gRPC, WebSocket, SSE, or arbitrary streaming transforms.
- Binary body mutation or transparent compression rewrite.
- Imports, npm packages, filesystem modules, network calls, or stateful scripts.
- Request method, target, host, scheme, path, or query rewrites.
- Synthetic responses, delays, aborts, routing, shadowing, or provider selection.
- Indefinite activation or activation longer than one hour.
- Shared/team transform registries, hosted execution, or automatic source sync.
- Automatic transform inclusion in project export, environment clone,
  recordings, mocks, or reproduction bundles.
- Transform-specific MCP tools.
- Claiming hostile multi-tenant isolation on a local single-user daemon.
- A heavyweight browser IDE in the initial slice.

## Release gates

The feature is ready only when all of these are true:

- A captured exchange can create, preview, enable, attribute, expire, disable,
  update, and delete a named edge-scoped transform.
- Request and response headers plus bounded text/JSON bodies transform through
  local, container, and mock providers with correct HTTP framing.
- Remote modes and read-only enforcement cannot be bypassed through code,
  stale state, or provider handoff.
- The daemon remains healthy under timeout, allocation, malformed source,
  worker crash, protocol corruption, and queue saturation tests.
- No source, secret header, body, private address, ownership key, or thrown data
  leaks through summaries, events, timeline, errors, logs, or MCP.
- Traffic and recordings distinguish original/effective phases and identify
  exact transform revisions.
- The inactive proxy path does no compilation, IPC, SQLite work, or body
  buffering and meets the benchmark budget recorded in the ADR.
- CLI human/JSON output, browser keyboard/accessibility behavior, and
  disable-all are covered.
- API/OpenAPI/events/export versions and every consumer move together without
  compatibility aliases.
- `gofmt` and web formatting expectations are satisfied.
- `go test ./tests/architecture`, focused daemon/CLI/web tests, `make test`,
  ordinary isolated E2E suites, and `git diff --check` pass.
- Tracked web assets are regenerated, the full executable is built, daemon
  status is inspected, and a normal daemon restart succeeds before UI handoff.

## Follow-on possibilities

After the first release is proven, evaluate these separately rather than
expanding the initial contract implicitly:

- simple form-authored header/query rewrite presets over the same artifact;
- exact query matching and explicitly safe query rewriting;
- gzip/brotli decode, transform, and re-encode;
- WebSocket message transforms with separate stream limits;
- reusable transform export/import with source review and signatures;
- scenario-runner activation of reviewed named transforms;
- reproduction-bundle inclusion with explicit secret/source inspection;
- an editor with richer TypeScript language services;
- a separate, immutable MCP code-transform capability; and
- route or method rewrites designed together with remote intercept/shadow
  traffic rather than added as an unscoped patch.
