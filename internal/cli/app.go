package cli

import (
	"context"
	"io"
	"os"
	"time"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/daemon/control"
	"github.com/portless-run/portless/internal/daemon/instance"
	"github.com/portless-run/portless/internal/diagnostics"
	"github.com/portless-run/portless/internal/installation"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery"
	"github.com/portless-run/portless/internal/relay"
	relayinstall "github.com/portless-run/portless/internal/relay/install"
)

var Version = "dev"

type CLI struct {
	Out                 io.Writer
	Err                 io.Writer
	paths               installation.Layout
	daemon              daemonController
	local               localDependencies
	jsonOutput          bool
	noColor             bool
	completionOutput    bool
	colorPreference     colorPreference
	colorSource         string
	environmentOverride string
	completionCache     map[string][]string
}

// daemonController is the CLI's narrow process-control boundary. Product
// operations use api/client; this interface exists only for starting,
// authenticating, stopping, resetting, and removing the local daemon.
type daemonController interface {
	Ensure(context.Context) (instance.Record, error)
	ReadRecord() (instance.Record, error)
	Inspect(context.Context) (control.Inspection, error)
	Connect(context.Context) (*apiclient.Client, instance.Record, error)
	ConnectExisting(context.Context) (*apiclient.Client, instance.Record, error)
	Stop(context.Context, control.StopOptions) (control.StopResult, error)
	ResetApplicationState(context.Context, bool) (installation.ResetStateResult, instance.Record, error)
	RemoveInstallationState(context.Context, bool) (installation.StateRemoval, error)
}

// localDependencies collects the host operations that cannot go through the
// daemon API. Keeping them behind one seam makes command behavior testable
// without launching processes, prompting for privileges, or opening a browser.
type localDependencies struct {
	daemon                 daemonController
	inspectRelay           func(context.Context) (relayinstall.InstallationStatus, error)
	installRelay           func(context.Context, relayinstall.SetupRequest) error
	restartRelay           func(context.Context, relayinstall.RestartRequest) error
	uninstallRelay         func(context.Context, relayinstall.UninstallRequest) (bool, error)
	validateRelayOwner     func(relayinstall.InstallationStatus, int) error
	validateRelayUninstall func(relayinstall.InstallationStatus, int, bool) error
	waitRelay              func(context.Context, time.Duration) error
	checkRelaySocket       func(context.Context, string) error
	diagnose               func(context.Context, installation.Layout, diagnostics.Scope, int) (diagnostics.Report, error)
	inspectState           func(installation.Layout) (installation.StateStatus, error)
	findProjectRoot        func(context.Context, string) (string, error)
	workingDirectory       func() (string, error)
	userIDs                func() (int, int)
	effectiveUID           func() int
	resolvedExecutable     func() (string, error)
	launchBrowser          func(string) error
	inspectLauncher        func() launcherPlan
	removeLauncher         func(launcherPlan) (bool, error)
}

type upOutput struct {
	Environment model.Environment `json:"environment"`
	Operation   model.Operation   `json:"operation"`
	Warnings    []string          `json:"warnings"`
}

type browserOutput struct {
	URL     string `json:"url"`
	Service string `json:"service,omitempty"`
	Opened  bool   `json:"opened"`
	Error   string `json:"error,omitempty"`
}

type relayStatusOutput struct {
	State string `json:"state"`
	relayinstall.InstallationStatus
}

type relayActionOutput struct {
	Action string `json:"action"`
	relayStatusOutput
}

type actionOutput struct {
	Action      string `json:"action"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Name        string `json:"name,omitempty"`
	Path        string `json:"path,omitempty"`
	Status      string `json:"status,omitempty"`
}

type logsOutput struct {
	Project     string           `json:"project"`
	Environment string           `json:"environment"`
	Entries     []model.LogEntry `json:"entries"`
}

type environmentContextOutput struct {
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

func New(out, errOut io.Writer, dataDirectory string) (*CLI, error) {
	return newWithDependencies(out, errOut, dataDirectory, localDependencies{})
}

func newWithDependencies(out, errOut io.Writer, dataDirectory string, overrides localDependencies) (*CLI, error) {
	paths, err := installation.ResolveLayout(dataDirectory)
	if err != nil {
		return nil, err
	}
	local := defaultLocalDependencies(paths)
	mergeLocalDependencies(&local, overrides)
	return &CLI{Out: out, Err: errOut, paths: paths, daemon: local.daemon, local: local}, nil
}

func defaultLocalDependencies(paths installation.Layout) localDependencies {
	return localDependencies{
		daemon:                 control.New(paths),
		inspectRelay:           relayinstall.Inspect,
		installRelay:           relayinstall.Install,
		restartRelay:           relayinstall.Restart,
		uninstallRelay:         relayinstall.Uninstall,
		validateRelayOwner:     relayinstall.ValidateOwnership,
		validateRelayUninstall: relayinstall.ValidateUninstallOwnership,
		waitRelay:              relay.WaitUntilReady,
		checkRelaySocket:       relay.CheckSocket,
		diagnose:               diagnostics.Run,
		inspectState:           installation.InspectState,
		findProjectRoot:        discovery.FindRoot,
		workingDirectory:       currentWorkingDirectory,
		userIDs:                requestingUserIDs,
		effectiveUID:           os.Geteuid,
		resolvedExecutable:     resolvedExecutable,
		launchBrowser:          launchBrowser,
		inspectLauncher:        inspectLauncher,
		removeLauncher:         removeLauncher,
	}
}

func mergeLocalDependencies(target *localDependencies, overrides localDependencies) {
	if overrides.daemon != nil {
		target.daemon = overrides.daemon
	}
	if overrides.inspectRelay != nil {
		target.inspectRelay = overrides.inspectRelay
	}
	if overrides.installRelay != nil {
		target.installRelay = overrides.installRelay
	}
	if overrides.restartRelay != nil {
		target.restartRelay = overrides.restartRelay
	}
	if overrides.uninstallRelay != nil {
		target.uninstallRelay = overrides.uninstallRelay
	}
	if overrides.validateRelayOwner != nil {
		target.validateRelayOwner = overrides.validateRelayOwner
	}
	if overrides.validateRelayUninstall != nil {
		target.validateRelayUninstall = overrides.validateRelayUninstall
	}
	if overrides.waitRelay != nil {
		target.waitRelay = overrides.waitRelay
	}
	if overrides.checkRelaySocket != nil {
		target.checkRelaySocket = overrides.checkRelaySocket
	}
	if overrides.diagnose != nil {
		target.diagnose = overrides.diagnose
	}
	if overrides.inspectState != nil {
		target.inspectState = overrides.inspectState
	}
	if overrides.findProjectRoot != nil {
		target.findProjectRoot = overrides.findProjectRoot
	}
	if overrides.workingDirectory != nil {
		target.workingDirectory = overrides.workingDirectory
	}
	if overrides.userIDs != nil {
		target.userIDs = overrides.userIDs
	}
	if overrides.effectiveUID != nil {
		target.effectiveUID = overrides.effectiveUID
	}
	if overrides.resolvedExecutable != nil {
		target.resolvedExecutable = overrides.resolvedExecutable
	}
	if overrides.launchBrowser != nil {
		target.launchBrowser = overrides.launchBrowser
	}
	if overrides.inspectLauncher != nil {
		target.inspectLauncher = overrides.inspectLauncher
	}
	if overrides.removeLauncher != nil {
		target.removeLauncher = overrides.removeLauncher
	}
}
