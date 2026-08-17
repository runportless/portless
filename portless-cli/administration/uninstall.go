package administration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-cli/command"
	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/system/installation"
	"github.com/portless-run/portless/portless-relay"
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

type launcherPlan = command.LauncherPlan

type uninstallOutput struct {
	Action                    string                        `json:"action"`
	Confirmed                 bool                          `json:"confirmed"`
	Forced                    bool                          `json:"forced"`
	Changed                   bool                          `json:"changed"`
	Complete                  bool                          `json:"complete"`
	Projects                  int                           `json:"projects"`
	Environments              int                           `json:"environments"`
	ManagedVolumeEnvironments int                           `json:"managedVolumeEnvironments"`
	ProcessesStopped          int                           `json:"processesStopped"`
	RuntimeCleanup            []contract.RuntimeResetResult `json:"runtimeCleanup,omitempty"`
	Daemon                    uninstallDaemonOutput         `json:"daemon"`
	Relay                     uninstallRelayOutput          `json:"relay"`
	Data                      uninstallDataOutput           `json:"data"`
	Launcher                  launcherPlan                  `json:"launcher"`
	WillRemove                []string                      `json:"willRemove,omitempty"`
	Removed                   []string                      `json:"removed,omitempty"`
	Blockers                  []string                      `json:"blockers,omitempty"`
	Errors                    []string                      `json:"errors,omitempty"`
}

type uninstallRuntimePreparation struct {
	Prepared                  bool
	Projects                  int
	Environments              int
	ManagedVolumeEnvironments int
	ActiveEnvironments        []string
	Processes                 int
	Runtimes                  []contract.RuntimeResetResult
	Cancel                    func()
}

type uninstallStepHooks struct {
	prepareRuntime func(context.Context, bool) (uninstallRuntimePreparation, error)
	removeRelay    func(context.Context) (bool, error)
	removeState    func(context.Context, bool) (installation.StateRemoval, error)
	removeLauncher func(launcherPlan) (bool, error)
}

func (c *Commands) uninstall(ctx context.Context, options uninstallOptions) error {
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

	if !c.JSONOutput {
		fmt.Fprintln(c.Out, "Uninstalling Portless...")
	}
	hooks := uninstallStepHooks{
		prepareRuntime: func(stepContext context.Context, force bool) (uninstallRuntimePreparation, error) {
			return c.prepareUninstallRuntime(stepContext, preview.Data.Present, force)
		},
		removeRelay: func(stepContext context.Context) (bool, error) {
			return c.removeRelayForUninstall(stepContext, preview.Relay)
		},
		removeState: func(stepContext context.Context, force bool) (installation.StateRemoval, error) {
			return c.Daemon.RemoveInstallationState(stepContext, force)
		},
		removeLauncher: c.Local.RemoveLauncher,
	}
	result, err := executeUninstallSteps(ctx, preview, options, hooks)
	if err != nil {
		if len(result.Errors) > 0 {
			if outputErr := c.printUninstallComplete(result); outputErr != nil {
				return outputErr
			}
			if c.JSONOutput {
				_ = command.WriteErrorOutput(c.Err, "UNINSTALL_INCOMPLETE", err.Error())
			} else {
				fmt.Fprintln(c.Err, "portless:", err)
			}
			return &command.ReportedError{}
		}
		return err
	}
	return c.printUninstallComplete(result)
}

func (c *Commands) inspectUninstall(ctx context.Context) (uninstallOutput, error) {
	state, err := c.Local.InspectState(c.Paths)
	if err != nil {
		return uninstallOutput{}, err
	}
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return uninstallOutput{}, err
	}
	launcher := c.Local.InspectLauncher()
	result := uninstallOutput{
		Action: "uninstall", Changed: false, Complete: false,
		Daemon: uninstallDaemonOutput{State: "stopped", ActiveEnvironments: []string{}},
		Relay: uninstallRelayOutput{
			Installed: status.Installed, State: status.State(), Service: status.Service, OwnerUID: status.OwnerUID,
			TargetSocket: status.TargetSocket, DNSTargetSocket: status.DNSTargetSocket, HelperPath: status.HelperPath,
			ConfigurationPath: status.ConfigurationPath, ReceiptPath: status.ReceiptPath, ResolverPath: status.ResolverPath,
			EndpointPoolReady: status.EndpointPoolReady, AdministratorPrompt: status.Installed && c.Local.EffectiveUID() != 0,
		},
		Data:       uninstallDataOutput{Path: state.Path, Present: state.Present},
		Launcher:   launcher,
		WillRemove: append([]string(nil), uninstallRemovalCategories...),
	}

	if status.Installed {
		uid, _ := c.Local.UserIDs()
		result.Blockers = append(result.Blockers, relayUninstallBlockersWithValidator(status, c.Paths, uid, c.Local.ValidateRelayOwner)...)
	}

	if !state.Present {
		result.Daemon.InventoryAvailable = true
		return result, nil
	}
	inspection, inspectErr := c.Daemon.Inspect(ctx)
	if inspectErr == nil {
		result.Daemon.State = "running"
		result.Daemon.PID = inspection.Record.PID
		result.Daemon.InstanceID = inspection.Record.InstanceID
		result.Daemon.ActiveEnvironments = append([]string{}, inspection.Identity.ActiveEnvironments...)
		sort.Strings(result.Daemon.ActiveEnvironments)
		if inspection.Compatible && inspection.CurrentBuild {
			client, _, connectErr := c.Daemon.ConnectExisting(ctx)
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

	record, recordErr := c.Daemon.ReadRecord()
	switch {
	case recordErr == nil:
		result.Daemon.State = "unverified"
		result.Daemon.PID = record.PID
		result.Daemon.InstanceID = record.InstanceID
		result.Daemon.Problem = inspectErr.Error()
	case errors.Is(recordErr, os.ErrNotExist):
		result.Daemon.State = "stopped"
		if _, databaseErr := os.Lstat(c.Paths.Database); errors.Is(databaseErr, os.ErrNotExist) {
			result.Daemon.InventoryAvailable = true
		}
	default:
		result.Daemon.State = "unverified"
		result.Daemon.Problem = recordErr.Error()
	}
	return result, nil
}

func relayUninstallBlockers(status relay.InstallationStatus, paths installation.Layout, uid int) []string {
	return relayUninstallBlockersWithValidator(status, paths, uid, relay.ValidateOwnership)
}

func relayUninstallBlockersWithValidator(status relay.InstallationStatus, paths installation.Layout, uid int, validateOwner func(relay.InstallationStatus, int) error) []string {
	if !status.Installed {
		return nil
	}
	if ownershipErr := validateOwner(status, uid); ownershipErr != nil {
		return []string{ownershipErr.Error() + "; full uninstall will not override relay ownership—inspect `portless relay status` and use `portless relay uninstall --force` separately only if that installation should be removed"}
	}
	if status.TargetSocket != paths.IngressSocket || status.DNSTargetSocket != paths.DNSSocket {
		return []string{fmt.Sprintf("the relay targets a different Portless data directory (%s, %s); full uninstall will not remove another installation's relay", status.TargetSocket, status.DNSTargetSocket)}
	}
	return nil
}

func (c *Commands) prepareUninstallRuntime(ctx context.Context, dataPresent, force bool) (uninstallRuntimePreparation, error) {
	if !dataPresent {
		return uninstallRuntimePreparation{}, nil
	}
	var (
		client *apiclient.Client
		err    error
	)
	if force {
		client, err = c.connectCurrentDaemonForForcedReset(ctx)
	} else {
		client, _, err = c.Daemon.Connect(ctx)
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
	prepared, err := client.PrepareReset(ctx, force)
	if err != nil {
		return uninstallRuntimePreparation{}, err
	}
	cancel := func() {
		cancelContext, cancelRequest := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelRequest()
		_ = client.CancelReset(cancelContext)
	}
	return uninstallRuntimePreparation{
		Prepared: true, Projects: plan.Projects, Environments: plan.Environments,
		ManagedVolumeEnvironments: plan.ManagedVolumeEnvironments, ActiveEnvironments: append([]string{}, plan.ActiveEnvironments...),
		Processes: prepared.Processes, Runtimes: append([]contract.RuntimeResetResult(nil), prepared.Runtimes...), Cancel: cancel,
	}, nil
}

func (c *Commands) removeRelayForUninstall(ctx context.Context, relayStatus uninstallRelayOutput) (bool, error) {
	if !relayStatus.Installed {
		return false, nil
	}
	executable, err := c.Local.ResolvedExecutable()
	if err != nil {
		return false, err
	}
	if !c.JSONOutput {
		fmt.Fprintln(c.Out, "Removing the HTTP/DNS relay requires administrator approval.")
	}
	output := c.Out
	if c.JSONOutput {
		output = c.Err
	}
	uid, _ := c.Local.UserIDs()
	return c.Local.UninstallRelay(ctx, relay.UninstallRequest{
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
		result.RuntimeCleanup = append([]contract.RuntimeResetResult(nil), preparation.Runtimes...)
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

func runtimeCleanupChanged(results []contract.RuntimeResetResult) bool {
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
