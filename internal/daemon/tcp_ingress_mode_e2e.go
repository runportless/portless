//go:build e2e

package daemon

// E2E installations can run beside a developer's real Portless installation.
// They therefore keep TCP dependency proxies on private ephemeral loopback
// ports and do not claim addresses from the machine-wide relay endpoint pool.
const e2ePrivateTCPIngress = true
