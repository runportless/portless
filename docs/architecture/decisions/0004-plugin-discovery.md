# ADR 0004: discovery uses bounded in-process plugins

Status: accepted

Portless discovers a source tree through an explicit registry of framework detectors and topology analyzers. Framework detectors return source-relative service candidates and evidence; they do not construct or persist the final project model. The discovery engine resolves overlapping claims, rejects name and topology conflicts, selects the primary service, normalizes paths, and validates the complete model before the application may store it.

Plugins receive a read-only workspace capability rather than unrestricted filesystem paths. The workspace is rooted with `os.Root`, does not follow source symlinks, opens only indexed regular files, detects files changing during a scan, and applies depth, file-count, per-file, total-read, and time limits. Discovery does not execute source commands or make network requests. Filesystem, budget, cancellation, parser, and plugin failures make the scan incomplete; application rescans therefore retain the last complete topology.

The framework registry contains Spring Boot, NestJS, Express, Fastify, Next.js, Go, and FastAPI detectors plus a topology analyzer for application-service URL references. Managed dependencies are deliberately outside the topology analyzer and use the resource-plugin contract in ADR 0005. Ecosystem adapters share manifest and package-manager behavior while framework recognizers retain explicit precedence. NestJS and Next.js, for example, supersede their underlying Express or Fastify claims for the same package; unrelated claims for one service unit are errors.

Portless does not load native Go plugins. Native plugins would run with the daemon's authority, weaken the workspace boundary, and introduce platform and toolchain coupling. A future third-party plugin mechanism, if needed, must use a versioned subprocess protocol with explicit installation, bounded resources, a scrubbed environment, and an independently reviewed permission model.
