# ADR 0001: one machine-local daemon

Status: accepted

Portless uses one lazily started loopback daemon per user account. It centrally allocates ports, owns stable `.localhost` ingress, coordinates projects, and keeps the UI alive independently from any one CLI invocation.

A per-project daemon would make two checkouts race for control ports and would duplicate the gateway. A permanently installed system service would add setup and privilege. The CLI therefore starts the user daemon on demand under a startup lock and discovers it through a mode-0600 record.
