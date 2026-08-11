package bootstrap

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
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/daemon"
	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/store"
	"github.com/portless-run/portless/webui"
)

func RunDaemon(ctx context.Context, paths Paths, preferredPort int) error {
	for _, directory := range []string{paths.Root, paths.Logs, paths.Temporary} {
		if err := ensurePrivateDirectory(directory); err != nil {
			return err
		}
	}
	authManager, err := auth.LoadOrCreate(paths.Token)
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
	buildID, err := CurrentBuildID()
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	identity := daemon.Identity{
		Product: daemon.Product, ProtocolVersion: daemon.ProtocolVersion, APIVersion: api.APIVersion,
		InstallationID: installationIDFromKey(ownershipKey), InstanceID: instanceID, BuildID: buildID,
		PID: os.Getpid(), StartedAt: startedAt, ActiveEnvironments: []string{},
	}
	controlStore, err := store.Open(paths.Database)
	if err != nil {
		return err
	}
	defer controlStore.Close()
	broker := events.NewBroker()
	app := application.New(controlStore, broker, application.Config{DataDirectory: paths.Root, InstallationKey: ownershipKey})
	defer app.Close(context.Background())
	listener, err := listenControl(preferredPort)
	if err != nil {
		return err
	}
	defer listener.Close()
	ingressListener, err := listenIngress(paths.Ingress)
	if err != nil {
		return err
	}
	defer ingressListener.Close()
	defer removeIngressSocket(paths.Ingress)
	port := listener.Addr().(*net.TCPAddr).Port
	apiHandler, err := api.New(app, authManager, webui.Assets())
	if err != nil {
		return err
	}
	shutdownRequested := make(chan struct{})
	var shutdownOnce sync.Once
	handler := &lifecycleHandler{
		next: apiHandler, auth: authManager, identity: identity,
		activeEnvironments: func(ctx context.Context) ([]string, error) {
			environments, err := app.Environments(ctx, "")
			if err != nil {
				return nil, err
			}
			active := make([]string, 0, len(environments))
			for _, environment := range environments {
				if environment.Status != model.EnvironmentStopped {
					active = append(active, model.EnvironmentSelector(environment.Project, environment.Name))
				}
			}
			return active, nil
		},
		shutdown: func() { shutdownOnce.Do(func() { close(shutdownRequested) }) },
	}
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
	record := ControlRecord{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.Token, StartedAt: identity.StartedAt, ProcessHint: filepath.Base(os.Args[0]),
	}
	if err := writeControl(paths, record); err != nil {
		return fmt.Errorf("publish daemon discovery record: %w", err)
	}
	defer removeOwnControl(paths, identity.InstanceID)
	slog.Info("Portless daemon ready", "port", port, "ingressSocket", paths.Ingress, "pid", os.Getpid(), "instance", identity.InstanceID, "build", identity.BuildID[:12])
	errChannel := make(chan error, 2)
	go func() {
		errChannel <- controlServer.Serve(listener)
	}()
	go func() {
		errChannel <- ingressServer.Serve(ingressListener)
	}()
	signalContext, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-signalContext.Done():
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return errors.Join(controlServer.Shutdown(shutdownContext), ingressServer.Shutdown(shutdownContext))
	case <-shutdownRequested:
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return errors.Join(controlServer.Shutdown(shutdownContext), ingressServer.Shutdown(shutdownContext))
	case err := <-errChannel:
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

func listenIngress(path string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket ingress path %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale ingress socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect ingress socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on private ingress socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		removeIngressSocket(path)
		return nil, fmt.Errorf("protect private ingress socket: %w", err)
	}
	return listener, nil
}

func removeIngressSocket(path string) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
}

func listenControl(preferred int) (net.Listener, error) {
	if preferred <= 0 {
		preferred = 7331
	}
	for port := preferred; port < preferred+100; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return listener, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
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
