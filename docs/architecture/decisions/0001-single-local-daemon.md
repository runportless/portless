# ADR 0001: one machine-local daemon

Status: accepted

Portless uses one lazily started daemon per user account. It centrally allocates ports, serves the control plane and application ingress through a private Unix socket, coordinates projects, and keeps the UI alive independently from any one CLI invocation.

A per-project daemon would make two checkouts race for control ports and would duplicate the gateway. The CLI therefore starts the user daemon on demand under a startup lock and discovers its private control port through a mode-0600 record inside a mode-0700 user-owned directory.

The discovery record is not sufficient proof of identity. Every API-using CLI invocation authenticates a fixed lifecycle endpoint with the private CLI token and compares the response with both the record and local installation state. The handshake binds together:

- a fingerprint derived from the installation ownership key;
- a random instance fingerprint generated for each daemon start;
- the daemon PID and start time;
- lifecycle and application API protocol versions; and
- a SHA-256 fingerprint of the executable that started the daemon.

This catches stale development daemons even when their coarse API version has not changed. It also prevents a healthy process listening on the recorded port, a reused PID, or an edited record from being treated as the daemon. The private identity and shutdown routes require CLI bearer authentication and are not served on application hosts or available to browser sessions. The control API separately exposes browser-safe daemon status and guarded restart routes; browser mutations still require the session CSRF token and same-origin checks.

An authenticated older build is replaced automatically when there are no active environments or every managed runtime can be handed off safely. Explicit `portless daemon stop`, `portless daemon restart`, and UI restart use the same handoff guard. The UI never offers force restart. CLI `--force` may interrupt active environments and can replace a legacy daemon after additionally checking process ownership and command arguments. Raw PID signaling is never the normal control path.

Browser sessions are retained in a mode-0600 session record under the user's private Portless directory. That lets an authenticated control-plane tab reconnect after an in-process daemon replacement while preserving server-side expiry and logout revocation.

Clean HTTP URLs and conventional TCP ports require privileged loopback setup. `portless setup` is the first-run shortcut for `portless relay install`; both install one deliberately narrow system service that binds `127.0.0.1:80` and UDP/TCP `127.77.0.1:1053`, immediately drops to the installing user's UID/GID, and forwards bytes to that user's separate mode-0600 HTTP and DNS daemon sockets. The scoped resolver uses the dedicated high DNS port instead of competing with a system-owned port-53 listener. On macOS the same installation provisions a bounded, reserved `127.77.0.x` alias pool on `lo0` and a resolver client for descendant `localhost` names; Linux already treats `127/8` as bindable loopback and `systemd-resolved` synthesizes those localhost records. The relay returns the protocol-defined loopback answer for every name in the special-use `localhost` zone even while the user daemon is unavailable. Dynamic `portless.test` queries are forwarded to the daemon and answered only from its durable endpoint-allocation table; names outside those two narrow zones are refused, so the relay does not become a general DNS forwarder. Discovery, orchestration, state, authentication, routing, and the UI remain in the unprivileged daemon. Ongoing inspection, restart, and removal use the explicit `portless relay` command group.

Full installation removal is a separate top-level lifecycle because application reset intentionally preserves installation identity and restarts an empty daemon. `portless uninstall` is non-mutating until explicit confirmation. Confirmed removal first authenticates and purges every recorded process/container runtime, then removes the current user's machine-wide relay while its ownership receipt and CLI helper remain available, stops the authenticated daemon under the startup lock without replacement, atomically detaches and deletes the validated user-owned data root, and removes only a verified installed CLI launcher. Each boundary stops later steps on failure. Full uninstall never inherits the relay-only ownership override, and it preserves an executable target when the launcher is a development symlink.
