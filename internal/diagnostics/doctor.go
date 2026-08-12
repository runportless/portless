package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/runtime/container/docker"
	"github.com/portless-run/portless/internal/runtime/container/podman"
)

type Scope string

const (
	ScopeAll     Scope = "all"
	ScopeDaemon  Scope = "daemon"
	ScopeRelay   Scope = "relay"
	ScopeRuntime Scope = "runtime"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusInfo Status = "info"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

type Check struct {
	Code        string `json:"code"`
	Component   string `json:"component"`
	Status      Status `json:"status"`
	Summary     string `json:"summary"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type Summary struct {
	Passed        int `json:"passed"`
	Informational int `json:"informational"`
	Warnings      int `json:"warnings"`
	Failed        int `json:"failed"`
	Skipped       int `json:"skipped"`
}

type Report struct {
	Scope   Scope   `json:"scope"`
	Healthy bool    `json:"healthy"`
	Summary Summary `json:"summary"`
	Checks  []Check `json:"checks"`
}

type dependencies struct {
	checkDaemon        func(context.Context, bootstrap.Paths) (bootstrap.ControlRecord, error)
	inspectRelay       func(context.Context) (ingress.InstallationStatus, error)
	checkIngressSocket func(context.Context, string) error
	processAlive       func(int) error
	lookupIP           func(context.Context, string) ([]net.IPAddr, error)
	portListening      func(context.Context) (bool, error)
	probeRuntimes      func(context.Context) []container.ProbeResult
}

func ParseScope(value string) (Scope, error) {
	if value == "" {
		return ScopeAll, nil
	}
	scope := Scope(strings.ToLower(strings.TrimSpace(value)))
	switch scope {
	case ScopeAll, ScopeDaemon, ScopeRelay, ScopeRuntime:
		return scope, nil
	default:
		return "", fmt.Errorf("doctor scope must be daemon, relay, or runtime")
	}
}

// Run performs read-only diagnostics. It never starts the daemon, invokes
// sudo, changes runtime selection, or repairs the relay.
func Run(ctx context.Context, paths bootstrap.Paths, scope Scope, uid int) (Report, error) {
	if _, err := ParseScope(string(scope)); err != nil {
		return Report{}, err
	}
	return run(ctx, paths, scope, uid, defaultDependencies()), nil
}

func run(ctx context.Context, paths bootstrap.Paths, scope Scope, uid int, dependencies dependencies) Report {
	checks := make([]Check, 0, 16)
	if scope == ScopeAll || scope == ScopeDaemon {
		checks = append(checks, daemonChecks(ctx, paths, uid, dependencies)...)
	}
	if scope == ScopeAll || scope == ScopeRelay {
		checks = append(checks, relayChecks(ctx, paths, uid, dependencies)...)
	}
	if scope == ScopeAll || scope == ScopeRuntime {
		checks = append(checks, runtimeChecks(ctx, dependencies)...)
	}
	report := Report{Scope: scope, Checks: checks}
	for _, check := range checks {
		switch check.Status {
		case StatusPass:
			report.Summary.Passed++
		case StatusInfo:
			report.Summary.Informational++
		case StatusWarn:
			report.Summary.Warnings++
		case StatusFail:
			report.Summary.Failed++
		case StatusSkip:
			report.Summary.Skipped++
		}
	}
	report.Healthy = report.Summary.Failed == 0
	return report
}

func daemonChecks(ctx context.Context, paths bootstrap.Paths, uid int, dependencies dependencies) []Check {
	checks := make([]Check, 0, 6)
	if detail, err := securePath(paths.Root, uid, pathDirectory); err != nil {
		remediation := fmt.Sprintf("Confirm the directory belongs to your user, then run `chmod 700 %s`.", strconv.Quote(paths.Root))
		if info, statErr := os.Stat(paths.Root); statErr != nil || !info.IsDir() {
			remediation = "Run `portless up` or `portless ui` to initialize and start Portless."
			checks = append(checks, failed("daemon.data_directory", "daemon", "Data directory is not usable", detailOrError(detail, err), remediation))
			return append(checks,
				skipped("daemon.control_record", "daemon", "Control record was not checked"),
				skipped("daemon.process", "daemon", "Daemon process was not checked"),
				skipped("daemon.api", "daemon", "Daemon API was not checked"),
				skipped("daemon.authentication", "daemon", "CLI authentication was not checked"),
				skipped("daemon.ingress_socket", "daemon", "Private ingress socket was not checked"),
			)
		}
		checks = append(checks, failed("daemon.data_directory", "daemon", "Data directory ownership or permissions are unsafe", detailOrError(detail, err), remediation))
	} else {
		checks = append(checks, passed("daemon.data_directory", "daemon", "Data directory is private and accessible", detail))
	}

	record, recordErr := bootstrap.ReadControl(paths)
	controlDetail, controlPathErr := securePath(paths.Control, uid, pathRegular)
	if recordErr != nil {
		detail := joinDetails(detailOrError(controlDetail, controlPathErr), errorDetail("read control record", recordErr))
		checks = append(checks, failed("daemon.control_record", "daemon", "Daemon control record is missing or invalid", detail, "Run `portless up` or `portless ui` to start a fresh daemon."))
		return append(checks,
			skipped("daemon.process", "daemon", "Daemon process was not checked"),
			skipped("daemon.api", "daemon", "Daemon API was not checked"),
			skipped("daemon.authentication", "daemon", "CLI authentication was not checked"),
			skipped("daemon.ingress_socket", "daemon", "Private ingress socket was not checked"),
		)
	}
	controlProblems := make([]string, 0, 2)
	if controlPathErr != nil {
		controlProblems = append(controlProblems, detailOrError(controlDetail, controlPathErr))
	}
	if record.TokenPath != paths.Token {
		controlProblems = append(controlProblems, fmt.Sprintf("configured token: %s; expected: %s", record.TokenPath, paths.Token))
	}
	legacyRecord := record.ProtocolVersion == "" || record.InstallationID == "" || record.InstanceID == "" || record.BuildID == "" || record.StartedAt.IsZero()
	if legacyRecord {
		controlProblems = append(controlProblems, "authenticated daemon identity metadata is missing")
	}
	if len(controlProblems) > 0 {
		remediation := "Stop the daemon, correct the data-directory ownership, and run `portless up` to recreate its control record."
		if legacyRecord && len(controlProblems) == 1 {
			remediation = "Replace this legacy daemon once with `portless daemon restart --force`."
		}
		checks = append(checks, failed("daemon.control_record", "daemon", "Daemon control record ownership, permissions, or paths are unsafe", strings.Join(controlProblems, "; "), remediation))
	} else {
		checks = append(checks, passed("daemon.control_record", "daemon", "Daemon control record is valid", fmt.Sprintf("%s; port %d", controlDetail, record.Port)))
	}

	if err := dependencies.processAlive(record.PID); err != nil {
		checks = append(checks, failed("daemon.process", "daemon", "Recorded daemon process is not running", fmt.Sprintf("pid %d: %v", record.PID, err), "Run `portless up` or `portless ui` to start a fresh daemon."))
	} else {
		checks = append(checks, passed("daemon.process", "daemon", "Daemon process is running", fmt.Sprintf("pid %d", record.PID)))
	}

	healthRecord, healthErr := dependencies.checkDaemon(ctx, paths)
	switch {
	case errors.Is(healthErr, bootstrap.ErrLegacyDaemon):
		checks = append(checks, failed("daemon.api", "daemon", "Legacy daemon cannot prove its identity to this CLI", healthErr.Error(), "Replace it once with `portless daemon restart --force`. The guarded fallback verifies process ownership and command arguments before signaling it."))
	case healthErr != nil:
		checks = append(checks, failed("daemon.api", "daemon", "Daemon identity or compatibility check failed", healthErr.Error(), "Run `portless daemon status`, then `portless daemon restart`; use `--force` only for a verified legacy daemon or when interrupting active environments is acceptable."))
	case healthRecord.APIVersion != api.APIVersion:
		checks = append(checks, failed("daemon.api", "daemon", "Daemon protocol does not match this CLI", fmt.Sprintf("daemon: %s; CLI: %s", healthRecord.APIVersion, api.APIVersion), "Restart the Portless daemon with the current CLI."))
	default:
		checks = append(checks, passed("daemon.api", "daemon", "Daemon identity is authenticated and compatible", fmt.Sprintf("protocol %s; API %s; instance %s; build %s", healthRecord.ProtocolVersion, healthRecord.APIVersion, shortIdentity(healthRecord.InstanceID), shortIdentity(healthRecord.BuildID))))
	}
	if healthErr == nil {
		if len(healthRecord.RecoveryProblems) > 0 {
			checks = append(checks, failed("daemon.runtime_recovery", "daemon", "One or more service runtimes could not be recovered", strings.Join(healthRecord.RecoveryProblems, "; "), "Run `portless status` and inspect affected service logs. Stop orphaned services before starting replacements."))
		} else {
			checks = append(checks, passed("daemon.runtime_recovery", "daemon", "Persisted runtime ownership and proxy routes are consistent", "daemon state "+healthRecord.State))
		}
	}

	authDetail, authErr := securePath(paths.Token, uid, pathRegular)
	if authErr == nil {
		content, readErr := os.ReadFile(paths.Token)
		if readErr != nil {
			authErr = readErr
		} else if strings.TrimSpace(string(content)) == "" {
			authErr = errors.New("authentication token is empty")
		}
	}
	if authErr != nil {
		checks = append(checks, failed("daemon.authentication", "daemon", "CLI authentication token is not usable", detailOrError(authDetail, authErr), "Restart the daemon after correcting ownership and permissions in the Portless data directory."))
	} else {
		checks = append(checks, passed("daemon.authentication", "daemon", "CLI authentication token is private and readable", authDetail))
	}

	socketDetail, socketPathErr := securePath(paths.Ingress, uid, pathSocket)
	var socketHealthErr error
	if info, err := os.Lstat(paths.Ingress); err == nil && info.Mode()&os.ModeSocket != 0 {
		socketHealthErr = dependencies.checkIngressSocket(ctx, paths.Ingress)
	}
	if socketPathErr != nil || socketHealthErr != nil {
		detail := joinDetails(detailOrError(socketDetail, socketPathErr), errorDetail("health check", socketHealthErr))
		checks = append(checks, failed("daemon.ingress_socket", "daemon", "Private ingress socket is not usable", detail, "Restart the Portless daemon by running `portless up` or `portless ui`."))
	} else {
		checks = append(checks, passed("daemon.ingress_socket", "daemon", "Private ingress socket is healthy", socketDetail))
	}
	return checks
}

func relayChecks(ctx context.Context, paths bootstrap.Paths, uid int, dependencies dependencies) []Check {
	checks := make([]Check, 0, 8)
	status, inspectErr := dependencies.inspectRelay(ctx)
	dnsContext, cancelDNS := context.WithTimeout(ctx, 2*time.Second)
	dnsDetail, dnsErr := checkLocalhostDNS(dnsContext, dependencies.lookupIP)
	cancelDNS()
	if errors.Is(dnsErr, errUnsafeLocalhostResolution) {
		checks = append(checks, failed("relay.localhost_dns", "relay", ".localhost names do not resolve exclusively to loopback", dnsErr.Error(), "Correct the local DNS or resolver configuration for the reserved .localhost domain."))
	} else if dnsErr != nil {
		if inspectErr == nil && status.Healthy {
			checks = append(checks, informed("relay.localhost_dns", "relay", "System resolver defers .localhost mapping to clients", dnsErr.Error()+"; the clean-URL end-to-end check succeeded"))
		} else {
			checks = append(checks, warned("relay.localhost_dns", "relay", "System resolver does not expose .localhost addresses", dnsErr.Error(), "Browsers and curl normally map the reserved .localhost suffix themselves; if clean URLs fail there, inspect local resolver or security software."))
		}
	} else {
		checks = append(checks, passed("relay.localhost_dns", "relay", ".localhost names resolve to loopback", dnsDetail))
	}

	if inspectErr != nil {
		checks = append(checks, failed("relay.installation", "relay", "Relay installation could not be inspected", inspectErr.Error(), "Run `portless relay status`, then `portless relay install` to repair it."))
		checks = append(checks, relaySkippedChecks()...)
		checks = append(checks, portCheck(ctx, false, dependencies))
		checks = append(checks, skipped("relay.end_to_end", "relay", "End-to-end routing was not checked"))
		return checks
	}
	if status.Platform == "unsupported" {
		checks = append(checks, failed("relay.installation", "relay", "The privileged relay is unsupported on this platform", "Portless currently supports launchd on macOS and systemd on Linux.", "Use a supported macOS or systemd Linux host."))
		checks = append(checks, relaySkippedChecks()...)
		checks = append(checks, portCheck(ctx, false, dependencies))
		checks = append(checks, skipped("relay.end_to_end", "relay", "End-to-end routing was not checked"))
		return checks
	}
	if !status.Installed {
		checks = append(checks, failed("relay.installation", "relay", "Clean-URL relay is not installed", status.ConfigurationPath, "Run `portless relay install` or `portless setup`."))
		checks = append(checks, relaySkippedChecks()...)
		checks = append(checks, portCheck(ctx, false, dependencies))
		checks = append(checks, skipped("relay.end_to_end", "relay", "End-to-end routing was not checked"))
		return checks
	}

	missing := make([]string, 0, 2)
	if !status.HelperPresent {
		missing = append(missing, status.HelperPath)
	}
	if !status.ConfigurationPresent {
		missing = append(missing, status.ConfigurationPath)
	}
	if len(missing) > 0 {
		checks = append(checks, failed("relay.installation", "relay", "Relay installation is incomplete", "missing: "+strings.Join(missing, ", "), "Run `portless relay install` to repair the relay."))
	} else {
		checks = append(checks, passed("relay.installation", "relay", "Relay helper and service configuration are installed", status.Service))
	}

	switch {
	case !status.ReceiptPresent:
		checks = append(checks, warned("relay.receipt", "relay", "Relay ownership receipt is missing", "This is a legacy or partially completed installation.", "Run `portless relay install` to create a current receipt."))
	case status.OwnerUID <= 0:
		checks = append(checks, failed("relay.receipt", "relay", "Relay ownership receipt is invalid", status.Problem, "Run `portless relay uninstall --force`, then `portless relay install`."))
	default:
		checks = append(checks, passed("relay.receipt", "relay", "Relay ownership receipt is valid", status.ReceiptPath))
	}

	switch {
	case status.OwnerUID <= 0:
		checks = append(checks, failed("relay.ownership", "relay", "Relay owner could not be determined", status.Problem, "Run `portless relay uninstall --force`, then `portless relay install`."))
	case status.OwnerUID != uid:
		checks = append(checks, failed("relay.ownership", "relay", "Relay belongs to a different local user", fmt.Sprintf("configured UID %d; current UID %d", status.OwnerUID, uid), "Run `portless relay uninstall --force` before installing it for this user."))
	default:
		checks = append(checks, passed("relay.ownership", "relay", "Relay belongs to the current user", fmt.Sprintf("UID %d, GID %d", status.OwnerUID, status.OwnerGID)))
	}

	if status.TargetSocket != paths.Ingress {
		checks = append(checks, failed("relay.target", "relay", "Relay targets a different daemon socket", fmt.Sprintf("configured: %s; expected: %s", emptyAsUnknown(status.TargetSocket), paths.Ingress), "Run `portless relay install` to repair the relay target."))
	} else {
		checks = append(checks, passed("relay.target", "relay", "Relay targets the current daemon socket", status.TargetSocket))
	}

	if status.Running {
		checks = append(checks, passed("relay.service", "relay", "Relay system service is running", status.Service))
	} else {
		checks = append(checks, failed("relay.service", "relay", "Relay system service is not running", status.Problem, "Run `portless relay restart`; use `portless relay install` if restart fails."))
	}
	checks = append(checks, portCheck(ctx, true, dependencies))
	if status.Healthy {
		checks = append(checks, passed("relay.end_to_end", "relay", "Clean URL reaches the Portless daemon", ingress.ControlOrigin))
	} else {
		checks = append(checks, failed("relay.end_to_end", "relay", "Clean URL cannot reach the Portless daemon", status.HealthError, "Run `portless doctor daemon`, then `portless relay restart` once the daemon is healthy."))
	}
	return checks
}

func runtimeChecks(ctx context.Context, dependencies dependencies) []Check {
	probeContext, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	probes := dependencies.probeRuntimes(probeContext)
	ready := make([]string, 0, len(probes))
	details := make([]string, 0, len(probes))
	for _, probe := range probes {
		detail := string(probe.Name) + ": " + probe.State
		if probe.Version != "" {
			detail += " " + probe.Version
		}
		if probe.Reason != "" {
			detail += " (" + probe.Reason + ")"
		}
		details = append(details, detail)
		if probe.State == "ready" {
			ready = append(ready, string(probe.Name))
		}
	}
	if len(ready) == 0 {
		return []Check{warned("runtime.container", "runtime", "No container runtime is ready", strings.Join(details, "; "), "Start or install Docker Engine or Podman if this project uses managed containers.")}
	}
	return []Check{passed("runtime.container", "runtime", "Container runtime is available", strings.Join(details, "; "))}
}

func relaySkippedChecks() []Check {
	return []Check{
		skipped("relay.receipt", "relay", "Ownership receipt was not checked"),
		skipped("relay.ownership", "relay", "Relay ownership was not checked"),
		skipped("relay.target", "relay", "Relay target was not checked"),
		skipped("relay.service", "relay", "Relay service was not checked"),
	}
}

func portCheck(ctx context.Context, installed bool, dependencies dependencies) Check {
	probeContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	listening, err := dependencies.portListening(probeContext)
	if err != nil {
		return failed("relay.port_80", "relay", "Could not inspect 127.0.0.1:80", err.Error(), "Inspect local listeners on port 80 and retry.")
	}
	if installed && listening {
		return passed("relay.port_80", "relay", "A listener is accepting connections on 127.0.0.1:80", ingress.DefaultListenAddress)
	}
	if installed {
		return failed("relay.port_80", "relay", "Nothing is listening on 127.0.0.1:80", "The relay is installed but is not accepting connections.", "Run `portless relay restart`; use `portless relay install` if restart fails.")
	}
	if listening {
		return failed("relay.port_80", "relay", "Port 80 is occupied by an unrecognized listener", ingress.DefaultListenAddress, "Stop the process using 127.0.0.1:80, then run `portless relay restart`.")
	}
	return passed("relay.port_80", "relay", "Port 80 appears available for Portless", ingress.DefaultListenAddress)
}

func checkLocalhostDNS(ctx context.Context, lookup func(context.Context, string) ([]net.IPAddr, error)) (string, error) {
	names := []string{"portless.localhost", "doctor.local.portless.localhost"}
	resolved := make([]string, 0, len(names))
	for _, name := range names {
		addresses, err := lookup(ctx, name)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", name, err)
		}
		if len(addresses) == 0 {
			return "", fmt.Errorf("%s returned no addresses", name)
		}
		for _, address := range addresses {
			if !address.IP.IsLoopback() {
				return "", fmt.Errorf("%w: %s resolved to non-loopback address %s", errUnsafeLocalhostResolution, name, address.IP)
			}
		}
		resolved = append(resolved, name)
	}
	return strings.Join(resolved, ", "), nil
}

var errUnsafeLocalhostResolution = errors.New("unsafe .localhost resolution")

type expectedPathKind int

const (
	pathDirectory expectedPathKind = iota
	pathRegular
	pathSocket
)

func securePath(path string, uid int, kind expectedPathKind) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return path, err
	}
	switch kind {
	case pathDirectory:
		if !info.IsDir() {
			return path, errors.New("path is not a directory")
		}
	case pathRegular:
		if !info.Mode().IsRegular() {
			return path, errors.New("path is not a regular file")
		}
	case pathSocket:
		if info.Mode()&os.ModeSocket == 0 {
			return path, errors.New("path is not a Unix socket")
		}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return path, errors.New("file ownership is unavailable")
	}
	if uid > 0 && int(stat.Uid) != uid {
		return path, fmt.Errorf("owned by UID %d instead of UID %d", stat.Uid, uid)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return path, fmt.Errorf("permissions %04o allow group or other access", info.Mode().Perm())
	}
	return fmt.Sprintf("%s (%04o)", path, info.Mode().Perm()), nil
}

func processAlive(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	err = process.Signal(syscall.Signal(0))
	if errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

func portListening(ctx context.Context) (bool, error) {
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "tcp", ingress.DefaultListenAddress)
	if err == nil {
		_ = connection.Close()
		return true, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return false, nil
	}
	return false, err
}

func probeRuntimes(ctx context.Context) []container.ProbeResult {
	runtimes := []container.Runtime{podman.New("", ""), docker.New("", "")}
	probes := make([]container.ProbeResult, 0, len(runtimes))
	for _, runtime := range runtimes {
		probeContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		probes = append(probes, runtime.Probe(probeContext))
		cancel()
	}
	return probes
}

func defaultDependencies() dependencies {
	return dependencies{
		checkDaemon: bootstrap.CheckDaemon, inspectRelay: ingress.Inspect,
		checkIngressSocket: ingress.CheckSocket, processAlive: processAlive,
		lookupIP: net.DefaultResolver.LookupIPAddr, portListening: portListening,
		probeRuntimes: probeRuntimes,
	}
}

func passed(code, component, summary, detail string) Check {
	return Check{Code: code, Component: component, Status: StatusPass, Summary: summary, Detail: detail}
}

func informed(code, component, summary, detail string) Check {
	return Check{Code: code, Component: component, Status: StatusInfo, Summary: summary, Detail: detail}
}

func warned(code, component, summary, detail, remediation string) Check {
	return Check{Code: code, Component: component, Status: StatusWarn, Summary: summary, Detail: detail, Remediation: remediation}
}

func failed(code, component, summary, detail, remediation string) Check {
	return Check{Code: code, Component: component, Status: StatusFail, Summary: summary, Detail: detail, Remediation: remediation}
}

func skipped(code, component, summary string) Check {
	return Check{Code: code, Component: component, Status: StatusSkip, Summary: summary}
}

func detailOrError(detail string, err error) string {
	if err == nil {
		return detail
	}
	if detail == "" {
		return err.Error()
	}
	return detail + ": " + err.Error()
}

func errorDetail(prefix string, err error) string {
	if err == nil {
		return ""
	}
	return prefix + ": " + err.Error()
}

func joinDetails(values ...string) string {
	nonempty := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			nonempty = append(nonempty, value)
		}
	}
	return strings.Join(nonempty, "; ")
}

func emptyAsUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func shortIdentity(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
