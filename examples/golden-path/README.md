# Golden path fixture

This intentionally tiny checkout exercises the full Portless loop without requiring application package installation:

- `checkout`: a NestJS-shaped Node service with an `ORDERS_URL` dependency.
- `orders`: a NestJS-shaped Node service with Postgres and Redis-compatible configuration.
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

After startup, request `http://checkout.golden-path.localhost/checkout`. The external request and internal `checkout → orders` call appear as separate traffic events. Add a latency or 503 fault to the internal edge and repeat the request.
