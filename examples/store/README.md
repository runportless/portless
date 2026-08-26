# Store example

Store is a compact, stateful commerce application that exercises the complete
Portless loop from one checkout:

- `checkout`: a Node service that checks inventory and exposes the public API;
- `orders`: a Node service that persists orders in its PostgreSQL instance and
  caches reads in Redis-compatible Valkey;
- `inventory`: a Java 17 Spring Boot application that atomically reserves
  durable stock in its own PostgreSQL instance;
- `inventory-postgres`: the discovered managed source of truth for stock and
  reservations;
- `orders-postgres`: the discovered managed source of truth for orders;
- `orders-redis`: the discovered managed Valkey cache; and
- `checkout → inventory`, `checkout → orders`,
  `inventory → inventory-postgres`, `orders → orders-postgres`, and
  `orders → orders-redis` source-aware edges.

The Node manifests include a pinned `@nestjs/core` development dependency as
static NestJS discovery evidence while keeping the example servers small and
readable with Node's standard HTTP library. Orders uses the standard `pg` and
`redis` clients for its real data path. Inventory uses the checked-in Gradle
wrapper, Spring Web, Spring JDBC, the PostgreSQL driver, and Spring Boot
Actuator. Both inventory and orders use the ordinary generic host `postgres`
in their datasource templates. Portless scopes that placeholder to the owning
consumer on the first scan, producing `inventory-postgres` and
`orders-postgres` without a declaration or manual rename. Orders' generic
`redis` host similarly becomes `orders-redis`.

## Prerequisites and startup

Store requires Node.js 22.12 or newer, Java 17 or newer, npm, and a running
Docker or Podman engine. From the repository root:

```bash
make
make example-store-dependencies
cd examples/store
portless up
```

The first application build downloads locked npm, Gradle, and Maven
dependencies. Portless automatically uses the first ready container engine and
remembers that choice. Use `portless runtime status` to inspect the choice, or
select one with `portless runtime use docker|podman` while environments are
stopped. No Compose file is created or used.

## Create and retrieve an order

Open the public checkout service in a browser to choose an item, submit an
order, and inspect the complete HTTP response:

```text
http://checkout.local.store.localhost
```

The page sends the same JSON request as this command-line equivalent:

```bash
curl --request POST \
  --header 'content-type: application/json' \
  --data '{"sku":"coffee-mug","quantity":2}' \
  http://checkout.local.store.localhost/checkout
```

Inventory atomically decrements `coffee-mug` stock from 24 to 22 and records a
reservation before orders creates the order. The response includes both the
reservation and a PostgreSQL-generated order ID. Substitute that order ID in
the following request:

```bash
curl http://checkout.local.store.localhost/orders/1
curl http://checkout.local.store.localhost/orders/1
```

The first lookup reports `"cache":"miss"`: orders reads PostgreSQL and fills
Valkey for 60 seconds. The second reports `"cache":"hit"` and returns directly
from Valkey. Redis stores complete order JSON under `store:order:<id>`; it never
decides stock, and an outage falls back to the orders database.

Try `usb-c-cable` in the checkout request to see the inventory database reject
an out-of-stock reservation before the orders database or cache is used. If
order creation fails after a successful reservation, checkout calls inventory
to release the stock again.

The dependency endpoints remain stable and source aware:

```bash
portless url orders-postgres
portless url inventory-postgres
portless url orders-redis
portless connection show inventory:inventory-postgres
portless connection show orders:orders-postgres
portless connection show orders:orders-redis
```

The public endpoints include `inventory-postgres.local.store.portless.test:5432`,
`orders-postgres.local.store.portless.test:5432`, and
`orders-redis.local.store.portless.test:6379`. Inventory receives
`inventory-postgres.via-inventory.local.store.portless.test:5432`; orders
receives
`orders-postgres.via-orders.local.store.portless.test:5432` and
`orders-redis.via-orders.local.store.portless.test:6379`. Each edge therefore
retains its caller and resource-instance identity.

## Inspect real protocol traffic

Open Traffic and expand the checkout trace, or switch to Exchanges. The create
and two lookup requests produce bounded, decoded operations including:

- `POSTGRESQL UPDATE` and `INSERT` from inventory to reserve stock;
- a separate `POSTGRESQL INSERT` from orders for durable order creation;
- `REDIS GET`, followed by `POSTGRESQL SELECT` and `REDIS SET`, on the first
  lookup; and
- `REDIS GET` without a PostgreSQL query on the cached lookup.

Exchange detail shows the parameterized SQL and Redis command payloads, with
the two PostgreSQL targets kept distinct. Startup migrations also appear as
background `POSTGRESQL CREATE` operations. PostgreSQL and Redis do not carry
HTTP trace headers, so Portless relates these operations to the HTTP trace
conservatively by caller identity and timing.

Checkout forwards incoming W3C `traceparent`, `tracestate`, and `baggage`
headers, both B3 encodings, and Datadog propagation headers across its HTTP
dependencies. Portless normalizes those formats into one trace without an
external tracing backend.

## Persistence and debugging

Inventory and orders create and seed their schemas idempotently before becoming
ready. Their PostgreSQL instances remain independent sources of truth, while
Valkey can be discarded and rebuilt. An ordinary `portless down` followed by
`portless up` preserves stock, reservations, and orders in separate managed
volumes. Use `portless down --volumes --yes` only when you explicitly want to
delete that data.

To debug checkout, start Portless from its service directory:

```bash
cd examples/store/apps/checkout
portless up --no-open
```

Attach your IDE to the printed Node inspector. In another IDE window, do the
same from `examples/store/apps/orders` to debug orders simultaneously. Use
`portless service manage orders` to return only orders to normal managed mode,
or `portless up --managed` to return every debug service to managed mode.

## Validation

From the repository root:

```bash
make test-example-store
make test-e2e-store
```

The E2E target uses a temporary Portless home and private ingress, starts two
real PostgreSQL containers plus Valkey, verifies decoded inventory SQL, order
SQL, and cache commands, and proves both stock and orders survive process
restarts and ordinary environment down/up.
