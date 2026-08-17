# Server-Sent Events

Environment live events are exposed at:

```text
GET /api/v1/environments/{projectName}/{environmentName}/stream?topic=service.state&topic=traffic.http
```

The endpoint uses the browser session cookie or CLI bearer token. Topic filters are optional. Each message has a daemon-local SSE `id`, a typed `event`, and a JSON domain payload:

```text
id: 4813
event: traffic.http
data: {"project":"billing","environment":"local","sequence":307,"source":"checkout","target":"orders","method":"GET","path":"/orders","status":200,"durationMs":18}
```

Current topics:

- `environment.state`
- `service.state`
- `operation.state`
- `recording.state`
- `fault.state`
- `traffic.http`
- `traffic.tcp`
- `traffic.tcp.activity`

`traffic.tcp.activity` is an ephemeral live signal for open TCP connections. It
reports `open`, `data`, `heartbeat`, and `close` phases with the current active
connection count and byte deltas. This lets the topology animate long-lived
Postgres and Redis connections before their final `traffic.tcp` event exists.
It is not retained in traffic snapshots or recordings.

After a daemon handoff, snapshot responses can temporarily report environment
and service state as `recovering`. The replacement daemon does not emit a
synthetic replay for state changes that occurred while it was absent; clients
must reload snapshots after reconnecting. A completed reconciliation emits the
normal `environment.state`/`service.state` updates for subsequent changes, and
the durable timeline includes an `environment.reconciled` entry.

The broker is intentionally bounded and nonblocking. A slow UI is never allowed to stall proxied application traffic. Subscriptions are isolated by project and environment. Snapshot endpoints are authoritative after a reconnect; the UI reloads environment, timeline, recording, and fault state when it observes a lifecycle event.

Traffic snapshots use the unified endpoint:

```text
GET /api/v1/environments/{projectName}/{environmentName}/traffic?protocol=http&after=307&limit=250
GET /api/v1/environments/{projectName}/{environmentName}/traffic?protocol=tcp&edge=checkout:postgres
GET /api/v1/environments/{projectName}/{environmentName}/traffic/308
```

`protocol=tcp` includes raw TCP and protocol-aware Postgres and Redis events. The detail response includes captured request and response headers for HTTP traffic; credential-bearing headers are redacted before an event enters the broker or a recording.
