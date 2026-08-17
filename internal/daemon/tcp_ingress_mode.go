//go:build !e2e

package daemon

// Production daemons publish stable TCP dependency and public endpoints from
// the relay-owned 127.77.0.0/24 loopback pool.
const e2ePrivateTCPIngress = false
