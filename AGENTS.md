# Portless contributor instructions

This file applies to the entire repository. A more specific `AGENTS.md` in a
subdirectory may add or override instructions for that subtree.

## Start here

Before making a material change, read the sources that define the affected
contract:

- `README.md` for the current product behavior and user workflow.
- `docs/e2e-testing.md` before changing or running an end-to-end suite.
- `portless-daemon/api/openapi.yaml` and `portless-daemon/api/events.md` when
  changing the daemon's HTTP or event boundary.

Treat the code and tests as authoritative when prose and implementation have
drifted. Correct affected documentation in the same change.

## Product principles

Portless is a local application-environment control plane, not a wrapper around
Docker Compose and not a hosted service. Preserve these product invariants:

- A developer can begin with `portless up`; a required `portless.yaml` or
  account must not become the entry cost.
- A project is one logical application that may span several repositories. An
  environment reuses that topology while choosing local, container, or remote
  providers independently.
- Public identities are readable project, environment, service, source,
  recording, and fault names. Opaque ownership keys stay private and must not
  leak into commands, URLs, or browser routes.
- Public HTTP and TCP endpoints are stable, clean names. Process ports,
  container ports, proxy listeners, and ownership keys are private runtime
  details.
- Dependency proxies preserve the source-to-target edge. Do not collapse
  source-aware routing into a target-only shortcut; traffic, recordings, and
  faults depend on caller identity.
- Discovery is static, bounded, and read-only. Discovery plugins inspect a
  root-confined workspace and return declarations; they do not execute project
  code or read arbitrary host paths.
- Remote providers require explicit classification and write policy. Enforce a
  read-only policy locally before a request leaves the machine.
- Runtime adoption and destructive cleanup fail closed when ownership cannot be
  proven. Never solve ambiguity by killing an unverified process or deleting
  an unlabeled container, volume, network, resolver, or service.
- The relay is a narrow machine-wide privilege boundary. Keep privileged input
  fixed, verify its ownership receipt, bind only the documented loopback
  listeners, and drop to the installing user before serving traffic.

This is greenfield development. Do not add compatibility packages, deprecated
aliases, forwarding commands, dual wire formats, or legacy behavior unless the
user explicitly asks for compatibility. Change a contract coherently across
all callers, tests, generated assets, and documentation instead.

## Product and package ownership

There are six top-level product roots but one Go module and one distributed
`portless` executable:

- `portless-cli`: Cobra command tree, current-checkout selection, human and
  JSON rendering, confirmations, color, completion, and browser launching.
- `portless-daemon`: daemon composition, API, control plane, state, discovery,
  providers, processes, containers, debugging, proxies, traffic, and runtime
  ownership.
- `portless-relay`: privileged HTTP/DNS relay runtime, health, installation,
  restart, ownership, and removal.
- `portless-web`: React/TypeScript control plane and the assets embedded by the
  daemon.
- `portless-site`: Astro/TypeScript public marketing site deployed separately
  to GitHub Pages; it is never embedded in the daemon.
- `portless-mcp`: local stdio MCP runtime, capability policy, scoped tool
  registry, redaction, limits, and MCP result mapping. It is consumed by the
  CLI and is not a separate executable or daemon API.

Place behavior in the product that owns it and then in a domain-oriented
package. Do not create a generic top-level or nested `internal`, `pkg`,
`common`, or standalone `api` dumping ground; a narrowly scoped helper package
must still have one explicit owner and purpose.

Important dependency rules are enforced by `tests/architecture`:

- `portless-cli/cmd/portless` is only an executable entry point.
- The `portless-cli` root is composition only. Feature behavior belongs in
  `environment`, `projects`, `observe`, `traffic`, or `administration`; shared
  CLI mechanics belong in `command`.
- CLI feature packages do not import one another. They communicate through the
  shared command context and typed daemon API.
- CLI commands use `portless-daemon/api/client`; they do not construct API
  paths or import the API server, control plane, or database.
- Only `portless-cli/administration` consumes `portless-mcp`.
  `portless-mcp` depends on the official MCP SDK and only the daemon API client
  and contract; it does not import CLI or daemon implementation packages.
- `portless-daemon/api/contract` owns wire types. The API client depends only on
  that contract, and the server adapts the contract to injected capabilities.
- `portless-daemon/control` owns out-of-process daemon inspection, startup,
  replacement, shutdown, reset, and uninstall behavior. It must continue to
  work when the feature API is unavailable.
- Daemon feature packages do not import the CLI, relay implementation, API
  server, or daemon composition root.
- The relay does not import CLI or daemon control-plane implementations.
- `portless-daemon/system/installation` uses only the Go standard library.

Run `go test ./tests/architecture` after moving packages, adding imports, or
exporting declarations.

## API and contract changes

There is no standalone API product. The wire boundary lives under
`portless-daemon/api`:

1. Change `contract` types first.
2. Update the typed `client`.
3. Update server routing/adapters and control-plane behavior.
4. Update CLI and web consumers.
5. Update OpenAPI/event documentation and contract, unit, and E2E tests.

Do not expose the client's raw HTTP transport or build daemon routes in the
CLI. API, daemon lifecycle, and supervisor protocol versions are semantic
version strings. Change them deliberately when their compatibility contract
changes, even though the greenfield source tree does not retain old adapters.

Secrets must never be returned through inspection APIs. Resource providers
return secret-bearing runtime values separately from redacted `SafeValues`.
Preserve that boundary in new API fields, logs, errors, traffic capture, and
tests.

## Go conventions

- Use Go 1.26 language and standard-library facilities unless a dependency has
  a clear product-level benefit.
- Format changed Go files with `gofmt`.
- Keep contexts and timeouts on I/O, process, runtime, network, and persistence
  boundaries. Do not hide unbounded background work in request handlers.
- Wrap errors with the failed operation and preserve sentinels for public error
  classification. User-facing remediation belongs at the application or CLI
  boundary.
- Every exported function, method, type, constant, variable, and exported
  method in a public interface under `portless-cli`, `portless-daemon`, or
  `portless-relay` needs meaningful GoDoc beginning with the exact identifier.
  The architecture suite enforces this.
- Keep tests beside the package that owns the behavior. The CLI composition
  root retains only composition, global execution-policy, dependency, and full
  command-tree contract tests.
- Do not add compatibility-only tests or production facades for retired
  contracts; update tests to the current contract.

## CLI conventions

- The default output is concise, human-readable text. Every data-bearing
  command must also support the global `--json` behavior; streaming commands
  emit JSON Lines.
- Incomplete parent commands should display useful help instead of a Cobra
  argument-count error or an accidental API request.
- Preserve the existing command groups and ownership packages. Add a new group
  only when the user-facing mental model requires one.
- Color is restrained and semantic. Respect saved color preference,
  `--no-color`, `NO_COLOR`, non-TTY output, JSON output, and completion output.
- Commands that start asynchronous operations wait and render progress unless
  their explicit contract says otherwise. Avoid allowing duplicate actions
  while an operation is already running.
- Destructive operations are preview-first, require explicit confirmation, and
  report exactly what was or was not removed.

## Web conventions

- Use the existing React, TypeScript, and CSS architecture; do not introduce a
  UI framework for a localized change.
- Keep dark and light themes working by using shared theme variables instead of
  hard-coded page colors.
- Match the dense, professional control-plane visual language already in
  `portless-web/src/styles/app.css`.
- Support keyboard interaction and visible focus for interactive controls.
  Icon-only buttons require an accessible name.
- Disable mutually exclusive lifecycle actions while a request or operation is
  pending. Render failures through the shared structured error presentation,
  not raw red text.
- Keep public endpoint URLs visible and copyable where services are inspected.
- Update nearby Vitest coverage for component behavior. Update Playwright when
  a user journey or API contract changes.

`portless-web/dist` is tracked because the Go executable embeds it. Never edit
it by hand. After changing web source or public assets, regenerate it with
`make web` (or `make`) and include the resulting hashed assets. Do not commit
`portless-web/node_modules`, Playwright reports, test results, coverage, or
`bin` artifacts.

Building web assets does not update an already running Portless daemon. When a
web change needs to be visible in the developer's current control-plane page,
build the complete executable and restart the daemon with that exact checkout:

```bash
make
./bin/portless daemon restart
```

`make web`, `make test-web`, and `make test` may regenerate
`portless-web/dist`, but they do not replace the running executable. Do not
hand off a refreshable local UI change until the normal daemon restart has
succeeded; after it does, a browser refresh loads the new hashed bundle. Do not
use `daemon restart --force` as a routine fallback. Inspect
`./bin/portless daemon status` first and obtain explicit authorization because
a forced replacement can interrupt active environments.

The public website is independently owned by `portless-site`. Keep it fully
static and compatible with GitHub Pages. Product screenshots must come from a
running Portless application, never from explainer-video frames. Do not track
`portless-site/dist`, `.astro`, or `node_modules`; build it with `make site` and
validate it with `make test-site`.

## Build and validation

The root Makefile is the supported build interface:

```bash
make                 # install locked web dependencies as needed, build web, build bin/portless
make test            # web typecheck/unit/build, then all Go tests
make test-go         # all Go tests only
make test-web        # web typecheck, Vitest, and production build
make site-dev        # run the marketing site development server
make site            # marketing site typecheck and production build
make test-site       # marketing site typecheck, tests, and production build
make release-check   # validate the GoReleaser configuration
make release-snapshot # build unpublished macOS/Linux release archives
```

Use focused checks while iterating:

```bash
go test ./portless-daemon/controlplane
go test ./portless-cli/traffic
go test ./portless-relay
npm --prefix portless-web run typecheck
npm --prefix portless-web test
```

Before handing off a normal implementation change, run the narrow tests while
developing and then `make test`. Also run `git diff --check`. If a complete
suite cannot run, state exactly what was skipped and why.

The ordinary E2E suites use isolated temporary Portless homes and compiled
product binaries:

```bash
make install-e2e-browser   # one-time Chromium installation
make test-e2e-cli
make test-e2e-ui
make test-e2e              # both default suites
```

`make test-e2e-resources` uses the local Docker or Podman engine and may pull
images, but it still isolates Portless application state.

The following suites are machine-destructive and are never routine validation:

```bash
make test-e2e-relay-destructive
make test-e2e-relay-destructive-resources
```

Run either only when the user explicitly authorizes a machine-level relay test
and after reading `docs/e2e-testing.md`. They temporarily replace the real
relay, use administrator approval, port 80, the DNS resolver, and loopback
address configuration, and require no active Portless environments.

Likewise, do not run `portless reset`, `portless uninstall`, relay
install/uninstall, or commands that stop a developer's services as an
incidental test. Use isolated test homes or ask for explicit authorization.

## Change discipline

1. Inspect the working tree and preserve unrelated user changes.
2. Identify the product and package that own the behavior before editing.
3. Implement the smallest coherent vertical change; do not leave duplicate old
   contracts behind.
4. Add or update tests at the owning layer and at the user-journey layer when
   behavior crosses products.
5. Regenerate tracked web assets and update affected documentation.
6. Run formatting, architecture checks, focused tests, and the appropriate
   complete non-destructive suite.

Prefer a clear current design over speculative abstraction. If a new boundary
is necessary, name it after the product behavior it owns and document the
dependency direction in the architecture plan and guard tests.
