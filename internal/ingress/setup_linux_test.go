//go:build linux

package ingress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformConfigurationOwnerReadsSystemdUnit(t *testing.T) {
	request := SetupRequest{TargetSocket: "/home/dev/Portless Data/ingress.sock", DNSTargetSocket: "/home/dev/Portless Data/dns.sock", UID: 1000, GID: 1000}
	path := filepath.Join(t.TempDir(), "portless-ingress.service")
	if err := os.WriteFile(path, renderSystemdUnit(request), 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid, socket, dnsSocket, err := platformConfigurationOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if uid != request.UID || gid != request.GID || socket != request.TargetSocket || dnsSocket != request.DNSTargetSocket {
		t.Fatalf("unexpected owner: uid=%d gid=%d socket=%q dns=%q", uid, gid, socket, dnsSocket)
	}
}

func TestResolvedConfigurationUsesDedicatedPortlessDNSPort(t *testing.T) {
	if actual := string(renderResolvedConfiguration()); actual != "[Resolve]\nDNS=127.77.0.1:1053\nDomains=~portless.test\n" {
		t.Fatalf("unexpected resolved configuration: %q", actual)
	}
}
