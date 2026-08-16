// Package control discovers, authenticates, starts, stops, and replaces the
// per-user Portless daemon. It never composes or serves the daemon itself.
package control

import (
	"context"
	"net/http"
	"os"
	"time"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/daemon/instance"
	"github.com/portless-run/portless/internal/installation"
)

// Hooks isolates process, clock, and HTTP operations used by daemon control.
// Production callers normally use New; tests can replace only the operations
// relevant to the behavior they exercise.
type Hooks struct {
	Now           func() time.Time
	Wait          func(context.Context, time.Duration) error
	HTTPClient    func(time.Duration) *http.Client
	StartDaemon   func(installation.Layout) error
	ProcessAlive  func(int) (bool, error)
	VerifyProcess func(context.Context, installation.Layout, instance.Record) error
	SignalProcess func(int, os.Signal) error
}

// Manager is the process-side lifecycle facade used by the CLI and read-only
// diagnostics. Its fixed layout prevents callers from mixing installation
// paths across lifecycle operations.
type Manager struct {
	layout installation.Layout
	hooks  Hooks
}

func New(layout installation.Layout) *Manager {
	return NewWithHooks(layout, Hooks{})
}

func NewWithHooks(layout installation.Layout, hooks Hooks) *Manager {
	if hooks.Now == nil {
		hooks.Now = time.Now
	}
	if hooks.Wait == nil {
		hooks.Wait = waitDuration
	}
	if hooks.HTTPClient == nil {
		hooks.HTTPClient = func(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }
	}
	if hooks.StartDaemon == nil {
		hooks.StartDaemon = startDaemonProcess
	}
	if hooks.ProcessAlive == nil {
		hooks.ProcessAlive = processIsAlive
	}
	if hooks.VerifyProcess == nil {
		hooks.VerifyProcess = verifyRecordedDaemonProcess
	}
	if hooks.SignalProcess == nil {
		hooks.SignalProcess = signalProcess
	}
	return &Manager{layout: layout, hooks: hooks}
}

func (m *Manager) Layout() installation.Layout {
	return m.layout
}

func (m *Manager) Ensure(ctx context.Context) (instance.Record, error) {
	return m.ensureDaemon(ctx)
}

func (m *Manager) ReadRecord() (instance.Record, error) {
	return instance.Read(m.layout)
}

func (m *Manager) Inspect(ctx context.Context) (Inspection, error) {
	return m.inspectDaemon(ctx)
}

func (m *Manager) Check(ctx context.Context) (instance.Record, error) {
	return m.checkDaemon(ctx)
}

func (m *Manager) Connect(ctx context.Context) (*apiclient.Client, instance.Record, error) {
	return m.connect(ctx)
}

func (m *Manager) ConnectExisting(ctx context.Context) (*apiclient.Client, instance.Record, error) {
	return m.connectExisting(ctx)
}

func (m *Manager) Stop(ctx context.Context, options StopOptions) (StopResult, error) {
	return m.stopDaemon(ctx, options)
}

func (m *Manager) ResetApplicationState(ctx context.Context, force bool) (installation.ResetStateResult, instance.Record, error) {
	return m.resetApplicationState(ctx, force)
}

func (m *Manager) RemoveInstallationState(ctx context.Context, force bool) (installation.StateRemoval, error) {
	return m.removeInstallationState(ctx, force)
}

func waitDuration(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
