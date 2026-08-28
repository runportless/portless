package runtime

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommandPassesValidatedRuntimeIdentityToAuthorization(t *testing.T) {
	var stderr bytes.Buffer
	authorizeError := errors.New("stop before binding")
	var received Identity
	code := Command([]string{
		"--socket", "/tmp/portless/ingress.sock",
		"--dns-socket", "/tmp/portless/dns.sock",
		"--uid", "501",
		"--gid", "20",
	}, &stderr, func(_ context.Context, identity Identity) error {
		received = identity
		return authorizeError
	})
	if code != 1 || !strings.Contains(stderr.String(), authorizeError.Error()) {
		t.Fatalf("relay command code=%d stderr=%q", code, stderr.String())
	}
	if received.TargetSocket != "/tmp/portless/ingress.sock" || received.DNSTargetSocket != "/tmp/portless/dns.sock" || received.UID != 501 || received.GID != 20 {
		t.Fatalf("authorization received unexpected runtime identity: %#v", received)
	}
}

func TestCommandRejectsInvalidRuntimeIdentityBeforeAuthorization(t *testing.T) {
	var stderr bytes.Buffer
	authorized := false
	code := Command([]string{
		"--socket", "/tmp/portless/not-ingress.sock",
		"--dns-socket", "/tmp/portless/dns.sock",
		"--uid", "501",
		"--gid", "20",
	}, &stderr, func(context.Context, Identity) error {
		authorized = true
		return nil
	})
	if code != 1 || authorized || !strings.Contains(stderr.String(), "ingress.sock") {
		t.Fatalf("invalid runtime identity code=%d authorized=%v stderr=%q", code, authorized, stderr.String())
	}
}
