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

	"github.com/portless-run/portless/portless-daemon/api/contract"
	apiserver "github.com/portless-run/portless/portless-daemon/api/server"
	"github.com/portless-run/portless/portless-daemon/auth"
	"github.com/portless-run/portless/portless-daemon/controlplane"
	"github.com/portless-run/portless/portless-daemon/database"
	portlessdns "github.com/portless-run/portless/portless-daemon/dns"
	"github.com/portless-run/portless/portless-daemon/events"
	daemonidentity "github.com/portless-run/portless/portless-daemon/identity"
	"github.com/portless-run/portless/portless-daemon/lifecycle"
	"github.com/portless-run/portless/portless-daemon/system/installation"
	"github.com/portless-run/portless/portless-relay"
	portlessweb "github.com/portless-run/portless/portless-web"
)

var (
	ErrExecutableChanged = errors.New("Portless executable changed")
	ErrRestartRequested  = errors.New("Portless daemon restart requested")
)

type Config struct {
	Layout        installation.Layout
	PreferredPort int
}

func Run(ctx context.Context, config Config) error {
	paths := config.Layout
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
	defer app.Close(context.Background())
	reconciliation, err := app.Reconcile(ctx)
	if err != nil {
		identity.RecoveryProblems = append(identity.RecoveryProblems, err.Error())
	} else {
		identity.RecoveryProblems = append(identity.RecoveryProblems, reconciliation.Unverifiable...)
	}
	identity.State = "ready"
	identity.HandoffReady, _ = app.CanHandoff(ctx)
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
	dnsListener, err := listenPrivateSocket(paths.DNSSocket, "DNS")
	if err != nil {
		return err
	}
	defer dnsListener.Close()
	defer removeIngressSocket(paths.DNSSocket)
	port := listener.Addr().(*net.TCPAddr).Port
	shutdownRequested := make(chan struct{})
	restartRequested := make(chan struct{}, 1)
	replacementRequested := make(chan struct{}, 1)
	var shutdownOnce sync.Once
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Auth: authManager, Identity: identity,
		HandoffStatus:      app.CanHandoff,
		ActiveEnvironments: app.ActiveEnvironments,
		Shutdown:           func() { shutdownOnce.Do(func() { close(shutdownRequested) }) },
		Replace: func() {
			select {
			case restartRequested <- struct{}{}:
			default:
			}
		},
	})
	apiHandler, err := apiserver.New(apiserver.Dependencies{
		Application: app, Auth: authManager, Assets: portlessweb.Assets(),
		DaemonControl: lifecycleAPIControl{handler: handler},
		InspectRelay: func(ctx context.Context) (contract.RelayStatus, error) {
			status, err := relay.Inspect(ctx)
			return contract.RelayStatus(status), err
		},
	})
	if err != nil {
		return err
	}
	handler.SetNext(apiHandler)
	controlServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	ingressServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	record := daemonidentity.Record{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		State: identity.State, HandoffReady: identity.HandoffReady, RecoveryProblems: identity.RecoveryProblems,
		TokenPath: paths.AuthToken, StartedAt: identity.StartedAt, ProcessHint: filepath.Base(os.Args[0]),
	}
	if err := daemonidentity.Write(paths, record); err != nil {
		return fmt.Errorf("publish daemon discovery record: %w", err)
	}
	defer daemonidentity.RemoveOwn(paths, identity.InstanceID)
	slog.Info("Portless daemon ready", "port", port, "ingressSocket", paths.IngressSocket, "dnsSocket", paths.DNSSocket, "pid", os.Getpid(), "instance", identity.InstanceID, "build", identity.BuildID[:12])
	errChannel := make(chan error, 3)
	go func() {
		errChannel <- controlServer.Serve(listener)
	}()
	go func() {
		errChannel <- ingressServer.Serve(ingressListener)
	}()
	dnsContext, stopDNS := context.WithCancel(ctx)
	defer stopDNS()
	go func() {
		errChannel <- portlessdns.Serve(dnsContext, dnsListener, controlStore)
	}()
	watchContext, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	go watchExecutable(watchContext, executable, buildID, app.CanHandoff, replacementRequested)
	select {
	case <-ctx.Done():
		stopDNS()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return errors.Join(controlServer.Shutdown(shutdownContext), ingressServer.Shutdown(shutdownContext))
	case <-shutdownRequested:
		stopDNS()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return errors.Join(controlServer.Shutdown(shutdownContext), ingressServer.Shutdown(shutdownContext))
	case <-replacementRequested:
		stopDNS()
		_ = controlStore.SetDaemonInstanceState(context.Background(), instanceID, "draining", false)
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		shutdownErr := errors.Join(controlServer.Shutdown(shutdownContext), ingressServer.Shutdown(shutdownContext))
		return errors.Join(ErrExecutableChanged, shutdownErr)
	case <-restartRequested:
		stopDNS()
		_ = controlStore.SetDaemonInstanceState(context.Background(), instanceID, "draining", false)
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		shutdownErr := errors.Join(controlServer.Shutdown(shutdownContext), ingressServer.Shutdown(shutdownContext))
		return errors.Join(ErrRestartRequested, shutdownErr)
	case err := <-errChannel:
		stopDNS()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = controlServer.Shutdown(shutdownContext)
		_ = ingressServer.Shutdown(shutdownContext)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type lifecycleAPIControl struct {
	handler *lifecycle.Handler
}

func (c lifecycleAPIControl) Status(ctx context.Context) (contract.DaemonStatus, error) {
	identity, err := c.handler.Status(ctx)
	if err != nil {
		return contract.DaemonStatus{}, err
	}
	return contract.DaemonStatus{
		State: identity.State, PID: identity.PID, StartedAt: identity.StartedAt,
		InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		HandoffReady: identity.HandoffReady, RecoveryProblems: append([]string(nil), identity.RecoveryProblems...),
		ActiveEnvironments: append([]string(nil), identity.ActiveEnvironments...),
	}, nil
}

func (c lifecycleAPIControl) Restart(ctx context.Context, instanceID string) (contract.DaemonRestart, error) {
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
	return contract.DaemonRestart{
		Restarting: true, PreviousInstanceID: result.InstanceID, Handoff: result.Handoff,
		ActiveEnvironments: append([]string(nil), result.ActiveEnvironments...),
	}, nil
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
