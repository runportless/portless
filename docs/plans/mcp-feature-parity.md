# MCP feature parity for the first release

Status: implemented and validated (2026-09-06). All 64 tools are registered;
24 are available by default. Scope/capability enforcement, safe inspection,
mock authoring/activation, replay/comparison, complete recording exports,
finite faults, guarded cleanup, configuration, bounded stdio/results, the shared
inventory, Settings, and API/documentation updates are implemented.

Validation passed: focused owner and architecture tests, MCP race tests,
`make lint`, `make test` (461 web tests and the complete Go suite),
`make test-e2e-cli`, `make test-e2e-ui` (71 browser tests), and `git diff --check`.
The compiled MCP journeys cover durable lifecycle, the complete application
workflow, and multi-source configuration/cleanup. Transport recovery, large
exports, payload limits, redaction, and stale/concurrent mutation boundaries
also have focused SDK, API, control-plane, and persistence regression coverage.
See the current [MCP README](../../portless-mcp/README.md) for usage and test links.

## 1. Release outcome

An MCP host should be able to perform Portless's existing application-development
workflows through the same daemon behavior as the CLI and browser:

```text
Discover or create a project → start an environment → inspect traffic and traces
→ capture a recording → create and preview a mock → activate it
→ prepare, edit, and send a replay → inspect the response comparison
→ restore providers → remove temporary debugging artifacts
```

It should also be able to clone environments, configure providers and checkouts,
maintain project sources, and export the project's declaration. All affected
resources must remain inside the server's immutable startup scope.

The release target is **39 new tools and extensions to existing tools**, taking
the inventory from 15 to **24 default inspection tools**, and from 24 to **63
tools with every capability enabled**. Registry tests now verify these counts
against the real SDK for every valid capability combination. Tools stay individually
named and typed; do not add an arbitrary CLI, HTTP, SQL, or shell execution tool.

Parity means completing the same product workflows. MCP retains its explicit
permission boundaries, finite experiments, bounded responses, and polling
interface. Machine setup and administration remain outside this scope.

All tools proposed below are part of the release parity target. The phases define
implementation order, not a claim that the later phases are already available.

## 2. Verified baseline before implementation and implementation constraints

Read these files again before modifying their contracts:

The following table records the gaps at planning time. The current code, tests,
and MCP README describe the implemented contract.

| Owner | Original behavior and consequence for this work |
| --- | --- |
| [MCP README](../../portless-mcp/README.md), [server](../../portless-mcp/server.go), [configuration](../../portless-mcp/mcp.go) | Fifteen default inspection tools; one sensitive-traffic tool, three lifecycle tools, and five recording/fault tools appear behind startup flags. The registry is fixed for the session. |
| [Scope resolver](../../portless-mcp/scope.go) | Workspace, pinned-environment, and installation scopes are revalidated per call. There is no project scope or permission to introduce arbitrary source paths. |
| [Result mappings](../../portless-mcp/results.go) | Traffic summaries omit mock/replay attribution, trace carriers, WebSocket classification, and decoded protocol metadata. Sensitive exchange detail already returns those contract fields; extend summary mapping rather than inventing another decoder. |
| [Typed API client](../../portless-daemon/api/client) | Most required operations already exist. MCP must continue to import only the daemon client/contract and official MCP SDK across product boundaries. |
| [Replay API](../../portless-daemon/api/server/traffic_replay.go) | Every replay route currently rejects `ClientKindMCP` with `REPLAY_CAPABILITY_REQUIRED`. Registration alone cannot enable replay. |
| [Replay contract](../../portless-daemon/api/contract/traffic_replay.go) | Workspaces use public numbers, two identity timestamps, optimistic revisions, and run numbers for admission receipts. Replacement request bodies allow 25 MiB of decoded UTF-8. |
| [Project control plane](../../portless-daemon/controlplane/projects.go) | Discovery persists a project. Adding/removing a logical source affects shared project topology. Rescan also updates the project definition, even though its API path addresses one environment. Creation and rescan currently use hard-coded `CLI` timeline actors. |
| [Recording API](../../portless-daemon/api/server/traffic.go) | Export currently reads at most 10,000 events. Recordings may retain 100,000. The typed byte-returning client also has a general 16 MiB response read limit. A large export must not be reported as complete after either cutoff. |
| [Mock control plane](../../portless-daemon/controlplane/mocks.go), [matcher](../../portless-daemon/mocks/matcher.go) | Saved scenarios can contain 1,000 routes and 8 MiB of response bodies. One route body allows 1 MiB; preview request bodies allow 256 KiB. Returning whole scenarios through MCP would exceed its result budget. |
| [OpenAPI](../../portless-daemon/api/openapi.yaml), [events](../../portless-daemon/api/events.md) | API version is currently 17.0.0. Fault documentation has drifted: code uses POST enable/disable and DELETE for deletion. Replay admission produces normal traffic, mock, fault, and recording observations, not a separate replay event stream. |
| [Settings generator](../../portless-web/src/features/mcp/mcpConfiguration.ts) | Capability flags and tool counts are currently hard-coded in TypeScript and rendered by `MCPSettings.tsx`. Update generated configuration, descriptions, count tests, and browser coverage with registry changes. |

Existing lifecycle admission already has durable idempotency keys, operation
numbers, bounded waits, and `MCP` actor attribution. Reuse those contracts.
Existing replay execution already enforces source-to-target identity, provider
generation, remote policy, capture bounds, credential redaction, and duplicate
suppression. MCP must not implement a second execution or comparison engine.

## 3. Permission and scope contract

### 3.1 Capability categories

Retain the three existing flags and add two flags with distinct consequences:

| Symbol | Startup permission | Meaning |
| --- | --- | --- |
| I | Always enabled | Safe inspection, trace metadata, mock metadata/preview, and safe declaration export. |
| S | `--allow-sensitive-traffic` | Captured application data, saved mock payloads and matcher values, recording export, and replay observations. It never grants access to provider runtime secrets. |
| L | `--allow-lifecycle` | Start/stop/debug/manage services and environments; permit provider transitions when combined with their owning feature permission. |
| T | `--allow-traffic-control` | Recordings, finite faults, mock authoring/import, and removal of traffic artifacts. Update the flag description to state this broader behavior. |
| R | New `--allow-replay` | Create/edit/release replay workspaces and explicitly send application requests. Requires S. |
| C | New `--allow-configuration` | Discover/create projects, change topology and checkout configuration, clone environments, and forget eligible application state. |

Combination rules:

- Replay tools require **R + S**. Reject startup with R but without S with a
  useful configuration error; do not silently enable sensitive access.
- Enabling/disabling mock scenarios and Disable All Mocks require **T + L**
  because provider transitions can stop or restore service processes.
- Changing a service binding requires **C + L**, including when the environment
  is stopped. Keep the tool contract independent of current runtime state.
- Importing a recording into mocks requires **T + S**. Importing supplied OpenAPI
  content requires T. Neither import activates the scenario.
- Starting a recording with `capturePayloads: true` requires **T + S**. Without S,
  reject that argument instead of quietly falling back to metadata capture.
- Mock inspection and preview are I tools. Requesting saved headers, body, or
  matcher values through them requires S. Mutation results default to metadata;
  a mutation flag must not become an alternate route to read existing payloads.

Startup flags are authorization to expose capabilities to the MCP host. Tool
annotations describe effects; they are not authorization checks. A tool argument
cannot broaden startup permissions or scope. Do not add another approval step
for ordinary reads or already authorized reversible actions.

For destructive calls, the host receives a concrete preview and explicitly
applies it as described in section 4.4. A confirmation boolean records the
requested action; it does not prove that a human personally approved it.

### 3.2 Add an immutable project scope

Add `portless mcp serve --project <name>` and `Config.Project`. It is mutually
exclusive with global `--env` and `--all-environments`; omission retains the
existing workspace default.

| Scope | Environment access | Project-wide changes and creation |
| --- | --- | --- |
| Pinned environment | Exactly the configured `project/environment`. Replay destination must also be that environment. | No clone, rescan, logical-source changes, project rename, or project forget. Environment binding/checkout changes and eligible environment forget can be allowed. |
| Workspace | Environments associated with the startup checkout, re-resolved on every call. | Discovery/create may register sources inside the authorized workspace roots. Shared-project mutations and clone require project or installation scope. |
| Project, new | Every current or future environment in exactly the named project. | Clone, rescan, logical-source changes, rename, and eligible forget within that project. Creating an unrelated project is denied. |
| Installation | All named projects and environments in this installation. | All proposed project/environment tools, subject to source-root and capability restrictions. |

Project inspection in a pinned/workspace server must filter environment summaries
and checkout information to the visible environment set. Shared logical service
and source names can be shown; hidden environments, their paths, operations, and
traffic cannot be exposed through a project DTO.

Workspace-scoped creation must include a source that associates the resulting
environment with the startup checkout. An additional allowed source root does
not authorize creating a project that the server could not subsequently access.
If discovery resolves an existing environment, authorize it before returning
its project data. Do not persist a new association merely to widen scope.

Do not infer permission to mutate a project from the visibility of one of its
environments. In particular, classify rescan as a shared-project mutation.
Project/installation scope supplies the entire affected scope up front, avoiding
a check-then-write race over a partial list of visible environments.

Renaming a project does not rewrite a running MCP server's startup scope. A
project-scoped rename returns the new selectors and explains that the host must
restart with the new project name. Subsequent access through the old scope fails
closed. An installation-scoped host can continue using the new name.

Replace installation membership checks that scan only the first 1,000
environments with an exact typed lookup after authorizing the selector. Scope
checks must not depend on display pagination limits.

### 3.3 Authorize new source paths at startup

Add repeatable `--source-root <directory>` and `Config.AllowedSourceRoots`.
Workspace scope includes its canonical startup checkout by default. Explicit
source roots authorize filesystem discovery/configuration inputs; they do not
grant visibility into additional existing Portless environments.

For pinned/project/installation configurations, introducing a new checkout path
requires an explicit source root. Existing registered checkout paths may be used
by normal lifecycle operations and authorized rescans. Additional roots permit
sibling repositories and worktrees without allowing arbitrary host-file reads.

Resolve roots once at startup. Validate every introduced path against them,
including symlinks and ancestor workspace discovery. Reject `..` or symlink
escapes and discovery that would select a root above the authorized boundary.
Path inspection here is limited to validation; discovery still belongs to the
daemon and must remain static, bounded, and read-only.

Enforce root confinement again in daemon discovery at the filesystem boundary,
using its root-confined discovery machinery. Extend typed discovery/source input
contracts to carry the selected allowed root. Do not rely exclusively on MCP's
`filepath` prefix check, which can race with path changes. Update CLI/UI adapters
coherently where those contract types are shared.

A tool must never clone a Git repository, execute an arbitrary command, or fetch
an arbitrary URL to obtain source or import content. The daemon's existing
automatic environment-worktree preparation remains owned by environment startup.

## 4. Common tool contract

### 4.1 Inputs and result envelopes

- Require `environment: "project/environment"` on environment tools. Project
  tools use a public `project` name. Services, routes, scenarios, faults,
  recordings, and sources use their existing public names.
- Keep strict schemas and reject unknown fields. Reuse contract validation and
  daemon rules rather than copying feature implementations into MCP.
- Return scope identities, a typed result, warnings/limitations, and
  `untrustedData: true` wherever application-derived text can occur.
- Preserve structured daemon codes and remediation. Bound and redact error
  messages, subjects, details, and stderr diagnostics as well as successful
  results. Never log submitted replay credentials, OpenAPI contents, or bodies.
- Put new feature-specific inputs/outputs in their owning MCP files. Avoid
  expanding `results.go` into another large unrelated contract container.

### 4.2 Bounds and pagination

Preserve eight concurrent calls, two concurrent mutations, the existing rate
limit, and a 1 MiB complete MCP result budget. Check the serialized MCP envelope,
including SDK-generated text content if it duplicates structured content.

Use compact list DTOs. Trace lists default to 50 rows, maximum 200. New
project/mock metadata lists default to 100, maximum 500. Paginate trace spans and
scenario routes. Existing list defaults can remain unless their payload size
requires a smaller page.

Every partial response must provide an explicit continuation or explain why
evidence is unavailable. Distinguish daemon capture truncation from MCP display
truncation. Do not return a generic result-too-large error after successfully
executing a mutation when a compact receipt can still be returned.

For inspectable text, default to a bounded prefix and provide explicit byte
range continuation where complete saved content is available. Bind continuation
to resource identity/revision. Preserve UTF-8 boundaries and repeated headers;
do not feed truncated display content back into a write as a complete document.

Recording export uses bounded chunks of the original export document, described
in section 7. Project declaration export uses the same response-size discipline
with revision-bound chunks when it does not fit inline. Neither accepts an
output filesystem path or exposes a daemon bearer URL.

Bound incoming MCP messages before expensive decoding and feature execution.
Normal import/route tools retain their domain-specific decoded limits. Replay
must accept the existing 25 MiB decoded request-body contract, including JSON
escaping overhead, without applying that size to every ordinary tool input.
Exercise the installed SDK's actual stdio path in these tests.

### 4.3 Mutation admission and retry behavior

- Keep existing durable idempotency for lifecycle operations. Use it for
  binding changes and mock activation through existing typed API methods.
- Return the operation number and caller-visible key even if waiting times out.
  Default wait is 30 seconds; accept 0 through 120 seconds. Zero means return
  after admission, not launch a hidden MCP background loop.
- Replay uses its existing identity/revision/run-number contract, not a second
  operation/idempotency layer.
- Retry reads once on the existing eligible connection errors. Do not replay a
  mutation automatically after a lost response. Recover by reading its receipt
  or inspecting the named resource.
- For synchronous creation, import, rename, and deletion, document the actual
  guarantees. In particular, mock imports append routes and are not safely
  retryable merely because the tool name sounds declarative.
- Do not use the current universal `mutationTool` idempotent annotation for all
  additions. Set read-only, destructive, idempotent, and open-world annotations
  per operation. Replay run and provider transitions can affect configured
  remote services and need accurate descriptions/annotations.
- Cancellation ends the MCP wait. It does not claim to cancel an already
  admitted daemon operation or application request.
- Carry actor `MCP` through discovery, creation, rescan, clone, source changes,
  imports, and cleanup wherever those actions emit history. Remove hard-coded
  `CLI` actors in shared control-plane behavior, preserving correct CLI/UI actors.

### 4.4 Destructive previews and concurrency

Delete/forget/clear tools use `mode: "preview" | "apply"`, defaulting to preview.
An apply call requires `confirm: true` and the identity/preconditions returned
by its preview. This is one tool per operation, not a generic mutation engine.

Previews report exact names, affected environments, removed routes/services,
retained-event counts, blocking dependencies, and what data remains. Applications
are never stopped implicitly to make a deletion eligible.

Use resource-specific preconditions: project/environment identity and revisions,
recording start identity, fault creation identity and revision, and scenario/route
creation and modification identity. Names/revisions alone must not match a
deleted and recreated resource. The
daemon must validate them atomically with the change; MCP read-before-delete
alone is insufficient. Add narrowly scoped request/response contracts where the
existing endpoint lacks these conditions and update typed client/CLI/UI callers.

Clear live traffic through the reviewed sequence watermark so traffic arriving
after the preview is retained. Dispose affected originating replay payloads
consistently with the current clear behavior; admitted run receipts remain.
Do not reset exchange sequence counters or delete durable recordings.

Current forget implementations require stopped state. Preserve that requirement,
make the eligibility/precondition check atomic with deletion, and retain the
repository's ownership boundary when evaluating runtime state. Forgets do not
remove volumes, destroy worktrees, delete source repositories, or force an active
environment to stop. Report retained installation-owned resources and actionable
eligibility failures.

### 4.5 Required API extensions

These are supporting changes to existing product operations, not new MCP-only
business logic:

| Contract change | Required implementation |
| --- | --- |
| Paged project and mock metadata | Add query/summary DTOs and typed metadata reads, backed by bounded database/projection queries. Do not fetch every scenario body and truncate the HTTP response in MCP afterward. Expose metadata projection through explicit query options on the existing resources; update affected consumers and OpenAPI schemas. |
| One mock route read | Add GET on the existing named route resource, with identity-bound body ranges. Use it for `get_mock_route` instead of reading an entire scenario's bodies. |
| Trace continuation | Add revision-bound span paging and stable continuation where the existing list/detail API lacks it. Keep projection watermarks independent of returned page length. |
| Guarded cleanup/configuration | Add domain-owned previews and atomic apply preconditions for the affected named operations. Do not put SQL or ownership decisions in the MCP adapter. |
| Export delivery | Add the recording chunk contract and shared iterator described in section 7; provide revision-bound safe declaration chunks for large project exports. |
| Replay status | Add a metadata-only `TrafficReplayStatus` contract and typed read at the replay workspace's `/status` subresource. Include current and latest submitted destination identities, revision, run/receipts, and lifetime state without request/response bodies. Use it for scope preflight and receipt polling. |
| Source confinement and actors | Carry the selected authorized discovery root through typed source inputs and carry authenticated actors into the shared control-plane operations that currently hard-code CLI. |

The metadata projection and content delivery variants serve different operations;
they must not become compatibility schemas for retired contracts. Reuse one
canonical domain model, validation, and mutation implementation underneath them.

## 5. Inspection parity: nine new default tools

| Tool | Inputs beyond common scope | Result and API integration |
| --- | --- | --- |
| `portless_list_projects` | `limit`, continuation | Visible project summaries, source/service counts, and only visible environment summaries. Use `ListProjects` or derive the permitted project set from scoped environments before mapping. |
| `portless_get_project` | `project` | Safe logical topology, named sources, revision, issues, and visible environments. Use `Project`; do not serialize `ServiceDefinition.Environment` or private runtime structures wholesale. |
| `portless_export_project` | `project`, optional revision/continuation | Versioned, safe declaration content, redaction/omission metadata, and complete/continuation state. Use the declaration export contract after its safe projection and bounded delivery are established. |
| `portless_list_traces` | `service`, `edge`, `includeBackground`, `limit` | Compact trace summaries plus unchanged projection `revision` and `throughSequence`. Use `TrafficTraces`. |
| `portless_get_trace` | `number`, optional expected revision, span continuation/limit | Root and parent relationships, depth, offsets, durations, transaction groups, and exact/inferred/partial/ambiguous correlation. Use `TrafficTrace`; map each span's exchange to safe metadata. |
| `portless_list_mock_scenarios` | `limit`, continuation | Names, descriptions, route counts, activation/degraded state, target services, and active services. Extend the `ListMockScenarios` resource with a paged metadata projection; exclude route bodies from the query and result. |
| `portless_get_mock_scenario` | `scenario`, optional `service`, route continuation/limit | Scenario metadata plus paged route summaries. Extend the `MockScenario` resource with the metadata projection; retain declared ordering and route enabled state. |
| `portless_get_mock_route` | `scenario`, `route`, `includePayloads=false`, optional expected modification identity/body range | Full matcher/response metadata, with payload access and complete-body continuation gated by S. Use the new typed GET on the existing route resource. |
| `portless_preview_mock` | `scenario`, `request`, optional `draft` and `originalRoute`, `includePayloads=false` | Match/no-match, selected route, status, delay, and gated response payload. Use `PreviewMock`. Does not persist a draft, activate providers, delay the caller by the configured mock delay, or generate application traffic. |

Extend `portless_query_traffic` to retain:

- `background`, request kind, and mock scenario/route attribution;
- replay provenance and public sequence identities;
- available trace/span carriers and trace-context source;
- decoded TCP kind, application protocol, operation, inspection state, outcome,
  message counts, and truncation metadata.

Do not infer trace parentage or a trace number from an exchange sequence in the
MCP adapter. Correlation and trace membership come from the daemon projection.
Do not copy SQL text, Redis keys/values, NATS subjects/payloads, query values,
headers, or bodies into default summaries through a nested protocol structure.
Sensitive exchange inspection remains `portless_get_traffic_detail`.

Trace detail must strip payloads from every nested span, not just the trace root.
Use a trace revision on span pagination and return a structured stale-projection
result when it changes. Preserve the distinction between provisional,
background, inferred, and incomplete traces.

For mock inspection, query matcher names/types may be metadata, but matcher
values and response bodies/headers can contain imported application data.
Preview without S returns only match metadata, even for a supplied draft.
Use `get_mock_route` for bounded retrieval of the complete saved response.

Project declaration export must never be a raw pass-through of arbitrary
discovered environment values. Audit the existing export fields, implement the
safe projection at the owning daemon boundary, and report redactions explicitly.
Secret-bearing runtime values remain unavailable even with S. Do not claim a
redacted declaration contains all original configuration values.

## 6. Mock authoring and provider control: eight tools

| Tool | Permission | Inputs and behavior |
| --- | --- | --- |
| `portless_create_mock_scenario` | T | `environment`, `scenario`, optional `description`. Create an empty disabled scenario with `CreateMockScenario`; no implicit provider change. |
| `portless_put_mock_route` | T | `scenario`, addressed `route`, complete route draft. Use `PutMockRoute`; preserve service, method, path, query matcher kind/value, response status/headers/body, delay, and enabled state. A changed draft name performs the existing rename operation. |
| `portless_delete_mock_route` | T | Scenario/route plus preview/apply fields. Use guarded `DeleteMockRoute`; return updated compact scenario metadata and removed route identity. |
| `portless_delete_mock_scenario` | T | Scenario plus preview/apply fields. Use guarded `DeleteMockScenario`; require disabled state and report all routes removed. |
| `portless_set_mock_scenario_enabled` | T + L | `scenario`, `enabled`, `idempotencyKey`, `waitSeconds`. Use `SetMockScenarioEnabled`; return the durable operation and resulting activation state. |
| `portless_disable_all_mock_scenarios` | T + L | `idempotencyKey`, `waitSeconds`. Snapshot active/degraded scenarios and restore them sequentially through existing activation operations. Return per-scenario operations, completed/pending/failed names, and partial failure. |
| `portless_import_mock_recording` | T + S | `scenario`, `recording`, optional `services`. Use `ImportMockRecording`; require the same environment, a stopped recording, and a disabled scenario. Preserve import warnings and source/target identities. |
| `portless_import_mock_openapi` | T | `scenario`, `service`, `document`. Use `ImportMockOpenAPI`; accept supplied content only, honor the existing 1 MiB document bound, and return import warnings. |

Match the current mock feature set: fixed responses, service-scoped routes,
query equals/exists/regex matching, enabled/disabled routes, saved route order,
and preview against an optional complete draft. Do not add unsupported request
header/body matchers, stateful behavior, scripts, passthrough, or proxy fetches.

A preview request contains repeated query/header values even though saved fixed
response headers use the existing single-value mock contract. Do not reuse the
replay header model indiscriminately or normalize away repeated request values.

Preserve the daemon's editing/activation conflict rules. Activating a scenario
must retain exact previous bindings, transition all targeted services through
the existing provider operation, and preserve unrelated processes. Disabling
restores those bindings; MCP must not guess that the previous provider was local.

Disable All is bounded orchestration over typed operations, matching the UI's
workflow. Use stable child idempotency keys derived from the supplied batch key
and scenario name. Observe an outstanding child operation before proceeding.
When the wait budget expires, return pending and not-yet-started names; do not
leave a detached MCP goroutine continuing provider changes after the call ends.
Retries recover existing operations and inspect remaining state.

Route/OpenAPI input readers need JSON envelope space beyond their decoded body
limits. Verify exact-limit, escaped-text, and one-byte-over cases across the
typed client, server, and MCP; the current shared 1 MiB JSON reader must not
silently make the advertised decoded limits unreachable.

## 7. Recording, fault, and live-traffic completion: five tools

| Tool/change | Permission | Inputs and behavior |
| --- | --- | --- |
| Extend `portless_start_recording` | T; S for payload capture | Add `capturePayloads=false` and `maxPayloadBytes`. Preserve duration 1–3600 seconds and max 100,000 events. Payload limits use the current daemon default of 64 KiB and maximum of 1 MiB. Include capture mode and bounds in duplicate-name matching. |
| `portless_export_recording` | S | `recording`, optional export cursor and chunk size. Return resumable chunks of a complete versioned recording export; no arbitrary output path. |
| `portless_delete_recording` | T | Recording plus preview/apply fields. Report retained event count, require eligible non-active state, and delete through the guarded typed API. |
| `portless_enable_fault` | T | `fault`, expected revision. Use `SetFaultEnabled(true)` and return its effective scope/expiry. Preserve the original expiry and refuse expired rules. |
| `portless_delete_fault` | T | Fault plus preview/apply fields. Use guarded `DeleteFault` and report that the rule is removed. |
| `portless_clear_traffic` | T | Preview/apply fields and reviewed sequence watermark. Use the guarded `ClearTraffic` contract and report cleared count, through-sequence, and revision. |

`enable_fault` must not activate a persistent, unrestricted CLI-created fault
through the bounded MCP capability. Revalidate its exact edge, supported effects,
and finite remaining lifetime of at most one hour. Return a policy error for an
unbounded/ineligible rule without modifying it. Disabling and deleting such a
rule remain permitted. An expired rule requires explicit deletion/recreation;
do not silently extend it or reset its counters.

Keep existing recording/fault start, stop, disable, and Disable All Faults tools.
Starting another named recording with the same declared bounds covers Repeat;
it does not imply request replay or replaying a recording's exchanges.

### Complete recording export within the MCP budget

Implement this as a bounded delivery path for the existing schema-4 export:

1. Add `RecordingExportChunkQuery` / `RecordingExportChunk` contract types and a
   typed `RecordingExportChunk` client method. A chunk response carries schema
   version, public recording identity, snapshot event count/high-water mark,
   base64 bytes, next cursor, and `complete`.
2. Add a narrow GET `/recordings/{recordingName}/export/chunks` endpoint. Capture
   a stable snapshot of the recording's currently retained events on the first
   request. For active recordings, include only events through that watermark;
   report the snapshot rather than claiming to include later traffic.
3. Fetch recorded exchanges by indexed sequence pagination. The cursor binds
   recording start identity, snapshot watermark, export position, and an offset
   within a serialized event. This lets a single large exchange span multiple
   chunks without truncating any payload or decoding the entire recording.
4. Return at most 128 KiB of decoded export bytes per call, base64 encoded so
   JSON escaping cannot unpredictably expand a chunk. Reassembling chunks in
   order must produce one valid existing `RecordingExport` document, including
   its prefix, separators, and suffix. Completion is true only at its end.
5. Validate identity, cursor, scope, and S permission on every request. A cursor
   is a continuation, never authorization. Deletion/name reuse must invalidate
   it rather than substitute another recording. Do not retain export files or
   implement a generic artifact service solely for this feature.
6. Reuse the bounded iterator/encoder for the current CLI/UI export endpoint so
   it exports the selected snapshot completely beyond 10,000 events. Add a typed
   streaming-to-writer client method for CLI export rather than exposing raw
   HTTP transport or accumulating the whole download in `[]byte`.
7. Make the client's response limit detect overflow explicitly wherever byte
   reads remain; a successful short read at a limit is not a complete export.

Export delivery is lossless relative to the daemon's retained, redacted data.
It cannot recover bodies that were not captured. Preserve capture metadata,
decoded protocol content, replay provenance, and schema version. Do not label
MCP display prefixes as a complete recording export.

## 8. HTTP replay and comparison: five tools

All five require R + S. A comparison is part of the replay result, so there is
no separate arbitrary-response comparison tool.

| Tool | Inputs | Output/API method |
| --- | --- | --- |
| `portless_prepare_replay` | `environment`, live `sequence`, exact `startedAt` | `PrepareTrafficReplay`: frozen baseline summary, original logical edge, workspace identity/number, revision, next run number, redacted initial draft, omissions, and limitations. Sends no application request. |
| `portless_update_replay` | Origin `environment`, workspace number and both identity timestamps, expected `revision`, complete `draft` | `UpdateTrafficReplayDraft`: reviewed destination/provider/policy, new revision, prepared-input deadline, bounded redacted draft view, and limitations. No dispatch. |
| `portless_run_replay` | Origin scope, workspace identity, reviewed `revision`, explicit `runNumber`, `confirmRemoteWrite=false`, `waitSeconds` | `RunTrafficReplay`: compact admission receipt followed by bounded status/comparison if completed within the wait budget. |
| `portless_get_replay` | Origin scope, workspace identity, `includeResult=true`, optional result section/continuation | Metadata through `TrafficReplayStatus`, followed by authorized result sections through `TrafficReplay`: current receipt, previous receipts, frozen baseline metadata, latest request/destination, and daemon-computed comparison. This is also the recovery path after an uncertain send response. |
| `portless_close_replay` | Origin scope, workspace identity | `DeleteTrafficReplay`: release retained payloads and unused credentials. An admitted request continues and its compact receipt remains available until cleanup. |

### MCP admission at the daemon boundary

Replace the unconditional MCP deny only when the new permission contract is
implemented in the same slice. Add a contract-owned, versioned replay capability
declaration, for example `Portless-MCP-Replay-Capability: 1`, and a typed client
option such as `WithMCPReplayCapability()`.

The MCP gateway attaches it only when R and S were enabled at startup. Preserve
`Portless-Client-Kind: mcp`; never disguise MCP as CLI or drop the actor header.
Daemon replay routes keep rejecting MCP requests with missing/invalid capability
declarations. Existing authenticated CLI/browser behavior remains governed by
its normal authentication and CSRF boundary.

The declaration is an opt-in contract for the trusted local adapter, not a new
credential or a security boundary against a holder of the daemon bearer token.
MCP startup configuration and scope checks are the tool-call permission boundary.
Do not introduce a separate daemon login, arbitrary transport headers from tool
arguments, or a new authentication system.

### Preserve reviewed requests and recover receipts

1. Resolve the origin environment on every tool call. Before preparing a draft,
   independently authorize its destination environment in the same project.
2. Before run or result retrieval, validate every relevant stored destination
   against current scope: both the current draft and the latest submitted
   request/result can refer to different environments. Use the metadata-only
   status read for this check. Recheck the identities/revision of a fetched
   payload before returning it so a concurrent edit cannot substitute another
   destination. A foreign browser-created workspace is not automatically
   accessible merely because its origin is.
3. Submit a complete draft with fixed source and target; never accept a free
   destination URL, retarget the service, start an environment, or change a
   binding as part of replay. Omitted/truncated source bodies require replacement
   or an explicit empty body, exactly as in the CLI/UI.
4. Return the daemon's remote-write review requirements. An explicit
   `confirmRemoteWrite` applies only to that prepared revision and destination.
   It cannot override read-only provider policy or a changed provider generation.
5. Submit one explicit run number. On a dropped response, inspect the same
   workspace/receipt with both identity timestamps. Do not increment the run
   number, reprepare the request, or resend automatically to recover a response.
6. Preserve run state/outcome distinctions, including failures and uncertain
   observations. A deadline or closed workspace is not proof no request ran.
   Comparison states such as partial/unavailable must never become equality.
7. Map results compactly. Keep status/timing and comparison section states;
   page bounded difference entries where necessary. Preserve lossless JSON scalar
   spellings and before/after values as provided by the daemon.

The 25 MiB request limit remains an execution input limit. MCP responses show
bounded redacted previews and byte counts; they must not echo the full current
draft plus submitted body. Retain response capture/comparison limits. This work
does not add a size label to the replay UI or change its standard error notice.

Track only the identities of workspaces created by this stdio session for
best-effort cleanup at EOF/cancellation; do not retain a second copy of secrets
or close workspaces created by another client. Issue bounded close calls when
the session ends, with the daemon's existing one-hour idle cleanup as fallback.

Use `TouchTrafficReplay` for successful explicit interactions with an open
workspace where required. Do not start unconditional keepalive timers. Receipt
polling during a run need not extend the session forever; closed receipt reads
must remain recoverable without trying to revive the workspace. Prepared
credentials still expire independently in less than 60 seconds.

Unsupported replay types remain unchanged: recordings/whole traces, arbitrary
response pairs, cross-project remapping, binary/multipart, streams/SSE,
WebSockets, gRPC/TCP, saved collections, assertions, and load generation.

## 9. Project and environment configuration: twelve tools

| Tool | Permission | Inputs and daemon behavior |
| --- | --- | --- |
| `portless_discover_project` | C | Authorized `path`, optional `name`. Use `DiscoverProject`; explain that it persists discovery and may resolve an existing project. Return the resulting environment and warnings without starting it. |
| `portless_create_project` | C | `project`, named `sources[{name,path}]` under allowed roots. Use `CreateProject`; return the new project and stopped `local` environment. No required declaration file. |
| `portless_add_project_source` | C | `project`, configured `environment`, `source`, authorized `path`. Use `AddProjectSource`; report new services and every environment needing configuration. Requires project/installation scope. |
| `portless_delete_project_source` | C | `project`, `source`, preview/apply fields. Use guarded `DeleteProjectSource`; report removed services/connections across all environments. Do not remove the repository directory. |
| `portless_rename_project` | C | `project`, `name`, expected project revision. Use `RenameProject`; return updated public selectors and the configured-scope consequence. |
| `portless_clone_environment` | C | Source `environment`, new environment `name`. Use `CloneEnvironment`; return a stopped clone and provenance. Requires project/installation scope; starting later uses normal daemon worktree preparation. |
| `portless_change_service_binding` | C + L | `environment`, `service`, complete provider binding, `idempotencyKey`, `waitSeconds`. Use `ChangeBinding`; return the durable operation and effective safe binding. |
| `portless_set_source_checkout` | C | `environment`, `source`, authorized `path`. Use `SetSourceCheckout`; return warnings, revision, and configuration issues. Preserve stopped-state requirements. |
| `portless_remove_source_checkout` | C | `environment`, `source`, preview/apply fields. Use guarded `RemoveSourceCheckout`; detach only that environment's path without removing the logical source or filesystem checkout. |
| `portless_rescan_environment` | C | `environment`, expected project/environment revisions. Use `RescanEnvironment`; require project/installation scope because shared topology can change. Return warnings and refreshed configuration. |
| `portless_forget_environment` | C | `environment`, preview/apply fields. Use guarded `ForgetEnvironment`; report retained-data removal and eligibility failures. |
| `portless_forget_project` | C | `project`, preview/apply fields. Use guarded `ForgetProject`; preview every affected environment and retained artifact. Requires project/installation scope. |

Bindings support the product's current local, container, remote, and mock
choices. Preserve required source/scenario selectors. Remote bindings require
explicit classification and write policy, validated by the daemon; never
default an unspecified policy to read-write. Binding configuration may specify
a remote endpoint because it is the existing product workflow; this does not
create an arbitrary HTTP-fetch tool or allow replay to bypass registered routing.

Shared topology writes must use the daemon's revision/transaction protections
and stopped-state checks. Return meaningful conflicts and configuration-required
results rather than attempting implicit stop/rescan/rebind sequences.

Keep `portless_start_environment` focused on an existing environment. The
zero-configuration MCP journey is explicit `discover_project` followed by
`start_environment`, requiring C + L. Discovery itself must not start a service,
install a relay, run a manifest script, or write `portless.yaml` into a checkout.

Do not add MCP tools that change the CLI's persisted `env select`/`env clear`
preference. Every MCP call already selects its target explicitly. Existing
environment/service inspection provides checkout lists, public URLs, health,
configuration, debugger attach details, and connections without extra aliases.

## 10. Implementation slices and dependencies

Deliver coherent feature slices. Do not advertise flags, tool counts, or
capabilities as working before their handlers and daemon changes land.

| Order | Scope and primary files | Dependency / acceptance criterion |
| --- | --- | --- |
| 1 | Capability/scope/result foundations: `portless-mcp/{mcp,server,scope,errors}.go`; narrowly owned `capabilities.go`, `project_scope.go`, `source_scope.go`; CLI administration MCP options. | Fix exact scope membership, define capability conjunctions and per-tool annotations, establish bounded DTO/continuation conventions, and test immutable scope. Introduce new scope/flags with their first functioning slice. |
| 2 | Inspection parity: MCP `trace_tools.go`, `trace_results.go`, `mock_inspect_tools.go`, `mock_results.go`, `project_inspect_tools.go`, `project_results.go`; extend traffic summary mapping. | Safe recursive redaction and default tool inventory pass. Any needed metadata/page endpoints go contract → client → server/control plane → consumers. Project export waits for safe projection/chunk support, not raw passthrough. |
| 3 | Mock workflow: MCP `mock_tools.go`; typed mock API guards/envelopes; existing activation operations. | Create → put/rename/toggle route → preview → activate → restore; import content/recording; partial Disable All recovery. T alone cannot perform lifecycle transitions or reveal saved payloads. |
| 4 | Replay workflow: MCP `replay_tools.go`, `replay_results.go`, narrowly owned session identity cleanup; contract/client/server replay capability. | Prepare/update cause zero application calls; reviewed run causes one; dropped replies recover receipts; both environments are scoped; remote and large-body cases behave like CLI/UI. |
| 5 | Recording/fault/traffic completion: MCP `recording_tools.go`, `fault_tools.go`, traffic clear; `api/{contract,client,server}/traffic*`; control-plane observability and `database/experiments.go`. | Full recording export survives event/byte boundaries, payload capture is gated, enable preserves finite expiry, and guarded cleanup preserves new traffic and unrelated artifacts. |
| 6 | Configuration lifecycle: MCP `project_tools.go`, `environment_tools.go`; project/source input contracts; control-plane projects/discovery/database. | Fresh-workspace discovery, clone, bindings, checkouts, multi-source changes, actor attribution, and eligible forget work entirely through MCP with path confinement and project scope. |
| 7 | Cross-product integration: CLI help, MCP README, Settings generator/component, OpenAPI/events, main README, implementation status, E2E docs. | Actual registry, generated configuration, CLI flags, descriptions, and documented capabilities agree. Full release journey and negative permission cases pass. |

Slices 3–6 use the same policy/result foundation. Recording import reuses the
existing daemon import path and does not require exporting a recording first.
Replay execution does not depend on introducing saved recordings or a scenario
runner. Configuration paths need their daemon confinement contract before those
tools are registered.

New public API fields, guarded mutations, or export resources follow the
repository's contract-first sequence. Deliberately update the API semantic
version from the version current at implementation time; update all generated
consumers coherently. Do not retain alternate legacy schemas or compatibility
facades. Correct the fault OpenAPI drift in the fault slice.

No new top-level product, generic `internal`/`pkg`/`common` package, independent
daemon API service, or direct MCP database/proxy/discovery import is necessary.

## 11. Settings, documentation, and inventory consistency

Update `MCPSettings.tsx` and `mcpConfiguration.ts` to generate project scope,
source-root arguments, replay/configuration flags, and valid combinations.
Explain scope and permission consequences in the existing settings presentation.
Keep keyboard access, visible focus, both themes, shell quoting, and clipboard
failure handling. Elevated choices must still reset on reload.

The generator must not silently check S when the user checks R. Show the
dependency and require a valid configuration before Copy is enabled. Explain
that mock activation requires lifecycle permission and binding changes require
both lifecycle and configuration permission.

Replace scattered additive count literals with a declarative tool inventory
inside `portless-mcp`, recording each tool's required capabilities and effect
annotations. Registration uses this inventory. Export a small generated metadata
fixture for web count tests/build input; it is product metadata, not a second
daemon API or a copy of implementations. Verify it against the real SDK
`tools/list` response, including compound permissions.

With the inventory in this plan, for boolean permissions L/S/T/R/C:

```text
tool count = 24 + 3L + 2S + 14T + 2(T∧L) + (T∧S)
                + 5(R∧S) + 11C + (C∧L)
```

R without S is invalid. The fully enabled count is 63. Tools remain fixed for
the session; a scoped tool can still return `SCOPE_DENIED` for an ineligible
target or `SOURCE_ROOT_REQUIRED` for a path outside configured roots. Count
available tool names, not individual optional input modes.

Update:

- `portless-mcp/README.md`: inventory, scope/flag matrices, response limits,
  confirmation/precondition behavior, and complete examples.
- `portless-cli/COMMANDS.md` and administration command-tree tests: flags,
  mutual exclusions, source roots, and stdout-only MCP protocol behavior.
- `README.md` and `docs/implementation-status.md`: remove MCP replay exclusion
  only once implemented; describe supported application-workflow parity.
- `portless-daemon/api/openapi.yaml` and `events.md`: replay capability,
  preconditions, export chunks, actor behavior, and corrected fault routes.
- `docs/e2e-testing.md`: new isolated MCP journeys and boundary coverage.

Generate tracked web assets with `make`. After a Settings UI change, build the
complete executable and perform the normal `./bin/portless daemon restart` from
this checkout before handing off a refreshable UI. Do not use forced restart as
routine validation.

## 12. Validation and release acceptance

### Unit, API, and contract coverage

| Boundary | Required evidence |
| --- | --- |
| Registry/schema | Exact tools for every capability combination; invalid R-without-S startup; fixed inventory; strict input/output schemas; accurate effect annotations; Settings count matches actual `tools/list`. |
| Scope | Pinned/workspace/project/installation reads and writes; denied second-environment replay; denied shared-project mutation from pinned/workspace scope; filtered nested project results; clone destination rules; rename does not broaden scope; more than 1,000 environments do not break authorized exact lookup. |
| Source paths | Fresh authorized checkout, sibling root explicitly configured, relative path normalization, symlink/ancestor escapes, changing symlinks at the read boundary, and no discovery execution. A denied path must reach neither discovery nor persistence. |
| Sensitive data | Sentinel secrets in nested trace spans, TCP fields, mock bodies/headers/query matchers, project declarations, export chunks, replay drafts/comparisons, errors, and stderr. I/T/C permissions alone do not expose payload data. |
| Limits | One-item and many-item responses near 1 MiB; worst-case escaped JSON; one route larger than a response preview; one recorded exchange spanning chunks; 25 MiB replay body accepted and one byte over rejected before dispatch. No mutation receipt disappears because a body is too large to display. |
| Replay | Every route requires the new MCP declaration; zero application calls during prepare/edit/get/close; exact run deduplication; lost response recovery; stale identity/revision/provider generation; read-only remote writes denied; explicit remote-write review; complete/partial/unavailable comparison; session close and idle fallback. |
| Mocks | Saved ordering and matcher semantics; route rename/enable; preview with complete unsaved draft; no events/traffic from preview; whole-scenario provider restoration; peers unchanged; import warnings; no blind import retry; partial bulk restoration and bounded waits. |
| Recording/faults | Metadata and payload capture gates; duplicate-name bounds; full export above 10,000 events and 16 MiB; stable active-recording snapshot; name reuse/deletion invalidates cursor; UTF-8/base64 integrity; no fault expiry extension or unbounded rule activation. |
| Configuration/cleanup | Correct MCP actor; revision conflicts; stopped-state guards; no implicit lifecycle changes; exact reviewed deletion; stale previews rejected; new traffic after clear preview retained; no volume/source deletion; original workspace services unaffected by clone operations. |
| Transport/recovery | Real stdio large inputs, JSON-RPC-only stdout, EOF/cancellation cleanup, rate/concurrency bounds, one reconnect for reads, no automatic retry of uncertain mutations, and compact receipts after wait timeout. |

Use focused test files beside each owner instead of putting all new behavior in
`portless-mcp/server_test.go`. Retain central inventory/schema/stdio tests there.
For shared daemon changes, test the owning control-plane/database/API behavior
as well as the MCP mapping. Run architecture guards after imports/exports.

### Isolated end-to-end journeys

Extend `tests/e2e/mcp_test.go` or split focused `mcp_*_test.go` files using the
existing compiled executable, official SDK client, isolated `PORTLESS_HOME`, and
temporary source fixtures. Read `docs/e2e-testing.md` before modifying the suite.

The primary release test performs control actions through MCP only:

1. Start MCP in a fresh authorized checkout; discover the project and start its
   environment. The test harness may prepare fixtures and generate an ordinary
   application request to provide a captured baseline.
2. Query exchanges and traces; inspect the HTTP root and a decoded dependency
   span, with correct attribution and confidence.
3. Start a payload recording, generate fixture traffic, stop it, export it in
   chunks, and reconstruct/validate the full schema-4 document.
4. Create a mock scenario and route, preview a complete draft without application
   traffic, activate it, and inspect the effective mock binding.
5. Prepare/edit/run one captured HTTP request and inspect the daemon's comparison.
   Assert one application request per admitted run and recover a deliberately
   lost response from its receipt.
6. Restore the original provider, enable/disable a finite fault, close replay,
   and delete the temporary mock/recording/fault through preview/apply calls.
7. Stop the isolated environment through MCP and verify no debugging artifact
   remains active. Harness cleanup remains a final isolated safety fallback.

Additional journeys cover project-scoped clone/checkouts/provider transitions,
multi-source add/remove and rescan, scoped rename/forget, large export integrity,
and a second environment outside scope. A loopback fixture represents remote
providers; prove denied writes produce zero upstream calls.

Update `portless-web/e2e/settings.spec.ts` for generated configurations and
permission combinations. Update recording-export and cleanup UI/CLI journeys
where their shared API changes. Existing replay and mock tests must continue to
pass against the same daemon behavior.

Required non-destructive validation before implementation handoff:

```bash
go test ./portless-mcp ./portless-cli/administration ./tests/architecture
go test ./portless-daemon/api/contract ./portless-daemon/api/client ./portless-daemon/api/server
go test ./portless-daemon/controlplane ./portless-daemon/database
go test -race ./portless-mcp
make lint
make test
make test-e2e-cli
make test-e2e-ui
git diff --check
```

Add focused replay/mocks/export owner tests while developing. The suite uses
isolated homes and fixture services; no destructive relay suite, real-install
reset/uninstall, or incidental shutdown of the developer's services is required.
Report any skipped check with its exact reason.

### Definition of done

Release parity is complete when all proposed tool workflows pass, scope and
payload boundaries hold across nested results and continuations, large exports
are complete, uncertain replay sends recover without duplicate requests, and
CLI/UI/MCP observe the same saved state and provider behavior. Documentation and
Settings must describe the actual shipped inventory. Passing isolated tests is
evidence for this feature; it does not replace the broader clean-machine and
release-candidate validation gates.

## 13. Intentional exclusions

- Relay install/uninstall/restart, daemon replacement/stop, runtime reset,
  installation reset/uninstall, volume destruction, arbitrary host commands,
  filesystem browsing, and arbitrary URL fetching.
- Installation-wide doctor and daemon/relay/runtime diagnostic views. Existing
  scoped environment/service health and structured errors remain available;
  broader machine diagnostics are a separate administration capability decision.
- MCP equivalents of browser opening, copy buttons, theme/color preferences,
  shell completion, UI navigation, and saved CLI checkout selection.
- A permanent MCP event subscription service. Bounded polling covers the current
  inspection and operation workflows; existing daemon events still feed the UI.
- Features the product itself has not shipped: saved replay collections,
  assertions/test suites, whole-recording execution, deterministic data restore,
  stateful mocks, WebSocket message capture/replay, or OTLP ingestion.

These exclusions should be stated as the release boundary rather than reported
as missing application-workflow parity.
