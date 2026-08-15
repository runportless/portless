# Initial implementation boundary

This repository is a runnable vertical implementation of the product direction, not a claim that every public-release hardening milestone is finished.

## Complete in this slice

- Reusable logical projects, named environments, and a top-level environment HTTP API.
- Static, bounded, plugin-driven single- and multi-checkout framework discovery for Spring Boot, NestJS, Express, Fastify, Next.js, Go HTTP/RPC services, and FastAPI; atomic incremental source addition, cross-source dependency compilation, generated declaration export, and fail-closed name conflict responses.
- A separate managed-resource registry with plugin-owned detection, version and client port resolution, declarative container plans, readiness, persistent named volumes, generated credentials, and redacted process bindings. PostgreSQL, Valkey, MySQL, and NATS are built in; a fixture plugin verifies that the shared engine and runtime require no resource-specific branch.
- Environment cloning, per-checkout selection, per-environment Git-worktree paths, and independently mutable component providers.
- Local process, plugin-backed managed Docker/Podman resource, and remote HTTP(S) providers with classification, health checks, and local read-only enforcement.
- Singleton daemon bootstrap, authenticated installation/instance/executable identity, guarded CLI and UI status/restart controls, automatic replacement of handoff-ready outdated builds, startup runtime reconciliation, private control record, read-only doctor diagnostics, Cobra command tree with generated shell completion, CLI token, restart-safe browser claim/session/CSRF flow, and embedded UI.
- Idempotent macOS launchd or systemd Linux first-run setup plus explicit relay install, status, restart, repair, and uninstall commands for clean port-80 `.localhost` URLs and scoped `.portless.test` TCP DNS, with a root-owned ownership receipt; the relay binds only its HTTP and DNS loopback addresses as root, drops privileges, and forwards to separate private per-user Unix sockets.
- Persistent authenticated process supervisors and a generic, label-owned Docker Engine/Podman lifecycle for validated resource plans, including daemon-crash adoption, automatic and explicit runtime selection, per-service start/stop/restart operations, plan-identity verification, and restart accounting.
- Stable application ingress, durable collision-free TCP loopback/DNS allocations, effective-connection inspection, source-aware internal edge proxies, a unified filtered HTTP/TCP traffic API, traffic detail with redacted request/response headers, bounded named recordings, and scoped named faults.
- Structured process and container logs across generations, merged service views, time and count filters, 10-generation retention, and a 16 MiB cap per generation stream.
- Human-first CLI inspection for projects, services, effective connections, timelines, recordings, and faults; every command also supports machine-readable `--json` output. `portless down --all` performs a checkout-independent, failure-aggregating machine-wide shutdown, with explicit confirmation before bulk volume removal. A guarded `portless reset` previews and, with `--yes`, clears stopped application state and installation-owned runtime resources while preserving Portless installation and preference state. `--force --yes` also purges active, unknown, or format-incompatible environments after authenticating their supervisors and installation-labeled container resources; daemon identity and reset planning remain available through normalized runtime ownership when versioned topology decoding fails. A separate preview-first `portless uninstall` performs failure-barriered full removal of those runtimes, the owned HTTP/DNS relay and resolver, daemon/data state, and a safely classified CLI launcher without deleting source builds.
- Context-aware shell completion for environment selectors and daemon-owned names. Completion is quiet and never starts or replaces the daemon.
- Project/environment UI, provider and source configuration, topology, service detail/configuration/logs, traffic, recording, fault, timeline, and command-palette views.
- A greenfield SQLite schema with durable logical project, environment, daemon ownership, service/supervisor runtime, dependency-listener, operation, timeline, recording, fault, and recorded-traffic state. Persisted discovery models carry an explicit format version and reject incompatible pre-resource state with reset-and-rediscover guidance.
- Unit and integration coverage for the principal lifecycle, routing, and experiment paths.

## Deliberately deferred from a hardened public release

- Zero-gap daemon upgrades and automatic rollback. A safe handoff keeps application processes and containers alive and restores their ingress and dependency listeners on the same ports, but requests can briefly fail while the old daemon releases listeners and the new daemon verifies ownership. If a supervisor, container label, or saved listener cannot be authenticated, Portless marks the service `unknown` and refuses to launch a duplicate; recovery remains an explicit operator action.
- Editable discovery overrides beyond source paths, change previews, and declaration import.
- Installation and sandboxing of third-party discovery or resource plugins. Version 1 registers trusted in-process plugins at build time; any external plugin mechanism requires a separate versioned subprocess and permission design.
- Push-based log SSE and byte-level rotation within an active generation. The CLI follows retained structured logs by bounded polling; generation count and file size are capped.
- Optional request/response body capture, query-value redaction, HAR export, and traffic table virtualization. The current recorder is metadata-first and JSON-only.
- TCP-specific reset/drop-after-byte effects. TCP edges support delay/rejection through the shared fault model; advanced stream effects remain.
- Operation cancellation, bounded-parallel graph execution, long-lived port lease recovery, and resource-cap enforcement under load.
- Windows support, nerdctl adapters, packaging, notarization, upgrade rollback tooling, SBOM generation, and release soak tests.

These items should be treated as release gates, not hidden behavior. The architecture keeps them behind store, runtime, proxy, and API boundaries so they can be added without changing the public naming model or user workflow.
