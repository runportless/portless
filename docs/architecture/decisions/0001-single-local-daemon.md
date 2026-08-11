# ADR 0001: one machine-local daemon

Status: accepted

Portless uses one lazily started daemon per user account. It centrally allocates ports, serves the control plane and application ingress through a private Unix socket, coordinates projects, and keeps the UI alive independently from any one CLI invocation.

A per-project daemon would make two checkouts race for control ports and would duplicate the gateway. The CLI therefore starts the user daemon on demand under a startup lock and discovers its private control port through a mode-0600 record.

Clean URLs require the conventional HTTP port. `portless setup` installs one deliberately narrow system service that binds only `127.0.0.1:80`, immediately drops to the installing user's UID/GID, and forwards bytes to that user's mode-0600 daemon socket. Discovery, orchestration, state, authentication, routing, and the UI remain in the unprivileged daemon; the system service has no control-plane logic.
