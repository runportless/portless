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

The broker is intentionally bounded and nonblocking. A slow UI is never allowed to stall proxied application traffic. Subscriptions are isolated by project and environment. Snapshot endpoints are authoritative after a reconnect; the UI reloads environment, timeline, recording, and fault state when it observes a lifecycle event.
