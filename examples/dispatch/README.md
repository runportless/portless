# Dispatch multi-checkout example

Dispatch is a small courier-control application designed to exercise Portless
across several real source checkouts. The browser app estimates a route,
schedules a delivery, persists it, and follows status events. Its topology is
large enough to make provider changes and source-aware traffic visible while
remaining understandable in one sitting.

The example deliberately has no `portless.yaml`, Compose file, or shared
application monorepo. A bootstrap script materializes these templates as three
independent Git repositories:

| Source | Stack | Services | Responsibility |
| --- | --- | --- | --- |
| `console` | Next.js | `console` | Server-rendered courier dashboard and browser-facing API routes. |
| `operations` | FastAPI and Fastify | `api`, `notifier` | Delivery persistence, orchestration, and the NATS-backed event feed. |
| `maps` | Go | `routing`, `geocoder` | Deterministic route estimates and location lookup. |

Portless also discovers `mysql` and `nats` as managed resources. The resulting
directed topology is:

```text
external -> console -> api -> geocoder
                    |     -> routing -> geocoder
                    |     -> mysql
                    |     -> nats
                    -> notifier -> nats
```

This exercises three languages, several framework detectors, multiple services
in one source, two container-resource plugins, HTTP and TCP edges, trace-context
propagation, stateful data, and asynchronous events.

## Prerequisites

Build Portless and run `portless setup` before starting the example. Dispatch
also needs:

- Go 1.26 or newer;
- Node.js 22.12 or newer and npm;
- Python 3.12 or newer and `uv`; and
- a running Docker or Podman engine for MySQL and NATS.

`uv` must be on the `PATH` inherited by the Portless daemon. If it was installed
after the daemon started, restart the daemon normally before bringing Dispatch
up.

## Bootstrap and run

From this directory, create the independent repositories and install their
locked dependencies:

```bash
make bootstrap-install
dispatch_workspace="$(cd workspace && pwd -P)"

portless project create dispatch \
  --source "console=$dispatch_workspace/checkouts/console" \
  --source "operations=$dispatch_workspace/checkouts/operations" \
  --source "maps=$dispatch_workspace/checkouts/maps"

cd "$dispatch_workspace/checkouts/console"
portless --env dispatch/local up
```

Bootstrap fails closed if its destination already exists. Set
`WORKSPACE=/another/path` when invoking `make` if you want a second materialized
copy. The generated `workspace/` directory is ignored by Git.

After startup, use the dashboard at
`http://console.local.dispatch.localhost`. The Portless environment page is at
`http://portless.localhost/environments/dispatch/local`.

In the dashboard:

1. Pick two locations and estimate a route.
2. Schedule the delivery to write it to MySQL and publish a NATS event.
3. Refresh the event feed to see the notifier's subscription result.
4. Advance the delivery through `assigned`, `picked up`, and `delivered`.

The readable delivery ID, such as `D-0001`, remains stable while the database's
private numeric key stays internal.

## Exercise the multi-checkout model

The included scenario creates a second worktree from the `maps` repository and
applies a tested patch that changes the route strategy and calculation:

```bash
cd /path/to/portless/examples/dispatch
dispatch_workspace="$(cd workspace && pwd -P)"
make scenic-worktree

portless --env dispatch/local env clone scenic
portless --env dispatch/local down
portless --env dispatch/scenic env checkout set maps \
  --path "$dispatch_workspace/worktrees/maps-scenic"
portless --env dispatch/scenic up
```

Open `http://console.scenic.dispatch.localhost` and estimate the same route. It
now reports the `scenic` strategy, while `dispatch/local` still points at the
main maps checkout. The logical project topology did not change; only one
environment's checkout binding did.

The two environments share their console and operations checkout paths, so run
only one at a time. To return to the baseline:

```bash
portless --env dispatch/scenic down
portless --env dispatch/local up
```

## Inspect source-aware traffic

Generate a route estimate, then inspect the complete chain or one exact edge:

```bash
portless --env dispatch/local traffic traces --service console
portless --env dispatch/local traffic list --edge console:api
portless --env dispatch/local traffic list --edge api:routing
portless --env dispatch/local traffic list --edge routing:geocoder
portless --env dispatch/local connection show routing:geocoder
```

`api:geocoder` and `routing:geocoder` remain distinct even though both target
the same service. That distinction is what keeps traffic, recordings, and
faults scoped to the actual caller.

## Record one dependency

Start a bounded recording, estimate a route in the dashboard, and stop it:

```bash
portless --env dispatch/local record start route-estimate \
  --edge api:routing \
  --capture-bodies \
  --duration 5m

portless --env dispatch/local record stop route-estimate
portless --env dispatch/local record show route-estimate
portless --env dispatch/local record export route-estimate
```

Body capture is intentionally explicit because retained application traffic
can contain sensitive data.

## Inject an edge-specific fault

Slow only the routing service's geocoder lookups:

```bash
portless --env dispatch/local fault add slow-routing-geocoder routing:geocoder \
  --method GET \
  --path '/locations/*' \
  --latency 1500 \
  --duration 5m
```

A route estimate now incurs the delay twice, while the API's direct location
searches on `api:geocoder` stay fast. Inspect the matching traffic, then disable
the rule without deleting its definition:

```bash
portless --env dispatch/local traffic list --edge routing:geocoder
portless --env dispatch/local fault disable slow-routing-geocoder
```

## Replace routing with a deterministic mock

Create a profile that reports a planned maintenance failure:

```bash
portless --env dispatch/local mock create routing-maintenance \
  --service routing \
  --description 'Routing maintenance window'

portless --env dispatch/local mock route set routing-maintenance estimates \
  --method GET \
  --path /estimates \
  --status 503 \
  --header 'content-type=application/json' \
  --body '{"error":{"code":"ROUTING_MAINTENANCE","message":"Routing is temporarily unavailable"}}'

portless --env dispatch/local mock preview routing-maintenance \
  --path /estimates
portless --env dispatch/local env bind routing --mock routing-maintenance
```

The dashboard now receives the controlled failure through `api:routing`, and
no request reaches `routing:geocoder`. Restore the local implementation without
restarting unrelated services:

```bash
portless --env dispatch/local env bind routing --local maps
```

The OpenAPI 3.1 contracts in `contracts/` can also seed mock profiles with
`mock create --from-openapi`.

## Substitute a read-only QA API

The small QA helper returns recognizable data and counts every write that
actually reaches it. Start it from a separate terminal:

```bash
cd /path/to/portless/examples/dispatch
make qa-api
```

Then clone the environment, stop the checkout-sharing baseline, and replace
only `api`:

```bash
portless --env dispatch/local env clone qa-assisted
portless --env dispatch/local down
portless --env dispatch/qa-assisted env bind api \
  --remote http://127.0.0.1:19090 \
  --classification qa \
  --write-policy read-only \
  --health-path /health
portless --env dispatch/qa-assisted up
```

The dashboard at `http://console.qa-assisted.dispatch.localhost` shows the QA
provider and its seeded delivery. Estimating remains available, but scheduling
a delivery is rejected locally with `403`; the request never reaches the QA
server. `curl http://127.0.0.1:19090/stats` should continue to report zero
mutations.

Return to the all-local environment with:

```bash
portless --env dispatch/qa-assisted down
portless --env dispatch/qa-assisted env bind api --local operations
portless --env dispatch/local up
```

## Validate the example

The focused suite runs the application unit tests, validates both nested Go
modules, syncs the locked Python environment, and builds the Next.js console:

```bash
make test

# Equivalent repository-root target:
make -C /path/to/portless test-example-dispatch
```

The repository's normal Go suite also verifies static discovery, the compiled
three-source topology, bootstrap safety, the worktree patch, and all OpenAPI
documents. The opt-in integration test runs the real application with managed
MySQL and NATS in an isolated Portless home:

```bash
make -C /path/to/portless test-e2e-dispatch
```

That target may pull container images. It does not touch the developer's normal
Portless state or machine relay.

## Stop and clean up

Stop the baseline and any optional environments you created:

```bash
portless --env dispatch/local down
portless --env dispatch/scenic down
portless --env dispatch/qa-assisted down
```

Commands for scenarios you did not create simply do not apply. Managed MySQL
data survives an ordinary `down`, so the project can be started again later.

To remove Dispatch permanently, run `down --volumes --yes` for each Dispatch
environment you created, then run
`portless --env dispatch/local project forget --yes`. Do not forget the project
before deciding what to do with its managed data. The generated Git
repositories and worktrees remain under the ignored `workspace/` directory
until you choose to remove them.
