# Package-structure refactor plan

Status: implemented and hardened on 2026-08-16. The repository architecture
tests now enforce the final dependency graph and retired package/relay names.

This refactor establishes explicit process, transport, lifecycle, and installation boundaries before traffic tracing or other feature work resumes. Portless is still version 1, so the implementation will update all internal callers directly and will not leave compatibility packages, forwarding aliases, or duplicate implementations behind.

## Objectives

- Make the repository layout match the running system: one CLI, one per-user daemon and API, and one privileged relay.
- Make ordinary CLI commands and the web UI clients of the same daemon HTTP API.
- Turn `internal/daemon` into the daemon composition and lifecycle boundary instead of a directory containing only protocol DTOs.
- Split API contracts, the authenticated client, and the HTTP server so none of them depends on `bootstrap`.
- Replace `internal/ingress` with a relay package while retaining “ingress” where it accurately means inbound application traffic.
- Move installation paths, private-file validation, and state removal into a package that does not know about the CLI or HTTP API.
- Delete `internal/bootstrap`, whose current responsibilities span all of those areas.
- Preserve user-visible behavior, wire routes, persisted data, safety checks, and runtime ownership semantics while normalizing pre-release privileged relay identifiers.

## Non-goals

- No traffic tracing implementation; that remains deferred in `docs/plans/traffic-tracing.md`.
- No discovery, resource-plugin, runtime, proxy, store, or application behavior redesign.
- No database schema or persisted project-model change.
- No UI redesign and no direct UI access to daemon internals.
- No decomposition of the large `application.Service` in this pass.
- No automatic OpenAPI code generation in this pass.
- Keep the existing `ingress.sock` name because it accurately identifies the daemon's private application-ingress socket.

## Current problems

| Current area | Responsibilities mixed together |
| --- | --- |
| `internal/bootstrap` | Installation paths, private files, build identity, daemon discovery, process startup, authenticated lifecycle control, API client transport, daemon composition, reset, and uninstall state removal. |
| `internal/api` | API version and error contracts, routing, authentication, every handler, application-host ingress, SSE, and embedded UI serving in one 1,500-line file. |
| `internal/daemon` | Lifecycle wire DTOs only; it does not run or control the daemon. |
| `internal/ingress` | HTTP/TCP relay process, DNS relay, health checks, privileged installer, platform configuration, ownership receipt, and uninstall. |
| `internal/cli` | Command construction and rendering plus raw API path construction, daemon process lifecycle, relay installation, reset/uninstall orchestration, and several API request/response DTOs. |
| `cmd/portless` | Top-level process dispatch plus argument parsing and execution logic for every hidden process mode. |

The result is misleading ownership and undesirable imports. For example, the CLI imports `application`, `runtime/container`, and `project/discovery` for types even though normal commands communicate with the daemon, while the API server imports the concrete relay installer merely to report relay status.

## Target layout

```text
cmd/portless/
  main.go                         process-mode dispatch only

internal/
  api/
    contract/
      version.go                  API version and common envelopes
      daemon.go                   daemon API request/response shapes
      relay.go                    relay status response
      project.go                  project request/response shapes
      environment.go              environment request/response shapes
      runtime.go                  runtime request/response shapes
      traffic.go                  existing traffic shapes until tracing work
      events.go                   SSE envelope/topic definitions
    client/
      client.go                   authenticated JSON transport
      system.go
      projects.go
      environments.go
      services.go
      runtime.go
      traffic.go
      experiments.go
      events.go
    server/
      server.go                   construction and top-level host routing
      routes.go                   API route dispatch
      auth.go                     claim/session/mutation guards
      daemon.go
      relay.go
      runtime.go
      projects.go
      environments.go
      services.go
      traffic.go
      experiments.go
      operations.go
      assets.go                    embedded UI fallback
      response.go                 JSON/decode/method helpers
      errors.go                   domain-to-API error mapping

  cli/
    app.go                        CLI dependencies and Run
    root.go                       root command and global flags
    project.go
    environment.go
    service.go
    runtime.go
    traffic.go
    experiments.go
    daemon.go
    relay.go
    maintenance.go                reset and uninstall commands
    completion.go
    context.go                    cwd/environment selection
    output.go                     JSON and human rendering helpers
    color.go
    errors.go

  daemon/
    run.go                        daemon composition root
    sockets.go                    control/HTTP-ingress/DNS listeners
    watch.go                      executable replacement watcher
    command.go                    hidden daemon-mode argument handling
    lifecycle/
      contract.go                 identity/shutdown protocol and versions
      handler.go                  authenticated lifecycle HTTP handler
    control/
      manager.go                  lifecycle facade used by CLI/diagnostics
      inspect.go                  authenticated identity/compatibility checks
      launch.go                   serialized lazy daemon startup
      stop.go                     guarded shutdown and verified signal fallback
      connect.go                  construct API clients after verification
      reset.go                    stop/reset/restart coordination
      uninstall.go                stop and remove installation state coordination
    instance/
      record.go                   private daemon discovery record shared with the daemon process

  relay/
    relay.go                      privileged HTTP stream relay
    dns.go                        privileged UDP/TCP DNS relay
    health.go                     public relay and private-socket health probes
    command.go                    hidden relay-mode argument handling
    install/
      manager.go                  install/restart/inspect/uninstall operations
      status.go                   ownership and installation status
      receipt.go                  validated ownership receipt
      platform_darwin.go
      platform_linux.go
      platform_unsupported.go

  installation/
    layout.go                     resolve all paths under the data root
    private.go                    private directory/file validation
    identity.go                   installation and executable fingerprints
    reset.go                      remove application state from a stopped install
    remove.go                     inspect/remove a stopped complete installation

  application/
  auth/
  diagnostics/
  dns/
  events/
  model/
  networking/
  project/
  proxy/
  resource/
  runtime/
  store/
```

The listed files are responsibility boundaries, not a requirement that every file have exactly one type. If two adjacent files remain small, they can be combined without changing the package boundaries.

## Required dependency direction

```text
cmd/portless
  -> cli
  -> daemon                    only for the hidden daemon process mode
  -> relay + relay/install     only for hidden privileged relay modes
  -> runtime/supervisor        only for the hidden service-runner mode

cli
  -> api/client + api/contract
  -> daemon/control
  -> relay/install
  -> installation
  -> diagnostics
  -> project/discovery            only in app.go dependency composition for checkout-root lookup

daemon
  -> api/server
  -> daemon/lifecycle
  -> application, auth, dns, events, store, webui
  -> installation

api/server
  -> api/contract
  -> application + auth
  -> injected daemon and relay status interfaces

daemon/control
  -> api/client + api/contract
  -> daemon/instance
  -> daemon/lifecycle
  -> installation

api/client
  -> api/contract

relay/install
  -> relay, dns, networking

relay
  -> dns and other low-level networking helpers

api/contract
  -> model where an existing domain value is also the wire value

installation
  -> Go standard library only
```

The following reverse dependencies are forbidden:

- Domain packages such as `application`, `project`, `proxy`, `resource`, `runtime`, and `store` may not import `cli`, `api/server`, `daemon`, or `relay/install`.
- `api/client` may not import CLI, daemon control, server, application, store, or relay packages.
- `api/server` may not import CLI, daemon control, or the concrete relay installer.
- `relay` and `relay/install` may not import the daemon, API server, application, or CLI.
- `installation` may not import any other Portless package.
- `daemon/control` may not import `api/server`, `application`, `store`, or the daemon composition package.

Add a standard-library-only architecture test that parses Go imports and fails on these forbidden edges. This prevents the cleanup from gradually collapsing back into another `bootstrap` package.

## Important behavioral boundary

“The CLI talks to the daemon API” applies to ordinary product operations:

- projects, environments, sources, bindings, services, logs, traffic, recordings, faults, operations, runtime selection, and browser claims use typed `api/client` methods;
- the web UI continues to use the same `/api/v1` routes and SSE endpoint.

The CLI must retain narrow local responsibilities where the daemon API cannot be assumed to exist:

- locate the current checkout and resolve local source paths;
- discover, authenticate, start, stop, or replace the daemon;
- inspect/install/restart/uninstall the privileged relay;
- perform guarded reset/uninstall recovery when the daemon or stored model is incompatible;
- remove a verified CLI launcher during full uninstall;
- generate shell completion without starting a stopped daemon;
- launch the user's browser.

Those exceptions call `daemon/control`, `relay/install`, or `installation`; they must not be implemented as raw process/filesystem logic scattered through command handlers.

## Current-to-target file mapping

| Current file or area | Target |
| --- | --- |
| `bootstrap/client.go` transport and `ClientError` | `api/client/client.go` and `api/contract/errors.go` |
| `bootstrap/client.go` `Connect`/`ConnectExisting` | `daemon/control/connect.go` |
| `bootstrap/control.go` | `daemon/instance/record.go` plus `daemon/control/{inspect,launch}.go` |
| `bootstrap/lifecycle.go` | `daemon/control/stop.go` |
| `bootstrap/reset.go` | `daemon/control/reset.go`, calling installation reset primitives |
| `bootstrap/daemon.go` | `daemon/{run,sockets,watch}.go` |
| `bootstrap/lifecycle_handler.go` | `daemon/lifecycle/handler.go` |
| `daemon/protocol.go` | `daemon/lifecycle/contract.go` |
| `bootstrap/paths.go` | `installation/{layout,private,reset}.go` |
| `bootstrap/identity.go` | installation identity/private-file helpers plus a daemon-local instance-ID helper |
| `bootstrap/uninstall.go` | `installation/remove.go` plus `daemon/control/uninstall.go` |
| `api/server.go` wire structs | `api/contract` |
| `api/server.go` handlers | grouped files in `api/server` |
| CLI-local API DTOs and `application.SourceInput` usage | `api/contract` request/response types |
| CLI raw `client.Do` calls and URL assembly | typed methods in `api/client` |
| `ingress/relay.go`, `ingress/dns_relay.go` | `relay/relay.go`, `relay/dns.go` |
| `ingress/setup.go` health portions | `relay/health.go` |
| `ingress/setup*.go` installation portions | `relay/install` |
| large `cli.go` and `commands.go` | responsibility-oriented files within package `cli` |
| hidden-mode parsing in `cmd/portless/main.go` | `daemon.Command`, `relay.Command`, and `relay/install` command entry points; `main` only dispatches |

Tests move with the responsibility they verify. Mixed bootstrap tests must be split rather than copied wholesale.

## Implementation phases

Each phase is a separately reviewable, compiling change. A phase is not complete while both its old and new implementation remain in the tree.

### Phase 0: freeze behavior and enforce the intended graph

1. Record the current public API version, lifecycle protocol version, route list, hidden process modes, data-root paths, relay identifiers, and E2E commands.
2. Add architecture import tests with the target rules initially scoped to packages already conforming; tighten each rule as its migration phase lands.
3. Add or retain focused characterization tests for:
   - authenticated daemon discovery and build mismatch;
   - safe handoff and active-environment refusal;
   - format-independent forced reset;
   - relay ownership validation;
   - API error envelopes and route behavior;
   - completion not starting the daemon.
4. Do not change API JSON, status codes, persisted paths, or command output in this phase.

Exit gate: the existing suite is green and the import test can express every final forbidden dependency.

### Phase 1: extract installation primitives

1. Introduce `installation.Layout` and `installation.ResolveLayout` from `bootstrap.Paths` and `ResolvePaths`.
2. Rename fields for clarity at all call sites in one change:
   - `Ingress` -> `IngressSocket`;
   - `DNS` -> `DNSSocket`;
   - `Token` -> `AuthToken`;
   - `Lock` -> `StartupLock`.
3. Move private directory and private regular-file verification to `installation/private.go`.
4. Move installation ID and executable fingerprint helpers to `installation/identity.go`; keep random daemon instance generation inside the daemon runtime.
5. Split stopped-state operations:
   - `ResetApplicationState` becomes a primitive that removes only project/runtime data while preserving installation identity and preferences;
   - installation inspection/removal validates a complete root but assumes daemon shutdown has already been coordinated.
6. Split `bootstrap/paths_test.go` so path/reset/removal tests move to `installation`; lifecycle/reset orchestration tests remain for the later control phase.
7. Update CLI, diagnostics, bootstrap, relay tests, and E2E harnesses to use `installation.Layout` directly. Do not leave `type Paths = installation.Layout` behind.

Exit gate: installation has only standard-library imports; path safety and symlink/ownership tests pass; no code imports `bootstrap` merely to obtain paths or installation identity.

### Phase 2: establish API contracts and transport client

1. Create `api/contract` and move `APIVersion`, error envelopes, remediation values, daemon/relay status responses, and request/response DTOs out of server and CLI files.
2. Add named contract types for currently anonymous request/response bodies, including project discovery, source inputs, environment context, up/down requests, runtime reset, browser claims, list envelopes, and mutation results.
3. Contract response types may contain existing `model` values in this refactor; they must not import `application`, `store`, concrete runtime implementations, daemon control, or relay installation.
4. Move the generic authenticated JSON transport and response-size limits from `bootstrap.Client` into `api/client.Client`.
5. Keep daemon discovery out of the API client. Construct it with an already verified base URL, token, and `http.Client`.
6. Change the temporary bootstrap connector to return `*api/client.Client`, then update CLI function signatures and tests. This isolates transport before daemon control moves.
7. Add transport tests for authentication headers, JSON encoding/decoding, error envelopes, response limits, empty responses, and cancellation.
8. Check `api/openapi.yaml` against the named contracts and correct documentation drift without changing runtime behavior.

Exit gate: CLI code no longer refers to `bootstrap.Client` or `bootstrap.ClientError`; API version and wire error types have exactly one owner.

### Phase 3: extract the daemon lifecycle protocol and handler

1. Move lifecycle paths, protocol version, identity, shutdown request/response, and lifecycle errors into `daemon/lifecycle`.
2. Move the authenticated lifecycle HTTP handler into that package and give it explicit callbacks for active environments, handoff status, shutdown, and replacement.
3. Keep identity available during incompatible application-state recovery exactly as it is now.
4. Make the handler expose browser-safe status/restart operations through an adapter returning `api/contract` types; the API server should not import daemon control.
5. Move lifecycle handler tests and preserve checks for control-host enforcement, CLI-only shutdown, active-environment refusal, forced shutdown, browser restart, and inventory failure.
6. Remove the old root `internal/daemon` protocol package once all imports use `daemon/lifecycle`.

Exit gate: lifecycle contracts and handler are independent of daemon composition and control-process discovery.

### Phase 4: extract daemon control

1. Introduce `control.Manager` holding `installation.Layout` and injectable process/clock/HTTP hooks for tests.
2. Move the private discovery `Record` and atomic read/write into `daemon/instance/record.go`, so both the daemon process and client-side control package depend on a neutral owner rather than the daemon importing its controller.
3. Move authenticated inspection and compatibility checks into `inspect.go`.
4. Move serialized lazy startup, startup lock handling, detached process launch, readiness polling, and log-tail diagnostics into `launch.go`.
5. Move graceful shutdown, handoff checks, verified PID fallback, signal escalation, and wait logic into `stop.go`.
6. Make `Connect` and `ConnectExisting` return `api/client.Client` only after daemon identity and installation ownership are verified.
7. Move reset orchestration into `control.Manager.ResetApplicationState`: acquire the startup lock, authenticate/stop the daemon, call the stopped-state installation primitive, start an empty daemon, and verify readiness.
8. Split uninstall state handling:
   - `control.Manager.RemoveInstallationState` authenticates/stops the daemon and serializes against startup;
   - `installation.RemoveStoppedState` performs the final validated filesystem detach/removal and never starts a replacement.
9. Move control/reset/uninstall orchestration tests into `daemon/control`, including the forced-reset regression and failure to authenticate a live recorded PID.
10. Update CLI and diagnostics to depend on `control.Manager`; remove global bootstrap lifecycle calls.

Exit gate: no daemon process discovery, startup, stop, reset, or authenticated-connect code remains under `bootstrap` or `cli`.

### Phase 5: split the API server

1. Move the server to package `api/server`; update the daemon composition code to import it.
2. Introduce a constructor dependency object containing:
   - `*application.Service`;
   - `*auth.Manager`;
   - embedded UI assets;
   - a narrow daemon status/restart interface using `api/contract`;
   - a narrow relay-status function using `api/contract`.
3. Remove the direct concrete `ingress`/relay-installer import from the HTTP server. The daemon composition root supplies the adapter.
4. Split top-level routing, auth/session handling, domain handlers, SSE, assets, JSON helpers, and error mapping into the target files without changing route behavior.
5. Replace anonymous wire objects with named `api/contract` values.
6. Add `application.Service` facade methods for application ingress and event subscription if needed so the server does not reach through `Proxy()` or `Broker()` accessors.
7. Preserve host separation: application hosts route only application ingress, and control/auth routes remain unavailable there.
8. Split `server_test.go` by route group while keeping one shared authenticated test harness.

Exit gate: `internal/api` contains only `contract`, `client`, and `server`; server tests prove existing routes, status codes, authentication, SSE summaries, and UI fallback still work.

### Phase 6: make `internal/daemon` the composition root

1. Move daemon assembly from `bootstrap/daemon.go` into `daemon.Run` with an explicit config containing `installation.Layout` and preferred control port.
2. Keep construction in one place: store, broker, application service, auth manager, lifecycle controller, API server, HTTP listeners, DNS server, and embedded assets.
3. Move private Unix socket validation/listening into `daemon/sockets.go` and retain refusal to replace non-socket paths.
4. Move executable watching and handoff-triggered replacement into `daemon/watch.go`.
5. Make `cmd/portless` create the signal context; `daemon.Run` responds to context cancellation and typed shutdown/replacement reasons instead of installing its own OS signal handler.
6. Move hidden daemon-mode flag parsing into `daemon.Command`; keep `syscall.Exec` replacement at the process-entry boundary.
7. Move daemon socket and executable-watcher tests into the daemon package.
8. Update diagnostic labels and comments while retaining the valid distinction between the daemon's application-ingress socket and the privileged relay.

Exit gate: `cmd/portless` does not compose the daemon, and `bootstrap` no longer runs a server.

### Phase 7: split and rename the relay package

1. Move the HTTP and DNS data plane from `internal/ingress` to `internal/relay`.
2. Move public health checks and private target-socket probes to `relay/health.go`.
3. Move install/restart/inspect/uninstall, ownership status, receipts, and platform code to `relay/install`.
4. Make CLI and diagnostics call `relay/install`; make the API receive only a mapped contract status through constructor injection.
5. Move hidden relay-mode parsing out of `cmd/portless`; `main` dispatches to relay entry points.
6. Rename Go identifiers and user-facing code comments that incorrectly call the privileged process “ingress.” Keep “application ingress” for the daemon-side routing concept.
7. Name privileged resources consistently as relay resources: `dev.portless.relay` on launchd, `portless-relay.service` on systemd, relay helper/receipt paths, and `__relay`/`__*-relay` private modes. Portless is pre-release version 1, so there is no migration or compatibility alias for the earlier development-only identifiers. Keep `ingress.sock` because it belongs to the daemon's application-ingress boundary.
8. Move unit tests with their code and update the destructive relay E2E imports without changing its safety gates.
9. Remove `internal/ingress` completely; do not leave a forwarding package.

Exit gate: the package name reflects the relay process, installed-resource ownership tests still pass, and application-ingress terminology remains semantically correct.

### Phase 8: thin and split the CLI

1. Give `CLI` explicit dependencies for daemon control, API-client construction, relay installation, diagnostics, browser launch, clock, and filesystem location where tests need substitution.
2. Split `cli.go` and `commands.go` by user-facing resource using the target filenames; keep one `cli` package so shared global flags and output policy remain straightforward.
3. Implement typed `api/client` methods by endpoint group and replace raw paths in CLI code. Path escaping belongs to the client, not command handlers.
4. Move API request/response shapes to `api/contract`; remove CLI imports of `application`, concrete container implementations, and relay runtime types that exist only to share structs. Permit `project/discovery` only in `app.go`, where the injected checkout-root dependency is composed; command handlers cannot import it.
5. Keep `model` imports only for actual domain values returned by contracts and rendered by the CLI.
6. Centralize environment resolution, idempotency headers, operation waiting, SSE reconnect/deduplication, and client-error rendering.
7. Keep completion on the read-only `ConnectExisting` path. It must never install, start, replace, reset, or repair anything.
8. Keep daemon, relay, reset, and uninstall commands as explicit local-control exceptions described above.
9. Split tests by command area and use fake typed clients/control interfaces rather than broad `httptest` servers where HTTP transport is not what the test is verifying.

Exit gate: normal CLI commands use typed API-client methods; raw `/api/v1` strings exist only in API client/server tests and transport packages; command files focus on validation, invocation, and presentation.

### Phase 9: delete the transitional package and harden the result

1. Remove the final `internal/bootstrap` files and directory.
2. Tighten architecture tests to the complete dependency rule set.
3. Add package comments documenting ownership and allowed dependencies for `api/contract`, `api/client`, `api/server`, `daemon`, `daemon/control`, `daemon/lifecycle`, `relay`, `relay/install`, and `installation`.
4. Update README architecture text, E2E documentation, OpenAPI descriptions, and implementation status where package names or relay terminology changed.
5. Confirm the generated `webui/dist` remains generated and is changed only through the web build.
6. Search for stale package names, obsolete type names, duplicate API DTOs, and raw API route construction outside allowed packages.

Exit gate:

```text
rg 'internal/bootstrap|internal/ingress' --glob '*.go'     # no matches
rg '"/api/v1/' internal/cli                              # no matches
go list ./...                                             # no import cycles
```

## Verification gates

Run focused tests after each phase, then the following before declaring the refactor complete:

```text
gofmt on every changed Go file
go test ./...
go test -race ./internal/api/... ./internal/daemon/... ./internal/relay/... ./internal/installation/... ./internal/cli/...
go vet ./...
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
make test-e2e-cli
make test-e2e-ui
go vet -tags=e2e ./cmd/portless ./tests/e2e
git diff --check
```

Run `make test-e2e-relay-destructive` only on a host deliberately prepared for its privileged install/uninstall cycle. Its existing opt-in environment guard must remain.

The end-to-end acceptance scenarios are:

1. A normal CLI command lazily starts and authenticates the daemon, then completes through the API.
2. CLI completion with no daemon remains quiet and non-mutating.
3. The UI and CLI observe the same environment/service state and API errors.
4. An active environment survives a safe daemon handoff and is rejected when handoff cannot be proven.
5. `down --all` still works without checkout selection.
6. `reset --force --yes` still recovers format-incompatible active state.
7. Relay install/status/restart/uninstall retains ownership and target-socket checks.
8. Application `.localhost` ingress and `.portless.test` TCP discovery still traverse the real relay and daemon boundaries.
9. Reset preserves installation identity/preferences; uninstall removes only the verified installation root and owned runtime/relay resources.

## Review checkpoints

The implementation should pause for review at these boundaries:

1. **Target graph approval:** package names, dependency rules, and the explicit CLI local-control exceptions.
2. **Foundation approval:** installation, API contract/client, and daemon control extracted; confirm the abstractions are useful before moving the server.
3. **Process-boundary approval:** daemon and relay packages own their real processes and `bootstrap` is nearly empty.
4. **CLI approval:** typed client surface and command file organization before deleting the final transitional code.
5. **Completion approval:** import-graph test, full E2E evidence, and documentation diff.

## Completion criteria

- The repository visibly contains the four expected primary areas: API, CLI, daemon, and relay.
- `internal/bootstrap` and `internal/ingress` do not exist.
- API contracts, API client transport, and API server transport have distinct packages.
- The daemon package is the only composition root for the long-running per-user service.
- The relay package owns only relay data-plane behavior; privileged installation is isolated under `relay/install`.
- Installation filesystem safety has a standard-library-only package boundary.
- Ordinary CLI behavior reaches application state exclusively through typed daemon API clients.
- UI behavior remains exclusively API-based.
- Lifecycle, reset, uninstall, and completion retain their fail-closed safety properties.
- Architecture tests prevent forbidden import directions.
- Full unit, race, vet, web, CLI E2E, and UI E2E gates pass.
