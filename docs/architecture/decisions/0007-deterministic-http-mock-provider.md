# ADR 0007: HTTP mock scenarios coordinate service-scoped providers

Status: accepted

A mock scenario belongs to one environment and groups named routes across
logical HTTP services. The scenario owns its name and description, but no
single service; each route requires a target service. At runtime, mock remains
a fourth provider kind alongside local process, managed container, and remote
HTTP(S), rather than a separate test server outside the project model.
Environment cloning copies scenarios, routes, bindings, and private restoration
records independently with the rest of the environment configuration.

Enabling a scenario validates every target and reserves its service set before
changing providers. That set includes disabled routes, so toggling a route
never silently releases a service. Only disjoint scenarios can be enabled
together. The daemon saves exact previous bindings, including remote write
policy and health configuration, in private durable restoration records before
the first transition. Disabling restores those bindings instead of guessing a
default local provider. Individual provider changes cannot bypass scenario
ownership.

Activation is serialized with route edits and other environment mutations,
and uses the existing service-scoped handoff for each target. Portless
stops only the selected local service, starts an unprivileged private listener
on `127.0.0.1:0`, and retargets the existing source-aware edge proxies. Callers
keep their injected service URL; peer processes, debugger sessions, endpoints,
and generations do not change. Mock requests therefore pass through the same
traffic, trace, recording, and fault path as a real local or remote target.
Each captured exchange records the target provider, scenario, and matched route.
Each matcher swap is atomic, but a multi-service activation is a tracked
operation, not a simultaneous cross-service traffic switch. A partial failure
rolls back completed transitions in reverse order. If rollback cannot finish,
restoration records remain available and activation reports `degraded`;
disabling the scenario retries restoration. Activation describes binding
ownership, not runtime health. A daemon interruption can leave a recoverable
partial transition and must not report the scenario as fully enabled.

Matching is deterministic and isolated by service. An enabled route matches an HTTP method, an exact or
parameterized path, and optional required query values. More specific paths and
query sets win; definitions that remain ambiguous at the same specificity are
rejected within that service before they can serve traffic. Identical paths on
different services do not conflict. A route returns fixed status, headers,
body, and delay. An unmatched request returns `501` so a missing scenario is
visible rather than accidentally accepted.

API 15.0.0 represents each required query parameter as an explicit `match`
operator (`equals`, `exists`, or `regex`) and an optional `value`. Regex patterns
use Go/RE2 syntax and match an entire decoded value, with any repeated value
allowed to satisfy the rule. They are compiled once with bounds of 4096 bytes per
pattern and 262144 bytes per scenario. At equal path and query-count specificity,
more exact query matchers win, then more regex matchers, then presence matchers.
Equal-rank regex rules are conservatively considered overlapping unless paths or
other exact query constraints distinguish them. The web editor leaves regex syntax
validation to the daemon, avoiding differences between JavaScript and Go regex.
Database schema 11 converts stored exact/presence values transactionally on open;
HTTP accepts only the explicit operator contract.

Scenarios start empty and disabled. Routes can then be added individually,
derived from a stopped recording, or imported from a local OpenAPI 3.0/3.1 JSON
or YAML document. Recording imports retain each exchange's target service;
OpenAPI imports require an explicit service. Imports validate and persist as
one batch and require a disabled scenario. While active, response edits and
route toggles are allowed, but changing target coverage requires disabling
first. OpenAPI
import resolves only references contained in that document and never performs
a network fetch. Recording bodies are metadata-only by default and require an
explicit, bounded capture option because application payloads can contain
sensitive data.

Matcher preview accepts a sample request and an optional unsaved route draft.
API 16.0.0 uses one envelope with request, optional draft, and optional
originalRoute. Replacing an existing route requires its saved name; the
draft may use a new unique name, and a deleted original fails explicitly. A new draft
appends to an in-memory scenario copy and cannot replace a duplicate name.
The full candidate scenario is validated, so normal ambiguity, service
eligibility, response-size bounds, route-enabled state, and specificity rules
apply. Scenario activation is unnecessary. The browser evaluates the selected
route draft plus saved peer routes, while CLI preview evaluates saved routes.
Other retained browser drafts are not silently included.

Saving uses the route URL to identify the original and the required body `name`
as its destination identity. Renames and configuration changes commit in one
database transaction, retain creation time, and reject an occupied destination.
The validated scenario then replaces active matchers without a provider handoff.
The web editor moves selection and its URL to the saved name. Historical traffic
keeps the route name that actually served that exchange.

Repeated request headers and a bounded request body describe the complete
request under test. They are validated in memory and do not become implicit
match criteria. Preview makes no persistent or provider change, contacts no
application, and creates no traffic, recording, event, or timeline history.
Shared response construction preserves mock-owned headers and the structured
unmatched 501 response. Preview applies HEAD/204/304 body suppression and
reports configured delay without waiting; transport-added headers such as Date
and Content-Length are outside its scope. The browser preserves draft fields
and sample inputs between Edit and Preview, and marks a result outdated after
the sample, draft, or saved scenario routes change.

The implementation does not execute scripts, maintain request-sequence state,
match request bodies, render templates, proxy unmatched requests, or emulate
WebSocket, gRPC, raw TCP, or binary protocols. Those behaviors would change the
security and determinism boundary and require separate decisions.

The API, CLI, browser routes, and storage use the scenario contract directly.
There is no profile migration, active-mock preservation, legacy wire decoder,
or compatibility command. Prior development mock data is not carried forward.
