//go:build linux

package installation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

const (
	systemdUnitName   = "portless-relay.service"
	systemdHelperPath = "/usr/local/libexec/portless/portless-relay"
	systemdUnitPath   = "/etc/systemd/system/portless-relay.service"
	systemdReceipt    = "/var/lib/portless/relay.json"
	systemdResolver   = "/etc/systemd/resolved.conf.d/portless.conf"
)

type linuxPlatform struct {
	commands   commandRunner
	operations platformOperations
	details    *platformInstallation
}

func newHostPlatform() hostPlatform { return linuxPlatform{commands: execCommandRunner{}} }

func (platform linuxPlatform) installation() platformInstallation {
	if platform.details != nil {
		return *platform.details
	}
	return platformInstallation{
		Name: "systemd", Service: systemdUnitName, HelperPath: systemdHelperPath,
		ConfigurationPath: systemdUnitPath, ReceiptPath: systemdReceipt, ResolverPath: systemdResolver,
		LifecycleLockPath: "/var/lib/portless/relay.lock",
	}
}

func (platform linuxPlatform) install(ctx context.Context, request SetupRequest) (resultErr error) {
	details := platform.installation()
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "is-active", "--quiet", "systemd-resolved.service"); err != nil {
		return fmt.Errorf("clean TCP endpoint DNS requires an active systemd-resolved service on Linux: %w", err)
	}
	previousState, err := platform.serviceState(ctx)
	if err != nil {
		return err
	}
	previouslyEnabled, err := platform.unitEnabled(ctx)
	if err != nil {
		return err
	}
	transaction, err := platform.operations.beginArtifactTransaction(details.HelperPath, details.ConfigurationPath, details.ResolverPath, details.ReceiptPath)
	if err != nil {
		return err
	}
	committed := false
	serviceTouched := false
	enablementTouched := false
	artifactsChanged := false
	defer func() {
		if resultErr == nil || committed {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, platform.rollbackInstall(rollbackContext, transaction, previousState, previouslyEnabled, serviceTouched, enablementTouched, artifactsChanged))
	}()
	if err := platform.operations.copyExecutable(request.Executable, details.HelperPath); err != nil {
		return fmt.Errorf("install relay helper executable: %w", err)
	}
	artifactsChanged = true
	if err := platform.operations.writeRootFile(details.ConfigurationPath, renderSystemdUnitFor(details, request), 0o644); err != nil {
		return fmt.Errorf("install relay system service: %w", err)
	}
	if err := platform.operations.writeRootFile(details.ResolverPath, renderResolvedConfiguration(), 0o644); err != nil {
		return fmt.Errorf("install scoped portless.test resolver: %w", err)
	}
	if previousState == relayServiceRunning {
		serviceTouched = true
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "stop", details.Service); err != nil {
			return fmt.Errorf("stop existing relay system service: %w", err)
		}
	}
	if err := platform.operations.waitForAddresses(ctx, 2*time.Second); err != nil {
		return err
	}
	if err := platform.operations.writeReceipt(details, request); err != nil {
		return err
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	enablementTouched = true
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "enable", details.Service); err != nil {
		return err
	}
	serviceTouched = true
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", details.Service); err != nil {
		return err
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", "systemd-resolved.service"); err != nil {
		return fmt.Errorf("activate scoped portless.test resolver: %w", err)
	}
	if err := platform.operations.waitUntilReady(ctx, 8*time.Second); err != nil {
		return fmt.Errorf("verify installed relay readiness: %w", err)
	}
	committed = true
	return transaction.commit()
}

func (platform linuxPlatform) rollbackInstall(ctx context.Context, transaction *artifactTransaction, previousState relayServiceState, previouslyEnabled, serviceTouched, enablementTouched, artifactsChanged bool) error {
	details := platform.installation()
	var rollbackErr error
	if serviceTouched {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "stop", details.Service); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("stop failed relay service during rollback: %w", err))
		}
	}
	if enablementTouched && !previouslyEnabled {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "disable", details.Service); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore relay service disablement: %w", err))
		}
	}
	rollbackErr = errors.Join(rollbackErr, transaction.rollback())
	if artifactsChanged {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("reload restored relay service definition: %w", err))
		}
		if enablementTouched && previouslyEnabled {
			if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "enable", details.Service); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore relay service enablement: %w", err))
			}
		}
		if previousState == relayServiceRunning {
			if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", details.Service); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restart restored relay service: %w", err))
			}
		}
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", "systemd-resolved.service"); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("reload restored system resolver: %w", err))
		}
	}
	return rollbackErr
}

func (platform linuxPlatform) restart(ctx context.Context) error {
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", platform.installation().Service); err != nil {
		return fmt.Errorf("restart relay system service: %w", err)
	}
	return nil
}

func (platform linuxPlatform) uninstall(ctx context.Context, _ uninstallSpec) error {
	details := platform.installation()
	state, err := platform.serviceState(ctx)
	if err != nil {
		return err
	}
	configurationPresent, err := platform.operations.pathExists(details.ConfigurationPath)
	if err != nil {
		return fmt.Errorf("inspect relay system service before removal: %w", err)
	}
	if state == relayServiceRunning {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "disable", "--now", details.Service); err != nil {
			return fmt.Errorf("stop and disable relay system service: %w", err)
		}
	} else if configurationPresent {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "disable", "--now", details.Service); err != nil {
			return fmt.Errorf("disable relay system service: %w", err)
		}
	}
	state, err = platform.serviceState(ctx)
	if err != nil {
		return err
	}
	if state != relayServiceStopped {
		return fmt.Errorf("relay system service %s is still active", details.Service)
	}
	if err := platform.operations.removeFile(details.ConfigurationPath); err != nil {
		return fmt.Errorf("remove %s: %w", details.ConfigurationPath, err)
	}
	if err := platform.operations.removeFile(details.ResolverPath); err != nil {
		return fmt.Errorf("remove %s: %w", details.ResolverPath, err)
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd after relay removal: %w", err)
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", "systemd-resolved.service"); err != nil {
		return fmt.Errorf("reload system resolver after relay removal: %w", err)
	}
	_ = runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "reset-failed", details.Service)
	for _, path := range []string{details.HelperPath, details.ReceiptPath} {
		if err := platform.operations.removeFile(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	removeDirectoryIfEmpty(filepath.Dir(details.HelperPath))
	removeDirectoryIfEmpty(filepath.Dir(details.ReceiptPath))
	return nil
}

func (platform linuxPlatform) serviceState(ctx context.Context) (relayServiceState, error) {
	output, err := platform.commands.combinedOutput(ctx, "/usr/bin/systemctl", "is-active", "--quiet", platform.installation().Service)
	if err == nil {
		return relayServiceRunning, nil
	}
	if exitCode, ok := commandExitCode(err); ok && (exitCode == 3 || exitCode == 4) {
		return relayServiceStopped, nil
	}
	detail := strings.TrimSpace(string(output))
	if detail != "" {
		return relayServiceUnknown, fmt.Errorf("inspect relay system service: %w: %s", err, detail)
	}
	return relayServiceUnknown, fmt.Errorf("inspect relay system service: %w", err)
}

func (platform linuxPlatform) unitEnabled(ctx context.Context) (bool, error) {
	output, err := platform.commands.combinedOutput(ctx, "/usr/bin/systemctl", "is-enabled", "--quiet", platform.installation().Service)
	if err == nil {
		return true, nil
	}
	if exitCode, ok := commandExitCode(err); ok && (exitCode == 1 || exitCode == 4) {
		return false, nil
	}
	detail := strings.TrimSpace(string(output))
	if detail != "" {
		return false, fmt.Errorf("inspect relay system service enablement: %w: %s", err, detail)
	}
	return false, fmt.Errorf("inspect relay system service enablement: %w", err)
}

func renderSystemdUnit(request SetupRequest) []byte {
	return renderSystemdUnitFor((linuxPlatform{}).installation(), request)
}

func renderSystemdUnitFor(details platformInstallation, request SetupRequest) []byte {
	return []byte(strings.Join([]string{
		"[Unit]",
		"Description=Portless localhost relay",
		"After=local-fs.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s __relay --socket %s --dns-socket %s --uid %d --gid %d", details.HelperPath, systemdQuoteArgument(request.TargetSocket), systemdQuoteArgument(request.DNSTargetSocket), request.UID, request.GID),
		"Restart=on-failure",
		"RestartSec=2",
		"NoNewPrivileges=true",
		"PrivateDevices=true",
		"PrivateTmp=true",
		"ProtectClock=true",
		"ProtectControlGroups=true",
		"ProtectKernelLogs=true",
		"ProtectKernelModules=true",
		"ProtectKernelTunables=true",
		"ProtectSystem=strict",
		"RestrictAddressFamilies=AF_UNIX AF_INET",
		"RestrictRealtime=true",
		"RestrictSUIDSGID=true",
		"LockPersonality=true",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SETGID CAP_SETUID",
		"UMask=0077",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n"))
}

func systemdQuoteArgument(value string) string {
	return strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
}

func renderResolvedConfiguration() []byte {
	return []byte("[Resolve]\nDNS=" + relayruntime.DefaultDNSAddress + "\nDomains=~portless.test\n")
}

func (platform linuxPlatform) prepareRuntime(_ context.Context, config relayruntime.Identity) error {
	receipt, err := platform.operations.readReceipt(platform.installation())
	if err != nil {
		return fmt.Errorf("verify relay installation before starting listeners: %w", err)
	}
	return validateRuntimeReceipt(receipt, config)
}

func (platform linuxPlatform) expectedArtifacts(receipt installationReceipt) ([]expectedArtifact, error) {
	details := platform.installation()
	request := SetupRequest{TargetSocket: receipt.TargetSocket, DNSTargetSocket: receipt.DNSTargetSocket, UID: receipt.OwnerUID, GID: receipt.OwnerGID}
	return []expectedArtifact{
		{path: details.ConfigurationPath, content: renderSystemdUnitFor(details, request)},
		{path: details.ResolverPath, content: renderResolvedConfiguration()},
	}, nil
}

func (linuxPlatform) loopbackPoolStatus() (endpointPoolStatus, error) {
	return endpointPoolStatus{ready: true, detail: "IPv4 127/8 is routed by the Linux loopback interface"}, nil
}
