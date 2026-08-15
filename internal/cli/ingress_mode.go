//go:build !e2e

package cli

// e2ePrivateIngress is deliberately a compile-time switch rather than an
// environment variable or public flag. Production binaries must always prove
// that the machine-wide HTTP/DNS relay belongs to this installation before
// starting an environment.
const e2ePrivateIngress = false
