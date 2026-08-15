package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/runtime/container"
)

var uninstallRemovalCategories = []string{
	"all Portless-supervised processes and installation-owned container resources",
	"the privileged HTTP/DNS relay, resolver entry, and reserved loopback endpoint pool",
	"all projects, environments, traffic, recordings, faults, logs, preferences, credentials, and installation state",
	"the CLI launcher when it can be identified without deleting a source-tree build",
}

type uninstallDaemonOutput struct {
	State                string   `json:"state"`
	PID                  int      `json:"pid,omitempty"`
	InstanceID           string   `json:"instanceId,omitempty"`
	Problem              string   `json:"problem,omitempty"`
	InventoryAvailable   bool     `json:"inventoryAvailable"`
	TopologyIncompatible bool     `json:"topologyIncompatible"`
	ActiveEnvironments   []string `json:"activeEnvironments"`
}

type uninstallRelayOutput struct {
	Installed           bool   `json:"installed"`
	Removed             bool   `json:"removed"`
	State               string `json:"state"`
	Service             string `json:"service,omitempty"`
	OwnerUID            int    `json:"ownerUid,omitempty"`
	TargetSocket        string `json:"targetSocket,omitempty"`
	DNSTargetSocket     string `json:"dnsTargetSocket,omitempty"`
	HelperPath          string `json:"helperPath,omitempty"`
	ConfigurationPath   string `json:"configurationPath,omitempty"`
	ReceiptPath         string `json:"receiptPath,omitempty"`
	ResolverPath        string `json:"resolverPath,omitempty"`
	EndpointPoolReady   bool   `json:"endpointPoolReady"`
	AdministratorPrompt bool   `json:"administratorPrompt"`
}

type uninstallDataOutput struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Removed bool   `json:"removed"`
}

type launcherPlan struct {
	Path       string `json:"path,omitempty"`
	Target     string `json:"target,omitempty"`
	Kind       string `json:"kind"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	Removed    bool   `json:"removed"`
	executable string
}

type uninstallOutput struct {
	Action                    string                  `json:"action"`
	Confirmed                 bool                    `json:"confirmed"`
	Forced                    bool                    `json:"forced"`
	Changed                   bool                    `json:"changed"`
	Complete                  bool                    `json:"complete"`
	Projects                  int                     `json:"projects"`
	Environments              int                     `json:"environments"`
	ManagedVolumeEnvironments int                     `json:"managedVolumeEnvironments"`
	ProcessesStopped          int                     `json:"processesStopped"`
	RuntimeCleanup            []container.ResetResult `json:"runtimeCleanup,omitempty"`
	Daemon                    uninstallDaemonOutput   `json:"daemon"`
	Relay                     uninstallRelayOutput    `json:"relay"`
	Data                      uninstallDataOutput     `json:"data"`
	Launcher                  launcherPlan            `json:"launcher"`
	WillRemove                []string                `json:"willRemove,omitempty"`
	Removed                   []string                `json:"removed,omitempty"`
	Blockers                  []string                `json:"blockers,omitempty"`
	Errors                    []string                `json:"errors,omitempty"`
}

type uninstallRuntimePreparation struct {
	Prepared                  bool
	Projects                  int
	Environments              int
	ManagedVolumeEnvironments int
	ActiveEnvironments        []string
	Processes                 int
	Runtimes                  []container.ResetResult
	Cancel                    func()
}

type uninstallStepHooks struct {
	prepareRuntime func(context.Context, bool) (uninstallRuntimePreparation, error)
	removeRelay    func(context.Context) (bool, error)
	removeState    func(context.Context, bool) (bootstrap.InstallationStateRemoval, error)
	removeLauncher func(launcherPlan) (bool, error)
}

func (c *CLI) uninstall(ctx context.Context, options uninstallOptions) error {
	preview, err := c.inspectUninstall(ctx)
	if err != nil {
		return err
	}
	preview.Confirmed = options.yes
	preview.Forced = options.force
	if !options.yes {
		return c.printUninstallPreview(preview)
	}
	if len(preview.Blockers) > 0 {
		return errors.New(strings.Join(preview.Blockers, "; "))
	}
	if len(preview.Daemon.ActiveEnvironments) > 0 && !options.force {
		if preview.Daemon.TopologyIncompatible {
			return incompatibleActiveUninstallError(preview.Daemon.ActiveEnvironments)
		}
		return activeUninstallError(preview.Daemon.ActiveEnvironments)
	}
	if preview.Data.Present && !preview.Daemon.InventoryAvailable && !options.force {
		return errors.New("Portless cannot verify that every environment is stopped while the current application inventory is unavailable; start the current daemon and stop its environments, or use `portless uninstall --force --yes` to authenticate and purge recoverable runtimes")
	}
	if preview.Daemon.State == "unverified" && !options.force {
		return errors.New("the daemon could not be authenticated; retry with `portless uninstall --force --yes` to use the guarded daemon recovery path")
	}

	if !c.jsonOutput {
		fmt.Fprintln(c.Out, "Uninstalling Portless...")
	}
	hooks := uninstallStepHooks{
		prepareRuntime: func(stepContext context.Context, force bool) (uninstallRuntimePreparation, error) {
			return c.prepareUninstallRuntime(stepContext, preview.Data.Present, force)
		},
		removeRelay: func(stepContext context.Context) (bool, error) {
			return c.removeRelayForUninstall(stepContext, preview.Relay)
		},
		removeState: func(stepContext context.Context, force bool) (bootstrap.InstallationStateRemoval, error) {
			return bootstrap.RemoveInstallationState(stepContext, c.paths, force)
		},
		removeLauncher: removeLauncher,
	}
	result, err := executeUninstallSteps(ctx, preview, options, hooks)
	if err != nil {
		if len(result.Errors) > 0 {
			if outputErr := c.printUninstallComplete(result); outputErr != nil {
				return outputErr
			}
			if c.jsonOutput {
				_ = writeJSON(c.Err, errorOutput{Error: errorDetail{Code: "UNINSTALL_INCOMPLETE", Message: err.Error()}})
			} else {
				fmt.Fprintln(c.Err, "portless:", err)
			}
			return &reportedCommandError{}
		}
		return err
	}
	return c.printUninstallComplete(result)
}

func (c *CLI) inspectUninstall(ctx context.Context) (uninstallOutput, error) {
	state, err := bootstrap.InspectInstallationState(c.paths)
	if err != nil {
		return uninstallOutput{}, err
	}
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return uninstallOutput{}, err
	}
	launcher := inspectLauncher()
	result := uninstallOutput{
		Action: "uninstall", Changed: false, Complete: false,
		Daemon: uninstallDaemonOutput{State: "stopped", ActiveEnvironments: []string{}},
		Relay: uninstallRelayOutput{
			Installed: status.Installed, State: status.State(), Service: status.Service, OwnerUID: status.OwnerUID,
			TargetSocket: status.TargetSocket, DNSTargetSocket: status.DNSTargetSocket, HelperPath: status.HelperPath,
			ConfigurationPath: status.ConfigurationPath, ReceiptPath: status.ReceiptPath, ResolverPath: status.ResolverPath,
			EndpointPoolReady: status.EndpointPoolReady, AdministratorPrompt: status.Installed && os.Geteuid() != 0,
		},
		Data:       uninstallDataOutput{Path: state.Path, Present: state.Present},
		Launcher:   launcher,
		WillRemove: append([]string(nil), uninstallRemovalCategories...),
	}

	if status.Installed {
		uid, _ := requestingUserIDs()
		result.Blockers = append(result.Blockers, relayUninstallBlockers(status, c.paths, uid)...)
	}

	if !state.Present {
		result.Daemon.InventoryAvailable = true
		return result, nil
	}
	inspection, inspectErr := bootstrap.InspectDaemon(ctx, c.paths)
	if inspectErr == nil {
		result.Daemon.State = "running"
		result.Daemon.PID = inspection.Record.PID
		result.Daemon.InstanceID = inspection.Record.InstanceID
		result.Daemon.ActiveEnvironments = append([]string{}, inspection.Identity.ActiveEnvironments...)
		sort.Strings(result.Daemon.ActiveEnvironments)
		if inspection.Compatible && inspection.CurrentBuild {
			client, _, connectErr := bootstrap.ConnectExisting(ctx, c.paths)
			if connectErr == nil {
				plan, inventoryErr := loadResetPlan(ctx, client)
				if inventoryErr != nil {
					return uninstallOutput{}, inventoryErr
				}
				result.Projects = plan.Projects
				result.Environments = plan.Environments
				result.ManagedVolumeEnvironments = plan.ManagedVolumeEnvironments
				result.Daemon.ActiveEnvironments = append([]string{}, plan.ActiveEnvironments...)
				result.Daemon.InventoryAvailable = true
				result.Daemon.TopologyIncompatible = plan.TopologyIncompatible
			} else {
				result.Daemon.Problem = connectErr.Error()
			}
		} else {
			result.Daemon.Problem = strings.Join(inspection.Problems, "; ")
		}
		return result, nil
	}

	record, recordErr := bootstrap.ReadControl(c.paths)
	switch {
	case recordErr == nil:
		result.Daemon.State = "unverified"
		result.Daemon.PID = record.PID
		result.Daemon.InstanceID = record.InstanceID
		result.Daemon.Problem = inspectErr.Error()
	case errors.Is(recordErr, os.ErrNotExist):
		result.Daemon.State = "stopped"
		if _, databaseErr := os.Lstat(c.paths.Database); errors.Is(databaseErr, os.ErrNotExist) {
			result.Daemon.InventoryAvailable = true
		}
	default:
		result.Daemon.State = "unverified"
		result.Daemon.Problem = recordErr.Error()
	}
	return result, nil
}

func relayUninstallBlockers(status ingress.InstallationStatus, paths bootstrap.Paths, uid int) []string {
	if !status.Installed {
		return nil
	}
	if ownershipErr := ingress.ValidateOwnership(status, uid); ownershipErr != nil {
		return []string{ownershipErr.Error() + "; full uninstall will not override relay ownership—inspect `portless relay status` and use `portless relay uninstall --force` separately only if that installation should be removed"}
	}
	if status.TargetSocket != paths.Ingress || status.DNSTargetSocket != paths.DNS {
		return []string{fmt.Sprintf("the relay targets a different Portless data directory (%s, %s); full uninstall will not remove another installation's relay", status.TargetSocket, status.DNSTargetSocket)}
	}
	return nil
}

func (c *CLI) prepareUninstallRuntime(ctx context.Context, dataPresent, force bool) (uninstallRuntimePreparation, error) {
	if !dataPresent {
		return uninstallRuntimePreparation{}, nil
	}
	var (
		client *bootstrap.Client
		err    error
	)
	if force {
		client, err = c.connectCurrentDaemonForForcedReset(ctx)
	} else {
		client, _, err = bootstrap.Connect(ctx, c.paths)
	}
	if err != nil {
		return uninstallRuntimePreparation{}, err
	}
	plan, err := loadResetPlan(ctx, client)
	if err != nil {
		return uninstallRuntimePreparation{}, err
	}
	if len(plan.ActiveEnvironments) > 0 && !force {
		if plan.TopologyIncompatible {
			return uninstallRuntimePreparation{}, incompatibleActiveUninstallError(plan.ActiveEnvironments)
		}
		return uninstallRuntimePreparation{}, activeUninstallError(plan.ActiveEnvironments)
	}
	var prepared struct {
		Processes int                     `json:"processes"`
		Runtimes  []container.ResetResult `json:"runtimes"`
	}
	if err := client.Do(ctx, http.MethodPost, "/api/v1/runtime/reset", map[string]bool{"force": force}, &prepared); err != nil {
		return uninstallRuntimePreparation{}, err
	}
	cancel := func() {
		cancelContext, cancelRequest := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelRequest()
		_ = client.Do(cancelContext, http.MethodPost, "/api/v1/runtime/reset/cancel", nil, nil)
	}
	return uninstallRuntimePreparation{
		Prepared: true, Projects: plan.Projects, Environments: plan.Environments,
		ManagedVolumeEnvironments: plan.ManagedVolumeEnvironments, ActiveEnvironments: append([]string{}, plan.ActiveEnvironments...),
		Processes: prepared.Processes, Runtimes: append([]container.ResetResult(nil), prepared.Runtimes...), Cancel: cancel,
	}, nil
}

func (c *CLI) removeRelayForUninstall(ctx context.Context, relay uninstallRelayOutput) (bool, error) {
	if !relay.Installed {
		return false, nil
	}
	executable, err := resolvedExecutable()
	if err != nil {
		return false, err
	}
	if !c.jsonOutput {
		fmt.Fprintln(c.Out, "Removing the HTTP/DNS relay requires administrator approval.")
	}
	output := c.Out
	if c.jsonOutput {
		output = c.Err
	}
	uid, _ := requestingUserIDs()
	return ingress.Uninstall(ctx, ingress.UninstallRequest{
		Executable: executable, UID: uid, Force: false, Stdin: os.Stdin, Stdout: output, Stderr: c.Err,
	})
}

func executeUninstallSteps(ctx context.Context, preview uninstallOutput, options uninstallOptions, hooks uninstallStepHooks) (uninstallOutput, error) {
	result := preview
	result.Confirmed = true
	result.Forced = options.force
	result.WillRemove = nil
	preparation, err := hooks.prepareRuntime(ctx, options.force)
	if err != nil {
		return result, fmt.Errorf("remove Portless-managed runtimes: %w", err)
	}
	cleanupFinished := false
	if preparation.Prepared && preparation.Cancel != nil {
		defer func() {
			if !cleanupFinished {
				preparation.Cancel()
			}
		}()
	}
	if preparation.Prepared {
		result.Projects = preparation.Projects
		result.Environments = preparation.Environments
		result.ManagedVolumeEnvironments = preparation.ManagedVolumeEnvironments
		result.Daemon.ActiveEnvironments = append([]string{}, preparation.ActiveEnvironments...)
		result.ProcessesStopped = preparation.Processes
		result.RuntimeCleanup = append([]container.ResetResult(nil), preparation.Runtimes...)
		if preparation.Processes > 0 || runtimeCleanupChanged(preparation.Runtimes) {
			result.Changed = true
			result.Removed = append(result.Removed, "Portless-supervised processes and installation-owned container resources")
		}
	}

	relayRemoved, err := hooks.removeRelay(ctx)
	if err != nil {
		result.Errors = append(result.Errors, "relay removal failed; daemon state and CLI launcher were preserved: "+err.Error())
		return result, fmt.Errorf("remove Portless relay: %w", err)
	}
	result.Relay.Removed = relayRemoved
	result.Relay.Installed = false
	result.Relay.State = "not installed"
	if relayRemoved {
		result.Changed = true
		result.Removed = append(result.Removed, "HTTP/DNS relay, resolver, and loopback endpoint pool")
	}

	state, err := hooks.removeState(ctx, options.force)
	if err != nil {
		result.Errors = append(result.Errors, "daemon/data removal failed; CLI launcher was preserved: "+err.Error())
		return result, fmt.Errorf("remove Portless daemon and data: %w", err)
	}
	cleanupFinished = true
	result.Data.Removed = state.Removed
	result.Data.Present = false
	result.Daemon.State = "stopped"
	result.Daemon.PID = 0
	result.Daemon.InstanceID = ""
	if state.Removed {
		result.Changed = true
		result.Removed = append(result.Removed, "daemon and complete Portless data directory")
	}

	launcherRemoved, err := hooks.removeLauncher(result.Launcher)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("remove CLI launcher %s: %v", result.Launcher.Path, err))
		return result, fmt.Errorf("Portless runtimes, relay, daemon, and data were removed, but the CLI launcher remains at %s: %w", result.Launcher.Path, err)
	}
	result.Launcher.Removed = launcherRemoved
	if launcherRemoved {
		result.Changed = true
		result.Removed = append(result.Removed, "CLI launcher")
	}
	result.Complete = true
	return result, nil
}

func runtimeCleanupChanged(results []container.ResetResult) bool {
	for _, result := range results {
		if result.Containers > 0 || result.Volumes > 0 || result.Networks > 0 {
			return true
		}
	}
	return false
}

func activeUninstallError(environments []string) error {
	return fmt.Errorf("uninstall requires every environment to be stopped; active: %s; run `portless down --all`, then retry, or use `portless uninstall --force --yes`", strings.Join(environments, ", "))
}

func incompatibleActiveUninstallError(environments []string) error {
	return fmt.Errorf("stored application topology is incompatible with this Portless build, so active environments cannot be shut down individually: %s; use `portless uninstall --force --yes` to stop verified runtimes and remove Portless", strings.Join(environments, ", "))
}

func (c *CLI) printUninstallPreview(result uninstallOutput) error {
	if c.jsonOutput {
		return writeJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Portless uninstall preview"))
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Installation:")
	fmt.Fprintf(c.Out, "  Daemon: %s", result.Daemon.State)
	if result.Daemon.PID > 0 {
		fmt.Fprintf(c.Out, " (PID %d)", result.Daemon.PID)
	}
	fmt.Fprintln(c.Out)
	if result.Daemon.Problem != "" {
		fmt.Fprintln(c.Out, "  Daemon detail: "+result.Daemon.Problem)
	}
	if result.Daemon.InventoryAvailable {
		fmt.Fprintf(c.Out, "  Application state: %d %s, %d %s\n", result.Projects, counted(result.Projects, "project"), result.Environments, counted(result.Environments, "environment"))
		fmt.Fprintf(c.Out, "  Managed container environments: %d\n", result.ManagedVolumeEnvironments)
	} else {
		fmt.Fprintln(c.Out, "  Application state: inventory will be verified before confirmed removal")
		if result.Data.Present && !result.Forced {
			fmt.Fprintln(c.Out, "  Confirmation: requires a current running daemon, or --force to authenticate and purge recoverable runtimes")
		}
	}
	fmt.Fprintf(c.Out, "  Relay: %s", result.Relay.State)
	if result.Relay.Installed && result.Relay.Service != "" {
		fmt.Fprintf(c.Out, " (%s)", result.Relay.Service)
	}
	fmt.Fprintln(c.Out)
	if result.Relay.Installed {
		fmt.Fprintf(c.Out, "  Resolver: %s\n", result.Relay.ResolverPath)
		fmt.Fprintf(c.Out, "  Reserved TCP endpoint pool: %s\n", yesNo(result.Relay.EndpointPoolReady))
		if result.Relay.AdministratorPrompt {
			fmt.Fprintln(c.Out, "  Administrator approval: required during confirmed removal")
		}
	}
	fmt.Fprintf(c.Out, "  Data: %s", result.Data.Path)
	if !result.Data.Present {
		fmt.Fprint(c.Out, " (not installed)")
	}
	fmt.Fprintln(c.Out)
	printLauncherPlan(c, result.Launcher)

	if len(result.Daemon.ActiveEnvironments) > 0 {
		fmt.Fprintln(c.Out)
		label := "Uninstall is currently blocked by active environments:"
		if result.Forced {
			label = "Force uninstall will terminate verified Portless runtimes in these environments:"
		}
		fmt.Fprintln(c.Out, c.warning(c.Out, label))
		for _, environment := range result.Daemon.ActiveEnvironments {
			fmt.Fprintln(c.Out, "  "+environment)
		}
	}
	if len(result.Blockers) > 0 {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, c.warning(c.Out, "Safety blockers:"))
		for _, blocker := range result.Blockers {
			fmt.Fprintln(c.Out, "  "+blocker)
		}
	}

	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "This will permanently remove:")
	for _, item := range result.WillRemove {
		fmt.Fprintln(c.Out, "  "+item)
	}
	fmt.Fprintln(c.Out)
	command := "portless uninstall --yes"
	if result.Forced || result.Data.Present && !result.Daemon.InventoryAvailable {
		command = "portless uninstall --force --yes"
	}
	fmt.Fprintf(c.Out, "No changes were made. Run `%s` to continue.\n", command)
	return nil
}

func (c *CLI) printUninstallComplete(result uninstallOutput) error {
	if c.jsonOutput {
		return writeJSON(c.Out, result)
	}
	if result.Complete {
		fmt.Fprintln(c.Out, c.success(c.Out, "Portless uninstall complete."))
	} else if result.Relay.Installed || result.Data.Present {
		fmt.Fprintln(c.Out, c.warning(c.Out, "Portless uninstall did not complete."))
	} else {
		fmt.Fprintln(c.Out, c.warning(c.Out, "Portless uninstall needs one manual cleanup step."))
	}
	for _, runtime := range result.RuntimeCleanup {
		fmt.Fprintf(c.Out, "%s cleanup: %d containers, %d volumes, %d networks.\n", runtime.Runtime, runtime.Containers, runtime.Volumes, runtime.Networks)
	}
	if result.ProcessesStopped > 0 {
		fmt.Fprintf(c.Out, "Stopped %d supervised %s.\n", result.ProcessesStopped, counted(result.ProcessesStopped, "process"))
	}
	if result.Relay.Removed {
		fmt.Fprintln(c.Out, "Relay: removed (service, resolver, and loopback endpoint pool)")
	} else if result.Relay.Installed {
		fmt.Fprintln(c.Out, "Relay: still installed")
	} else {
		fmt.Fprintln(c.Out, "Relay: not installed")
	}
	if result.Data.Removed {
		fmt.Fprintln(c.Out, "Daemon and data: removed from "+result.Data.Path)
	} else if result.Data.Present {
		fmt.Fprintln(c.Out, "Daemon and data: still present at "+result.Data.Path)
	} else {
		fmt.Fprintln(c.Out, "Daemon and data: not installed")
	}
	switch {
	case result.Launcher.Removed:
		fmt.Fprintln(c.Out, "CLI launcher: removed from "+result.Launcher.Path)
	case result.Launcher.Action == "remove":
		fmt.Fprintln(c.Out, "CLI launcher: still installed at "+result.Launcher.Path)
	case result.Launcher.Action == "preserve":
		fmt.Fprintln(c.Out, "CLI launcher: preserved ("+result.Launcher.Reason+")")
		if result.Launcher.Path != "" {
			fmt.Fprintln(c.Out, "  "+result.Launcher.Path)
		}
	default:
		fmt.Fprintln(c.Out, "CLI launcher: not installed")
	}
	for _, message := range result.Errors {
		label := "Failure: "
		if !result.Relay.Installed && !result.Data.Present {
			label = "Manual cleanup: "
		}
		fmt.Fprintln(c.Out, label+message)
	}
	return nil
}

func printLauncherPlan(c *CLI, plan launcherPlan) {
	switch plan.Action {
	case "remove":
		fmt.Fprintf(c.Out, "  CLI launcher: remove %s (%s)\n", plan.Path, plan.Kind)
		if plan.Target != "" && plan.Target != plan.Path {
			fmt.Fprintf(c.Out, "  Source/build target: preserve %s\n", plan.Target)
		}
	case "preserve":
		fmt.Fprintf(c.Out, "  CLI launcher: preserve %s (%s)\n", plan.Path, plan.Reason)
	default:
		fmt.Fprintln(c.Out, "  CLI launcher: not found")
	}
}

func yesNo(value bool) string {
	if value {
		return "configured"
	}
	return "not configured"
}

func inspectLauncher() launcherPlan {
	invocation, err := resolveInvocationPath(os.Args[0])
	if err != nil {
		return launcherPlan{Kind: "unknown", Action: "preserve", Reason: err.Error()}
	}
	executable, err := os.Executable()
	if err != nil {
		return launcherPlan{Path: invocation, Kind: "unknown", Action: "preserve", Reason: err.Error()}
	}
	return classifyLauncher(invocation, executable, recognizedLauncherDirectories())
}

func resolveInvocationPath(argument string) (string, error) {
	path := argument
	if !strings.ContainsRune(path, filepath.Separator) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return "", fmt.Errorf("locate current CLI launcher: %w", err)
		}
		path = resolved
	}
	return filepath.Abs(path)
}

func classifyLauncher(invocation, executable string, installDirectories []string) launcherPlan {
	plan := launcherPlan{Path: invocation, Kind: "unknown", Action: "preserve", Reason: "launcher identity could not be verified"}
	invocation, invocationErr := filepath.Abs(invocation)
	if invocationErr != nil {
		plan.Reason = invocationErr.Error()
		return plan
	}
	plan.Path = filepath.Clean(invocation)
	executable, executableErr := filepath.Abs(executable)
	if executableErr != nil {
		plan.Reason = executableErr.Error()
		return plan
	}
	executable, executableErr = filepath.EvalSymlinks(executable)
	if executableErr != nil {
		plan.Reason = "current executable cannot be resolved: " + executableErr.Error()
		return plan
	}
	plan.executable = executable
	info, err := os.Lstat(plan.Path)
	if errors.Is(err, os.ErrNotExist) {
		plan.Kind = "missing"
		plan.Action = "not-found"
		plan.Reason = "launcher is already absent"
		return plan
	}
	if err != nil {
		plan.Reason = err.Error()
		return plan
	}
	if info.Mode()&os.ModeSymlink != 0 {
		plan.Kind = "symlink"
		target, err := filepath.EvalSymlinks(plan.Path)
		if err != nil {
			plan.Reason = "symlink target cannot be resolved: " + err.Error()
			return plan
		}
		plan.Target = target
		if sameFile(target, executable) {
			plan.Action = "remove"
			plan.Reason = "verified launcher symlink; its target will be preserved"
			return plan
		}
		plan.Reason = "symlink does not target the running Portless executable"
		return plan
	}
	if !info.Mode().IsRegular() {
		plan.Kind = "other"
		plan.Reason = "launcher is not a regular file or symlink"
		return plan
	}
	plan.Kind = "regular-file"
	plan.Target = plan.Path
	if filepath.Base(plan.Path) != "portless" || !sameFile(plan.Path, executable) {
		plan.Reason = "file is not the running portless executable"
		return plan
	}
	if !directoryRecognized(filepath.Dir(plan.Path), installDirectories) {
		plan.Reason = "running executable is outside a recognized CLI install directory; source-tree builds are never removed automatically"
		return plan
	}
	plan.Action = "remove"
	plan.Reason = "verified installed CLI executable"
	return plan
}

func removeLauncher(plan launcherPlan) (bool, error) {
	if plan.Action != "remove" || plan.Path == "" {
		return false, nil
	}
	fresh := classifyLauncher(plan.Path, plan.executable, recognizedLauncherDirectories())
	if fresh.Action == "not-found" {
		return true, nil
	}
	if fresh.Action != "remove" || fresh.Kind != plan.Kind || (plan.Target != "" && fresh.Target != plan.Target) {
		return false, errors.New("launcher changed after uninstall preflight; it was left in place")
	}
	if err := os.Remove(plan.Path); err != nil {
		return false, err
	}
	return true, nil
}

func recognizedLauncherDirectories() []string {
	directories := make([]string, 0, 8)
	if value := os.Getenv("GOBIN"); value != "" {
		directories = append(directories, value)
	}
	if value := os.Getenv("GOPATH"); value != "" {
		for _, root := range filepath.SplitList(value) {
			directories = append(directories, filepath.Join(root, "bin"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		directories = append(directories, filepath.Join(home, "go", "bin"), filepath.Join(home, "bin"), filepath.Join(home, ".local", "bin"))
	}
	directories = append(directories, "/usr/local/bin", "/opt/homebrew/bin")
	return directories
}

func directoryRecognized(directory string, recognized []string) bool {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return false
	}
	for _, candidate := range recognized {
		candidate, err = filepath.Abs(candidate)
		if err == nil && filepath.Clean(candidate) == filepath.Clean(directory) {
			return true
		}
	}
	return false
}

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
