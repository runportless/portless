package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/daemon"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/runtime/container"
)

func TestDaemonChecksHealthyExistingDaemonWithoutStartingIt(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "portless-doctor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	paths, err := bootstrap.ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Token, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := bootstrap.ControlRecord{
		PID: os.Getpid(), Port: 7331, ProtocolVersion: daemon.ProtocolVersion, APIVersion: api.APIVersion,
		InstallationID: "installation", InstanceID: "instance", BuildID: "build", State: "ready", HandoffReady: true,
		TokenPath: paths.Token, StartedAt: time.Now().UTC(), ProcessHint: "portless-test",
	}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Control, content, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", paths.Ingress)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(paths.Ingress, 0o600); err != nil {
		t.Fatal(err)
	}
	dnsListener, err := net.Listen("unix", paths.DNS)
	if err != nil {
		t.Fatal(err)
	}
	defer dnsListener.Close()
	if err := os.Chmod(paths.DNS, 0o600); err != nil {
		t.Fatal(err)
	}

	dependencies := dependencies{
		checkDaemon:        func(context.Context, bootstrap.Paths) (bootstrap.ControlRecord, error) { return record, nil },
		checkIngressSocket: func(context.Context, string) error { return nil },
		checkDNSSocket:     func(context.Context, string) error { return nil },
		processAlive:       func(int) error { return nil },
	}
	report := run(context.Background(), paths, ScopeDaemon, os.Getuid(), dependencies)
	if !report.Healthy || report.Summary.Passed != 8 || report.Summary.Failed != 0 {
		t.Fatalf("unexpected daemon report: %#v", report)
	}
	for _, check := range report.Checks {
		if check.Status != StatusPass {
			t.Errorf("check %s = %s, want pass: %s", check.Code, check.Status, check.Detail)
		}
	}
}

func TestDaemonChecksDoNotCreateMissingDataDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	paths, err := bootstrap.ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	report := run(context.Background(), paths, ScopeDaemon, os.Getuid(), dependencies{})
	if report.Healthy || report.Summary.Failed != 1 || report.Summary.Skipped != 6 {
		t.Fatalf("unexpected missing-daemon report: %#v", report)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("doctor created or changed its missing data directory: %v", err)
	}
}

func TestRelayChecksHealthyInstallation(t *testing.T) {
	paths, err := bootstrap.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid := 501
	dependencies := dependencies{
		lookupIP: loopbackLookup,
		inspectRelay: func(context.Context) (ingress.InstallationStatus, error) {
			return healthyRelayStatus(paths, uid), nil
		},
		portListening: func(context.Context) (bool, error) { return true, nil },
		dnsListening:  func(context.Context) (bool, error) { return true, nil },
	}
	report := run(context.Background(), paths, ScopeRelay, uid, dependencies)
	if !report.Healthy || report.Summary.Passed != 13 || report.Summary.Failed != 0 {
		t.Fatalf("unexpected healthy relay report: %#v", report)
	}
}

func TestRelayResolverUnavailableIsInformationalOnlyWhenEndToEndHealthy(t *testing.T) {
	paths, err := bootstrap.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid := 501
	base := dependencies{
		inspectRelay: func(context.Context) (ingress.InstallationStatus, error) {
			return healthyRelayStatus(paths, uid), nil
		},
		portListening: func(context.Context) (bool, error) { return true, nil },
		dnsListening:  func(context.Context) (bool, error) { return true, nil },
	}

	unavailable := base
	unavailable.lookupIP = func(_ context.Context, name string) ([]net.IPAddr, error) {
		if strings.HasSuffix(name, ".localhost") {
			return nil, errors.New("resolver does not expose .localhost")
		}
		return loopbackLookup(context.Background(), name)
	}
	report := run(context.Background(), paths, ScopeRelay, uid, unavailable)
	if !report.Healthy || report.Summary.Informational != 1 || report.Summary.Warnings != 0 || report.Summary.Failed != 0 {
		t.Fatalf("resolver limitation should be informational when clean URLs work: %#v", report)
	}
	if check := checkByCode(t, report, "relay.localhost_dns"); check.Status != StatusInfo || check.Remediation != "" {
		t.Fatalf("unexpected resolver informational check: %#v", check)
	}

	unhealthy := unavailable
	unhealthy.inspectRelay = func(context.Context) (ingress.InstallationStatus, error) {
		return ingress.InstallationStatus{Platform: "launchd", ConfigurationPath: "/fixed/config"}, nil
	}
	unhealthy.portListening = func(context.Context) (bool, error) { return false, nil }
	report = run(context.Background(), paths, ScopeRelay, uid, unhealthy)
	if report.Summary.Informational != 0 || report.Summary.Warnings != 1 || report.Summary.Failed != 2 {
		t.Fatalf("resolver limitation should remain a warning while clean URLs are unavailable: %#v", report)
	}

	unsafe := base
	unsafe.lookupIP = func(_ context.Context, name string) ([]net.IPAddr, error) {
		if strings.HasSuffix(name, ".localhost") {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		}
		return loopbackLookup(context.Background(), name)
	}
	report = run(context.Background(), paths, ScopeRelay, uid, unsafe)
	if report.Healthy || report.Summary.Failed != 1 {
		t.Fatalf("non-loopback .localhost resolution should fail: %#v", report)
	}
}

func TestRelayChecksExplainMissingInstallationAndPortConflict(t *testing.T) {
	paths, err := bootstrap.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dependencies := dependencies{
		lookupIP: loopbackLookup,
		inspectRelay: func(context.Context) (ingress.InstallationStatus, error) {
			return ingress.InstallationStatus{Platform: "launchd", ConfigurationPath: "/fixed/config"}, nil
		},
		portListening: func(context.Context) (bool, error) { return true, nil },
		dnsListening:  func(context.Context) (bool, error) { return false, nil },
	}
	report := run(context.Background(), paths, ScopeRelay, os.Getuid(), dependencies)
	if report.Healthy || report.Summary.Failed != 2 || report.Summary.Skipped != 9 {
		t.Fatalf("unexpected missing relay report: %#v", report)
	}
	if checkByCode(t, report, "relay.installation").Remediation != "Run `portless relay install` or `portless setup`." {
		t.Fatalf("missing relay did not provide install remediation: %#v", report)
	}
	if check := checkByCode(t, report, "relay.port_80"); check.Status != StatusFail || check.Summary != "Port 80 is occupied by an unrecognized listener" {
		t.Fatalf("unexpected port conflict check: %#v", check)
	}
}

func TestRuntimeUnavailableIsWarningBecauseContainersAreOptional(t *testing.T) {
	dependencies := dependencies{probeRuntimes: func(context.Context) []container.ProbeResult {
		return []container.ProbeResult{
			{Name: container.RuntimePodman, State: "missing", Reason: "not installed"},
			{Name: container.RuntimeDocker, State: "failed", Reason: "engine stopped"},
		}
	}}
	report := run(context.Background(), bootstrap.Paths{}, ScopeRuntime, os.Getuid(), dependencies)
	if !report.Healthy || report.Summary.Warnings != 1 || report.Summary.Failed != 0 {
		t.Fatalf("unexpected runtime report: %#v", report)
	}
}

func TestParseScopeRejectsUnknownComponent(t *testing.T) {
	if _, err := ParseScope("database"); err == nil {
		t.Fatal("unknown doctor scope was accepted")
	}
}

func loopbackLookup(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("::1")}}, nil
}

func healthyRelayStatus(paths bootstrap.Paths, uid int) ingress.InstallationStatus {
	return ingress.InstallationStatus{
		Platform: "launchd", Service: "dev.portless.ingress", Installed: true,
		Running: true, Healthy: true, HTTPHealthy: true, DNSHealthy: true, HelperPresent: true, ConfigurationPresent: true,
		ReceiptPresent: true, ResolverPresent: true, ResolverHealthy: true, OwnerUID: uid, OwnerGID: 20, TargetSocket: paths.Ingress, DNSTargetSocket: paths.DNS,
		EndpointPoolReady: true, EndpointPoolDetail: "64/64 addresses configured on lo0",
		HelperPath: "/fixed/helper", ConfigurationPath: "/fixed/config", ReceiptPath: "/fixed/receipt", ResolverPath: "/fixed/resolver",
	}
}

func checkByCode(t *testing.T, report Report, code string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Code == code {
			return check
		}
	}
	t.Fatalf("report did not contain check %s: %#v", code, report)
	return Check{}
}
