// Package command owns the shared execution context and command-line
// primitives used by Portless CLI feature packages.
package command

import (
	"context"
	"io"
	"time"

	"github.com/portless-run/portless/portless-cli/doctor"
	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/control"
	"github.com/portless-run/portless/portless-daemon/identity"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/system/installation"
	"github.com/portless-run/portless/portless-relay"
)

// DaemonController is the CLI's narrow out-of-process daemon lifecycle
// boundary. Ordinary product operations use the typed daemon API client.
type DaemonController interface {
	Ensure(context.Context) (identity.Record, error)
	ReadRecord() (identity.Record, error)
	Inspect(context.Context) (control.Inspection, error)
	Connect(context.Context) (*apiclient.Client, identity.Record, error)
	ConnectExisting(context.Context) (*apiclient.Client, identity.Record, error)
	Stop(context.Context, control.StopOptions) (control.StopResult, error)
	ResetApplicationState(context.Context, bool) (installation.ResetStateResult, identity.Record, error)
	RemoveInstallationState(context.Context, bool) (installation.StateRemoval, error)
}

// LauncherPlan describes a launcher that can be safely removed during full
// uninstall.
type LauncherPlan struct {
	Path       string `json:"path,omitempty"`
	Target     string `json:"target,omitempty"`
	Kind       string `json:"kind"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	Removed    bool   `json:"removed"`
	Executable string `json:"-"`
}

// Dependencies contains host operations that cannot be performed through the
// daemon API. Tests can replace individual functions without starting local
// processes, prompting for privileges, or opening a browser.
type Dependencies struct {
	InspectRelay           func(context.Context) (relay.InstallationStatus, error)
	InstallRelay           func(context.Context, relay.SetupRequest) error
	RestartRelay           func(context.Context, relay.RestartRequest) error
	UninstallRelay         func(context.Context, relay.UninstallRequest) (bool, error)
	ValidateRelayOwner     func(relay.InstallationStatus, int) error
	ValidateRelayUninstall func(relay.InstallationStatus, int, bool) error
	WaitRelay              func(context.Context, time.Duration) error
	CheckRelaySocket       func(context.Context, string) error
	Diagnose               func(context.Context, installation.Layout, doctor.Scope, int) (doctor.Report, error)
	InspectState           func(installation.Layout) (installation.StateStatus, error)
	FindProjectRoot        func(context.Context, string) (string, error)
	WorkingDirectory       func() (string, error)
	UserIDs                func() (int, int)
	EffectiveUID           func() int
	ResolvedExecutable     func() (string, error)
	LaunchBrowser          func(string) error
	InspectLauncher        func() LauncherPlan
	RemoveLauncher         func(LauncherPlan) (bool, error)
}

// Context is the shared state for one CLI instance. Feature packages embed it
// but own their command construction and product behavior.
type Context struct {
	Out                 io.Writer
	Err                 io.Writer
	Paths               installation.Layout
	Daemon              DaemonController
	Local               Dependencies
	JSONOutput          bool
	NoColor             bool
	CompletionOutput    bool
	ColorPreference     ColorPreference
	ColorSource         string
	EnvironmentOverride string
	CompletionCache     map[string][]string
}

type ActionOutput struct {
	Action      string `json:"action"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Name        string `json:"name,omitempty"`
	Path        string `json:"path,omitempty"`
	Status      string `json:"status,omitempty"`
}

type BrowserOutput struct {
	URL     string `json:"url"`
	Service string `json:"service,omitempty"`
	Opened  bool   `json:"opened"`
	Error   string `json:"error,omitempty"`
}

type relayStatusOutput struct {
	State string `json:"state"`
	relay.InstallationStatus
}

type EnvironmentContextOutput struct {
	Path        string            `json:"path"`
	Selector    string            `json:"selector"`
	Resolution  string            `json:"resolution"`
	Environment model.Environment `json:"environment"`
}

type errorOutput struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code        string                 `json:"code"`
	Message     string                 `json:"message"`
	Status      int                    `json:"status,omitempty"`
	Subject     map[string]any         `json:"subject,omitempty"`
	Details     map[string]any         `json:"details,omitempty"`
	Remediation []contract.Remediation `json:"remediation,omitempty"`
}
