// Package cli assembles and runs the Portless command-line application.
package cli

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/runportless/portless/portless-cli/administration"
	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-cli/doctor"
	"github.com/runportless/portless/portless-cli/environment"
	"github.com/runportless/portless/portless-cli/mocks"
	"github.com/runportless/portless/portless-cli/observe"
	"github.com/runportless/portless/portless-cli/projects"
	"github.com/runportless/portless/portless-cli/traffic"
	"github.com/runportless/portless/portless-daemon/control"
	"github.com/runportless/portless/portless-daemon/projects/discovery"
	"github.com/runportless/portless/portless-daemon/system/installation"
	"github.com/runportless/portless/portless-relay"
)

// Version is the Portless CLI version reported by the root command.
var Version = "dev"

// Distribution identifies the package channel that owns the current CLI
// launcher. Release packaging sets it at link time so uninstall can leave
// package-manager files to their package manager.
var Distribution = "source"

// CLI is the composition root for Portless's command-line product. Product
// behavior belongs to the feature packages; this type owns only shared state
// and command assembly.
type CLI struct {
	Out io.Writer
	Err io.Writer

	context        *command.Context
	environment    *environment.Commands
	projects       *projects.Commands
	observe        *observe.Commands
	traffic        *traffic.Commands
	mocks          *mocks.Commands
	administration *administration.Commands
}

// localDependencies is the composition seam used by tests to replace host
// operations without starting processes, prompting for privileges, or opening
// a browser.
type localDependencies struct {
	daemon                 command.DaemonController
	inspectRelay           func(context.Context) (relay.InstallationStatus, error)
	installRelay           func(context.Context, relay.SetupRequest) error
	restartRelay           func(context.Context, relay.RestartRequest) error
	uninstallRelay         func(context.Context, relay.UninstallRequest) (bool, error)
	validateRelayOwner     func(relay.InstallationStatus, int) error
	validateRelayUninstall func(relay.InstallationStatus, int, bool) error
	waitRelay              func(context.Context, time.Duration) error
	checkRelaySocket       func(context.Context, string) error
	diagnose               func(context.Context, installation.Layout, doctor.Scope, int) (doctor.Report, error)
	inspectState           func(installation.Layout) (installation.StateStatus, error)
	findProjectRoot        func(context.Context, string) (string, error)
	workingDirectory       func() (string, error)
	userIDs                func() (int, int)
	effectiveUID           func() int
	resolvedExecutable     func() (string, error)
	launchBrowser          func(string) error
	inspectLauncher        func() command.LauncherPlan
	removeLauncher         func(command.LauncherPlan) (bool, error)
}

// New constructs a CLI that writes to out and errOut and stores its local
// installation state in dataDirectory.
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
	shared := &command.Context{
		Out:    out,
		Err:    errOut,
		Paths:  paths,
		Daemon: local.daemon,
		Local: command.Dependencies{
			InspectRelay: local.inspectRelay, InstallRelay: local.installRelay, RestartRelay: local.restartRelay,
			UninstallRelay: local.uninstallRelay, ValidateRelayOwner: local.validateRelayOwner,
			ValidateRelayUninstall: local.validateRelayUninstall, WaitRelay: local.waitRelay,
			CheckRelaySocket: local.checkRelaySocket, Diagnose: local.diagnose, InspectState: local.inspectState,
			FindProjectRoot: local.findProjectRoot, WorkingDirectory: local.workingDirectory,
			UserIDs: local.userIDs, EffectiveUID: local.effectiveUID, ResolvedExecutable: local.resolvedExecutable,
			LaunchBrowser: local.launchBrowser, InspectLauncher: local.inspectLauncher, RemoveLauncher: local.removeLauncher,
		},
	}
	return &CLI{
		Out: out, Err: errOut, context: shared,
		environment: environment.New(shared), projects: projects.New(shared), observe: observe.New(shared),
		traffic: traffic.New(shared), mocks: mocks.New(shared), administration: administration.New(shared),
	}, nil
}

func defaultLocalDependencies(paths installation.Layout) localDependencies {
	return localDependencies{
		daemon: control.New(paths), inspectRelay: relay.Inspect, installRelay: relay.Install,
		restartRelay: relay.Restart, uninstallRelay: relay.Uninstall, validateRelayOwner: relay.ValidateOwnership,
		validateRelayUninstall: relay.ValidateUninstallOwnership, waitRelay: relay.WaitUntilReady,
		checkRelaySocket: relay.CheckSocket, diagnose: doctor.Run, inspectState: installation.InspectState,
		findProjectRoot: discovery.FindRoot, workingDirectory: command.WorkingDirectory,
		userIDs: command.RequestingUserIDs, effectiveUID: os.Geteuid, resolvedExecutable: command.ResolvedExecutable,
		launchBrowser: command.LaunchBrowser,
		inspectLauncher: func() command.LauncherPlan {
			return command.InspectLauncher(Distribution)
		},
		removeLauncher: command.RemoveLauncher,
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
