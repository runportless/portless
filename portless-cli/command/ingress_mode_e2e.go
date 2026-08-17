//go:build e2e

package command

// The E2E binary runs each test with its own PORTLESS_HOME. A machine can only
// have one privileged relay, so isolated tests verify and use the daemon's
// private ingress socket instead. The daemon, discovery, supervisors, edge
// proxies, API, and embedded UI remain the real production implementations.
const e2ePrivateIngress = true
