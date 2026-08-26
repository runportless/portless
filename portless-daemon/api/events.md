# Server-Sent Events

Environment live events are exposed at:

```text
GET /api/v1/environments/{projectName}/{environmentName}/stream?topic=service.state&topic=traffic.exchange&topic=traffic.trace&topic=traffic.cleared
```

The endpoint uses the browser session cookie or CLI bearer token. Topic filters
are optional. Each message has a daemon-local SSE `id`, a typed `event`, and a
JSON domain payload:

```text
id: 4813
event: traffic.exchange
data: {"project":"billing","environment":"local","sequence":307,"protocol":"http","source":"checkout","target":"orders","background":false,"method":"GET","requestTarget":"/orders?limit=10","status":200,"durationMs":18}
```

Current topics:

- `environment.state`
- `service.state`
- `operation.state`
- `recording.state`
- `fault.state`
- `mock.state`
- `traffic.exchange`
- `traffic.trace`
- `traffic.cleared`
- `traffic.tcp.activity`

`mock.state` carries the current profile after a profile or route change, or a
small `{name, deleted}` tombstone after deletion. Clients should reload the
mock collection after receiving it because active profiles are recompiled and
swapped atomically.

`traffic.exchange` carries a completed HTTP request, decoded TCP operation, or
opaque TCP session summary. HTTP headers and bodies and decoded TCP message
fields/content are omitted from this notification; clients load the full
exchange on demand. The TCP summary retains its declared application protocol,
operation, inspection state, outcome, and message counts. `traffic.trace`
carries an updated trace summary whenever a newly completed exchange changes
that projection. Exchange summaries explicitly classify successful browser and
connection housekeeping as `background`; the raw exchange remains available.
Trace summaries identify the root `protocol`; a TCP-rooted
summary is `provisional` while an active HTTP request can still absorb it during
correlation. `traffic.cleared` reports the environment-local sequence through
which live exchanges and derived traces were removed. Durable recordings remain
available.

`traffic.tcp.activity` is an ephemeral live signal for open TCP connections. It
reports `open`, `data`, `heartbeat`, and `close` phases with the current active
connection count, byte deltas, and optional declared application protocol. This
preserves live connection counts and byte activity without waiting for socket
close. For declared application protocols, topology activity is driven by
completed logical exchanges instead of socket heartbeats, so an idle pool does
not appear to be application traffic. Undeclared TCP edges still use non-zero
byte activity. The signal is not retained in traffic snapshots or recordings.

After a daemon handoff, snapshot responses can temporarily report environment
and service state as `recovering`. The replacement daemon does not emit a
synthetic replay for state changes that occurred while it was absent; clients
must reload snapshots after reconnecting. A completed reconciliation emits the
normal `environment.state`/`service.state` updates for subsequent changes, and
the durable timeline includes an `environment.reconciled` entry.

The broker is bounded and nonblocking. A slow UI cannot stall proxied
application traffic. Subscriptions are isolated by project and environment.
Snapshot endpoints are authoritative after a reconnect. Traffic clients merge
snapshots and stream notifications by environment-local exchange sequence or
trace number.

Traffic snapshots use separate exchange and trace resources:

```text
GET /api/v1/environments/{projectName}/{environmentName}/traffic/exchanges?protocol=all&after=307&limit=250
GET /api/v1/environments/{projectName}/{environmentName}/traffic/exchanges?protocol=tcp&edge=checkout:postgres
GET /api/v1/environments/{projectName}/{environmentName}/traffic/exchanges/308
GET /api/v1/environments/{projectName}/{environmentName}/traffic/traces?edge=checkout:orders
GET /api/v1/environments/{projectName}/{environmentName}/traffic/traces/307
DELETE /api/v1/environments/{projectName}/{environmentName}/traffic
```

Exchange detail preserves the exact request target, repeated non-sensitive
header values, a bounded prefix of inspectable HTTP bodies, and bounded decoded
messages for declared PostgreSQL, Redis/Valkey, MySQL, and NATS edges. Decoded
TCP operations are completed at application-protocol boundaries rather than
socket close, which keeps pooled connections useful in traces. Unknown,
encrypted, unsupported, malformed, or capacity-limited traffic falls back to a
session exchange with wire-byte totals and an inspection reason.
Successful PostgreSQL driver/pool validation and session setup plus Redis
handshake, client metadata, authentication, database selection, and validation
commands are retained as background exchanges. Their standalone traces are
excluded by default but available with `background=include`; failures and
faulted operations remain foreground.

Known credential-bearing HTTP headers and protocol-authentication fields are replaced
with `[REDACTED]` before retention. Application payload values otherwise remain
visible for local debugging.
HTTP trace identifiers are normalized to lower-hex W3C widths after extraction
from W3C/OpenTelemetry, B3 single or multi-header, or Datadog propagation.
Exchange detail identifies whether that context was generated, recognized as a
context Portless previously injected, or continued from an application-supplied
format. Portless-injected propagation carriers are internal correlation
metadata and are omitted from captured `requestHeaders`.
Trace summaries omit spans; trace detail returns the complete current tree and
waterfall projection.
