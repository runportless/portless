// Package control discovers, authenticates, starts, stops, and replaces the
// per-user Portless daemon. It never composes or serves the daemon itself.
package control

import (
	"context"
	"net/http"
	"os"
	"time"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/system/installation"
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
	VerifyProcess func(context.Context, installation.Layout, identity.Record) error
	SignalProcess func(int, os.Signal) error
}

// Manager is the process-side lifecycle facade used by the CLI and read-only
// diagnostics. Its fixed layout prevents callers from mixing installation
// paths across lifecycle operations.
type Manager struct {
	layout installation.Layout
	hooks  Hooks
}

// New constructs a daemon lifecycle manager using production host operations.
func New(layout installation.Layout) *Manager {
	return NewWithHooks(layout, Hooks{})
}

// NewWithHooks constructs a daemon lifecycle manager with optional host-operation
// overrides, filling unspecified hooks with production implementations.
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

// Layout returns the fixed installation layout managed by m.
func (m *Manager) Layout() installation.Layout {
	return m.layout
}

// Ensure returns a compatible current daemon, starting or safely replacing one
// when necessary.
func (m *Manager) Ensure(ctx context.Context) (identity.Record, error) {
	return m.ensureDaemon(ctx)
}

// ReadRecord reads the persisted daemon discovery record without mutation.
func (m *Manager) ReadRecord() (identity.Record, error) {
	return identity.Read(m.layout)
}

// Inspect authenticates and compares an existing daemon without changing it.
func (m *Manager) Inspect(ctx context.Context) (Inspection, error) {
	return m.inspectDaemon(ctx)
}

// Check returns the live record for a compatible daemon running the current build.
func (m *Manager) Check(ctx context.Context) (identity.Record, error) {
	return m.checkDaemon(ctx)
}

// Connect returns an authenticated API client, ensuring a compatible daemon is running.
func (m *Manager) Connect(ctx context.Context) (*apiclient.Client, identity.Record, error) {
	return m.connect(ctx)
}

// ConnectExisting returns an authenticated client only when a compatible
// current-build daemon is already running.
func (m *Manager) ConnectExisting(ctx context.Context) (*apiclient.Client, identity.Record, error) {
	return m.connectExisting(ctx)
}

// Stop performs an authenticated daemon shutdown and applies options when
// active environments or an unverified legacy process are present.
func (m *Manager) Stop(ctx context.Context, options StopOptions) (StopResult, error) {
	return m.stopDaemon(ctx, options)
}

// ResetApplicationState stops owned runtimes as permitted by force, removes
// persistent application state, and returns the replacement daemon record.
func (m *Manager) ResetApplicationState(ctx context.Context, force bool) (installation.ResetStateResult, identity.Record, error) {
	return m.resetApplicationState(ctx, force)
}

// RemoveInstallationState stops the daemon and removes its installation data,
// refusing active environments unless force is true.
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
