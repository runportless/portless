//go:build linux

package ingress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformConfigurationOwnerReadsSystemdUnit(t *testing.T) {
	request := SetupRequest{TargetSocket: "/home/dev/Portless Data/ingress.sock", UID: 1000, GID: 1000}
	path := filepath.Join(t.TempDir(), "portless-ingress.service")
	if err := os.WriteFile(path, renderSystemdUnit(request), 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid, socket, err := platformConfigurationOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if uid != request.UID || gid != request.GID || socket != request.TargetSocket {
		t.Fatalf("unexpected owner: uid=%d gid=%d socket=%q", uid, gid, socket)
	}
}
