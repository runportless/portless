# Portless MCP

`portless mcp serve` exposes the local application control plane over stdio.
The MCP host launches the distributed `portless` executable. This adapter uses
the same authenticated, typed daemon API as the CLI and embedded UI; it opens
no network listener and never returns the daemon bearer token.

The default is **24 inspection tools**. Enabling all five capabilities exposes
**64 tools**. Tool names, scope, source grants and permissions are fixed for the
lifetime of the stdio process. Tool arguments cannot grant additional access.

## Configure a client

Open **Settings → MCP** to generate client JSON or a shell command. Select an
environment, workspace, project, or installation scope and the capabilities
you need. The generator shows the actual registry count. Elevated selections
reset on reload. Replay requires selecting sensitive traffic explicitly;
Copy remains disabled until the configuration is valid.

For inspection from a checkout:

```json
{
  "mcpServers": {
    "portless": {
      "command": "portless",
      "args": ["mcp", "serve"],
      "cwd": "/Users/dev/workspace/store"
    }
  }
}
```

Verify your host supports `cwd`. Use an absolute executable path if a desktop
host cannot resolve `portless`. For one existing environment, use
`["--env", "store/local", "mcp", "serve"]`; no working directory is required.

This project-scoped configuration enables all application workflows:

```json
{
  "mcpServers": {
    "portless-store": {
      "command": "portless",
      "args": [
        "mcp", "serve", "--project", "store",
        "--source-root", "/Users/dev/workspace/store",
        "--source-root", "/Users/dev/workspace/inventory",
        "--allow-lifecycle", "--allow-traffic-control",
        "--allow-sensitive-traffic", "--allow-replay", "--allow-configuration"
      ]
    }
  }
}
```

Equivalent CLI launch:

```bash
portless mcp serve --project store \
  --source-root /Users/dev/workspace/store \
  --source-root /Users/dev/workspace/inventory \
  --allow-lifecycle --allow-traffic-control \
  --allow-sensitive-traffic --allow-replay --allow-configuration
```

The host sends JSON-RPC requests to this process. `--json` is not valid with
`mcp serve`: stdout is reserved for protocol messages and diagnostics use
stderr. Copying a configuration does not start the server or change a project.

## Scope and source grants

| Startup selection | Accessible environments | Shared project mutations |
| --- | --- | --- |
| Default workspace | Current checkout's selected or inferred environments, resolved on each call | Denied |
| `--env store/local` | That exact environment | Denied |
| `--project store` | All current environments of that named project | Allowed with configuration permission |
| `--all-environments` | Every project and environment in this installation | Allowed with configuration permission |

`--env`, `--project`, and `--all-environments` are mutually exclusive. Shared
mutations include adding/removing a logical source, rename, clone, rescan, and
forgetting a whole project. Project inspection from workspace or pinned scope
filters hidden environments and hidden clone provenance. Renaming a project
does not alter a fixed startup scope: restart the MCP host with the new name.

Configuration permits static discovery and new checkout paths only inside
canonical startup `--source-root` directories. Repeat the flag for sibling
repositories. Workspace configuration also authorizes the canonical startup
checkout. Source grants never expand environment visibility. Project,
environment and installation scopes need an explicit root to introduce paths.
Existing registered sources can be rescanned within their own directory roots.

Paths are normalized and symlink-resolved, and the daemon repeats confinement
at its filesystem read boundary using directory handles. It cannot inspect an
ancestor outside the grant or follow a replaced root/symlink outside it.
Workspace creation must associate a discovered source with the startup
checkout before anything is persisted. Discovery reads existing project files
without modifying them. It does not execute project scripts, start services,
or install a relay.

## Permissions

| Flag | Access |
| --- | --- |
| None | Safe project/environment inspection, traces, mock metadata and matching preview, logs, operations, timeline, recording/fault metadata |
| `--allow-lifecycle` (L) | Start/stop existing environments and change service state |
| `--allow-sensitive-traffic` (S) | Captured payload inspection, complete recording export and optional mock payload reads |
| `--allow-traffic-control` (T) | Bounded recording/fault operations, mock authoring, and guarded traffic/artifact cleanup |
| `--allow-replay` (R) | Reviewed HTTP replay and comparison; requires S at startup |
| `--allow-configuration` (C) | Discovery, project topology, sources, clone, checkouts, rescan and guarded metadata cleanup |

Mock activation/restoration requires **T + L**. Recording-to-mock import
requires **T + S**. Recording `capturePayloads:true` requires **S** in addition
to the recording tool's T permission. `includePayloads:true` on mock reads or
preview requires S. Service binding changes require **C + L**. Invalid replay
startup fails instead of silently granting sensitive access.

Remote bindings require explicit classification and write policy. Replay
cannot bypass read-only policy or substitute an arbitrary destination URL.
Mutations preserve the `MCP` actor on durable operations and supported timeline
events. Tool annotations describe individual effects; replay can issue writes,
imports append routes, and provider operations can start/stop local services or
contact configured remote services. Annotation hints are not permissions.

## Workflows

Fresh checkout: call `portless_discover_project` with `path` and optional
`name`, then `portless_start_environment` with its returned public selector.
Discovery needs C; starting needs L. There is no hidden discovery or creation
inside the start tool.

Mock workflow: create a named scenario, save full route drafts with
`portless_put_mock_route`, then use `portless_preview_mock` with a complete
request and optional unsaved draft. Set `draft.name` to rename the addressed
route, or `draft.enabled` to toggle it. Preview creates no traffic. Activate
with `portless_set_mock_scenario_enabled`; disabling restores the saved
providers. Mutation responses contain metadata, never saved response bodies.
Imports accept supplied OpenAPI text or a stopped recording from that same
environment; they do not fetch documents from URLs.

Replay workflow:

1. Query live traffic and retain an exchange's `sequence` and exact `startedAt`.
2. Call `portless_prepare_replay` with that identity and origin environment.
3. Submit a complete draft to `portless_update_replay` with workspace `number`,
   `createdAt`, `daemonStartedAt`, and reviewed `revision`.
4. Call `portless_run_replay` with the new revision and explicit `runNumber`.
   Supply `confirmRemoteWrite:true` only after reviewing the returned destination.
5. Inspect `portless_get_replay` and its daemon-computed comparison. Close the
   workspace with `portless_close_replay` when finished.

Prepare/edit/read/close send no application requests. A run sends one reviewed
HTTP request to the original logical source-to-target edge in an authorized
same-project environment. Every current and prior submitted destination is
scope-checked before results are returned. `waitSeconds` is bounded to 0–120.
A lost run reply triggers receipt inspection, never automatic resubmission.
`admissionUnknown` and `timedOutWaiting` do not mean that nothing ran. Earlier
run receipts never substitute the latest run's comparison. Partial/unavailable
comparison states must not be interpreted as equality.

Execution accepts the existing 25 MiB UTF-8 request body limit. MCP returns
bounded display prefixes and byte/capture metadata; a display prefix is not a
complete replacement body. Session shutdown closes only workspaces prepared by
that stdio session, with a bounded cleanup deadline and daemon idle cleanup as
fallback. Closing does not cancel an admitted run or erase its receipt.

Recording export: call `portless_export_recording` with `recording` and optional
`maxBytes` (up to 131072), then repeat with each returned `nextCursor`. Base64
decode and concatenate `data` in order until `complete:true`. The result is one
complete schema-5 JSON recording, including events beyond 10,000 and exports
larger than 16 MiB. An active recording exports the reported event-count and
sequence snapshot, excluding later traffic. Deletion, name reuse or daemon
restart invalidates continuation. Export is lossless relative to retained,
redacted captures; uncaptured bodies cannot be recovered. CLI/UI downloads use
the same complete encoder. CLI file export stages a private file and publishes
it only after a successful complete stream.

Cleanup tools default to `mode:"preview"`. Review the returned effects and
`blocked` reasons, then repeat with `mode:"apply"`, `confirm:true`, and the
complete `expected` object. Identity and revisions are checked atomically with
the deletion. Configuration previews also bind affected environments/artifacts
with a state digest. Traffic clear additionally requires `throughSequence`
from preview; newer exchanges remain. Cleanup never implicitly stops services
or deletes checkout files or managed volumes.

Re-enabling a fault requires its current `expectedRevision`, returned by
`portless_get_fault`. The rule must still have an exact edge and an unexpired
finite lifetime within one hour; enabling it preserves its original expiry.

Lifecycle/binding/mock activation return durable operations and idempotency
keys. Reuse the same key only for an explicit retry of the same action. If
admission or waiting becomes uncertain, inspect the receipt before retrying.
Append-style imports are not automatically retried. Disable All Mocks snapshots
active/degraded scenarios, restores sequentially, and reports completed,
pending and failed names; above 100 active scenarios, use named operations.

## Bounded results and input

The complete MCP response budget is 1 MiB, including both SDK text and structured
content. Result views bound headers, bodies, warnings and differences. Project
declarations and saved mock payloads use UTF-8 byte ranges with modification or
creation/revision preconditions; reassemble every range before parsing. Trace
span pages require the returned revision on continuation. Hidden environment
metadata and credential-bearing runtime fields are not returned.

Stdio frames are validated before the SDK decodes tool arguments. Ordinary
messages are capped at 8 MiB. Only an authorized replay-update envelope receives
the larger worst-case JSON-escaped body allowance. Pretty printing, embedded
newlines, fake tool names inside body text and deep nesting cannot bypass these
bounds. Output display limits remain separate from execution input limits.

The server permits eight concurrent calls and two concurrent mutations, with a
bounded token-bucket request rate. Read calls use a ten-second deadline and at
most one reconnect; mutation calls do not automatically retry. Errors preserve
stable codes and bounded redacted metadata. Logs, traffic, payloads and errors
are untrusted application data, never instructions to execute.

## Tool inventory

The canonical [`tool-inventory.json`](tool-inventory.json) supplies registration
permissions and effect annotations. `go generate ./portless-mcp` copies its
metadata for Settings; tests compare the generated fixture to real SDK
`tools/list` responses for every valid combination.

The count is `24 + 3L + 2S + 14T + 2(T∧L) + (T∧S) + 5(R∧S) + 11C + (C∧L)`.

| Tool | Required permission |
| --- | --- |
| `portless_add_project_source` | C |
| `portless_apply_fault` | T |
| `portless_change_service_binding` | C + L |
| `portless_change_service_state` | L |
| `portless_clear_traffic` | T |
| `portless_clone_environment` | C |
| `portless_close_replay` | R + S |
| `portless_create_mock_scenario` | T |
| `portless_create_project` | C |
| `portless_delete_fault` | T |
| `portless_delete_mock_route` | T |
| `portless_delete_mock_scenario` | T |
| `portless_delete_project_source` | C |
| `portless_delete_recording` | T |
| `portless_disable_all_faults` | T |
| `portless_disable_all_mock_scenarios` | T + L |
| `portless_disable_fault` | T |
| `portless_discover_project` | C |
| `portless_enable_fault` | T |
| `portless_export_project` | Inspection |
| `portless_export_recording` | S |
| `portless_forget_environment` | C |
| `portless_forget_project` | C |
| `portless_get_connection` | Inspection |
| `portless_get_environment` | Inspection |
| `portless_get_fault` | Inspection |
| `portless_get_mock_route` | Inspection |
| `portless_get_mock_scenario` | Inspection |
| `portless_get_operation` | Inspection |
| `portless_get_project` | Inspection |
| `portless_get_recording` | Inspection |
| `portless_get_replay` | R + S |
| `portless_get_service` | Inspection |
| `portless_get_service_configuration` | Inspection |
| `portless_get_timeline` | Inspection |
| `portless_get_trace` | Inspection |
| `portless_get_traffic_detail` | S |
| `portless_import_mock_openapi` | T |
| `portless_import_mock_recording` | T + S |
| `portless_list_connections` | Inspection |
| `portless_list_environments` | Inspection |
| `portless_list_faults` | Inspection |
| `portless_list_mock_scenarios` | Inspection |
| `portless_list_operations` | Inspection |
| `portless_list_projects` | Inspection |
| `portless_list_recordings` | Inspection |
| `portless_list_traces` | Inspection |
| `portless_prepare_replay` | R + S |
| `portless_preview_mock` | Inspection |
| `portless_put_mock_route` | T |
| `portless_query_traffic` | Inspection |
| `portless_read_logs` | Inspection |
| `portless_remove_source_checkout` | C |
| `portless_rename_project` | C |
| `portless_rescan_environment` | C |
| `portless_run_replay` | R + S |
| `portless_set_mock_scenario_enabled` | T + L |
| `portless_set_mock_scenario_policy` | T + L |
| `portless_set_source_checkout` | C |
| `portless_start_environment` | L |
| `portless_start_recording` | T |
| `portless_stop_environment` | L |
| `portless_stop_recording` | T |
| `portless_update_replay` | R + S |

## Release boundary

MCP does not expose daemon/relay replacement, installation/reset/uninstall,
volume destruction, shell execution, arbitrary filesystem browsing, arbitrary
URL fetching, or persisted CLI checkout selection. Browser navigation, copy
buttons, themes and shell completion are presentation features. Whole-recording
or whole-trace replay, binary/multipart/streaming replay, saved collections,
assertions and load generation remain outside the current product contract.

## Validation

Run `go test ./portless-mcp`, `go test -race ./portless-mcp`, `make lint`, and
`make test`. The compiled-product MCP journeys are in
[`tests/e2e/mcp_test.go`](../tests/e2e/mcp_test.go),
[`tests/e2e/mcp_parity_test.go`](../tests/e2e/mcp_parity_test.go), and
[`tests/e2e/mcp_configuration_test.go`](../tests/e2e/mcp_configuration_test.go), exercised by
`make test-e2e-cli`. Settings generation and permission interactions are covered
by the default `make test-e2e-ui` suite. Both suites isolate Portless state and
leave the developer's installation alone; see
[`docs/e2e-testing.md`](../docs/e2e-testing.md).

Mock scenarios expose `unmatchedRequests`: `reject` (default) replaces the service
and returns 501 for misses; `forward` keeps its real provider and forwards only
unmatched requests. Choose the policy at creation or change it with
`portless_set_mock_scenario_policy` (traffic control plus lifecycle). Policy changes
preserve routes and enabled state, return durable operation receipts, and attempt
to restore the old mode if the provider handoff fails.
Creation uses T; activation still requires T + L.
Preview returns `outcome` with a fixed nested `response`, a forwarding
`destination`, or a blocking `reason`, without application traffic. Sensitive
preview response headers and bodies remain gated by S. `service.mock` reports
partial intervention separately from provider and runtime health. Traffic
`mockOutcome` distinguishes mocked, forwarded, rejected, and blocked requests.
