# ADR 0007: HTTP mocks are environment-scoped service providers

Status: accepted

A mock replaces one logical HTTP service in one environment. It is therefore a
fourth provider kind alongside local process, managed container, and remote
HTTP(S), rather than a separate test server outside the project model. A mock
profile belongs to exactly one service and contains named routes. Environment
cloning copies profiles and routes with the rest of the environment
configuration.

Binding a profile uses the existing service-scoped provider handoff. Portless
stops only the selected local service, starts an unprivileged private listener
on `127.0.0.1:0`, and retargets the existing source-aware edge proxies. Callers
keep their injected service URL; peer processes, debugger sessions, endpoints,
and generations do not change. Mock requests therefore pass through the same
traffic, trace, recording, and fault path as a real local or remote target.
Each captured exchange records the target provider, profile, and matched route.

Matching is deterministic. An enabled route matches an HTTP method, an exact or
parameterized path, and optional required query values. More specific paths and
query sets win; definitions that remain ambiguous at the same specificity are
rejected before they can serve traffic. A route returns fixed status, headers,
body, and delay. An unmatched request returns `501` so a missing scenario is
visible rather than accidentally accepted.

Profiles can start empty, derive exact request/response examples from a stopped
recording, or import a local OpenAPI 3.0/3.1 JSON or YAML document. OpenAPI
import resolves only references contained in that document and never performs
a network fetch. Recording bodies are metadata-only by default and require an
explicit, bounded capture option because application payloads can contain
sensitive data.

Matcher preview accepts repeated request headers and a bounded request body so
the CLI and control plane can describe the complete request under test. Those
values are validated in memory, then discarded. They do not create traffic or
timeline history and do not become implicit match criteria.

The first implementation does not execute scripts, maintain scenario state,
match request bodies, render templates, proxy unmatched requests, or emulate
WebSocket, gRPC, raw TCP, or binary protocols. Those behaviors would change the
security and determinism boundary and require separate decisions.
