# Initial implementation boundary

This repository is a runnable vertical implementation of the product direction, not a claim that every public-release hardening milestone is finished.

## Complete in this slice

- Reusable logical projects, named environments, and a top-level environment HTTP API.
- Static single- and multi-checkout discovery, cross-source dependency compilation, generated declaration export, and name conflict responses.
- Environment cloning, per-checkout selection, per-environment Git-worktree paths, and independently mutable component providers.
- Local process, managed Docker/Podman container, and remote HTTP(S) providers with classification, health checks, and local read-only enforcement.
- Singleton daemon bootstrap, private control record, read-only doctor diagnostics, Cobra command tree with generated shell completion, CLI token, browser claim/session/CSRF flow, and embedded UI.
- Idempotent macOS launchd or systemd Linux setup, status, repair, and uninstall for clean port-80 `.localhost` URLs, with a root-owned ownership receipt; the relay binds loopback as root, drops privileges, and forwards only to the private per-user Unix socket.
- Process and Docker Engine/Podman lifecycle for the discovered first-release templates, including automatic and explicit runtime selection.
- Stable application ingress, internal edge proxies, live event stream, redacted traffic summaries, bounded named recordings, and scoped named faults.
- Project/environment UI, provider and source configuration, topology, service detail/configuration/logs, traffic, recording, fault, timeline, and command-palette views.
- A greenfield SQLite schema with durable logical project, environment, runtime, operation, timeline, recording, fault, and recorded-traffic state.
- Unit and integration coverage for the principal lifecycle, routing, and experiment paths.

## Deliberately deferred from a hardened public release

- Crash reconciliation that re-adopts host processes after the daemon itself is killed. Containers remain owned and discoverable by labels, but this build does not fully reconstruct all proxy targets after restart.
- Editable discovery overrides beyond source paths, change previews, and declaration import.
- Log rotation and push-based log SSE. Logs are per-generation and the CLI follows them by bounded polling.
- Optional request/response body capture, query-value redaction, HAR export, and traffic table virtualization. The current recorder is metadata-first and JSON-only.
- TCP-specific reset/drop-after-byte effects. TCP edges support delay/rejection through the shared fault model; advanced stream effects remain.
- Operation cancellation, bounded-parallel graph execution, long-lived port lease recovery, and resource-cap enforcement under load.
- Container-engine log aggregation in the service log drawer.
- Windows support, nerdctl adapters, packaging, notarization, upgrade rollback tooling, SBOM generation, and release soak tests.

These items should be treated as release gates, not hidden behavior. The architecture keeps them behind store, runtime, proxy, and API boundaries so they can be added without changing the public naming model or user workflow.
