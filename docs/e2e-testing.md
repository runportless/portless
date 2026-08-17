# End-to-end testing

Portless has two end-to-end suites. Both exercise the compiled product rather
than replacing the daemon or API with test doubles:

- The CLI suite starts the real `portless` executable, daemon, process
  supervisors, edge proxies, and fixture applications.
- The UI suite starts the same stack and drives the embedded production UI in
  Chromium with Playwright.

Every test receives a temporary `PORTLESS_HOME` and temporary source
checkouts. Teardown performs a forced Portless reset, stops the isolated
daemon, and removes the temporary directory. The suite does not read or change
the developer's normal `~/.portless` installation.

## Run the suites

Install the Playwright Chromium build once:

```bash
make install-e2e-browser
```

Then run both suites with one command:

```bash
make test-e2e
```

The suites can also run independently:

```bash
make test-e2e-cli
make test-e2e-ui
```

`make test` remains the fast unit, component, and build-validation suite. It
does not install a browser or run E2E tests.

## What is covered

The CLI E2E suite protects these product contracts:

- zero-configuration discovery and a complete `up`, request, inspect, logs,
  `down` lifecycle;
- context-aware startup from a nested service directory, Portless-owned Node
  inspectors, additive debug modes, independent return to normal mode, and
  clean environment-wide reset with `up --managed`;
- human-readable default output, valid `--json` output, grouped help, and
  useful help for incomplete commands;
- lossless traffic detail and header capture;
- bounded recordings and persistent fault creation, matching, disable,
  re-enable, export, and deletion;
- authenticated daemon restart with adoption of the original service
  processes and proxy routes;
- several source repositories compiled into one project, environment cloning,
  adding a source after cloning, and explicit remediation of the other
  environment;
- a mixed environment with local services and a remote QA dependency,
  including traffic attribution and local enforcement of its read-only write
  policy;
- forced reset when ordinary lifecycle state is from an incompatible model.

The Playwright suite protects these browser journeys:

- browser authentication and one-use claim consumption;
- project, environment, sidebar, and breadcrumb navigation;
- environment creation through the modal without duplicating project sources;
- browser theme persistence;
- services, copyable endpoints, topology edges, service details, and logs;
- starting a real Portless-owned Node debugger from the service drawer,
  displaying its attach endpoint, and returning the service to normal mode;
- captured request and response inspection with complete headers;
- recording and fault workflows;
- environment stop/start controls; and
- daemon details, full-screen drawer behavior, restart, reconnect, and runtime
  adoption.

## Test-only ingress

Production `portless up` requires the installed machine-level relay because
the public product contract uses clean port-80 and TCP DNS endpoints. Normal
CI jobs must not install privileged services or claim machine ports.

The E2E binary is therefore compiled with the `e2e` Go build tag. That tag
changes only the CLI's ingress preflight: it requires the isolated daemon's
private Unix ingress socket instead of the machine relay. Application requests
still cross the real daemon ingress router and all real per-edge proxies. A
normal `make` build cannot activate this path because the production source
file hard-codes it off.

The shared fixture at `tests/fixtures/store-lite` intentionally has no Portless
declaration and no external dependencies. It is discovered as:

```text
client -> checkout -> inventory
                   -> orders
```

The multi-source test materializes those applications as separate temporary Go
modules so it exercises project compilation rather than a monorepo shortcut.
The `tests/fixtures/debug-node` workspace provides two small NestJS-shaped Node
services with safe direct-node launch commands. It verifies real inspector
listeners and process ownership without installing application dependencies.

## Destructive relay integration

The default E2E suites deliberately do not mutate machine-level networking.
Relay installation and removal have a separate, deliberately destructive test:

```bash
make test-e2e-relay-destructive
```

This target builds a dedicated binary with the normal production behavior, so
it does not replace the executable watched by a running development daemon. It
then runs the read-only safety preflight, asks `sudo` to cache administrator
approval, and runs serially against the real fixed Portless service, port 80,
DNS listener, resolver configuration, and loopback address pool. It is never
included by `make test` or `make test-e2e`.

The harness performs these safety checks before changing the machine:

- it must run as the normal non-root user through the deliberately named
  destructive Make target;
- a machine-wide lock prevents concurrent destructive relay suites;
- an existing relay must have a valid receipt owned by the current user;
- its HTTP and DNS sockets must identify one recognizable Portless home; and
- the daemon behind an existing relay must be reachable and report no active
  environments; a stopped daemon is rejected because it cannot prove that no
  supervised runtime survived it.

If an owned relay exists, the harness records its socket targets and daemon
state, removes it, installs the test relay against a temporary
`PORTLESS_HOME`, and restores the original relay during `TestMain` teardown.
Restoration also runs after a failed assertion or panic. A forced cleanup is
allowed only after the harness has removed the verified original installation
and taken exclusive ownership of the machine relay slot.

The scenario verifies:

- install and idempotent repair through the public CLI;
- receipt ownership, target sockets, launch service, helper, resolver, address
  pool, HTTP, direct DNS, and system-resolver health;
- a production `portless up`, clean control and application URLs through
  `127.0.0.1:80`, and rejection of unknown hosts;
- relay restart without losing application routing;
- the relay's controlled `503` response while the isolated daemon is stopped,
  followed by daemon recovery and runtime adoption; and
- uninstall, removal of every reported system artifact and listener, resolver
  removal, and idempotent repeated uninstall.

The test is suitable for a Portless developer machine when no environment is
running, but it temporarily interrupts the existing relay. A process kill,
machine restart, or terminal loss can prevent teardown, so a disposable macOS
or systemd Linux runner remains the safest place to automate it.

The following machine integrations are still separate future coverage:

- real `*.portless.test` application endpoints backed by PostgreSQL or Valkey;
- Docker and Podman resource provisioning, volume preservation/removal, and
  orphan cleanup; and
- full `portless uninstall`, including application data and CLI launcher
  removal in addition to the relay.

## Failures and artifacts

Playwright runs serially because the scenarios intentionally share one real
environment. On failure it retains a screenshot, video, trace, and error
context under `web/test-results/`. Open a trace with:

```bash
npm --prefix web exec -- playwright show-trace web/test-results/<test>/trace.zip
```

CLI failures print the isolated daemon log in the failing assertion. To target
one CLI scenario while developing:

```bash
make e2e-binary
PORTLESS_E2E_BINARY="$PWD/bin/portless-e2e" \
  go test -count=1 -tags=e2e ./tests/e2e -run TestCLIZeroConfigurationLifecycle -v
```
