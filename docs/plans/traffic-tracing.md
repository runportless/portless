# Traffic tracing plan

Status: implemented in the API 8 traffic contract. Passive trace projection,
trace-first UI, raw exchanges, bounded pause buffering, and CLI trace inspection
are now part of the product. Framework-specific propagation remains follow-on work.

## Goal

Replace the current protocol-separated list of completed exchanges with a trace-first view that explains one user action across application services and managed resources. For the store example, opening `/checkout` should read as one trace containing:

```text
GET /checkout                         external -> checkout
|- GET /inventory/coffee-mug          checkout -> inventory
`- GET /orders                        checkout -> orders
   |- TCP session                     orders -> redis
   `- TCP session                     orders -> postgres
```

The raw exchange list remains available for debugging and as the source of truth.

## Product behavior

- Make **Traces** the default traffic mode and retain **Exchanges** as a raw mode.
- Combine HTTP and TCP exchanges in one trace instead of requiring a protocol toggle.
- Order spans by start time and show their overlap and duration in a compact waterfall.
- Treat browser-generated traffic such as `/favicon.ico` as separate background activity. Collapse it by default; do not silently discard it.
- Let selecting any span open the full existing request, response, fault, provider, and recording details.
- Label correlation as `exact`, `inferred`, `partial`, or `ambiguous`. The UI must not present timing inference as certainty.
- Pausing the live view buffers new exchanges and applies them on resume; it must not lose them merely because rendering is paused.
- Clearing removes the live exchanges and derived traces for one environment,
  preserves monotonic sequence allocation, and never deletes durable recordings.

## Capture semantics

Traffic is developer-owned local diagnostic data, but known credential-bearing
headers are replaced with `[REDACTED]` before an exchange enters live retention
or a recording. Other request and response values remain developer-visible.

- Preserve the exact HTTP request target, including the escaped path and raw query string, for display and export.
- Keep `Path` as the decoded or normalized pathname used by fault matching; do not make fault rules depend on query values.
- Preserve repeated request and response header values rather than joining them
  irreversibly. Replace authorization, cookies, and common API-key/token header
  values before retaining the exchange.
- Preserve captured request and response bodies verbatim up to a documented memory limit.
- Use explicit truncation flags and captured/observed byte counts when a body exceeds the limit.
- Display `HEADERS`; sensitive header values already arrive as `[REDACTED]` and
  cannot be revealed by API clients.
- Document that bodies and non-sensitive headers can still contain local
  application data even though known credential headers are redacted.

Size limits are resource protection, not a content-scrubbing policy. This decision applies to traffic capture; it does not remove secret handling from unrelated daemon logs, process arguments, or persisted configuration.

## Data model

Replace the overloaded event shape with an exchange model and trace projections. Exact names can be settled with the package refactor, but the required information is:

### Traffic exchange

- Existing environment, sequence, protocol, source, target, provider, timing, status, byte, fault, recording, and error fields.
- `requestTarget`: exact escaped path plus raw query.
- `requestKind`: navigation, subresource, fetch/XHR, service, or unknown, derived from `Sec-Fetch-*` and other bounded request metadata.
- Complete multi-value request and response headers.
- Body contents, observed byte counts, captured byte counts, and truncation flags.
- Optional normalized lower-hex `traceId`, `spanId`, and `parentSpanId` derived
  from W3C/OpenTelemetry, B3 single or multi-header, or Datadog context. B3
  64-bit and Datadog trace identifiers are left-padded to the API's 128-bit
  representation; Datadog's `_dd.p.tid` supplies the high 64 bits when present.

### Traffic trace

- A stable trace key.
- Root exchange or an explicit orphan/ambiguous grouping.
- Start, completion, duration, aggregate status, and span count.
- Correlation quality: exact, inferred, partial, or ambiguous.
- A flat span list with parent identity and start offset so API clients can render either a tree or waterfall.

The raw exchange remains authoritative. A trace is a projection and can be rebuilt as grouping logic improves.

## Correlation strategy

Use exact propagated trace context when it exists. W3C context takes precedence
when several valid formats are present; B3 single-header takes precedence over
B3 multi-header, followed by Datadog. Portless synchronizes valid alternate
carriers already present on the request and always emits a W3C bridge. Otherwise
apply conservative timing and topology inference:

1. An HTTP exchange sourced from `external` is a root candidate.
2. A child is eligible only when its source service equals the candidate parent's target service.
3. The child must start while the parent is active.
4. Attach the child only when exactly one active parent is eligible.
5. Apply the same rule recursively to HTTP and TCP descendants.
6. Leave exchanges ungrouped when multiple parents are plausible; do not guess.
7. Mark all timing-derived relationships as inferred.
8. Mark a trace partial when retention, daemon restart, late arrival, or missing edges prevent a complete tree.

Sequence numbers are assigned when exchanges complete, so they must not determine nesting or display order. Start timestamps determine the waterfall; sequence remains an exchange identity.

Transparent proxies alone cannot reliably establish causality when requests overlap. Exact cross-service correlation requires applications or framework integrations to propagate trace context. Follow-on framework work may add Node/NestJS/Express hooks, Spring instrumentation, and Go/OpenTelemetry integration, but it is not a prerequisite for the conservative initial trace view.

## Ownership and package boundary

Create the focused `portless-daemon/traffic` subsystem inside the daemon product. It should own:

- bounded live exchange retention and sequence allocation;
- exact-context parsing and conservative correlation;
- trace projection and summaries;
- query/filter behavior shared by API clients;
- recording integration at the traffic boundary.

The generic event broker should publish notifications, not own traffic storage or trace assembly. The proxy should capture exchanges and submit them to `traffic`; the API should only translate traffic queries and stream updates.

## API and streaming

- Add a trace collection endpoint under the environment traffic resource, for example `GET /api/v1/environments/{project}/{environment}/traffic/traces`.
- Let the raw traffic endpoint return all protocols with `protocol=all` while retaining protocol filtering.
- Return trace summaries first and load full span/detail data on demand where that materially reduces payload size.
- Publish a protocol-neutral completed-exchange event such as `traffic.exchange`.
- Publish trace changes through a trace-oriented event such as `traffic.trace` so the UI can update an existing trace when a later child completes.
- Keep topology-oriented TCP activity separate from completed TCP exchanges.
- Define one deterministic snapshot-plus-stream merge rule so reconnects do not duplicate or omit exchanges.
- Clear the live window through the environment traffic resource and publish the
  cleared sequence high-water mark so concurrent clients retain newer traffic.

Because this is version 1, the implementation may replace the current traffic response and event contracts cleanly rather than maintaining a compatibility layer.

## Web implementation

Extract traffic UI code from `ProjectPage.tsx` into a feature boundary:

```text
portless-web/src/features/traffic/
  TrafficPanel.tsx
  TraceList.tsx
  TraceRow.tsx
  TraceWaterfall.tsx
  ExchangeTable.tsx
  TrafficDetail.tsx
  trafficState.ts
```

`trafficState.ts` should own snapshot/stream merging, deduplication, bounded buffering while paused, filtering, and selected-span reconciliation. Rendering components should receive already-normalized traces or exchanges.

## Delivery sequence

1. Move traffic ownership out of the generic event broker after the package refactor establishes final API/daemon boundaries.
2. Introduce the exchange model, exact request-target capture, repeated headers,
   credential-header redaction, and updated recordings/exports.
3. Implement exact trace-context parsing and conservative trace assembly with explicit correlation quality.
4. Add trace/raw API queries and protocol-neutral streaming with deterministic reconnect behavior.
5. Extract the web traffic feature and ship Traces, Exchanges, the waterfall, background grouping, details, and pause buffering.
6. Add optional framework-specific trace propagation only after the passive/exact-when-present model is proven.

## Verification

### Unit tests

- Exact request target retains escaped path and raw query.
- Repeated non-sensitive headers are returned losslessly and credential-bearing
  values are redacted before retention.
- Bodies stop only at the size cap and report observed/captured bytes and truncation accurately.
- Existing W3C/OpenTelemetry, B3, and Datadog trace context retains one
  normalized trace identity; directly propagated parent spans produce exact
  parentage.
- Store-style HTTP and TCP timing produces the expected inferred tree.
- Overlapping requests with more than one eligible parent remain ambiguous.
- Completion order does not change nesting or waterfall order.
- Evicted, late, and restart-separated exchanges produce partial traces.
- Pause buffering, snapshot/stream deduplication, and reconnect merging are deterministic.

### API and UI tests

- Trace summaries, full spans, raw all-protocol results, filters, and detail lookup have stable shapes.
- Credential-bearing headers remain redacted in detail responses and recordings;
  repeated safe headers, query values, and bounded bodies remain inspectable.
- Incoming exchanges update an existing trace without duplicating it.
- Traces/Exchanges switching, expansion, filtering, pagination, background
  grouping, span detail, pause/resume, clear, empty state, and narrow layouts work.
- The detail panel says `HEADERS`, never `HEADERS · REDACTED`.

### End-to-end test

Run the store fixture, open `/`, then `/checkout`, and assert:

- `/` and `/checkout` are distinct root traces;
- `/checkout` contains checkout-to-inventory and checkout-to-orders HTTP spans;
- Redis and PostgreSQL TCP sessions appear beneath the orders span when timing is unambiguous;
- favicon traffic is retained as collapsed background activity;
- raw mode contains every underlying exchange;
- an injected authorization header is redacted while the query value and a
  non-sensitive repeated header remain available in exchange detail.

## Non-goals for the first tracing slice

- Claiming exact causality where no trace context exists.
- Parsing database, Redis/Valkey, or other TCP application protocols.
- Capturing arbitrary TCP payload contents.
- Replacing a full OpenTelemetry backend or collector.
- Framework-specific auto-instrumentation in the initial passive correlation release.
