// Package daemon owns the long-running Portless daemon composition root.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	apiserver "github.com/runportless/portless/portless-daemon/api/server"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/daemonlog"
	"github.com/runportless/portless/portless-daemon/database"
	portlessdns "github.com/runportless/portless/portless-daemon/dns"
	"github.com/runportless/portless/portless-daemon/events"
	daemonidentity "github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/lifecycle"
	"github.com/runportless/portless/portless-daemon/system/directorypicker"
	"github.com/runportless/portless/portless-daemon/system/installation"
	"github.com/runportless/portless/portless-relay"
	portlessweb "github.com/runportless/portless/portless-web"
)

var (
	// ErrExecutableChanged requests a graceful daemon handoff after the on-disk
	// executable changes.
	ErrExecutableChanged = errors.New("Portless executable changed")
	// ErrRestartRequested requests an explicit graceful daemon replacement.
	ErrRestartRequested = errors.New("Portless daemon restart requested")
)

// Config defines the installation layout and preferred private control port
// for a daemon process.
type Config struct {
	Layout         installation.Layout
	PreferredPort  int
	Build          BuildInfo
	RestartReceipt *contract.DaemonRestart
}

// Run composes, reconciles, publishes, and serves one per-user Portless daemon
// until shutdown, replacement, cancellation, or a listener failure.
func Run(ctx context.Context, config Config) error {
	paths := config.Layout
	build := normalizedBuildInfo(config.Build)
	for _, directory := range []string{paths.Root, paths.Logs, paths.Temporary} {
		if err := installation.EnsurePrivateDirectory(directory); err != nil {
			return err
		}
	}
	instanceLock, err := os.OpenFile(paths.InstanceLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon instance lock: %w", err)
	}
	defer instanceLock.Close()
	if err := os.Chmod(paths.InstanceLock, 0o600); err != nil {
		return fmt.Errorf("protect daemon instance lock: %w", err)
	}
	if err := syscall.Flock(int(instanceLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another Portless daemon already owns this data directory")
	}
	defer syscall.Flock(int(instanceLock.Fd()), syscall.LOCK_UN)
	authManager, err := auth.LoadOrCreate(paths.AuthToken)
	if err != nil {
		return fmt.Errorf("initialize local authentication: %w", err)
	}
	authToken, err := installation.ReadPrivateTextFile(paths.AuthToken)
	if err != nil {
		return fmt.Errorf("read local authentication token: %w", err)
	}
	ownershipKey, err := loadOrCreateKey(paths.OwnershipKey)
	if err != nil {
		return fmt.Errorf("initialize runtime ownership key: %w", err)
	}
	instanceID, err := newInstanceID()
	if err != nil {
		return err
	}
	buildID, err := installation.CurrentBuildID()
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	identity := lifecycle.Identity{
		Product: lifecycle.Product, ProtocolVersion: lifecycle.ProtocolVersion, APIVersion: contract.APIVersion,
		InstallationID: installation.IDFromKey(ownershipKey), InstanceID: instanceID, BuildID: buildID,
		PID: os.Getpid(), StartedAt: startedAt, State: "reconciling", RecoveryProblems: []string{}, ActiveEnvironments: []string{},
	}
	controlStore, err := database.Open(paths.Database)
	if err != nil {
		return err
	}
	defer controlStore.Close()
	broker := events.NewBroker()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	if err := controlStore.RecordDaemonInstance(ctx, database.DaemonInstance{
		InstanceID: instanceID, BuildID: buildID, PID: os.Getpid(), State: "reconciling", StartedAt: startedAt,
	}); err != nil {
		return err
	}
	defer controlStore.SetDaemonInstanceState(context.Background(), instanceID, "stopped", true)
	app := controlplane.New(controlStore, broker, controlplane.Config{
		DataDirectory: paths.Root, InstallationKey: ownershipKey, DaemonInstanceID: instanceID, Executable: executable,
		PrivateTCPIngress: e2ePrivateTCPIngress,
	})
	var closeApplicationOnce sync.Once
	closeApplication := func(closeContext context.Context) {
		closeApplicationOnce.Do(func() { app.Close(closeContext) })
	}
	defer closeApplication(context.Background())
	// Recovered processes can health-check through source-aware TCP endpoints.
	// Serve their durable DNS allocations before reconciliation begins.
	dnsListener, err := listenPrivateSocket(paths.DNSSocket, "DNS")
	if err != nil {
		return err
	}
	defer dnsListener.Close()
	defer removeIngressSocket(paths.DNSSocket)
	dnsContext, stopDNS := context.WithCancel(ctx)
	defer stopDNS()
	errChannel := make(chan error, 3)
	go func() {
		errChannel <- portlessdns.Serve(dnsContext, dnsListener, controlStore)
	}()
	reconciliation, err := app.Reconcile(ctx)
	if err != nil {
		identity.RecoveryProblems = append(identity.RecoveryProblems, err.Error())
	} else {
		identity.RecoveryProblems = append(identity.RecoveryProblems, reconciliation.Unverifiable...)
	}
	select {
	case dnsErr := <-errChannel:
		if dnsErr != nil {
			return dnsErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("private DNS server stopped during runtime reconciliation")
	default:
	}
	identity.State = "ready"
	if err := controlStore.SetDaemonInstanceState(ctx, instanceID, "ready", false); err != nil {
		return err
	}
	listener, err := listenControl(config.PreferredPort)
	if err != nil {
		return err
	}
	defer listener.Close()
	ingressListener, err := listenIngress(paths.IngressSocket)
	if err != nil {
		return err
	}
	defer ingressListener.Close()
	defer removeIngressSocket(paths.IngressSocket)
	port := listener.Addr().(*net.TCPAddr).Port
	shutdownRequested := make(chan struct{})
	replacements := newReplacementCoordinator()
	var shutdownOnce sync.Once
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Auth: authManager, Identity: identity,
		HandoffStatus:      app.CanHandoff,
		ActiveEnvironments: app.ActiveEnvironments,
		Shutdown:           func() { shutdownOnce.Do(func() { close(shutdownRequested) }) },
	})
	lastRestart := pendingRestartStatus(config.RestartReceipt, instanceID)
	apiHandler, err := apiserver.New(apiserver.Dependencies{
		Application: app, Auth: authManager, Assets: portlessweb.Assets(),
		DaemonControl: lifecycleAPIControl{
			handler: handler, logs: daemonlog.NewReader(paths.DaemonLog, authToken, ownershipKey), app: app,
			build: build, runningBuildID: buildID, executable: executable,
			replacements: replacements, lastRestart: lastRestart,
		},
		SystemVersion: build.Version,
		InspectRelay: func(ctx context.Context) (contract.RelayStatus, error) {
			status, err := relay.Inspect(ctx)
			return contract.RelayStatus(status), err
		},
		SelectDirectory: directorypicker.Select,
	})
	if err != nil {
		return err
	}
	handler.SetNext(apiHandler)
	serverContext, stopServing := context.WithCancel(ctx)
	defer stopServing()
	controlServer := &http.Server{
		Handler:           handler,
		BaseContext:       func(net.Listener) context.Context { return serverContext },
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	ingressServer := &http.Server{
		Handler:           handler,
		BaseContext:       func(net.Listener) context.Context { return serverContext },
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	record := daemonidentity.Record{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		State: identity.State, RecoveryProblems: identity.RecoveryProblems,
		TokenPath: paths.AuthToken, StartedAt: identity.StartedAt, ProcessHint: filepath.Base(os.Args[0]),
	}
	if err := daemonidentity.Write(paths, record); err != nil {
		return fmt.Errorf("publish daemon discovery record: %w", err)
	}
	defer daemonidentity.RemoveOwn(paths, identity.InstanceID)
	completeRestartStatus(lastRestart, time.Now().UTC())
	go func() {
		errChannel <- controlServer.Serve(listener)
	}()
	go func() {
		errChannel <- ingressServer.Serve(ingressListener)
	}()
	slog.Info("Portless daemon ready", "port", port, "ingressSocket", paths.IngressSocket, "dnsSocket", paths.DNSSocket, "pid", os.Getpid(), "instance", identity.InstanceID, "build", identity.BuildID[:12])
	if lastRestart != nil {
		arguments := []any{"event", "daemon.restart.complete", "restart", lastRestart.RestartID, "reason", lastRestart.Reason, "previousInstance", lastRestart.PreviousInstanceID, "instance", lastRestart.InstanceID, "targetBuild", lastRestart.TargetBuildID, "durationMs", lastRestart.DurationMS, "withinSLA", lastRestart.WithinSLA}
		if lastRestart.WithinSLA {
			slog.Info("Portless daemon restart complete", arguments...)
		} else {
			slog.Warn("Portless daemon restart missed readiness SLA", arguments...)
		}
	}
	watchContext, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	go watchExecutable(watchContext, executable, buildID, app.CanHandoff, func(targetBuildID string) {
		activeEnvironments, _ := app.ActiveEnvironments(watchContext)
		receipt, prepareErr := replacements.prepare("executable-change", instanceID, targetBuildID, time.Now().UTC(), activeEnvironments, ErrExecutableChanged)
		if prepareErr != nil {
			slog.Error("Prepare automatic daemon replacement", "error", prepareErr)
			return
		}
		replacements.commit(receipt.RestartID)
	})
	select {
	case <-ctx.Done():
		stopDNS()
		return shutdownHTTPServers(stopServing, ordinaryShutdownTimeout, controlServer, ingressServer)
	case <-shutdownRequested:
		stopDNS()
		return shutdownHTTPServers(stopServing, ordinaryShutdownTimeout, controlServer, ingressServer)
	case replacement := <-replacements.requests:
		stopDNS()
		_ = controlStore.SetDaemonInstanceState(context.Background(), instanceID, "draining", false)
		slog.Info("Portless daemon restart draining", "event", "daemon.restart.draining", "restart", replacement.receipt.RestartID, "reason", replacement.receipt.Reason)
		drainStartedAt := time.Now()
		shutdownErr := shutdownHTTPServers(stopServing, replacementDrainTimeout, controlServer, ingressServer)
		drainDuration := time.Since(drainStartedAt)
		cleanupStartedAt := time.Now()
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), replacementCleanupTimeout)
		closeApplication(cleanupContext)
		cleanupCancel()
		slog.Info("Portless daemon restart exec", "event", "daemon.restart.exec", "restart", replacement.receipt.RestartID, "drainMs", drainDuration.Milliseconds(), "cleanupMs", time.Since(cleanupStartedAt).Milliseconds(), "elapsedMs", time.Since(replacement.receipt.AcceptedAt).Milliseconds())
		return errors.Join(&replacementExit{receipt: replacement.receipt, cause: replacement.cause}, shutdownErr)
	case err := <-errChannel:
		stopDNS()
		_ = shutdownHTTPServers(stopServing, ordinaryShutdownTimeout, controlServer, ingressServer)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type lifecycleAPIControl struct {
	handler        *lifecycle.Handler
	logs           *daemonlog.Reader
	app            *controlplane.Service
	build          BuildInfo
	runningBuildID string
	executable     string
	replacements   *replacementCoordinator
	lastRestart    *contract.DaemonRestartStatus
}

// Status adapts lifecycle identity into the public daemon status contract.
func (c lifecycleAPIControl) Status(ctx context.Context) (contract.DaemonStatus, error) {
	identity, err := c.handler.Status(ctx)
	if err != nil {
		return contract.DaemonStatus{}, err
	}
	return contract.DaemonStatus{
		State: identity.State, PID: identity.PID, StartedAt: identity.StartedAt,
		InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		RecoveryProblems:   append([]string(nil), identity.RecoveryProblems...),
		ActiveEnvironments: append([]string(nil), identity.ActiveEnvironments...),
	}, nil
}

// Diagnostics returns one bounded operational snapshot for the daemon drawer.
func (c lifecycleAPIControl) Diagnostics(ctx context.Context, includeStorage bool) (contract.DaemonDiagnostics, error) {
	if err := ctx.Err(); err != nil {
		return contract.DaemonDiagnostics{}, err
	}
	operational := c.app.Diagnostics(ctx, includeStorage)
	result := contract.DaemonDiagnostics{
		CollectedAt: time.Now().UTC(),
		Inventory: contract.DaemonManagedInventory{
			Processes: operational.Inventory.Processes, Containers: operational.Inventory.Containers,
			ProxyListeners: operational.Inventory.ProxyListeners, ActiveEnvironments: operational.Inventory.ActiveEnvironments,
			Problems: append([]string(nil), operational.Inventory.Problems...),
		},
		Recovery: contract.DaemonRecoveryStatus{
			Result: operational.Recovery.Result, CompletedAt: operational.Recovery.CompletedAt,
			DurationMS: operational.Recovery.Duration.Milliseconds(), Recovered: operational.Recovery.Recovered,
			Problems: append([]string(nil), operational.Recovery.Problems...),
		},
		Build: contract.DaemonBuildProvenance{
			Version: c.build.Version, Distribution: c.build.Distribution, Commit: c.build.Commit,
			RunningBuildID: c.runningBuildID,
		},
		LastRestart: cloneRestartStatus(c.lastRestart),
	}
	onDiskBuildID, err := installation.BuildIDForPath(c.executable)
	if err != nil {
		result.Build.Problem = err.Error()
	} else {
		result.Build.OnDiskBuildID = onDiskBuildID
		result.Build.Current = onDiskBuildID == c.runningBuildID
	}
	if operational.Storage != nil {
		storage := operational.Storage
		result.Storage = &contract.DaemonStorageStatus{
			DatabaseBytes: storage.DatabaseBytes, RecordingCount: storage.RecordingCount,
			RecordedEventCount: storage.RecordedEventCount, RecordedBytes: storage.RecordedBytes,
			LiveTrafficExchanges: storage.LiveTrafficExchanges, LiveTrafficBytes: storage.LiveTrafficBytes,
			ServiceLogBytes: storage.ServiceLogBytes, DaemonLogBytes: storage.DaemonLogBytes,
			TrafficExchangeLimitPerEnvironment: storage.TrafficExchangeLimitPerEnvironment,
			TrafficPayloadLimitPerEnvironment:  storage.TrafficPayloadLimitPerEnvironment,
			RecordingDefaultEventLimit:         storage.RecordingDefaultEventLimit,
			RecordingMaximumEventLimit:         storage.RecordingMaximumEventLimit,
			RecordingDefaultPayloadLimit:       storage.RecordingDefaultPayloadLimit,
			RecordingMaximumPayloadLimit:       storage.RecordingMaximumPayloadLimit,
			ServiceLogGenerationLimit:          storage.ServiceLogGenerationLimit,
			ServiceLogStreamLimitBytes:         storage.ServiceLogStreamLimitBytes,
			TrafficPrunedAt:                    storage.TrafficPrunedAt, ServiceLogsPrunedAt: storage.ServiceLogsPrunedAt,
			Problems: append([]string(nil), storage.Problems...),
		}
	}
	return result, nil
}

// Logs returns one bounded, safely redacted daemon-log tail.
func (c lifecycleAPIControl) Logs(ctx context.Context) (contract.DaemonLogSnapshot, error) {
	snapshot, err := c.logs.Snapshot(ctx)
	if err != nil {
		return contract.DaemonLogSnapshot{}, err
	}
	return contract.DaemonLogSnapshot{Content: snapshot.Content, Truncated: snapshot.Truncated}, nil
}

// HandoffStatus performs and adapts a fresh lifecycle handoff verification.
func (c lifecycleAPIControl) HandoffStatus(ctx context.Context) (contract.DaemonHandoffStatus, error) {
	status, err := c.handler.VerifyHandoff(ctx)
	if err != nil {
		return contract.DaemonHandoffStatus{}, err
	}
	return contract.DaemonHandoffStatus{
		State: string(status.State), VerifiedAt: status.VerifiedAt,
		Problems:           append([]string(nil), status.Problems...),
		ActiveEnvironments: append([]string(nil), status.ActiveEnvironments...),
	}, nil
}

// Restart requests lifecycle replacement and adapts its structured result or error.
func (c lifecycleAPIControl) Restart(ctx context.Context, instanceID, reason string) (contract.DaemonRestart, error) {
	auditStartedAt := time.Now()
	result, err := c.handler.Restart(ctx, instanceID)
	if err != nil {
		var lifecycleError *lifecycle.LifecycleError
		if errors.As(err, &lifecycleError) {
			return contract.DaemonRestart{}, &contract.DaemonControlError{
				Code: lifecycleError.Code, Message: lifecycleError.Message,
				ActiveEnvironments: append([]string(nil), lifecycleError.ActiveEnvironments...),
				Problems:           append([]string(nil), lifecycleError.Problems...),
			}
		}
		return contract.DaemonRestart{}, err
	}
	targetBuildID, err := installation.BuildIDForPath(c.executable)
	if err != nil {
		return contract.DaemonRestart{}, fmt.Errorf("inspect replacement daemon build: %w", err)
	}
	acceptedAt := time.Now().UTC()
	receipt, err := c.replacements.prepare(reason, result.InstanceID, targetBuildID, acceptedAt, result.ActiveEnvironments, ErrRestartRequested)
	if err != nil {
		return contract.DaemonRestart{}, err
	}
	slog.Info("Portless daemon restart accepted", "event", "daemon.restart.accepted", "restart", receipt.RestartID, "reason", receipt.Reason, "deadline", receipt.DeadlineAt, "auditMs", time.Since(auditStartedAt).Milliseconds())
	return receipt, nil
}

// CommitRestart begins a prepared replacement after the API response is flushed.
func (c lifecycleAPIControl) CommitRestart(restartID string) {
	if !c.replacements.commit(restartID) {
		slog.Warn("Portless daemon restart commit ignored", "restart", restartID)
	}
}

func pendingRestartStatus(receipt *contract.DaemonRestart, instanceID string) *contract.DaemonRestartStatus {
	if receipt == nil {
		return nil
	}
	return &contract.DaemonRestartStatus{
		RestartID: receipt.RestartID, Reason: receipt.Reason,
		PreviousInstanceID: receipt.PreviousInstanceID, InstanceID: instanceID,
		TargetBuildID: receipt.TargetBuildID, AcceptedAt: receipt.AcceptedAt,
		DeadlineAt: receipt.DeadlineAt,
	}
}

func completeRestartStatus(status *contract.DaemonRestartStatus, readyAt time.Time) {
	if status == nil {
		return
	}
	status.ReadyAt = readyAt
	status.DurationMS = max(0, readyAt.Sub(status.AcceptedAt).Milliseconds())
	status.WithinSLA = !readyAt.After(status.DeadlineAt)
}

func cloneRestartStatus(status *contract.DaemonRestartStatus) *contract.DaemonRestartStatus {
	if status == nil {
		return nil
	}
	clone := *status
	return &clone
}

func newInstanceID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create daemon instance identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func loadOrCreateKey(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err == nil && len(content) > 0 {
		return string(content), os.Chmod(path, 0o600)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	encoded := hex.EncodeToString(value[:])
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return "", err
	}
	return encoded, nil
}

func normalizedBuildInfo(value BuildInfo) BuildInfo {
	if value.Version == "" {
		value.Version = "dev"
	}
	if value.Distribution == "" {
		value.Distribution = "source"
	}
	if value.Commit == "" {
		value.Commit = "unknown"
	}
	return value
}
