# End-to-end testing

Portless has two default end-to-end suites and five explicit opt-in integration
boundaries. All of them exercise the compiled product rather than replacing
the daemon or API with test doubles:

- The CLI suite starts the real `portless` executable, daemon, process
  supervisors, edge proxies, and fixture applications.
- The UI suite starts the same stack and drives the embedded production UI in
  Chromium with Playwright.
- The managed-resource suite additionally provisions real PostgreSQL, Valkey,
  MySQL, and NATS containers with Docker or Podman.
- The Store example suite runs the real stateful single-checkout example with
  Spring Boot, Node.js, two PostgreSQL instances, and Valkey.
- The Dispatch example suite runs the real three-source example, including its
  Next.js, FastAPI, Fastify, and Go services plus MySQL and NATS.
- The Chat example suite drives the real in-memory Node.js chat room through
  Chromium, including both WebSocket paths, reconnection, and topology protocol
  labels from live and retained handshakes, with HTTP + WS labels for mixed traffic.
- The destructive relay suite installs the production machine relay and uses
  the real port-80 and DNS integration.

The default, managed-resource, Store, Dispatch, and Chat tests receive a temporary
`PORTLESS_HOME`. The first two also receive temporary source checkouts. Store
and Dispatch, along with Chat, run tracked example sources and write only
ignored dependency and build output. Teardown performs a forced Portless
reset, stops the isolated daemon, and removes the temporary directory. Those
suites do not read or change the developer's normal `~/.portless` installation. The relay suite is
the explicit machine-level exception described below.

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

Real managed-resource lifecycle coverage is opt-in because it requires a
ready Docker or Podman engine and may pull images:

```bash
make test-e2e-resources

# Force one engine when testing runtime-specific behavior:
make test-e2e-resources RESOURCE_E2E_RUNTIME=podman
```

The multi-checkout Dispatch application has its own opt-in target. It installs
the example's locked dependencies, uses the selected container engine, and may
pull MySQL or NATS images:

```bash
make test-e2e-dispatch
make test-e2e-dispatch RESOURCE_E2E_RUNTIME=docker
```

The stateful Store application has a corresponding opt-in target for two
managed PostgreSQL instances and Valkey:

```bash
make test-e2e-store
make test-e2e-store RESOURCE_E2E_RUNTIME=podman
```

`make test` remains the fast unit, component, and build-validation suite. It
does not install a browser or run E2E tests.

## What is covered

The CLI E2E suite protects these product contracts:

- MCP stdio negotiation, immutable inventory and scope, JSON-RPC-only stdout,
  durable lifecycle idempotency and MCP actor attribution;
- a full MCP application journey through fresh discovery/start, traces,
  payload recording and chunk reassembly, mock authoring/preview/activation,
  replay comparison and deduplication, provider restoration, finite fault
  reactivation, guarded artifact cleanup, clone and project cleanup;
- an MCP configuration journey through multi-source creation, clone, source
  addition, checkout changes, classified remote binding, rescan, logical-source
  removal, project rename with immutable scope, and guarded environment/project
  cleanup;
- zero-configuration discovery and a complete `up`, request, inspect, logs,
  `down` lifecycle;
- application `/api/` and `/auth/` ingress with preserved query strings, POST
  bodies, application headers, and source-to-target traffic attribution;
  control-shaped paths remain application requests and do not inherit control
  browser security headers;
- framework-plugin discovery for Spring Boot with Gradle and Maven, NestJS,
  Express, Fastify, Next.js, Go, and FastAPI, including commands, port
  contracts, statically proven HTTP readiness paths with TCP fallback, evidence,
  debugger metadata, precedence, deterministic rescan, and fail-closed malformed
  manifests;
- resource-plugin discovery for PostgreSQL, Valkey, MySQL, and NATS, including
  versions, ports, generated environment bindings, and dependency edges;
- context-aware startup from a nested service directory, Portless-owned Node
  inspectors, additive debug modes, independent return to normal mode, and
  clean environment-wide reset with `up --managed`;
- human-readable default output, valid `--json` output, grouped help, and
  useful help for incomplete commands;
- exact traffic targets, repeated header capture, credential-header redaction,
  bounded Redis, PostgreSQL, MySQL, and NATS operation decoding, and explicit
  session fallbacks for encrypted or incomplete protocol traffic;
- bounded recordings and persistent fault creation, matching, disable,
  re-enable, export, and deletion;
- single-request HTTP replay with CLI request overrides, retained receipts,
  frozen-baseline response comparison, and remote-policy enforcement;
- authenticated daemon restart within the fixed five-second readiness deadline,
  with adoption of the original service processes and proxy routes while live
  browser event streams reconnect;
- hard daemon crashes and executable replacement with exact process adoption,
  plus service crashes, degraded state, retained logs, and recovery;
- reboot-shaped loss where durable supervisor files still say `ready` but the
  authenticated supervisor PIDs and application process groups are gone,
  including automatic restart by one `portless up` and direct forced reset;
- `down --all` from an ambiguous checkout and across multiple simultaneously
  active worktrees;
- automatic worktree preparation when starting a cloned Git-backed environment,
  including current uncommitted code, retained CLI selection, independent live
  responses, reuse after stop/start, and daemon handoff without restarting the
  original processes;
- several source repositories compiled into one project, environment cloning,
  adding a source after cloning, explicit remediation of the other
  environment, and project-wide source deletion;
- a mixed environment with local services and a remote QA dependency,
  including traffic attribution, local enforcement of its read-only write
  policy, and active local/remote provider handoffs that preserve unrelated
  service PIDs and generations;
- partial HTTP scenarios preserving real process IDs, dependency paths, normal
  daemon adoption, WebSocket sessions, and strict/forward preview decisions;
- deterministic multi-service scenario and route creation, service-specific
  matcher preview, whole-scenario activation, mock traffic attribution,
  dependency short circuiting, peer-process preservation, and restoration of
  every target provider;
- forced reset when ordinary lifecycle state is from an incompatible model.

The Playwright suite protects these browser journeys:

- oversized replay bodies rejected before preparation or dispatch through the
  shared, dismissible error notice, with editing and successful retry available;
- replay preparation from exchange and trace drawers and the expanded or maximized
  waterfall's root-request icon without dispatch, keyboard focus restoration,
  maximized trace summaries with readable metadata and accessible controls in
  both themes and narrow windows, repeated
  request headers and replacement text bodies, same-project destination
  selection, frozen response comparison with lossless large JSON numbers,
  replay summary shown only on Response diff, collapsible difference details
  with keyboard controls and retained expansion state across response tabs,
  formatted Body, Headers, and Raw tabs inside each response pane beneath its
  HTTP header, synchronized diff-pane tabs, matching trace tab geometry,
  typography, and colors in both themes,
  Request and Response columns aligned beneath the replay header with destination
  controls and request errors confined to Request, and
  captured/replacement request bodies and response panes that fill the available
  height and scroll internally,
  explicit remote-write confirmation, and closing/reopening a pending run
  without a second application request;

- browser authentication and one-use claim consumption, including proof that
  requesting a claim path on an application host neither consumes the claim
  nor issues a Portless session cookie;
- application-defined browser policies, with inline scripts and same-origin
  frames working through the real application host without extra control policies;
- focused per-browser-tab project and environment navigation, running-environment
  shortcuts above the five most recently opened projects, separate current-context
  and runtime-state markers, recency ordering preserved across lifecycle changes,
  persistent visit history, search and keyboard navigation across both groups, direct
  environment shortcuts alongside remembered project destinations, the searchable project registry
  with direct configuration, hide, and safe forget workflows, the persistent
  collapsible icon rail, Settings return, and breadcrumbs;
- MCP configuration generation and permission combinations, with conditional
  project/source-root fields using consistent theme colors, typography, sizing,
  and keyboard focus from desktop through 390 px layouts;
- a persistent environment header across all eight views, health and public
  Open App links, shared lifecycle state with the command palette, and a single
  activity subscription that discards responses from a previous environment;
- compact recording, fault, and mock icons in that header, with themed colors,
  descriptive tooltips, keyboard navigation, and live activation state; the mock
  icon opens the scenarios list even when only one scenario is active;
- an Overview heading with environment identity and clone provenance kept out
  of the persistent header, no duplicate recording/fault/mock controls, and
  readable wrapping in focus mode and on narrow screens, plus sidebar badges
  that count each active full or partial mock scenario once across multiple services,
  retain the count after reload or a mock-type change, and show `1` only while recording,
  disappearing when those activities end;
- one shared red error notice for a failed environment start and its saved
  failure reason across all eight views, dismissal without a duplicate
  reappearing, persisted errors after reload, and a new failure after recovery;
- keyboard- and command-palette-driven focus mode, desktop hover navigation,
  explicit overlay navigation on narrow screens, nested dialog dismissal,
  focus restoration, and viewport-sized topology;
- environment creation from the persistent sidebar through the modal without
  duplicating project sources,
  including visible clone provenance that does not displace status messaging;
- starting a clone directly from its header while the original remains healthy,
  with automatic checkout preparation, nested source paths, preserved local
  files, unchanged original processes, and checkout reuse on a later start;
- browser theme persistence;
- services with aligned URLs and mock explanations in both themes and focus
  mode, copyable endpoints, hover- and focus-driven topology service
  previews with connected-edge emphasis, service details, and default-on live
  logs with a plain-text raw tab and pause/resume controls;
- live mock-binding badges on topology cards, scenario identification on hover
  and keyboard focus, and service-endpoint links into the scenario workspace,
  with stable card geometry in both themes and indicator removal on restoration;
- starting a real Portless-owned Node debugger from the service drawer,
  displaying its attach endpoint, preserving healthy environment semantics,
  and returning the service to normal mode;
- captured request and response inspection with repeated headers, redacted
  credentials, and Portless-injected trace carriers kept out of header views;
- recording, mock-provider, and fault workflows, including scenario-table-first
  navigation, aligned scenario columns with scrolling confined to the table,
  keyboard sorting and unclipped row menus in both themes down to 320 px,
  empty service-independent scenario creation, URL-addressable
  scenario split workspaces with URL-addressable route selection, a
  service-selecting right-hand editor that respects focus mode, retained drafts
  while switching routes, save/discard and selected-route deletion, clickable
  and sortable routes paginated at ten rows, whole-scenario activation,
  visible disabled-route badges in the list and beside the main route title,
  muted request details, and badge removal after re-enabling; stable peer service
  PIDs, stationary tables and route panes throughout activation and restoration,
  and traffic attribution;
- recording history rows with one ellipsis menu for Export and Delete, including
  export contents, deletion, and row layouts in both themes;
- a recording-history header menu with DELETE ALL and inline confirmation,
  cancellation on dismissal and pagination, keyboard access, both themes and
  narrow layouts, pending progress, and deletion across all pages while retaining
  the active recording;
- consistent on/off switches for mock scenarios, routes, and faults, including
  keyboard toggling without opening the scenario or changing the selected route;
  fault switches keep their saved state after a failed browser request and show
  pending progress while the real request is held, blocking repeat actions until
  it finishes; mock scenarios and faults default to name order and keep their row
  positions when toggled; switch states are inspected in dark and light themes;
- Disable All in the mock scenarios table, with sequential provider restoration,
  pending control locks, a reported partial failure and retry of remaining active
  scenarios, preserved routes and disabled scenarios, unchanged peer processes,
  and a disabled bulk action when all scenarios are off;
- reloading an edited mock route or navigating away from a new route without a
  native browser confirmation, with saved values restored after reload; explicit
  in-app Back navigation still offers its discard/keep-editing dialog;
- separate Request and Response route-configuration tabs with keyboard arrows,
  Home/End navigation, contextual fields in the active panel, retained edits across
  tabs, the editor remaining available alongside Preview, and complete-draft saves
  from the shared footer; the footer appears for new routes and edited saved routes, disappears
  after save, discard, or manual reversion, and stays hidden for preview-only changes;
  fixed tabs and visible footer remain usable in both themes and narrow layouts;
- mock request previews against unsaved new and existing routes in disabled
  scenarios, with saved-route precedence, unmatched 501 responses, disabled routes,
  repeated and empty query values, JSON/raw bodies, response headers, and unchanged
  saved scenarios, runtime providers, traffic, recordings, and timeline history;
  query section counts, keyboard expansion, add-row focus, retained values while folded,
  and reopening folded query sections on validation errors; draft/request/result retention
  while switching workspace views or configuration tabs, request suggestions that follow
  an unedited draft, preserved tested samples, explicit stale-result reruns,
  recoverable validation errors, cancellation when switching routes, and usable
  response panes in light/dark themes, focus mode, and narrow layouts;
- Edit/Preview workspace tabs with keyboard arrows and Home/End; Preview hides
  the route list and places the editor on the left and test request/response on
  the right, with retained selection, configuration tab, unsaved draft, request,
  and result when returning to Edit and reopening Preview; disabled Preview
  for empty scenarios and usable layouts in both themes and narrow viewports;
  a scenario title with its Mocks back link and an activation switch above joined
  Full mock and Partial mock buttons; the selected route title and method/path share a toolbar with Edit
  and Preview above the route list and editor, following selection and unsaved edits,
  with long names and paths fitting without covering either toolbar's controls;
- opening Preview from each route's ellipsis menu, selecting the correct route
  and retained draft, moving keyboard focus into Preview, and preserving the
  existing result when reopening the same route without automatically running it;
- editable mock response header tables with a trailing blank row, keyboard focus
  while adding names, tabbing to values, and removing rows; counted collapsible
  headings, plus-button focus, empty-section defaults, retained collapsed state
  across configuration tabs, and reopening the Response tab and headers for
  validation errors from Save or Preview; errors for duplicate
  header names regardless of case and values without names; exact colon-containing
  values through preview and save; and retained header
  drafts across route selection and inline Preview in light/dark and narrow layouts;
- attached Exact/Template path selectors and Equals/Exists query operators, with
  mode validation, disabled presence-value cells, save/reload persistence, and
  preview using the same match criteria in both themes and narrow layouts;
- required and preview query parameter tables with editable rows, keyboard focus,
  removal, retained route drafts and request values, duplicate required-name and
  missing-name validation, repeated names and empty preview values, exact equals-sign
  values, stale-result reruns, and Reset restoring the draft's requirements;
  query tables are checked in both themes and at narrow widths;
- normal daemon restart with a mocked caller that has no outgoing proxy ports,
  healthy recovery without restarting peer processes, and browser-driven
  scenario disabling, original-provider restoration, and deletion afterward;
- a fixed-width OPEN button replaced by Start All for stopped environments, with
  disabled startup progress in the same slot and no environment action menu;
  visible Search text across desktop, narrow, and focus-mode headers,
  Stop environment through Search, shared pending state with the command palette,
  and keyboard-accessible, disabled-while-pending lifecycle controls;
- Services-table Start All and an ellipsis menu containing STOP ALL with an
  inline confirmation before dispatch; keyboard access, cancellation on menu
  dismissal, both themes and narrow layouts, mixed-state suppression, stable
  header geometry, and shared pending
  state with the top header, Search, and per-service actions;
- project-page source add/delete and
  Bindings-page checkout configure/edit/remove workflows using the native
  directory picker;
- active service-scoped provider handoff with unrelated runtime preservation,
  plus stopped-environment remote binding persistence and restore;
- durable timeline rendering and pagination;
- trace-first traffic expansion, raw exchange filtering, buffered pause/resume,
  HTTP/TCP switching, summary-only filtered bursts, and reuse of unchanged
  expanded trace detail as unrelated requests arrive;
- keyboard topology inspection, command-palette navigation, runtime status,
  not-found routes, and automatic recovery from a failed control-plane poll;
- daemon details, restart timing, and logs; full-screen drawer behavior;
  blocked-handoff stop guidance and force-restart confirmation; five-second
  restart failure messaging, reconnect, and runtime adoption.

Replay journeys also verify close cleanup and fresh reopening, continued delivery
of an already admitted request after close, no workspace/expiry label, activity
renewal across two hours, and cleanup after one idle hour using the browser clock.
Daemon tests advance an injected clock to verify server idle retention and that
receipt polling and activity never extend prepared credential retention.

Mock workspace journeys verify that the shared route toolbar spans the route
list and editor, that the sort/add controls align with the configuration tabs,
and that the controls remain visible in both themes and narrow workspaces.
New-route journeys also save and cancel untouched defaults from Edit and
Preview, checking keyboard submission, persistence, and footer visibility.

The partial-mock browser journey also switches enabled and disabled scenarios
between full and partial mode using joined buttons below Enabled in the scenario header,
verifies keyboard interaction, pressed state, and vertical placement at desktop and narrow widths
in both themes, and checks real 501 versus
forwarded responses, keeps the route list limited to saved routes, preserves
unsaved route drafts, and checks persistence
after reload. Control-plane tests cover both directions and failed handoffs
restoring the old mode and original provider settings.

## WebSocket coverage

The default CLI suite exercises real WebSockets through application ingress and
checkout's generated orders dependency URL. It checks handshake traffic while a
connection is still open, redacted recording exports, normal daemon replacement
with process adoption, and reconnection after environment stop/start. The browser
suite uses Chromium's native WebSocket client on the application origin to test
text, binary, subprotocol negotiation, close, and WS labels in handshake-only traffic details.
No browser network route is stubbed.

The fixture remains dependency-free. Its `wstest` package implements only the
bounded frame cases needed by these tests; it is not a product WebSocket codec.
Proxy package tests cover fragmented/control/compressed byte streams, TLS,
read-only policy, admission limits, and lifecycle races. The relay runtime test
uses temporary TCP and Unix listeners to verify upgrade bytes and cancellation;
it does not install or replace the machine relay.

## Test-only ingress

Production `portless up` requires the installed machine-level relay because
the public product contract uses clean port-80 and TCP DNS endpoints. Normal
CI jobs must not install privileged services or claim machine ports.

The E2E binary is therefore compiled with the `e2e` Go build tag. That tag uses
the isolated daemon's private Unix ingress socket instead of the machine relay.
HTTP application requests still cross the real daemon ingress router and all
real per-edge proxies. TCP dependency edges use ephemeral `127.0.0.1` proxy
ports, and the isolated daemon does not publish public TCP listeners from the
machine-wide `127.77.0.0/24` pool. This lets the suites run beside a developer's
active Portless installation without competing for its clean endpoints. The
destructive relay suite uses a normal production build and separately verifies
stable clean TCP endpoints and system DNS. A normal `make` build cannot
activate the private path because its composition root hard-codes it off.

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

The browser fixture is shared across spec files. Environment journeys wait for
pending lifecycle operations and restore stopped services in `afterEach`, so a
failed assertion does not leave later recording or fault journeys without an
application. Intercepted lifecycle responses finish before their routes are
removed. Header positions are measured after the focus-mode transition finishes;
responsive overflow assertions retry while the viewport layout settles.
Keyboard focus-mode checks place the pointer away from the navigation reveal
edge before toggling; otherwise a pointer at the browser's origin can immediately
reopen the sidebar through its hover behavior. Hover navigation is exercised
explicitly after verifying that keyboard entry hides the sidebar.
Mock-preview journeys create their own disabled scenarios and delete them in
`finally` cleanup. Matching and validation use the real daemon. The cancellation
journey delays delivery of one real preview response, then switches routes and
verifies that its late result cannot replace the newly selected route's response.
Query regex journeys cover operator selection, retained patterns, invalid-pattern
errors without saving, full-value and repeated-value previews, save/reload, and
switching back to Equals or Exists. The matcher unit suite compares regex previews
with real HTTP responses from an isolated mock listener.
Route-renaming journeys cover retained name/configuration drafts across route
selection, duplicate-name rejection, previewing the new identity, saving from
Preview or Response, URL/selection updates, reload, and discarding another rename.
Control-plane tests verify active listeners keep their addresses and providers,
while database tests inject a save failure to verify name and configuration roll
back together.

## Managed-resource integration

`make test-e2e-resources` enables only the container-backed scenarios with
`PORTLESS_MANAGED_RESOURCE_E2E=1`. The target accepts
`RESOURCE_E2E_RUNTIME=auto|docker|podman`; `auto` uses the normal Portless
runtime selection. It verifies:

- real readiness and protocol probes for PostgreSQL, Valkey, MySQL, and NATS;
- generated connection values delivered to the consuming service;
- exact container adoption across daemon restart;
- reboot-shaped recovery of dead process supervisors and an externally stopped
  fully owned Valkey container, including recreation at new generations and
  preservation of data in the managed volume;
- ordinary `down`/`up` behavior and Valkey volume persistence;
- explicit `down --volumes --yes` data removal; and
- endpoint, upstream, and data isolation between two active environments,
  followed by `down --all`.

Each scenario uses a temporary Portless home and cleans up its containers and
volumes. The suite is safe for normal application state, but it intentionally
exercises the selected local container engine and may download several images.

## Store example integration

`make test-e2e-store` enables only `TestStoreExampleEndToEnd` with
`PORTLESS_STORE_EXAMPLE_E2E=1`. It runs the tracked Store source against a
temporary Portless home and private E2E ingress. It verifies:

- discovery and readiness of checkout, orders, inventory, consumer-scoped
  `inventory-postgres`, `orders-postgres`, and `orders-redis` resources;
- an atomic inventory reservation followed by a checkout persisted with a
  PostgreSQL-generated order ID;
- cache-aside reads that miss through PostgreSQL and then hit Valkey;
- decoded inventory PostgreSQL `UPDATE` plus order PostgreSQL `INSERT` and
  `SELECT` exchanges with captured SQL;
- decoded Redis `GET` and `SET` exchanges with the expected cache key;
- daemon restart with an idle PostgreSQL connection preserving every service
  process and allowing a fresh database query afterward;
- stock and order persistence across their owning process restarts; and
- both PostgreSQL volumes persisting across ordinary environment down/up; and
- the checkout page's inventory reset restoring the seed stock through the
  checkout-to-inventory dependency edge.

The normal Go suite separately asserts the statically discovered Store
topology and application-protocol classifications. `make test-example-store`
runs the Node unit tests and the Spring Boot inventory tests. See the
[Store walkthrough](../examples/store/README.md) for the interactive traffic,
fault, lifecycle, and debugger workflow.

## Dispatch example integration

`make test-e2e-dispatch` enables only `TestDispatchExampleEndToEnd` with
`PORTLESS_DISPATCH_EXAMPLE_E2E=1`. It runs the tracked application templates as
three source roots against a temporary Portless home and private E2E ingress.
It verifies:

- compilation of `console`, `operations`, and `maps` into one seven-service
  project, including consumer-scoped `api-mysql` and explicitly shared
  `dispatch-nats`;
- readiness of five local processes and both managed resources;
- location lookup and a route estimate across the source-aware HTTP graph;
- a delivery persisted to MySQL with a readable public ID;
- publication and consumption of the corresponding NATS event; and
- captured `console:api`, `console:notifier`, `api:geocoder`, `api:routing`,
  and `routing:geocoder` traffic.

The normal Go suite separately bootstraps temporary independent Git
repositories, verifies that the bootstrap refuses to overwrite them, applies
the scenic-routing worktree patch with `git apply --check`, validates the four
OpenAPI documents, and asserts the statically discovered topology. See the
[Dispatch walkthrough](../examples/dispatch/README.md) for the interactive
worktree, fault, recording, mock, and remote-provider scenarios.

## Chat example integration

Chat has a dedicated browser suite with its own locked Playwright dependency:

```bash
make example-chat-dependencies
make -C examples/chat install-browser
make test-example-chat
make test-e2e-chat
```

Browser installation is a separate one-time step. CI installs the example
package's exact Chromium revision with operating-system dependencies;
`make test-e2e-chat` does not download browsers automatically.

Each test creates a fresh temporary `PORTLESS_HOME` before its first CLI call,
requires the Make target's explicit `PORTLESS_E2E_BINARY`, and runs against
`examples/chat`. The application address uses the discovered Chat hostname
with the isolated daemon's control port. It traverses real ingress and the
real `chat:rooms` proxy without using the installed relay, machine DNS
configuration, or application process ports.

The serial suite runs without retries and verifies:

- discovery of exactly `chat`, `rooms`, and their HTTP dependency;
- independent browser sessions, Open another tab, messaging, presence, typing,
  and literal text rendering;
- inspectable HTTP 101 traffic on `external:chat` and `chat:rooms` while sockets
  remain open, with chat content absent from capture;
- room restart, chat restart, daemon adoption with preserved process IDs,
  backend unavailability, and environment stop/start;
- draft preservation and cancellation of retries after Leave; and
- keyboard use, scroll preservation, duplicate names, and 390 px/700 px/1280 px
  screenshots in both themes.

Node tests separately exercise uncertain delivery, confirmation/history
reconciliation, stale callbacks, limits, Origin/subprotocol rules, and cleanup.
Browser networking is real; the runnable app has no test-only failure endpoints.

Failure traces, screenshots, video, and reports go in the example's ignored
output directories. Each test writes redacted CLI output, daemon/service logs,
and cleanup results under its diagnostics folder. Set
`PORTLESS_E2E_ARTIFACT_DIR` to additionally retain installation diagnostics.
Installation keys and token-bearing state files are never copied. Cleanup runs
after partial startup, targets only that test's installation, and fails if a
known owned process remains. CI tests the minimum Node version and runs browser
journeys on macOS and Linux with Node 24, retaining failure artifacts for seven
days.

## Destructive relay integration

The default E2E suites deliberately do not mutate machine-level networking.
Relay installation and removal have a separate, deliberately destructive test:

```bash
make test-e2e-relay-destructive
```

To include a real Valkey container and verify its clean `*.portless.test` TCP
endpoint through system DNS, use the more explicit target:

```bash
make test-e2e-relay-destructive-resources
```

Both targets build a dedicated binary with the normal production behavior, so
they do not replace the executable watched by a running development daemon.
They then run the read-only safety preflight, ask `sudo` to cache administrator
approval, and run serially against the real fixed Portless service, port 80,
DNS listener, resolver configuration, and loopback address pool. Neither is
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
  `127.0.0.1:80`, a real one-use browser claim and authenticated session, and
  rejection of unknown hosts;
- relay restart without losing application routing;
- the relay's controlled `503` response while the isolated daemon is stopped,
  followed by daemon recovery and runtime adoption; and
- full uninstall preview and confirmation, removal of processes, application
  data, every reported system artifact and listener, resolver removal,
  preservation of a source-tree executable, and idempotent repeated uninstall.

The resource-enabled variant also verifies system resolution of a clean
Valkey hostname and a real TCP `PING` through the production relay.

The test is suitable for a Portless developer machine when no environment is
running, but it temporarily interrupts the existing relay. A process kill,
machine restart, or terminal loss can prevent teardown, so a disposable macOS
or systemd Linux runner remains the safest place to automate it.

Orphan-container cleanup after an externally interrupted engine operation and
automation on disposable macOS and Linux hosts remain separate future
coverage.

## Failures and artifacts

The Playwright suite is split into focused access/navigation, settings,
projects, environment, environment-errors, experiments, mock-preview, mock-recovery,
topology-mocks, traffic-list, traffic-inspection, traffic-waterfall, and daemon
journey specs. The specs still run with one
worker and stop after the first failure because they share one real isolated
Portless stack. To run one journey while developing:

```bash
make e2e-binary
npm --prefix portless-web run test:e2e -- traffic-waterfall.spec.ts
```

On failure Playwright retains a screenshot, video, trace, and error context
under `portless-web/test-results/`. Open a trace with:

```bash
npm --prefix portless-web exec -- playwright show-trace portless-web/test-results/<test>/trace.zip
```

CLI failures print the isolated daemon log in the failing assertion. To target
one CLI scenario while developing:

```bash
make e2e-binary
PORTLESS_E2E_BINARY="$PWD/bin/portless-e2e" \
  go test -count=1 -tags=e2e ./tests/e2e -run TestCLIZeroConfigurationLifecycle -v
```

The CI workflow runs the CLI and Playwright suites in independent jobs with
bounded step and job timeouts. Failed runs retain the complete command log and
isolated daemon log for seven days; browser failures also retain the Playwright
HTML report, trace, screenshot, video, and error context. Set
`PORTLESS_E2E_ARTIFACT_DIR` locally to preserve the same daemon-log diagnostics
outside the temporary test installation.
