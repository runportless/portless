# Golden path fixture

This intentionally tiny checkout exercises the full Portless loop without requiring application package installation:

- `checkout`: a NestJS-shaped Node service with an `ORDERS_URL` dependency.
- `orders`: a NestJS-shaped Node service that performs a PostgreSQL handshake and Redis-compatible `PING` for each order.
- `postgres`: discovered managed dependency.
- `redis`: discovered managed Valkey dependency.
- `checkout → orders`, `orders → postgres`, and `orders → redis` edges.

The fixture manifests include `@nestjs/core` only as static discovery evidence; the servers use Node's standard library so `npm run start:dev` is immediately runnable.

```bash
make
cd examples/golden-path
portless up
```

Portless automatically uses the first available engine and remembers that choice. Use `portless runtime status` to inspect both Docker and Podman, or `portless runtime use docker|podman` while environments are stopped to select one explicitly. No Compose file is created or used.

After startup, request `http://checkout.local.golden-path.localhost/checkout`. The topology shows the external request, the internal `checkout → orders` call, and live `orders → postgres` and `orders → redis` traffic. Add a latency, rejection, or abort fault to an edge and repeat the request.
