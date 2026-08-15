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
