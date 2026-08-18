# Future Portless features

Portless should become the shortest path from finding a local application bug
to producing a repeatable diagnosis that another developer or CI job can run.
The existing environment lifecycle, stable routing, debugging, traffic,
recording, fault, UI, CLI, and MCP foundations make that a more distinctive
direction than expanding into a generic container orchestrator.

## Recommended priorities

### 1. Reliable daemon and relay upgrades

Finish the lifecycle foundation before adding more runtime surface area:

- Detect when the installed privileged relay helper differs from the current
  Portless build and repair it through `portless relay install`.
- Minimize or eliminate the ingress gap during daemon replacement.
- Roll back automatically when a replacement cannot reconcile owned runtimes.
- Add supported packaging, signing, notarization, and upgrade tooling.
- Preserve explicit ownership checks and never replace an unverified process or
  system helper.

### 2. Reproduction bundles

Save everything needed to reproduce a diagnosis into one portable artifact:

- Project topology and environment bindings.
- Source revisions and checkout metadata.
- Sanitized effective configuration.
- Relevant logs, traces, and recorded exchanges.
- Fault rules and their scopes.
- References to compatible resource snapshots or seeds.

Example workflow:

```bash
portless reproduce save checkout-timeout
portless reproduce inspect checkout-timeout.portless
portless reproduce run checkout-timeout.portless
```

Bundles must exclude Portless ownership keys, daemon authentication material,
credentials, and headers already classified as sensitive. Inspection should be
possible before anything is started or restored.

### 3. Traffic replay and comparison

Allow a developer to replay one exchange or an entire trace against the current
service, another environment, or another source revision. Present structured
differences in status, headers, response body, errors, and latency.

Recorded downstream responses could also become bounded temporary simulations,
allowing a service to be debugged without every original dependency. Hoverfly's
capture-and-simulate workflow demonstrates the usefulness of this model:
[Hoverfly documentation](https://docs.hoverfly.io/_/downloads/en/stable/pdf/).

Example workflow:

```bash
portless traffic replay 142
portless traffic replay 142 --against store/qa-assisted
portless recording simulate checkout-bug --target inventory
```

Replay must retain the existing remote write-policy enforcement and require
explicit confirmation before sending a captured mutating request to a remote
provider.

### 4. Named data snapshots and seeds

Provide repeatable application data without copying potentially inconsistent
raw volumes:

```bash
portless snapshot create before-migration
portless snapshot restore clean-store
portless seed apply checkout-demo
```

Resource plugins should own consistent export and import behavior, such as
`pg_dump` and restore for PostgreSQL. Snapshot metadata should include the
resource plugin, engine version, schema version, environment, creation time,
and compatibility constraints. LocalStack's state-management model shows the
value of reusable local state and the importance of detecting incompatible
versions: [LocalStack state management](https://docs.localstack.cloud/aws/developer-tools/snapshots/).

### 5. Scenario runner

Compose setup, data, traffic behavior, requests, assertions, artifacts, and
cleanup into a repeatable workflow:

```bash
portless scenario run checkout-during-inventory-timeout
```

A scenario should be able to:

1. Start or select an environment.
2. Restore a snapshot or apply a seed.
3. Enable named fault rules.
4. Start a bounded recording.
5. Execute HTTP requests or an approved command.
6. Assert service state, response status, trace shape, or captured traffic.
7. Export a reproduction bundle on failure.
8. Restore faults and stop resources it started.

The same scenario contract should run locally and headlessly in CI. Garden's
workflows provide a useful reference for portable local and CI automation:
[Garden workflows](https://docs.garden.io/features/workflows).

### 6. Exact OpenTelemetry integration

Accept OTLP traces and merge them with Portless proxy observations. Optionally
launch supported Node.js and JVM services with standard auto-instrumentation so
that currently inferred relationships can become exact traces.

This should support:

- W3C trace-context extraction and injection.
- Exact parent/child relationships across HTTP services.
- Correlation of application spans with proxy exchanges.
- Database and messaging spans that cannot be inferred from HTTP alone.
- A clear distinction between application-reported and Portless-observed data.

OpenTelemetry propagators are the standard mechanism for moving trace context
between processes: [OpenTelemetry propagation](https://opentelemetry.io/docs/specs/otel/context/api-propagators/).

### 7. Framework-aware watch mode

Watch relevant source files and update only the affected service. Use native
framework reload behavior where it is safe, otherwise restart the Portless-owned
process while preserving its endpoint and debugger configuration.

The UI and CLI should explain:

- Which files triggered an update.
- Whether the process reloaded, restarted, or required a full rebuild.
- How long the update took.
- Why Portless ignored a file or fell back to a slower path.

Tilt's Live Update demonstrates the value of a fast, explainable edit loop:
[Tilt Live Update](https://docs.tilt.dev/live_update_reference.html).

### 8. Richer network failures

Extend faults beyond the existing latency, jitter, status, abort, probability,
method, and path controls:

- Independent upstream and downstream behavior.
- Bandwidth limits.
- Packet or chunk loss.
- Connection reset and slow close.
- Drop or reset after a byte limit.
- TCP connection exhaustion.
- DNS errors and delayed resolution.
- TLS expiry, trust, and handshake failures.

Toxiproxy documents a proven set of development and CI network effects:
[Toxiproxy](https://github.com/shopify/toxiproxy).

### 9. Remote intercept and shadow traffic

Existing remote bindings send local traffic to an explicitly classified remote
provider. A reverse intercept could route narrowly scoped QA requests to a
local service, while shadow mode could duplicate selected requests locally
without affecting the remote response.

Any implementation must include:

- Explicit remote installation and user identity.
- Narrow service, route, caller, or session scope.
- Time limits and an immediate kill switch.
- Safe defaults that prevent writes and production interception.
- Clear UI visibility of the remote blast radius.
- Audit history for activation and removal.

Telepresence calls the equivalent remote-to-local behavior an intercept:
[Telepresence intercepts](https://telepresence.io/docs/2.26/concepts/intercepts).

### 10. Optional project declarations

Keep zero-configuration discovery as the default, but let teams export a
reviewable declaration for onboarding, CI, and drift detection:

```bash
portless project export --output portless.project.json
portless project import portless.project.json
portless project diff portless.project.json
```

The declaration should describe intended topology, sources, resource types,
health checks, and supported overrides. It should not contain machine-specific
checkout paths, generated credentials, runtime ports, or private ownership
identifiers.

### 11. More protocols and resource plugins

Potential built-in or externally installed resources include:

- Kafka or Redpanda.
- RabbitMQ.
- MinIO or another local S3 implementation.
- Elasticsearch or OpenSearch.
- Mailpit.
- LocalStack.
- DynamoDB Local.

Messaging integrations should go beyond container lifecycle and expose
publish/consume topology, bounded message metadata, trace correlation, and
message-level delay, rejection, or loss.

## Smaller improvements

- HAR export and import.
- Configurable traffic capture and retention limits.
- Saved fault presets such as slow network, unavailable database, and flaky API.
- Response and trace comparison directly from the traffic UI.
- Search across services, logs, traffic, recordings, faults, and timeline.
- Service health and restart history over time.
- Operation cancellation and bounded-parallel dependency execution.
- IDE extensions for environment selection, one-click debugger attachment,
  service logs, and trace deep links.
- Windows support and additional container-runtime adapters.
- Third-party discovery and resource plugins through a versioned, sandboxed
  subprocess protocol.

## Product boundaries

Portless should avoid becoming:

- A general-purpose container build system.
- A local Kubernetes distribution.
- A deployment or production orchestration platform.
- A complete code editor.
- A hosted APM product.
- An unbounded collection of resource-specific branches in the daemon.

New features should reinforce the local application-environment control plane:
fast startup, safe mixed local and remote composition, debugging, observable
traffic, controlled failures, and repeatable diagnoses.

## Suggested delivery sequence

1. Reliable daemon and relay upgrades.
2. Reproduction bundles and traffic replay.
3. Named data snapshots and seeds.
4. Scenario runner with local and CI execution.
5. Exact OpenTelemetry integration.
6. Framework-aware watch mode and IDE integration.
7. Richer TCP and network failures.
8. Messaging protocols, additional resources, and optional remote intercepts.

