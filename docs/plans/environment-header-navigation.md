# Environment header and sidebar navigation implementation plan

Status: implemented and verified on 2026-09-02.

Created: 2026-09-02.

## Goal

Replace the environment's breadcrumb bar, large environment heading, and
horizontal destination tabs with one compact, persistent header. Use the
sidebar to select Overview, Topology, Traffic, Mocks, Recordings, Faults,
Bindings, and Timeline.

Environment health, Open App, lifecycle controls, and active recording/fault
indicators remain accessible from every environment view, including focus mode.
The content below the header gains the space previously occupied by the
universal heading and duplicate tabs. Overview retains its own environment
identity summary above the cards, including clone provenance and active
recording, fault, and mock-scenario links.

This plan implements the direction discussed on September 2. It does not
reorganize the eight destinations or remove tabs inside individual workspaces,
drawers, or Settings.

## Current implementation and constraints

The plan is based on the current working tree, which already includes changes
to the mock scenario workspace, environment navigation, and related tests.
Preserve those changes and recheck the affected files before implementation.

| Area | Current owner and behavior | Required change |
| --- | --- | --- |
| Shell header | `portless-web/src/components/Chrome.tsx` renders a sticky 64px top bar with breadcrumbs and command-palette access. | Make this the single persistent environment header. |
| Environment heading | `features/environment/EnvironmentPage.tsx` renders the environment name, status, clone origin, reason, activity indicators, Start/Stop, Open App, and the environment menu. | Move this presentation into the shell header through feature-owned components. |
| Destination navigation | Chrome and EnvironmentPage both render the same eight destinations. | Render the destination list in the sidebar; remove the horizontal environment tab row. |
| View definitions | `EnvironmentView` in Chrome, `EnvironmentTab` in `navigation.ts`, and arrays/helpers in App and EnvironmentPage duplicate the same route knowledge. | Give `features/environment/navigation.ts` one authoritative view definition and URL builder. |
| Live activity | `useEnvironmentActivity.ts` loads timeline, recordings, and faults and subscribes to environment events. | Share one activity instance between the header, sidebar counts, and page. |
| Lifecycle actions | EnvironmentPage and App's command-palette actions issue separate Start/Stop requests. The heading's busy state ends when the request returns. | Route both entry points through one action owner that follows the accepted operation. |
| Focus mode | Chrome hides the sidebar; CSS hides the entire environment heading. Chrome separately adds status to the top bar. | Retain the complete compact header and use the left edge for navigation. |
| Narrow screens | At 760px and below, the sidebar is hidden and the environment tabs remain scrollable. | Provide overlay navigation before removing this fallback. |
| Workspace sizing | Topology subtracts fixed 284px/184px offsets; the mock workspace assumes a 64px top bar. | Size these workspaces from the actual header height and available space. |
| Tests | Several suites identify the environment by its old heading. A traffic test uses `.tabs button` as a typography reference. | Replace these assumptions with assertions against the new user-visible structure. |

Paths beginning with `features/` or `components/` refer to
`portless-web/src/`; paths beginning with `e2e/` refer to `portless-web/`.
Other implementation paths are relative to the repository root unless a
section explicitly gives a working directory.

## Product and interaction decisions

### Persistent header

The wide layout should read approximately as follows. Names and counts are
illustrative:

```text
store / local / Traffic   ● Healthy · 6/6 ready   REC checkout-flow   ▲ 1 fault
                                              Open App ↗  Stop All  ⋯  ⌘K
```

At a wide viewport, those elements occupy one horizontal header. The example
wraps here for readability.

Use the existing 64px header height as the wide-layout target. Its structure is:

1. A navigation toggle on narrow screens outside focus mode.
2. Project, environment, and current-view breadcrumbs.
3. A compact environment-health link.
4. Active recording and fault indicators.
5. Open App, Start All/Stop All, and the environment action menu.
6. Existing global command-palette and daemon access.

The current view becomes the small visible page heading, such as **Traffic**.
Preserve one meaningful level-one heading for an environment page. The main
region should be labelled by that heading. Overview additionally shows the
environment name as a level-two heading, its clone-origin chip, and live
activity links. Show each mock scenario bound to a service once, with a direct
link into its workspace. Reuse the shared activity snapshot and current
bindings; the Overview heading does not fetch its own data. Keep it visible in
focus mode and allow names and chips to wrap on narrow screens.

The project breadcrumb opens project configuration. The environment breadcrumb
opens that environment's Overview. The current-view breadcrumb is marked as
the current page. Keep the Projects destination accessible through the sidebar
brand and project navigation.

Only render environment-specific header content when the current route resolves
to an actual environment. Projects, project configuration, Settings,
authentication, and not-found pages retain their appropriate page context.

### Health and status messages

Use the daemon's `environment.status` as the authoritative state and the
existing `StatusMark` tone mapping. A readiness count supplements that state;
it must not replace or independently redefine it.

Count services whose status is `ready` and label the denominator accurately as
all services. The current Overview count includes all services while its
caption says “required services”; correct that caption when sharing this
summary. An optional service or a configuration issue must not cause the
header to invent a different aggregate health state.

| State | Header behavior | Supporting detail |
| --- | --- | --- |
| Healthy | Show Healthy and the ready/total count. | Health link opens Overview's service list. |
| Starting, recovering, stopping | Show the actual transition and readiness count. | Keep mutually exclusive lifecycle controls disabled. |
| Degraded or failed | Show the actual state and readiness count. | Render a meaningful `environment.reason` visibly below the header. |
| Stopped | Show Stopped and offer Start All. | Do not reserve an empty “not running” row. |
| Unknown | Show Unknown with the existing unknown-state treatment. | Avoid inferring health from a partial snapshot. |
| No services | Show “No services” alongside the authoritative state. | Avoid presenting `0/0 ready` as a success indication. |
| Connection unavailable | Mark the displayed snapshot as stale/reconnecting. | Retain useful last-known context; do not present it as freshly verified health. |

Health must be understandable without color. Give its link an accessible name
containing the environment, state, readiness count, and destination. Use an
actual Overview URL so normal browser link behavior works.

Configuration issues, lifecycle errors, and meaningful environment reasons
belong in a content notice region immediately below the header. Keep full
messages and remediation readable; do not truncate them into a tooltip. Empty
notices consume no height. A stopped state already explained by the health
control does not need a repeated notice.

Clone provenance remains available. Show a compact **From local** annotation
with the environment context when space permits, and retain its full
explanation in the environment menu on every viewport. It must not replace a
health reason or force an empty row on environments that were not cloned.

### Open App and lifecycle actions

Open App remains a direct link on the right side of the header. Resolve it
through the current rule: find `environment.primaryService`, then its public
HTTP endpoint with `publicEndpoint(service, 'http')`.

Preserve the public URL and new-tab behavior. Do not fall back to a private
process port, an upstream address, an arbitrary service, or a remote-provider
URL that bypasses Portless. When no primary public HTTP endpoint exists, omit
the link as the current UI does. Environment health alone does not select or
rewrite its destination.

Keep Start All/Stop All directly accessible. Their behavior should be:

- Stopped environments offer Start All.
- Stable active environments offer Stop All.
- Starting, stopping, recovering, or a locally tracked pending lifecycle
  operation show the corresponding progress label and disable conflicting
  lifecycle actions.
- A synchronous in-flight guard prevents double submission before React has
  rendered the disabled state.
- Toolbar and command-palette actions share that guard and the same request,
  operation, and error state.
- Stop keeps `removeVolumes: false`.
- Unavailable control-plane connectivity disables mutations until state is
  available again. Open App can still use its known public URL.

Keep the existing stopped-only, preview-first Forget Environment dialog behind
the environment menu. Preserve its impact explanation, blocked-running state,
error presentation, confirmation, and focus restoration. The action must not
become an unconfirmed one-click deletion during the move.

### Recording and fault indicators

Move the existing clickable active-recording and active-fault indicators into
the persistent header. They stay visible in focus mode and navigate to their
existing environment-scoped destinations.

The removed tab row also carries recording totals and active-fault counts.
Supply those counts to the corresponding sidebar rows from the same activity
snapshot. Keep badges compact; an icon rail should expose their meaning through
an accessible description without losing its readable destination labels.

Long recording names can be ellipsized, with their full names available to
keyboard and assistive-technology users. At narrow widths, use short visible
labels such as **REC** and **1 fault**, preserving full accessible names and
direct links. Do not hide an active intervention solely inside the menu.

### Sidebar, focus mode, and narrow layouts

Preserve the existing project switcher, environment list, destination order,
desktop sidebar collapse preference, and focus-mode preference.

Add a visible, keyboard-accessible **Open navigation** button to the header on
narrow screens outside focus mode. Focus mode uses the left-edge control at
every viewport width and has no header navigation button. Both controls open
the same navigation content as the desktop sidebar.

Use these behaviors:

- On desktop, expanded and collapsed sidebar modes continue to work.
- In focus mode, the header and its actions remain visible; the fixed sidebar
  becomes an overlay. Preserve the existing focus-mode shortcut and palette
  commands.
- At 760px and below, use overlay navigation even when focus mode is off.
  Resizing must not change the saved desktop collapse preference.
- Explicit button or keyboard activation opens a navigation panel that remains
  open until selection, dismissal, or Escape. Hover exit must not close a
  panel opened explicitly.
- Keep left-edge hover reveal as a desktop convenience. Hover preview must not
  steal focus, and a panel containing keyboard focus must not auto-close.
- The focus-mode edge also opens navigation with a click, Enter, or Space.
  Focusing the edge alone leaves navigation closed so Escape can restore focus
  without reopening the overlay.
- A closed overlay must not leave offscreen links or buttons in the tab order.
- Explicit modal navigation uses the existing overlay focus and dismissal
  utilities where appropriate: focus entry, containment, Escape, backdrop
  dismissal, and restoration. Hover preview remains nonmodal.
- Coordinate nested project-switcher/create-environment dialogs with the
  existing overlay stack so only the topmost interaction handles Escape.
- Navigation selection closes the overlay. Escape returns focus to its
  trigger; a completed destination change moves focus to the current view
  heading when appropriate.

The header may wrap into deliberate rows on a constrained viewport. Preserve
environment/view context, readable health, Open App, lifecycle controls, and
active indicators. Shorten button captions before removing actions; accessible
names should still say Open App, Start All, or Stop All. Secondary clone
metadata and the full command-palette hint can yield space first.

Use available header width, including sidebar width, when validating wrapping.
A 1024px window with an expanded sidebar has less usable space than the same
window in focus mode. Long names must not create document-level horizontal
scroll or squeeze the status/actions out of the header.

True maximized drawers and panels retain their existing full-viewport behavior.
They are distinct from focus mode, which preserves the compact application
header.

## Code ownership and data flow

Keep the change in `portless-web`, with environment behavior under
`src/features/environment` and shell behavior under `src/components`.
Use the existing React, TypeScript, CSS, API, and overlay utilities.

### Canonical navigation

Make `features/environment/navigation.ts` own:

- the `EnvironmentView` type;
- the ordered view names and display labels;
- validation of a requested view; and
- `environmentUIPath`, including traffic filters and mock scenario/route
  selection.

Update App, Chrome, EnvironmentPage, and OverviewPanel to consume that
definition. Remove the duplicate type declarations, `Tab` alias, local arrays,
and environment-view URL builders. Keep icon rendering with the shell.

Use “view” terminology internally. The current URL contract remains
`/environments/{project}/{environment}?tab=traffic`, with Overview at the base
path. Removing visual tabs does not require a browser-route migration. Preserve
`edge`, `protocol`, `scenario`, `route`, and `create=route` behavior and URL
encoding. Do not introduce a second accepted route representation or redirects.

All eight views remain reachable through the sidebar and command palette.
Preserve useful existing palette labels while covering Overview and Timeline,
which currently lack dedicated view commands there.

### Shared activity and action ownership

Compose one activity hook and one lifecycle-action hook from App, before its
conditional rendering returns. Pass their data and callbacks to the header,
Chrome's sidebar badges, and EnvironmentPage.

`useEnvironmentActivity` currently requires an environment and is mounted by
EnvironmentPage. Adapt it to an optional active scope and a session identity.
With no valid active environment it performs no requests or subscriptions and
returns empty/inactive activity. It must not fetch on Settings or project-only
routes merely because the sidebar remembers a project.

Use the existing environment-session identity, including the daemon instance,
to invalidate scoped activity and action state. On an identity change:

1. Disconnect the old activity stream and cancel its timers/requests.
2. Clear or immediately mask activity and errors belonging to the old scope.
3. Load the new scope's snapshots and connect its stream.
4. Ignore any late result from an earlier identity.

Ensure old counts cannot appear even for the render before effect cleanup.
Changing only the current view must not recreate this activity subscription.
Traffic and Topology keep their own feature-specific streams; this change
removes duplication of environment activity, not all streams in the app.

Add `features/environment/useEnvironmentActions.ts` to own environment Start,
Stop, and Forget request state. Menu visibility and confirmation visibility can
remain local to the header-actions component, keyed by environment session.
Keep selected-service state and service drawers owned by EnvironmentPage.

Start/Stop must use the returned `Operation`, refresh promptly after acceptance,
and follow it to `succeeded` or `failed` through the existing
`waitForEnvironmentOperation` helper. Refresh again on completion. Treat a
failed operation as a structured action failure, not a successful HTTP request.

Make operation observation cancellable for environment changes, daemon
replacement, and unmounting. For header lifecycle tracking, use a 10-second
timeout per inspection request and a 120-second foreground observation budget,
expressed as named client-side constants. These are observation limits, not
operation execution limits or the daemon-restart SLA. Extend the shared waiter
to accept the caller's cancellation and timeout policy without inventing a new
API contract or changing unrelated callers' execution policy.

Timing out observation must say that the operation may still be running; it
must never automatically submit another Start/Stop or claim to have cancelled
the daemon operation. Keep mutations guarded while its outcome is unresolved,
and reconcile through the existing operation/environment events and subsequent
authoritative reads. Provide an explicit way to resume inspection of the same
operation after a tracking error. Changing scope cancels this local tracking,
not the accepted operation in the daemon.

An action already accepted for one environment can continue in the daemon after
the user changes environments. Its late result must not clear a different
environment's busy state, navigate away from the new environment, or display an
old error there. Changing views within the same environment retains progress.

Use `actionError` and `ActionErrorNotice` for lifecycle and activity-loading
failures. Preserve API error codes and remediation. Remove the heading's
ad-hoc action-failure string when its ownership moves.

### Header composition

Add `features/environment/EnvironmentHeader.tsx` with feature-owned context and
action components. Move the health presentation, clone metadata, activity
indicators, Open App, lifecycle buttons, environment menu, and forget-dialog
composition into these components. Pass data and callbacks explicitly.

Give AppChrome typed React-node slots for header context and environment
actions, plus narrowly scoped optional sidebar counts. Chrome owns the sticky
header layout, breadcrumbs, navigation toggle, global tools, and overlay state.
It must not start fetching environment activity or issue environment mutations.

EnvironmentPage then renders the Overview identity summary when selected,
content notices, selected view, and service drawer. It receives the shared
activity data and action error rather than mounting a second activity hook.
Remove its old universal heading, horizontal navigation, and redundant
lifecycle handlers.

Keep the current environment/daemon key on page content. Do not key the page
by header height, readiness count, focus mode, or sidebar state. Header updates
and layout changes must not reset mock drafts, filters, selection, or scroll
positions. Preserve the current rules for actual view/environment changes.

## Layout implementation

Use shared theme variables and the current control-plane density. Introduce
scoped header styles rather than globally restyling every `.button`, heading,
tab, or `.project-heading` in the application.

1. Consolidate environment content into the existing sticky top-bar position.
   Use `min-width: 0` and explicit shrink/wrap priorities for its groups.
2. Expose the rendered header height as a shell CSS variable, initially 64px.
   A single `ResizeObserver` can update it when wrapping or text size changes;
   clean up the observer with the shell. Avoid one observer per page.
3. Use that variable for the main region's available height and for any
   viewport-bound environment workspace.
4. Make dedicated Topology and the selected mock-scenario workspace use a
   vertical flex/grid layout: notices take their natural height and the main
   panel fills the remaining space with `min-height: 0`.
5. Remove the Topology offsets that account for the deleted heading/tabs.
   Remove the mock workspace's assumption that the header is always 64px.
6. Keep the mock route browser and editor scrolling independently, with their
   controls reachable after header wrapping or a notice appearing. Keep normal
   document scrolling for Overview, Timeline, and other existing list pages.
7. Preserve the standard 28px desktop and 14px narrow-page bottom gutters and
   48px environment panel-title treatment. Use a compact content top inset
   consistently in normal and focus modes.
8. Audit stacking with the sidebar overlay, project switcher, row menus,
   dialogs, traffic/service drawers, and maximized content. The header must
   not sit above a modal or cover its controls.
9. Delete obsolete environment-heading, status-spacer, and environment-tab
   styles after checking their consumers. Keep `.settings-tabs`, drawer tabs,
   Traffic's local tabs, and any shared heading styles still used elsewhere.

## Implementation sequence

### 1. Consolidate navigation definitions

Change `navigation.ts` and its consumers in App, Chrome, EnvironmentPage, and
OverviewPanel. Preserve generated URLs and all current mock selections. Update
navigation tests before changing rendered structure.

Completion condition: one environment-view definition drives parsing, labels,
order, and navigation; existing deep-link behavior remains covered.

### 2. Establish shared activity and lifecycle state

Move activity composition to App, adapt the hook's scope cleanup, and add the
environment-action hook. Connect the existing toolbar and command-palette
entry points to the same action owner during the refactor. Update the operation
waiter only to support the required observation lifetime and timeout behavior.

Completion condition: one environment-activity subscription, shared Start/Stop
locking, terminal-operation handling, and no stale cross-environment results.

### 3. Build and wire the compact header

Add EnvironmentHeader, header slots, current-view heading/breadcrumbs, sidebar
counts, and the environment menu/dialog move. Pass activity and notices into
EnvironmentPage. Remove its old heading and destination tab row together.

Completion condition: every environment view has the same complete compact
header; all prior heading information/actions have their specified destination.

### 4. Complete overlay and responsive navigation

Wire the narrow-screen navigation button and focus-mode edge, hover versus
explicit-open behavior, dismissal, focus handling, and preference persistence.
Retain project switching and environment creation through the same sidebar.

Completion condition: all destinations and environment actions are usable with
mouse, touch, and keyboard when the fixed sidebar is absent.

### 5. Recalculate workspace layout

Implement the shared header-height contract, remove obsolete offsets, and
verify Topology, Traffic, the mock scenario workspace, ordinary list pages, and
maximized drawers. Remove unused CSS after inspecting all consumers.

Completion condition: content uses the recovered space without clipping,
unexpected document scrolling, or inaccessible footer controls.

### 6. Update journey coverage, documentation, and generated assets

Make the test migrations described below, update README/E2E workflow prose,
run the required validation, regenerate embedded assets through the Makefile,
and restart the developer daemon from the complete built checkout.

These milestones describe implementation order. Deliver one coherent change;
do not ship an intermediate state that removes narrow-screen navigation or
duplicates the new and old header.

## Verification plan

### Vitest and component coverage

Use the repository's current Vitest and static-rendering conventions for
presentation and pure logic. Use Playwright for interactive focus, layout,
request, and subscription behavior; a new testing framework is unnecessary.

| Test owner | Coverage |
| --- | --- |
| `features/environment/navigation.test.ts` and `App.test.ts` | All view names/defaults, encoded identities, unchanged query contract, mock scenario/route/create links, traffic edge/protocol links, and Settings return routes. |
| New `features/environment/EnvironmentHeader.test.tsx` | All health states, readiness counts including no services, clone context, activity links, primary public HTTP URL selection, missing endpoints, progress/disabled presentation, and accessible labels. |
| `components/Chrome.test.tsx` | Current-view breadcrumbs, environment header slots, sidebar ordering/counts, icon rail, focus/narrow navigation trigger, and absence of environment controls on unrelated routes. |
| `features/environment/EnvironmentPage.test.ts` | Content rendering with supplied activity; remove obsolete tests requiring a horizontal tab order or empty status-message spacer. Move header/action assertions to the new owner. |
| `features/environment/useEnvironmentActivity.test.ts` | Preserve recording-count bounds; cover any extracted scope/identity logic used to reject old results. |
| New or existing operation-polling tests | Running-to-terminal outcomes, failed operation reporting, cancellation, request/observation timeout, and prevention of automatic action resubmission. |
| Forget dialog and existing service/provider tests | Preserve deletion safeguards and check affected callers if the shared operation-waiting behavior changes. |

### Playwright migrations

Update these existing dependencies explicitly:

- `e2e/helpers.ts`: `authenticate()` currently waits for a heading named after
  the environment. Wait for the correct authenticated route and its scoped
  header/current-view heading instead. Derive expected context from the target
  route rather than assuming every environment is the default fixture.
- `e2e/access-navigation.spec.ts`: replace heading-hiding and focus-only-status
  assertions with persistent-header, visible-action, and overlay-navigation
  assertions. Keep panel-title and bottom-gutter coverage.
- `e2e/environment.spec.ts`: replace the test measuring the tab row's vertical
  position with stable toolbar/content placement across Start/Stop states.
  Update debugger/provider checks that find health beside the old heading.
- `e2e/projects.spec.ts`: assert clone provenance and stopped-only forgetting
  in their new locations; preserve project/environment switching and focus
  restoration after the confirmation closes.
- `e2e/settings.spec.ts`: update the return-to-environment heading assertion
  while retaining exact-route restoration.
- `e2e/traffic-inspection.spec.ts`: remove the font comparison with
  `.tabs button`. Preserve the intended local request-tab typography using its
  own stable style/token assertion.
- `e2e/experiments.spec.ts`: retain mock route deep links, draft retention,
  independent pane scrolling, footer access, focus-mode behavior, and maximize
  behavior under the new header height.

Add or extend focused journeys for:

1. Navigating all eight views from the sidebar, with the correct current-view
   heading and browser back/forward behavior.
2. Opening navigation from the edge in focus mode, selecting a destination, and
   dismissing with Escape. Verify focus restoration, no content shift, and no
   header navigation button at desktop or narrow widths.
3. Navigating on a 390px-wide viewport with focus mode off; restore the viewport
   and verify the saved desktop sidebar preference was not changed.
4. Health, Open App, lifecycle controls, and active indicators remaining visible
   through focus-mode entry, reload, sidebar changes, and content scrolling.
5. Following an active recording/fault indicator and returning with browser
   history. Verify sidebar counts come from the same state.
6. Gating a lifecycle request/operation response to verify duplicate toolbar or
   palette invocations do not submit another mutation while progress is pending.
   Verify the error path uses the shared structured notice.
7. Changing views while an operation is pending and changing environments while
   an old request is delayed. The new environment must not show the old
   operation, error, recording, or fault state.
8. Daemon replacement/reconnect clearing stale environment-session state and
   reacquiring authoritative health/activity snapshots.
9. Opening a populated mock scenario, making a draft, toggling focus/collapse
   and resizing the header, then confirming the draft and Save/Discard controls
   are still present.

Verify Open App's link against the primary public HTTP endpoint and its new-tab
attributes. Continue making live fixture application requests through the
isolated E2E ingress helper; ordinary UI tests must not depend on the machine
relay or the developer's normal environment.

Use request observation to check that changing views or opening the navigation
overlay does not add duplicate environment-activity streams. Account for the
separate, legitimate Traffic and Topology subscriptions.

### Visual and accessibility checks

Check representative combinations rather than every possible cross product:

| Viewport | Purpose |
| --- | --- |
| 1440×900 | Expanded sidebar, complete header, normal data. |
| 1024×768 | Limited content width with both expanded and collapsed sidebar. |
| 1280×400 | Short viewport, page gutters, workspace scrolling, reachable actions. |
| 390×844 | Touch-sized layout, overlay navigation, wrapped header. |
| 320×700 | Minimum supported width, long names, health and actions still available. |

Cover both themes, a long project/environment/recording name, a cloned
environment, active recording plus faults, a meaningful failure reason, and
200% text/browser zoom. Verify keyboard focus indicators, no color-only status,
no hidden focusable sidebar content, no duplicate page headings, and no
unintended horizontal document overflow.

### Required commands and delivery

During implementation, run focused checks from `portless-web/`:

```bash
npm run typecheck
npm exec -- vitest run src/components/Chrome.test.tsx src/features/environment
```

Run any additional directly affected unit files while iterating. Before
handoff, run from the repository root:

```bash
go test ./tests/architecture
make lint
make test
make test-e2e-ui
git diff --check
```

Read `docs/e2e-testing.md` before changing/running its suites. Use the ordinary
isolated UI suite. No machine-destructive relay suite, normal-home reset,
uninstall, or incidental stopping of developer services is part of this work.

Update README's description of environment navigation and focus mode, plus the
corresponding journey coverage in `docs/e2e-testing.md`. Update this plan's
status when implementation is complete.

`portless-web/dist` is tracked. Generate its new assets through `make web`/the
normal build, include the resulting hashes, and never edit generated files by
hand. A final complete build and normal daemon restart are required to make
the change visible in the current developer control plane:

```bash
make
./bin/portless daemon restart
./bin/portless daemon status
```

After a successful restart, refresh an authenticated control-plane page and
verify the compact header is served by that checkout, including one focus-mode
and one mock-workspace check. If normal restart is blocked, inspect daemon
status and report the concrete handoff issue. Forced replacement requires the
explicit authorization described in AGENTS.md; it is not a routine fallback.

## Completion criteria

- [x] A single compact header replaces the universal environment heading and
      destination tabs; Overview has its own identity and activity summary.
- [x] Project/environment/current-view identity and authoritative health are
      visible on every environment view.
- [x] Open App, lifecycle actions, environment actions, and active intervention
      indicators remain accessible in normal, focus, and narrow layouts.
- [x] The same sidebar navigation works expanded, collapsed, and as an
      accessible overlay; all eight destinations remain available.
- [x] Existing URLs, mock route selections, traffic filters, browser history,
      project navigation preferences, and Settings return behavior work.
- [x] Activity has one owner; lifecycle actions share pending/error state; old
      scope results cannot leak into a new environment.
- [x] Health reasons, configuration issues, clone provenance, and structured
      errors retain their specified presentation.
- [x] Topology, Traffic, mock editing, ordinary pages, and maximized views use
      the available space correctly in both themes and tested viewport sizes.
- [x] Focus/layout changes preserve current mock drafts and page state.
- [x] Required tests, documentation, and tracked embedded assets are complete,
      with any concrete validation limitation reported.
- [x] The complete executable has been built, normal daemon restart succeeded,
      and the refreshed local UI shows the new header.

The daemon API, wire schemas, CLI, relay, MCP runtime, public website, and
provider behavior require no changes for this plan. If implementation discovers
a new boundary requirement, document that separately before expanding scope.


## Implementation result

The shared environment header, sidebar navigation, scoped activity owner, and
shared lifecycle actions are implemented in `portless-web`. Environment URLs
retain their `?tab=` contract, including traffic filters and mock route links.
The daemon wire contract and provider behavior were not changed by this work.

Validation completed:

- `make lint` and `go test ./tests/architecture` passed.
- `make test` passed: 230 web unit tests, public-site checks/tests/build, and
  the complete Go suite.
- `make test-e2e-ui` passed all 40 browser journeys, including scope changes,
  shared pending actions and errors, focus restoration, nested overlays,
  both themes, and viewport widths from 320px to 1440px.
- `git diff --check` passed, and `make` regenerated the embedded assets and
  complete executable.
- Normal daemon restart succeeded in 1820ms. The live `store/local` environment
  remained healthy, with all six service statuses, PIDs, and generations
  unchanged. Its mock workspace was verified in the running application in
  normal and focus modes, at 390×844 and 640×400, with editor controls reachable.

The browser checks caught and resolved an initial activity-subscription race,
focus timing when reopening navigation after a hover preview, and narrow-screen
menu clipping. No forced daemon replacement or machine-destructive validation
was used.
