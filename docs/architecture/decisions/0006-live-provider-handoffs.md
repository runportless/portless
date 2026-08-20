# ADR 0006: provider changes are service-scoped runtime handoffs

Status: accepted

An environment provider binding is both durable configuration and the routing decision for one logical service. Changing that binding on a running environment is therefore a lifecycle operation, not a synchronous database edit. `PUT /api/v1/environments/{project}/{environment}/bindings/{service}` returns a durable `change-provider` operation and accepts an idempotency key. Stopped environments still use the same operation contract, but only recompile their saved configuration.

An active transition holds the environment operation lock and revalidates the requested binding against the latest revision. A remote candidate is parsed and probed before the serving target changes. The database then updates the compiled model and one binding without deleting source bindings, service runtime ownership, connection runtime ownership, or stable endpoint allocations. The active model retains existing services and connections during the handoff so an unused dependency can be cleaned up during a later safe environment stop rather than disappearing underneath a running process.

Proxy listeners belong to directed source-to-target edges and are not rebuilt for a provider change. Every new request resolves the target service's current upstream, allowing the daemon to replace a local loopback target with a classified HTTP(S) target, or the reverse, without changing the endpoint already injected into callers. A local replacement is not published until its readiness check succeeds. Remote write policy and traffic attribution continue to be enforced by the same edge proxy.

Only the selected locally managed process is stopped or started. Other process and container runtimes keep their PID or container identity, generation, debugger state, endpoint, and connection listener. Source leases are acquired before a new local process starts and unused leases are released after completion or rollback.

If persistence, launch, readiness, or final endpoint setup fails after the old local process was stopped, the daemon restores the previous binding and target and restarts the previous local definition when necessary. A successful rollback leaves the environment derived from its actual service states while the operation remains failed and records the reason. A failed rollback marks the selected service failed instead of pretending the previous provider is available. Daemon restart interrupts the in-flight operation; normal runtime reconciliation uses the last committed binding and treats a generation-zero remote binding as recoverable configuration.

Changing or removing an environment checkout remains stopped-only. Source rediscovery can alter multiple service definitions and dependency edges, so it is not equivalent to selecting a different provider for one already-declared service.
