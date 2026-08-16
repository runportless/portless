package install

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDNSAddressAvailabilityChecksUDPAndTCP(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := packet.LocalAddr().String()
	if err := dnsAddressAvailable(address); err == nil || !strings.Contains(err.Error(), "UDP DNS address") {
		packet.Close()
		t.Fatalf("UDP collision was not detected: %v", err)
	}
	if err := packet.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dnsAddressAvailable(address); err != nil {
		t.Fatalf("available TCP/UDP address was rejected: %v", err)
	}
}

func TestValidateSetupRequestRequiresPrivateIngressSocket(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{Executable: executable, TargetSocket: filepath.Join(t.TempDir(), "ingress.sock"), DNSTargetSocket: filepath.Join(t.TempDir(), "dns.sock"), UID: 501, GID: 20}
	if err := validateSetupRequest(request); err != nil {
		t.Fatal(err)
	}
	request.TargetSocket = filepath.Join(t.TempDir(), "somewhere-else.sock")
	if err := validateSetupRequest(request); err == nil || !strings.Contains(err.Error(), "ingress.sock") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestInstallationStatusState(t *testing.T) {
	tests := []struct {
		status InstallationStatus
		want   string
	}{
		{status: InstallationStatus{}, want: "not installed"},
		{status: InstallationStatus{Installed: true}, want: "installed; service stopped"},
		{status: InstallationStatus{Installed: true, Running: true}, want: "running; not ready"},
		{status: InstallationStatus{Installed: true, Running: true, Healthy: true}, want: "ready"},
	}
	for _, test := range tests {
		if actual := test.status.State(); actual != test.want {
			t.Errorf("State() = %q, want %q", actual, test.want)
		}
	}
}

func TestValidateUninstallOwnership(t *testing.T) {
	if err := validateUninstallOwnership(InstallationStatus{OwnerUID: 501}, 501, false); err != nil {
		t.Fatal(err)
	}
	if err := validateUninstallOwnership(InstallationStatus{OwnerUID: 502}, 501, false); err == nil || !strings.Contains(err.Error(), "belongs to user ID 502") {
		t.Fatalf("unexpected cross-user error: %v", err)
	}
	if err := validateUninstallOwnership(InstallationStatus{}, 501, false); err == nil || !strings.Contains(err.Error(), "could not be determined") {
		t.Fatalf("unexpected unknown-owner error: %v", err)
	}
	if err := validateUninstallOwnership(InstallationStatus{OwnerUID: 502}, 501, true); err != nil {
		t.Fatalf("force should allow cross-user removal: %v", err)
	}
}

func TestValidateOwnershipRejectsUnknownAndOtherUsers(t *testing.T) {
	if err := ValidateOwnership(InstallationStatus{OwnerUID: 501}, 501); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOwnership(InstallationStatus{OwnerUID: 502}, 501); err == nil || !strings.Contains(err.Error(), "belongs to user ID 502") {
		t.Fatalf("unexpected cross-user error: %v", err)
	}
	if err := ValidateOwnership(InstallationStatus{}, 501); err == nil || !strings.Contains(err.Error(), "could not be determined") {
		t.Fatalf("unexpected unknown-owner error: %v", err)
	}
	if err := ValidateOwnership(InstallationStatus{OwnerUID: 501}, 0); err == nil || !strings.Contains(err.Error(), "non-root requesting user") {
		t.Fatalf("unexpected root-request error: %v", err)
	}
}

func TestRelayArgumentValues(t *testing.T) {
	uid, gid, socket, dnsSocket, err := relayArgumentValues([]string{
		"/helper", "__relay", "--socket", "/Users/dev/.portless/ingress.sock", "--dns-socket", "/Users/dev/.portless/dns.sock", "--uid", "501", "--gid", "20",
	})
	if err != nil {
		t.Fatal(err)
	}
	if uid != 501 || gid != 20 || socket != "/Users/dev/.portless/ingress.sock" || dnsSocket != "/Users/dev/.portless/dns.sock" {
		t.Fatalf("unexpected relay arguments: uid=%d gid=%d socket=%q dns=%q", uid, gid, socket, dnsSocket)
	}
	if _, _, _, _, err := relayArgumentValues([]string{"--socket", "/tmp/not-portless.sock", "--dns-socket", "/tmp/dns.sock", "--uid", "501", "--gid", "20"}); err == nil {
		t.Fatal("invalid socket was accepted")
	}
}

func TestReadInstallationReceiptValidatesFixedPlatformMetadata(t *testing.T) {
	root := t.TempDir()
	details := platformInstallation{
		Name: "test", Service: "portless-test", HelperPath: "/fixed/helper",
		ConfigurationPath: "/fixed/config", ReceiptPath: filepath.Join(root, "relay.json"),
	}
	receipt := installationReceipt{
		SchemaVersion: installationReceiptSchema, Platform: details.Name, Service: details.Service,
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/Users/dev/.portless/ingress.sock",
		DNSTargetSocket:   "/Users/dev/.portless/dns.sock",
		LoopbackAddresses: managedRelayLoopbackAddresses(),
		HelperPath:        details.HelperPath, ConfigurationPath: details.ConfigurationPath, InstalledAt: time.Now().UTC(),
	}
	content, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(details.ReceiptPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := readInstallationReceipt(details)
	if err != nil {
		t.Fatal(err)
	}
	if actual.OwnerUID != receipt.OwnerUID || actual.TargetSocket != receipt.TargetSocket {
		t.Fatalf("unexpected receipt: %#v", actual)
	}
	receipt.HelperPath = "/different/helper"
	content, _ = json.Marshal(receipt)
	if err := os.WriteFile(details.ReceiptPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unexpected mismatched receipt error: %v", err)
	}
}

func TestInstallationReceiptOnlyClaimsLoopbackPoolInSchemaThree(t *testing.T) {
	root := t.TempDir()
	details := platformInstallation{
		Name: "test", Service: "portless-test", HelperPath: "/fixed/helper",
		ConfigurationPath: "/fixed/config", ReceiptPath: filepath.Join(root, "relay.json"),
	}
	receipt := installationReceipt{
		SchemaVersion: 3, Platform: details.Name, Service: details.Service,
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/Users/dev/.portless/ingress.sock",
		DNSTargetSocket: "/Users/dev/.portless/dns.sock", HelperPath: details.HelperPath,
		ConfigurationPath: details.ConfigurationPath, InstalledAt: time.Now().UTC(),
	}
	write := func() {
		content, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(details.ReceiptPath, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "loopback address pool") {
		t.Fatalf("schema 3 receipt without owned addresses was accepted: %v", err)
	}
	receipt.SchemaVersion = 2
	write()
	if _, err := readInstallationReceipt(details); err != nil {
		t.Fatalf("legacy receipt should remain readable without claiming the address pool: %v", err)
	}
}
