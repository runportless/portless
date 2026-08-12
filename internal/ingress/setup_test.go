package ingress

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckAtRecognizesPortlessHealth(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "portless.localhost" || request.URL.Path != "/api/v1/health" {
			t.Errorf("unexpected request host=%q path=%q", request.Host, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ready":true,"apiVersion":"1"}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := checkAt(ctx, listener.Addr().String(), ControlOrigin); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAtRejectsUnrelatedPort80Service(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("not portless"))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	if err := checkAt(context.Background(), listener.Addr().String(), ControlOrigin); err == nil {
		t.Fatal("unrelated HTTP service was accepted as Portless ingress")
	}
}

func TestCheckSocketRecognizesPrivateDaemonIngress(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "portless-ingress-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketPath := filepath.Join(root, "ingress.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "portless.localhost" || request.URL.Path != "/api/v1/health" {
			t.Errorf("unexpected request host=%q path=%q", request.Host, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ready":true,"apiVersion":"1"}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	if err := CheckSocket(context.Background(), socketPath); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSetupRequestRequiresPrivateIngressSocket(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{Executable: executable, TargetSocket: filepath.Join(t.TempDir(), "ingress.sock"), UID: 501, GID: 20}
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
		{status: InstallationStatus{Installed: true, Running: true}, want: "running; daemon unavailable"},
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
	uid, gid, socket, err := relayArgumentValues([]string{
		"/helper", "__ingress", "--socket", "/Users/dev/.portless/ingress.sock", "--uid", "501", "--gid", "20",
	})
	if err != nil {
		t.Fatal(err)
	}
	if uid != 501 || gid != 20 || socket != "/Users/dev/.portless/ingress.sock" {
		t.Fatalf("unexpected relay arguments: uid=%d gid=%d socket=%q", uid, gid, socket)
	}
	if _, _, _, err := relayArgumentValues([]string{"--socket", "/tmp/not-portless.sock", "--uid", "501", "--gid", "20"}); err == nil {
		t.Fatal("invalid socket was accepted")
	}
}

func TestReadInstallationReceiptValidatesFixedPlatformMetadata(t *testing.T) {
	root := t.TempDir()
	details := platformInstallation{
		Name: "test", Service: "portless-test", HelperPath: "/fixed/helper",
		ConfigurationPath: "/fixed/config", ReceiptPath: filepath.Join(root, "ingress.json"),
	}
	receipt := installationReceipt{
		SchemaVersion: installationReceiptSchema, Platform: details.Name, Service: details.Service,
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/Users/dev/.portless/ingress.sock",
		HelperPath: details.HelperPath, ConfigurationPath: details.ConfigurationPath, InstalledAt: time.Now().UTC(),
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
