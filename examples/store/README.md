# Store example

This mixed-language storefront exercises the full Portless loop:

- `checkout`: a NestJS-shaped Node service that checks stock before creating an order.
- `orders`: a NestJS-shaped Node service that performs a PostgreSQL handshake and Redis-compatible `PING` for each order.
- `inventory`: a real Java 17 Spring Boot application with catalog, availability, and Actuator health endpoints.
- `postgres`: discovered managed dependency.
- `redis`: discovered managed Valkey dependency.
- `checkout → inventory`, `checkout → orders`, `orders → postgres`, and `orders → redis` edges.

The Node manifests include `@nestjs/core` as static discovery evidence while their tiny servers use Node's standard library. Inventory uses the checked-in Gradle wrapper, Spring Web, and Spring Boot Actuator. The first run downloads its pinned Gradle and Maven dependencies; Java 17 or newer is required.

```bash
make
cd examples/store
portless up
```

Running from the project root starts every service normally. For the usual
edit/debug loop, run `portless up` from the service directory instead. Portless
starts the rest of the environment normally and starts that service itself with
its discovered debugger enabled:

```bash
cd examples/store/apps/checkout
portless up --no-open
```

The command prints checkout's Node inspector address. In your IDE, choose
**Attach to Process** and select the matching Node process. There is no
Portless-specific launch profile or environment file; Portless already launched
the application with its complete generated environment and keeps the clean URL
routed to it.

To debug orders at the same time, leave checkout running, open a second IDE
window (or debug session), and run:

```bash
cd examples/store/apps/orders
portless up --no-open
```

Attach the second IDE to the matching orders process. Checkout and orders now
both run under Portless in debug mode while inventory, PostgreSQL, and Valkey
run normally. Use `portless service manage orders` to restart only
orders normally, or `portless up --managed` to restart every debug service in
normal managed mode.

Portless automatically uses the first available engine and remembers that choice. Use `portless runtime status` to inspect both Docker and Podman, or `portless runtime use docker|podman` while environments are stopped to select one explicitly. No Compose file is created or used.

After startup, run:

```bash
curl 'http://checkout.local.store.localhost/checkout?sku=coffee-mug&quantity=2'

# Stable host-access endpoints; no random host ports.
portless url postgres
portless url redis
portless connection show orders:postgres
portless connection show orders:redis
```

The public dependency endpoints are `postgres.local.store.portless.test:5432` and `redis.local.store.portless.test:6379`. Orders receives the source-aware variants `postgres.via-orders.local.store.portless.test:5432` and `redis.via-orders.local.store.portless.test:6379`, allowing a rule on one caller-to-dependency edge to stay isolated from every other caller.

Checkout first verifies stock with inventory, then creates the order. The topology shows the external request, `checkout → inventory`, `checkout → orders`, and live `orders → postgres` and `orders → redis` traffic. Try `usb-c-cable` to see an out-of-stock response, or add a latency, rejection, or abort fault to an edge and repeat the request.

Checkout forwards the incoming OpenTelemetry-default W3C `traceparent`,
`tracestate`, and `baggage` headers, both B3 encodings, and Datadog propagation
headers to its HTTP dependencies. Portless supplies W3C trace context at the
ingress proxy and recognizes each supported format, so the external, inventory,
and orders exchanges retain one normalized trace ID without requiring an
external tracing backend. Pass-through context produces exact parentage; a
framework that creates unobserved application spans retains the trace ID while
Portless labels proxy parentage conservatively. The PostgreSQL and Valkey
descendants remain timing-correlated because those raw TCP protocols do not
carry HTTP trace headers.
