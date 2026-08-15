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
- human-readable default output, valid `--json` output, grouped help, and
  useful help for incomplete commands;
- traffic detail capture and secret-header redaction;
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
- captured request and response inspection with redaction;
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

## Machine integration boundary

The default E2E suites deliberately do not mutate machine-level networking and
do not require Docker or Podman. The following checks belong in a separate,
disposable machine-integration job:

- privileged relay install, status, restart, and uninstall;
- clean `http://*.localhost` routing through port 80;
- `*.portless.test` DNS and conventional TCP ports;
- Docker and Podman resource provisioning, volume preservation/removal, and
  orphan cleanup; and
- full product uninstall, including resolver and launch service removal.

Those checks require an isolated macOS or Linux runner with administrator
access. They should never run on a developer workstation as part of
`make test-e2e`.

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
