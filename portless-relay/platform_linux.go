//go:build linux

package relay

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	systemdUnitName   = "portless-relay.service"
	systemdHelperPath = "/usr/local/libexec/portless/portless-relay"
	systemdUnitPath   = "/etc/systemd/system/portless-relay.service"
	systemdReceipt    = "/var/lib/portless/relay.json"
	systemdResolver   = "/etc/systemd/resolved.conf.d/portless.conf"
)

type linuxPlatform struct {
	commands commandRunner
}

func newHostPlatform() hostPlatform { return linuxPlatform{commands: execCommandRunner{}} }

func (linuxPlatform) installation() platformInstallation {
	return platformInstallation{
		Name: "systemd", Service: systemdUnitName, HelperPath: systemdHelperPath,
		ConfigurationPath: systemdUnitPath, ReceiptPath: systemdReceipt, ResolverPath: systemdResolver,
	}
}

func (platform linuxPlatform) install(ctx context.Context, request SetupRequest) (resultErr error) {
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
	transaction, err := beginArtifactTransaction(systemdHelperPath, systemdUnitPath, systemdResolver, systemdReceipt)
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
	if err := copyExecutableAtomically(request.Executable, systemdHelperPath); err != nil {
		return fmt.Errorf("install relay helper executable: %w", err)
	}
	artifactsChanged = true
	if err := writeRootFileAtomically(systemdUnitPath, renderSystemdUnit(request), 0o644); err != nil {
		return fmt.Errorf("install relay system service: %w", err)
	}
	if err := writeRootFileAtomically(systemdResolver, renderResolvedConfiguration(), 0o644); err != nil {
		return fmt.Errorf("install scoped portless.test resolver: %w", err)
	}
	if previousState == relayServiceRunning {
		serviceTouched = true
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "stop", systemdUnitName); err != nil {
			return fmt.Errorf("stop existing relay system service: %w", err)
		}
	}
	if err := waitForRelayAddressesAvailable(ctx, 2*time.Second); err != nil {
		return err
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	enablementTouched = true
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "enable", systemdUnitName); err != nil {
		return err
	}
	serviceTouched = true
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", systemdUnitName); err != nil {
		return err
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", "systemd-resolved.service"); err != nil {
		return fmt.Errorf("activate scoped portless.test resolver: %w", err)
	}
	if err := writeInstallationReceipt(platform.installation(), request); err != nil {
		return err
	}
	committed = true
	return transaction.commit()
}

func (platform linuxPlatform) rollbackInstall(ctx context.Context, transaction *artifactTransaction, previousState relayServiceState, previouslyEnabled, serviceTouched, enablementTouched, artifactsChanged bool) error {
	var rollbackErr error
	if serviceTouched {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "stop", systemdUnitName); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("stop failed relay service during rollback: %w", err))
		}
	}
	if enablementTouched && !previouslyEnabled {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "disable", systemdUnitName); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore relay service disablement: %w", err))
		}
	}
	rollbackErr = errors.Join(rollbackErr, transaction.rollback())
	if artifactsChanged {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("reload restored relay service definition: %w", err))
		}
		if enablementTouched && previouslyEnabled {
			if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "enable", systemdUnitName); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore relay service enablement: %w", err))
			}
		}
		if previousState == relayServiceRunning {
			if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", systemdUnitName); err != nil {
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
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", systemdUnitName); err != nil {
		return fmt.Errorf("restart relay system service: %w", err)
	}
	return nil
}

func (platform linuxPlatform) uninstall(ctx context.Context, _ uninstallSpec) error {
	state, err := platform.serviceState(ctx)
	if err != nil {
		return err
	}
	configurationPresent, err := pathExists(systemdUnitPath)
	if err != nil {
		return fmt.Errorf("inspect relay system service before removal: %w", err)
	}
	if state == relayServiceRunning {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "disable", "--now", systemdUnitName); err != nil {
			return fmt.Errorf("stop and disable relay system service: %w", err)
		}
	} else if configurationPresent {
		if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "disable", "--now", systemdUnitName); err != nil {
			return fmt.Errorf("disable relay system service: %w", err)
		}
	}
	state, err = platform.serviceState(ctx)
	if err != nil {
		return err
	}
	if state != relayServiceStopped {
		return fmt.Errorf("relay system service %s is still active", systemdUnitName)
	}
	if err := removeExactFile(systemdUnitPath); err != nil {
		return fmt.Errorf("remove %s: %w", systemdUnitPath, err)
	}
	if err := removeExactFile(systemdResolver); err != nil {
		return fmt.Errorf("remove %s: %w", systemdResolver, err)
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd after relay removal: %w", err)
	}
	if err := runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "restart", "systemd-resolved.service"); err != nil {
		return fmt.Errorf("reload system resolver after relay removal: %w", err)
	}
	_ = runHostCommand(ctx, platform.commands, "/usr/bin/systemctl", "reset-failed", systemdUnitName)
	for _, path := range []string{systemdHelperPath, systemdReceipt} {
		if err := removeExactFile(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	removeDirectoryIfEmpty(filepath.Dir(systemdHelperPath))
	removeDirectoryIfEmpty(filepath.Dir(systemdReceipt))
	return nil
}

func (platform linuxPlatform) serviceState(ctx context.Context) (relayServiceState, error) {
	output, err := platform.commands.combinedOutput(ctx, "/usr/bin/systemctl", "is-active", "--quiet", systemdUnitName)
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
	output, err := platform.commands.combinedOutput(ctx, "/usr/bin/systemctl", "is-enabled", "--quiet", systemdUnitName)
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
	return []byte(strings.Join([]string{
		"[Unit]",
		"Description=Portless localhost relay",
		"After=local-fs.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s __relay --socket %s --dns-socket %s --uid %d --gid %d", systemdHelperPath, systemdQuoteArgument(request.TargetSocket), systemdQuoteArgument(request.DNSTargetSocket), request.UID, request.GID),
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
	return []byte("[Resolve]\nDNS=" + DefaultDNSAddress + "\nDomains=~portless.test\n")
}

func (linuxPlatform) prepareRuntime(context.Context) error { return nil }

func (linuxPlatform) loopbackPoolStatus() (bool, string, error) {
	return true, "IPv4 127/8 is routed by the Linux loopback interface", nil
}
