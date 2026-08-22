//go:build darwin

package relay

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/networking"
)

func TestRenderLaunchdPlistEscapesSocketAndUsesFixedHelper(t *testing.T) {
	content, err := renderLaunchdPlist(SetupRequest{TargetSocket: "/Users/dev/a&b/ingress.sock", DNSTargetSocket: "/Users/dev/a&b/dns.sock", UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("invalid launchd plist XML: %v\n%s", err, content)
	}
	text := string(content)
	for _, expected := range []string{launchdLabel, launchdHelperPath, "/Users/dev/a&amp;b/ingress.sock", "<string>501</string>", "<string>20</string>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plist did not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "/Users/dev/a&b/") {
		t.Fatal("socket path was not XML escaped")
	}
}

func TestManagedRelayLoopbackAddressesReserveDNSOutsideEndpointPool(t *testing.T) {
	addresses := managedRelayLoopbackAddresses()
	if len(addresses) != networking.EndpointPoolSize+1 || addresses[0] != "127.77.0.1" || addresses[1] != "127.77.0.2" {
		t.Fatalf("unexpected managed loopback addresses: %#v", addresses)
	}
}

func TestDarwinResolverUsesDedicatedPortlessDNSPort(t *testing.T) {
	if actual := string(renderDarwinResolverConfiguration()); actual != "nameserver 127.77.0.1\nport 1053\n" {
		t.Fatalf("unexpected resolver configuration: %q", actual)
	}
}

func TestPlatformConfigurationOwnerReadsLegacyLaunchdPlist(t *testing.T) {
	content, err := renderLaunchdPlist(SetupRequest{TargetSocket: "/Users/dev/a&b/ingress.sock", DNSTargetSocket: "/Users/dev/a&b/dns.sock", UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dev.portless.relay.plist")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid, socket, dnsSocket, err := platformConfigurationOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if uid != 501 || gid != 20 || socket != "/Users/dev/a&b/ingress.sock" || dnsSocket != "/Users/dev/a&b/dns.sock" {
		t.Fatalf("unexpected owner: uid=%d gid=%d socket=%q dns=%q", uid, gid, socket, dnsSocket)
	}
}

func TestWaitForLaunchdUnloadedAllowsAsynchronousBootout(t *testing.T) {
	calls := 0
	err := waitForLaunchdUnloaded(context.Background(), time.Second, func(context.Context) (bool, error) {
		calls++
		return calls < 3, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("launchd probe calls = %d, want 3", calls)
	}
}

func TestWaitForLaunchdUnloadedReportsProbeAndTimeoutFailures(t *testing.T) {
	probeError := errors.New("launchctl unavailable")
	if err := waitForLaunchdUnloaded(context.Background(), time.Second, func(context.Context) (bool, error) {
		return false, probeError
	}); !errors.Is(err, probeError) {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if err := waitForLaunchdUnloaded(context.Background(), 0, func(context.Context) (bool, error) {
		return true, nil
	}); err == nil || !strings.Contains(err.Error(), "still loaded after") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}
