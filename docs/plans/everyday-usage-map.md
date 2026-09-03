# Everyday Portless usage map

Status: proposed content map, grounded in the current CLI, UI, and example
sources. The workflows were reviewed against source; they were not executed
or filmed for this document. Priorities and frequency are editorial hypotheses,
not usage telemetry.

Use the scenario IDs below to connect documentation, runnable examples, and
explainer storyboards. Each scenario starts with a developer's task, identifies
the commands and screens involved, and ends with an observable result.

## Content direction

The primary story is: start an application, work on one part of it, understand
what happened, and resume later with the same names and data.

Use the CLI for checkout context, repeatable actions, and terminal inspection.
Use the control plane for orientation, visual inspection, and interactive
changes. The application itself supplies the visible behavior; an IDE supplies
source editing and breakpoints. CLI and UI actions are usually alternative
ways to perform a step, so a walkthrough should not require doing both.

Give the daily loop the first position in docs and the main explainer. Introduce
recordings, faults, mocks, and alternate providers through a concrete problem
once the viewer has seen the application run.

## Scenario index

P0 is the first content set. P1 is the next set of focused guides. P2 is support
or integration material. Frequency describes when the need arises, independently
of production priority.

| ID | Developer scenario | Likely audience and frequency | Priority | Primary Portless UI |
| --- | --- | --- | --- | --- |
| S01 | Start or resume the application | Everyone; each work session | P0 | Overview, Open App |
| S02 | Find the right project, environment, and URL | Everyone; throughout the day | P0 | Project switcher, environment sidebar, Overview, Topology |
| S03 | Change, restart, or debug one service | Backend/full stack; during implementation | P0 | Service drawer: Logs, Details, Configuration |
| S04 | Find why a user action failed | Frontend/backend; during investigation | P0 | Traffic: Traces, Exchanges, request/response detail |
| S05 | Explain a database or cache interaction | Backend/full stack; during investigation | P0 | Traffic: TCP exchanges, Command/Result detail |
| S06 | Keep evidence of a reproducible problem | Developer/reviewer; when investigating a bug | P1 | Recordings, Traffic |
| S07 | Test a slow or unavailable dependency | Frontend/backend; during resilience work | P1 | Faults, Topology, Traffic |
| S08 | Build or demonstrate an uncommon response state | Frontend/full stack; during feature work | P1 | Mocks: scenario and route editor |
| S09 | Work locally against a QA dependency | Frontend/integration developer; when a local dependency is unsuitable | P1 | Bindings: Configure Provider, Traffic |
| S10 | Try a branch in another environment | Developer/reviewer; during branch comparison | P1 | Create Environment, Bindings: Checkouts |
| S11 | Run an application spread across repositories | New teammate/full stack; initial setup and topology changes | P1 | Projects, Sources, Environments, Topology |
| S12 | Stop work and keep application data | Everyone; end of session | P0 | Stop All, Start All |
| S13 | Refresh discovery after changing application structure | Developer; occasional | P2 | Bindings and Topology for verification |
| S14 | Diagnose a startup or local networking problem | Everyone; occasional | P2 | Portless System, Settings: Runtime |
| S15 | Give an assistant access to the running environment | Assistant user; setup, then recurring inspection | P2 | Settings: MCP |

The short daily walkthrough is S01 → S02 → S03 → S04 → S12. S05 adds a useful
data story; S06–S10 are independent task guides.

## Shared example assumptions

- Installation and the one-time `portless setup` step are prerequisites. A
  registered project, declaration file, or account is not required for the
  first `portless up` from a supported checkout.
- [Store](../../examples/store/README.md) is the main everyday application. It
  has `checkout`, `inventory`, and `orders`, two PostgreSQL resources, and
  Valkey. Follow its dependency preparation before these recipes.
- [Dispatch](../../examples/dispatch/README.md) supplies the multi-repository,
  worktree, QA-provider, and caller-specific demonstrations. Follow its
  bootstrap instructions before scenarios that assume `dispatch/local` exists.
- `/path/to/portless` is the user's repository location. Explicit `--env`
  selectors make the intended environment visible in reusable recipes.
- Scenario cards are independent. Start with the named environment ready and
  with earlier experimental faults or mock scenarios disabled, unless a card
  explicitly establishes a different starting state. Reuse a saved scenario
  by enabling it; choose a new name when creating another recording.
- Store's app is `http://checkout.local.store.localhost`. Dispatch's app is
  `http://console.local.dispatch.localhost`. `portless ui` opens the control
  plane; `portless open <service>` opens an application service.

## S01 — Start or resume the application

**Situation:** A developer has a checkout and wants to work on the application.

**CLI flow**, after preparing Store's dependencies:

```bash
cd /path/to/portless/examples/store
portless up
portless open checkout
```

`up` discovers an unregistered checkout, waits for startup, and opens the
environment control plane by default. Subsequent runs reuse the environment.

**UI flow:** In **Overview**, see services become ready, then choose **Open
App**. In the Store application, choose `coffee-mug`, quantity `1`, and press
**Create Order**. For a previously registered stopped environment, **Start All**
is the browser entry point.

**Visible proof:** The order is accepted through the clean application URL,
and its HTTP and resource activity appears in Portless.

**Content use:** The quickstart and opening explainer sequence. Store already
documents startup and order creation. Show dependency installation separately
from the recurring daily command.

## S02 — Find the right environment and endpoint

**Situation:** A developer switches projects or returns after a break and needs
to know what is running, where to open it, and which services communicate.

**CLI flow**, from the Store checkout:

```bash
portless project list
portless env select store/local
portless env current
portless status
portless url checkout
portless ui
```

**UI flow:** Use the **project switcher** and the project's **environment
sidebar**. In **Overview**, open or copy a service endpoint. In **Topology**,
select a service to inspect it, or select an edge to open scoped traffic.
**Projects** provides the full registry when a project is absent from recents.

**Visible proof:** The project/environment name, service state, and application
URL agree. With Store and Dispatch both running, switch between their distinct
application URLs without choosing host ports.

**Content use:** A short guide to project/environment selection and stable
URLs. Explain that `env select` saves terminal selection for the current
checkout; browser navigation has its own context. A `.localhost` URL is an
endpoint on this machine, not a link that exposes the app to a teammate.

## S03 — Work on one service

**Situation:** Orders needs a code change, a restart, or a breakpoint while the
rest of Store stays available.

**CLI flow:** Follow logs while editing:

```bash
portless --env store/local logs orders --since 10m --tail
```

In another terminal, restart that service after the change:

```bash
portless --env store/local service restart orders
```

For a breakpoint investigation:

```bash
portless --env store/local service debug orders
portless --env store/local service show orders
```

Attach the IDE to the reported Node inspector. After debugging, return the
service to its normal launch mode:

```bash
portless --env store/local service manage orders
```

**UI flow:** **Overview → orders** opens the service drawer. Use **Logs** for
output, **Details** for health and debugger information, and **Configuration**
for effective values. **Restart**, **Debug**, and **Run Normally** provide the
corresponding lifecycle actions. Editing and breakpoint control happen in the
IDE.

**Visible proof:** A breakpoint is reached or the edited behavior appears;
other services retain their runtimes. Inspect **Timeline** or run
`portless --env store/local timeline` to explain lifecycle changes.

**Content use:** The daily development guide. Store documents debugger startup;
add a small, reversible code-change exercise for the restart lesson. Current
automatic debugger discovery supports Node and JVM services.

## S04 — Find why a checkout failed

**Situation:** Store rejects an order, and the developer needs to locate the
dependency responsible.

**Trigger:** Submit `usb-c-cable` in the Store app. The seeded item is out of
stock. This supplies a concrete failure without changing application code.

**CLI flow:**

```bash
portless --env store/local traffic traces --service checkout
portless --env store/local traffic list --edge checkout:inventory
portless --env store/local connection show checkout:inventory
```

Inspect a returned result with `portless --env store/local traffic trace
<number>` or `portless --env store/local traffic show <sequence>`, substituting
the number from the relevant list.

**UI flow:** **Traffic → Traces**, filter for errors or `checkout`, expand the
request, and open the inventory span. Inspect **Request** and **Response**;
maximize the drawer to use **Compare**. **Exchanges** provides individual
observed requests. A topology edge is another entry point to the same filter.

**Visible proof:** The reservation request received `409` from inventory before
an order was created. A stocked `coffee-mug` order provides the successful
comparison.

**Content use:** “Find the failing dependency.” Keep the application action and
its evidence together in both the guide and recording. Traffic correlation
should retain the confidence shown by the product.

## S05 — Explain database and cache behavior

**Situation:** A developer wants to know whether an order read reached the
database or was served by the cache.

**Trigger:** Create a new Store order. Open
`http://checkout.local.store.localhost/orders/<id>` twice, replacing `<id>` with
the returned order ID. Make the second request within the 60-second cache
window. The browser or `curl` can generate these reads.

**CLI flow:**

```bash
portless --env store/local traffic list --edge orders:orders-postgres --protocol tcp
portless --env store/local traffic list --edge orders:orders-redis --protocol tcp
portless --env store/local url orders-postgres
```

**UI flow:** In **Traffic → Traces**, inspect the order reads and their resource
spans. In **Exchanges**, select **TCP** and the relevant edge. Open SQL or Redis
operations in **Command/Result** detail; inspect captured database rows and
Redis values.

**Visible proof:** The first response reports a cache miss with Redis GET,
PostgreSQL SELECT, and Redis SET. The second reports a cache hit with Redis GET.

**Content use:** “See what your database and cache actually did.” This recipe is
already documented in Store. Standalone TCP operations remain in Exchanges;
their association with an HTTP request can rely on caller identity and timing.
The UI inspects captured operations and bounded results; it is not a SQL editor.

## S06 — Keep evidence of a problem

**Situation:** A developer can reproduce a route-estimation problem and wants
to retain the dependency exchanges for investigation or a bug report.

**CLI flow**, with Dispatch running:

```bash
portless --env dispatch/local record start route-estimate \
  --edge api:routing --capture-payloads --duration 5m
```

Estimate a route in the Dispatch app, then finish and export the recording:

```bash
portless --env dispatch/local record stop route-estimate
portless --env dispatch/local record show route-estimate
portless --env dispatch/local record export route-estimate \
  --output route-estimate.json
```

**UI flow:** **Recordings → New Recording**: enter a name, choose `api → routing`,
and opt into payloads if the investigation needs them. Choose **Start
Recording**, perform the app action, and choose **Stop Recording**. Inspect the
history row and use its **Export** action. The CLI exposes the custom duration;
the current recording form does not.

**Visible proof:** The recording has retained events and produces a JSON file
containing the selected evidence. Use synthetic example data when preparing
an artifact intended for publication.

**Content use:** “Capture the request while you reproduce the bug.” Dispatch
already documents this flow. Export currently provides recording evidence;
portable environment reproduction and automatic replay have separate plans.

## S07 — Test one slow dependency

**Situation:** A developer wants to see the app's loading and error behavior
when one caller encounters a slow dependency.

**CLI flow**, with Dispatch running:

```bash
portless --env dispatch/local fault add slow-routing-geocoder routing:geocoder \
  --method GET --path '/locations/*' --latency 1500 --duration 10m
```

Estimate a route and use the direct location search. Inspect the two callers:

```bash
portless --env dispatch/local traffic list --edge routing:geocoder
portless --env dispatch/local traffic list --edge api:geocoder
```

After checking the behavior:

```bash
portless --env dispatch/local fault disable slow-routing-geocoder
```

**UI flow:** **Faults → Create Fault**: select `routing → geocoder`, choose
**Latency**, enter `1500`, and set automatic disable to ten minutes. The form
applies to the selected connection; method/path restrictions in the CLI example
are additional CLI controls. Inspect **Traffic** and disable the saved rule
from **Faults** afterward. Error-status and abort rules support separate lessons.

**Visible proof:** The route's geocoder requests incur the delay while direct
`api → geocoder` location searches remain fast. The UI's match count and scoped
traffic identify which calls were affected.

**Content use:** “Slow one dependency without changing its other callers.”
Dispatch already contains this scenario. Show the app before, during, and after
the fault; the rule form alone does not demonstrate the result.

## S08 — Build an uncommon response state with a mock

**Situation:** A frontend developer needs a repeatable sold-out state for a
normally available product.

**CLI flow**, with Store running and a baseline coffee-mug order verified:

```bash
portless --env store/local mock create sold-out \
  --description 'Coffee mug reservations are rejected'
portless --env store/local mock route set sold-out reserve-coffee-mug \
  --service inventory --method POST \
  --path /inventory/coffee-mug/reservations --status 409 \
  --header 'content-type=application/json' \
  --body '{"sku":"coffee-mug","name":"Coffee mug","requested":1,"onHand":0,"available":false,"warehouse":"local"}'
portless --env store/local mock preview sold-out \
  --service inventory --method POST \
  --path /inventory/coffee-mug/reservations
portless --env store/local mock enable sold-out
```

Create another coffee-mug order with quantity `1` in the app. After inspecting
the rejection, restore the previous inventory provider:

```bash
portless --env store/local mock disable sold-out
```

**UI flow:** **Mocks → Create Scenario → Add Route**. Select `inventory`, enter
the POST path and `409` JSON response, then **Save Route**. Enable the scenario
from its page, perform the checkout, and inspect the mock attribution in
**Traffic**. Disable the scenario to restore the original provider.

**Visible proof:** The same application action changes from accepted to
sold-out and back without editing checkout code or changing the caller's URL.

**Content use:** A frontend-state guide and a short visual explainer. This is a
new recipe using existing Store behavior. Store checkout actually calls
`POST /inventory/{sku}/reservations`; a mock of the GET availability endpoint
would not produce this checkout result.

A scenario replaces its target service, and unmatched requests return `501`.
For this focused demonstration, perform the specified reservation action.
After disabling the mock, Store's **Reset Inventory** can restore the fixture's
stock if needed. Extend a broader app walkthrough with all required routes.

Recording and OpenAPI imports can seed another disabled scenario through the
CLI. The current UI supports route authoring and activation; import and request
preview remain CLI workflows.

## S09 — Use a QA dependency while working locally

**Situation:** A developer wants the local console to use a QA API's data while
keeping other services local and blocking outgoing HTTP writes.

**Preparation:** With `dispatch/local` running, start the reproducible QA helper
in another terminal:

```bash
make -C /path/to/portless/examples/dispatch qa-api
```

**CLI flow:**

```bash
portless --env dispatch/local env bind api \
  --remote http://127.0.0.1:19090 \
  --classification qa --write-policy read-only --health-path /health
```

Reload the Dispatch app, estimate a route, and attempt to schedule a delivery.
Inspect the helper's mutation count, then restore the local API:

```bash
curl http://127.0.0.1:19090/stats
portless --env dispatch/local env bind api --local operations
```

**UI flow:** **Bindings → Configure Provider**: select `api`, choose the remote
provider, enter its URL, classification, write policy, and health path, then
choose **Switch Provider**. Inspect the resulting requests in **Traffic**.
Switch back to the `operations` checkout provider after the exercise.

**Visible proof:** The app displays recognizable QA data. Reads work, the
attempted write is rejected locally, and the helper records zero mutations.
The application URL remains the same; unrelated services retain their runtimes.

**Content use:** “Use QA data from your local app.” Dispatch already supplies
the helper and a cloned-environment variant. This variant demonstrates the
implemented live provider handoff. A real workflow supplies its own classified
QA endpoint; the helper makes the published example self-contained. A read-only
policy is an HTTP method boundary, so choose an API whose reads follow that
contract. Stop the helper after restoring the local provider.

## S10 — Try a branch in another environment

**Situation:** A developer wants to compare a maps change with the baseline
without replacing the project's normal checkout binding.

**CLI flow**, after Dispatch bootstrap:

```bash
cd /path/to/portless/examples/dispatch
make scenic-worktree
portless --env dispatch/local env clone scenic
portless --env dispatch/local down
portless --env dispatch/scenic env checkout set maps \
  --path ./workspace/worktrees/maps-scenic
portless --env dispatch/scenic up
```

Estimate the same route at `http://console.scenic.dispatch.localhost`. Return
to the baseline afterward:

```bash
portless --env dispatch/scenic down
portless --env dispatch/local up
```

**UI flow:** Use the **+** beside **Environments** or **Create Environment** in
project configuration. Name the clone `scenic` and choose `local` as its source.
In the stopped clone, open **Bindings → Checkouts**, edit `maps`, and select
the prepared worktree. Stop the baseline and start the clone.

**Visible proof:** The same route has a different strategy in `scenic`, and
`local` retains the main maps checkout. The source owns both routing and
geocoder, so this changes that source's checkout binding.

**Content use:** A branch-comparison guide using Dispatch's existing tested
patch. Git/the example helper creates the worktree; Portless binds it. Cloning
copies environment configuration, not working trees or database contents.
These particular environments share other checkout paths and run one at a time.

## S11 — Assemble an application across repositories

**Situation:** A new teammate has separate console, operations, and maps
repositories that form one application.

**CLI flow**, starting with an unregistered Dispatch workspace:

```bash
cd /path/to/portless/examples/dispatch
make bootstrap-install
portless project create dispatch \
  --source console=./workspace/checkouts/console \
  --source operations=./workspace/checkouts/operations \
  --source maps=./workspace/checkouts/maps
cd workspace/checkouts/console
portless env select dispatch/local
portless up
```

**UI flow:** Open **Topology** to see the cross-repository application. Use
project configuration's **Sources** and **Environments** sections to inspect
the logical sources and configured environments. **Bindings → Checkouts**
shows the selected environment's paths. The initial project assembly is a CLI
workflow; the empty Projects screen points users to those commands.

**Visible proof:** Estimating and scheduling a delivery crosses the console,
API, routing/geocoder, MySQL, and NATS services within one project.

**Content use:** The multi-repository onboarding guide. Dispatch already
provides independent repositories and the application exercise. Adding a
logical source later is project-wide and requires every project environment
to be stopped; changing one environment's checkout is a separate operation.

## S12 — Stop for the day and resume with data

**Situation:** A developer wants to release application runtimes and keep the
orders and inventory state needed tomorrow.

**CLI flow:**

```bash
portless --env store/local down
```

At the next session:

```bash
portless --env store/local up
```

**UI flow:** Choose **Stop All** in the environment header. Later choose
**Start All**, then **Open App**. In this UI, “All” means the selected
environment's services. `portless down --all` is the separate CLI operation
for stopping all active environments.

**Visible proof:** A previously created order can still be read, inventory
state remains, and public application names are unchanged.

**Content use:** End the quickstart and everyday explainer here. Store already
documents persistent managed volumes. Ordinary shutdown preserves them;
volume deletion belongs in an explicit data-reset guide.

## Supporting workflows

| ID | Situation and commands | UI used | Content treatment |
| --- | --- | --- | --- |
| S13 | Application structure changed: `portless --env store/local down`, `portless --env store/local env rescan`, then `portless --env store/local up`. For a new repository, the template is `portless project source add <name> --path <checkout>` after all project environments stop. | Verify services, edges, and paths in Overview, Topology, and Bindings. Add Source exists in project configuration; rescan currently has no dedicated UI action. | A topology-maintenance guide. Supply a prepared structural change before making this a runnable example. Ordinary code edits belong to S03. |
| S14 | Startup or an endpoint fails: `portless doctor`, `portless daemon status`, `portless relay status`, `portless runtime status`. Initial machine setup uses `portless setup`. | If reachable, inspect Portless System's Status, Runtime, and Logs tabs; Settings → Runtime controls runtime preference. | Task-based troubleshooting and one-time setup docs. Keep diagnosis distinct from changing or removing installation state. |
| S15 | Ask an assistant about the current environment: the client launches `portless --env store/local mcp serve`. | Settings → MCP generates configuration with explicit scope and capabilities; the assistant's UI is where questions are asked. | An integration guide using the existing MCP README. Default tools inspect state; lifecycle, traffic control, and sensitive detail require their respective capabilities. |

## How the map becomes documentation

Keep the complete [command reference](../../portless-cli/COMMANDS.md) as the
syntax reference. Organize task guides by the developer's intent:

| Proposed guide | Scenarios | Reader's completion condition |
| --- | --- | --- |
| Run your application | S01, S02, S12 | Open a working app and resume it with retained data. |
| Develop and debug one service | S03 | Observe a code change or reach a breakpoint while peers remain available. |
| Follow a request through your application | S04, S05 | Explain an HTTP failure or a database/cache interaction from observed evidence. |
| Capture a problem | S06 | Produce bounded recording evidence for an investigation. |
| Test loading, failure, and unusual response states | S07, S08 | Produce a named condition, inspect its effect, and restore normal behavior. |
| Choose where a dependency runs | S09 | Switch a provider and explain its classification and write policy. |
| Work across repositories and branches | S11, S10, S13 | Identify project sources, configure an environment, and compare a prepared change. |
| Setup, troubleshooting, and assistant access | S14, S15 | Resolve the specific setup need or connect a scoped client. |

Each guide should include its scenario ID, concrete problem, prerequisites,
starting state, CLI path, UI path where available, app action, observable result,
and return-to-baseline steps. Keep one canonical set of service and scenario
names across its text, commands, example recipe, captions, and screenshots.

## How the map becomes examples

| Example | Role | Existing coverage | Content work to add |
| --- | --- | --- | --- |
| Store | Main everyday application | Startup, stateful checkout, PostgreSQL/Valkey inspection, debugging, persistence | Short named recipes for S03, S04, and S08; capture notes and expected outcomes for each. |
| Dispatch | Multi-repository and alternate-provider application | Three repositories, worktree patch, recordings, caller-specific faults, mocks, QA helper | Link recipes to scenario IDs; add the active QA handoff variant and exact UI steps. |
| Minimal single-service app, proposed | Lowest-prerequisite first encounter | Current discovery supports this workflow; the two public examples use managed resources | Add a small supported HTTP app to demonstrate `portless up` without a container-engine prerequisite. Choose one maintained framework and one visible endpoint. |

Prefer several small exercises in the existing applications over a new example
application for every feature. Each exercise needs a deterministic trigger,
recognizable output, and a bounded way back to the baseline. Rehearse the exact
recipe before marking it publication-ready. Avoid relying on a fixed order ID,
a previous recording's sequence number, or an already modified provider.

## How the map becomes explainer videos

Durations below are production targets. Each film needs a problem, an action,
visible evidence, and a resolved or restored state.

| Production order | Story and target duration | Scenario IDs | Essential sequence |
| --- | --- | --- | --- |
| 1 | A normal day with Portless, 90–120 seconds | S01, S02, S04, S12 | Run `up` → see ready services → open Store → create an order → inspect its traffic → stop and resume with the order retained. |
| 2 | Explain an unexpected request, 60–90 seconds | S04, S05; S06 as a follow-up clip | Perform an app action → find its trace → inspect a response or SQL/cache operation → state the specific cause. Give recording/export its own short follow-up if it obscures the main investigation. |
| 3 | Build the sold-out state, 60–90 seconds | S08 | Accepted coffee-mug order → author the reservation response → enable the mock → show rejection and attribution → disable and show recovery. |
| 4 | Slow one caller, 60–90 seconds | S07 | Baseline Dispatch interaction → enable edge fault → compare route estimation and direct lookup → inspect matching traffic → disable. |
| 5 | Use QA data locally, 60–90 seconds | S09 | Local app → switch API provider → show QA data → demonstrate blocked write and zero upstream mutations → restore local provider. |
| 6 | Compare a change across repositories, 90–120 seconds | S11, S10 | Briefly establish the three sources → baseline route → clone and bind prepared worktree → show scenic result → return to baseline. |

Use the real running application and control plane for product proof. The
existing [explainer preview](../../brand/video-preview/README.md) supplies a
production starting point; the map supplies focused stories and exact outcomes.
Capture product screenshots directly from the running app, following the
[product capture guidance](../../portless-site/src/assets/product/README.md).

Show readable project, environment, service, and edge names. Crop or maximize
the relevant drawer when a result is the teaching point. Explain prepared
dependencies or fixture state briefly; do not make setup time look like startup
time. State transitions and output must come from the demonstrated build.

## Current boundaries that affect the content

| Boundary | Consequence for guides and scripts |
| --- | --- |
| Discovery is static and bounded. | Prepare supported application dependencies; do not describe discovery as installing arbitrary projects or executing their code. |
| Browser and CLI context are independent. | Name the selected environment in every sequence; changing the browser sidebar does not persist terminal selection. |
| Source paths and rescans change compiled topology. | Stop the affected environment for checkout changes/rescans; stop all project environments for logical source changes. |
| Environment clone copies configuration. | Prepare worktrees separately and describe resource data separately. Dispatch's shared-checkout clones run one at a time. |
| Mocks replace target services with fixed HTTP responses. | Cover the demonstrated request paths; explain unmatched `501` responses and restore by disabling the scenario. Stateful behavior and passthrough are deferred. |
| CLI and UI expose different authoring controls. | Keep mock import/preview, rescan, advanced fault filters, and custom recording durations in their actual CLI paths. |
| Recording export is retained JSON evidence. | Portable reproduction bundles, automatic request replay, and data snapshots remain future work. |
| Inspection is bounded and correlation can be inferred. | Show available payloads and confidence; do not claim unlimited capture or exact causality for every protocol. |
| Clean URLs route locally. | Demonstrate local development and comparison; remote sharing is a separate product capability. |

## Source references and review status

The source review covered the [product README](../../README.md),
[implementation boundary](../implementation-status.md),
[command reference](../../portless-cli/COMMANDS.md), the CLI command builders,
Store's checkout and inventory handlers, and both example READMEs.

UI entry points were checked in
[EnvironmentPage](../../portless-web/src/features/environment/EnvironmentPage.tsx),
[ServiceDrawer](../../portless-web/src/features/environment/service/ServiceDrawer.tsx),
[TrafficControls](../../portless-web/src/features/traffic/TrafficControls.tsx),
[RecordingsPanel](../../portless-web/src/features/environment/recordings/RecordingsPanel.tsx),
[FaultsPanel](../../portless-web/src/features/environment/faults/FaultsPanel.tsx),
[MocksPanel](../../portless-web/src/features/mocks/MocksPanel.tsx),
[ConfigureProviderDialog](../../portless-web/src/features/environment/bindings/ConfigureProviderDialog.tsx),
and the project/source/environment configuration components.

The next content work is to turn the P0 scenarios into runnable recipes and
task guides, rehearse their application actions, then capture the first two
videos from those same recipes. A published recipe should record its build,
fixture starting state, successful run, and capture assets against the scenario
ID. New UI or command behavior should update this map before its next capture.
