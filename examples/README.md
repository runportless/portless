# Portless Examples

These runnable applications demonstrate Portless without requiring a
`portless.yaml` file or an account. Portless discovers their topology from
ordinary application files and sample environment values.

## Store

[Store](store/README.md) is a compact commerce application in one checkout. A
Node.js storefront and order service call a Spring Boot inventory service,
with PostgreSQL storing orders and Valkey supporting the application runtime.

Use Store to exercise the shortest `portless up` path, framework and resource
discovery, source-aware service dependencies, lifecycle controls, traffic and
fault inspection, multi-format trace propagation, and the Node.js debugger
workflow.

## Dispatch

[Dispatch](dispatch/README.md) is a courier operations application spread
across three independent Git checkouts. Its Next.js console estimates routes,
schedules and advances deliveries through FastAPI, and displays notifications
from Fastify. Go routing and geocoder services provide map data, MySQL persists
deliveries, and NATS carries delivery events.

Use Dispatch to exercise a project assembled from multiple source checkouts,
mixed-language discovery, HTTP and TCP dependencies, environment-specific
checkout and worktree bindings, traces, recordings, faults, mocks, and an
explicitly read-only remote-provider workflow.

Start with Store for a focused single-checkout walkthrough. Choose Dispatch
when you want to explore Portless's multi-checkout, environment, and provider
model in a larger topology.
