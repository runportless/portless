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
	"syscall"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/store"
	"github.com/portless-run/portless/webui"
)

func RunDaemon(ctx context.Context, paths Paths, preferredPort int) error {
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Logs, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Temporary, 0o700); err != nil {
		return err
	}
	authManager, err := auth.LoadOrCreate(paths.Token)
	if err != nil {
		return fmt.Errorf("initialize local authentication: %w", err)
	}
	ownershipKey, err := loadOrCreateKey(paths.OwnershipKey)
	if err != nil {
		return fmt.Errorf("initialize runtime ownership key: %w", err)
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
	port := listener.Addr().(*net.TCPAddr).Port
	app.SetControlPort(port)
	handler, err := api.New(app, authManager, webui.Assets(), port)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	record := ControlRecord{PID: os.Getpid(), Port: port, APIVersion: api.APIVersion, TokenPath: paths.Token, StartedAt: time.Now().UTC(), ProcessHint: filepath.Base(os.Args[0])}
	if err := writeControl(paths, record); err != nil {
		return fmt.Errorf("publish daemon discovery record: %w", err)
	}
	defer removeOwnControl(paths)
	slog.Info("Portless daemon ready", "port", port, "pid", os.Getpid())
	errChannel := make(chan error, 1)
	go func() {
		errChannel <- server.Serve(listener)
	}()
	signalContext, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-signalContext.Done():
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownContext)
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
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
