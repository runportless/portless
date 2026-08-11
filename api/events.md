# Server-Sent Events

Project live events are exposed at:

```text
GET /api/v1/projects/{projectName}/stream?topic=service.state&topic=traffic.http
```

The endpoint uses the browser session cookie or CLI bearer token. Topic filters are optional. Each message has a daemon-local SSE `id`, a typed `event`, and a JSON domain payload:

```text
id: 4813
event: traffic.http
data: {"project":"billing","sequence":307,"source":"checkout","target":"orders","method":"GET","path":"/orders","status":200,"durationMs":18}
```

Current topics:

- `project.state`
- `service.state`
- `operation.state`
- `recording.state`
- `fault.state`
- `traffic.http`
- `traffic.tcp`

The broker is intentionally bounded and nonblocking. A slow UI is never allowed to stall proxied application traffic. Snapshot endpoints are authoritative after a reconnect; the UI reloads project, timeline, recording, and fault state when it observes any lifecycle event.
