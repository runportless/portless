# ADR 0005: managed resources use one declarative plugin contract

Status: accepted

Managed dependencies are logical `resource` services. Their persisted identity is a canonical plugin type and version, and their client-facing protocol is ordinary TCP. A directed connection records the resource plugin responsible for formatting its process environment. Resource-specific protocol enums, container templates, and application-layer switches are not part of the version 1 model.

Each trusted in-process resource plugin implements three capabilities:

- `Detect` reads the same root-confined, bounded workspace used by framework discovery and returns evidence-backed resource candidates plus consumer environment claims.
- `Plan` resolves a version to a fully qualified container image, stable client port, static or generated-secret environment, optional command, named persistent volumes, and bounded TCP or exec readiness.
- `Bind` converts an active edge and the managed container's environment into one or more process variables, together with a separately validated redacted view for APIs and logs.

The shared registry canonicalizes aliases, rejects duplicate identifiers, validates the default and requested plans, contains plugin panics, and verifies that bindings match the target resource. The discovery engine owns candidate normalization, duplicate-name and environment-claim detection, deterministic ordering, and complete-model validation. The container manager consumes only validated plans and verifies ownership, resource type, version, image, and volume labels before starting or adopting runtime state. It does not switch on a database or broker name.

The initial registry contains PostgreSQL 17 (`postgres`, alias `postgresql`), Valkey 8 (`valkey`, alias `redis`), MySQL 8.4, and NATS 2. Adding another built-in resource consists of implementing the plugin and registering it; discovery, compilation, networking, and Docker/Podman lifecycle code remain unchanged.

Version 1 intentionally has no compatibility layer for the earlier container-template model. Persisted project definitions are wrapped in an explicit format version; older definitions fail with reset-and-rediscover guidance rather than being interpreted ambiguously. Daemon authentication and guarded reset inventory do not decode that model: they use normalized environment status and runtime-ownership rows so `portless reset --force --yes` can still verify and stop owned runtimes before erasing incompatible state. Native Go plugins are not loaded dynamically. A future third-party mechanism must use a versioned subprocess boundary, explicit installation and permissions, bounded resources, and a scrubbed environment.
