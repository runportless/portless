package installation

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	relayruntime "github.com/runportless/portless/portless-relay/runtime"
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

func TestArtifactDirectoryMustBeOwnedAndNotWritableByOtherUsers(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "artifacts")
	if err := ensureArtifactDirectory(directory, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := ensureArtifactDirectory(directory, os.Geteuid(), os.Getegid()); err == nil || !strings.Contains(err.Error(), "writable by group or other") {
		t.Fatalf("unsafe artifact directory was accepted: %v", err)
	}
	link := filepath.Join(t.TempDir(), "artifacts-link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureArtifactDirectory(link, os.Geteuid(), os.Getegid()); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("artifact directory symlink was accepted: %v", err)
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
	request.TargetSocket = filepath.Join(t.TempDir(), "line\nbreak", "ingress.sock")
	if err := validateSetupRequest(request); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("service path control character was accepted: %v", err)
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

func TestReadInstallationReceiptValidatesFixedPlatformMetadata(t *testing.T) {
	root := t.TempDir()
	details := platformInstallation{
		Name: "test", Service: "portless-test", HelperPath: "/fixed/helper",
		ConfigurationPath: "/fixed/config", ReceiptPath: filepath.Join(root, "relay.json"), ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
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
	if err := os.WriteFile(details.ReceiptPath, content, 0o644); err != nil {
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
	if err := os.WriteFile(details.ReceiptPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unexpected mismatched receipt error: %v", err)
	}
}

func TestInstallationReceiptRequiresCurrentSchemaAndSafeLoopbackManifest(t *testing.T) {
	root := t.TempDir()
	details := platformInstallation{
		Name: "test", Service: "portless-test", HelperPath: "/fixed/helper",
		ConfigurationPath: "/fixed/config", ReceiptPath: filepath.Join(root, "relay.json"), ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
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
		if err := os.WriteFile(details.ReceiptPath, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "loopback address pool") {
		t.Fatalf("schema 3 receipt without owned addresses was accepted: %v", err)
	}
	receipt.SchemaVersion = 2
	write()
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("legacy receipt was accepted: %v", err)
	}
	receipt.SchemaVersion = installationReceiptSchema
	receipt.LoopbackAddresses = []string{"127.77.0.1", "127.77.0.200"}
	write()
	actual, err := readInstallationReceipt(details)
	if err != nil {
		t.Fatalf("safe stale manifest was rejected: %v", err)
	}
	if receiptUsesCurrentLoopbackPool(actual) {
		t.Fatal("stale loopback manifest was reported as current")
	}
	receipt.LoopbackAddresses = []string{"127.77.0.1", "127.0.0.1"}
	write()
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "loopback address pool") {
		t.Fatalf("unsafe loopback manifest was accepted: %v", err)
	}
	receipt.LoopbackAddresses = []string{"127.77.0.1"}
	write()
	file, err := os.OpenFile(details.ReceiptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n{}"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("receipt with trailing data was accepted: %v", err)
	}
	if err := os.WriteFile(details.ReceiptPath, []byte(strings.Repeat("x", (64<<10)+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readInstallationReceipt(details); err == nil || !strings.Contains(err.Error(), "larger than 64 KiB") {
		t.Fatalf("oversized receipt was accepted: %v", err)
	}
}

func TestValidateRuntimeReceiptRequiresExactInstalledIdentity(t *testing.T) {
	receipt := installationReceipt{
		OwnerUID: 501, OwnerGID: 20,
		TargetSocket:      "/Users/dev/.portless/ingress.sock",
		DNSTargetSocket:   "/Users/dev/.portless/dns.sock",
		LoopbackAddresses: managedRelayLoopbackAddresses(),
	}
	config := relayruntime.Identity{
		TargetSocket: receipt.TargetSocket, DNSTargetSocket: receipt.DNSTargetSocket,
		UID: receipt.OwnerUID, GID: receipt.OwnerGID,
	}
	if err := validateRuntimeReceipt(receipt, config); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*relayruntime.Identity)
	}{
		{name: "uid", mutate: func(config *relayruntime.Identity) { config.UID++ }},
		{name: "gid", mutate: func(config *relayruntime.Identity) { config.GID++ }},
		{name: "http socket", mutate: func(config *relayruntime.Identity) { config.TargetSocket = "/tmp/other/ingress.sock" }},
		{name: "dns socket", mutate: func(config *relayruntime.Identity) { config.DNSTargetSocket = "/tmp/other/dns.sock" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mismatched := config
			test.mutate(&mismatched)
			if err := validateRuntimeReceipt(receipt, mismatched); err == nil || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("mismatched runtime identity was accepted: %v", err)
			}
		})
	}
	receipt.LoopbackAddresses = []string{"127.77.0.1"}
	if err := validateRuntimeReceipt(receipt, config); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale runtime receipt was accepted: %v", err)
	}
}
