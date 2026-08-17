//go:build linux

package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func currentPlatformInstallation() platformInstallation {
	return platformInstallation{
		Name: "systemd", Service: systemdUnitName, HelperPath: systemdHelperPath,
		ConfigurationPath: systemdUnitPath, ReceiptPath: systemdReceipt, ResolverPath: systemdResolver,
	}
}

func installPlatform(ctx context.Context, request SetupRequest) error {
	if err := runCommand(ctx, "/usr/bin/systemctl", "is-active", "--quiet", "systemd-resolved.service"); err != nil {
		return errors.New("clean TCP endpoint DNS requires an active systemd-resolved service on Linux")
	}
	if err := copyExecutableAtomically(request.Executable, systemdHelperPath); err != nil {
		return fmt.Errorf("install relay helper executable: %w", err)
	}
	if err := writeRootFileAtomically(systemdUnitPath, renderSystemdUnit(request), 0o644); err != nil {
		return fmt.Errorf("install relay system service: %w", err)
	}
	if err := writeRootFileAtomically(systemdResolver, renderResolvedConfiguration(), 0o644); err != nil {
		return fmt.Errorf("install scoped portless.test resolver: %w", err)
	}
	_ = runCommand(ctx, "/usr/bin/systemctl", "stop", systemdUnitName)
	if err := waitForRelayAddressesAvailable(ctx, 2*time.Second); err != nil {
		return err
	}
	if err := runCommand(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := runCommand(ctx, "/usr/bin/systemctl", "enable", "--now", systemdUnitName); err != nil {
		return err
	}
	if err := runCommand(ctx, "/usr/bin/systemctl", "restart", systemdUnitName); err != nil {
		return err
	}
	if err := runCommand(ctx, "/usr/bin/systemctl", "restart", "systemd-resolved.service"); err != nil {
		return fmt.Errorf("activate scoped portless.test resolver: %w", err)
	}
	return writeInstallationReceipt(request)
}

func restartPlatform(ctx context.Context) error {
	if err := runCommand(ctx, "/usr/bin/systemctl", "restart", systemdUnitName); err != nil {
		return fmt.Errorf("restart relay system service: %w", err)
	}
	return nil
}

func uninstallPlatform(ctx context.Context, _ bool) error {
	running, err := platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		if err := runCommand(ctx, "/usr/bin/systemctl", "disable", "--now", systemdUnitName); err != nil {
			return fmt.Errorf("stop and disable relay system service: %w", err)
		}
	} else {
		_ = runCommand(ctx, "/usr/bin/systemctl", "disable", "--now", systemdUnitName)
	}
	running, err = platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("relay system service %s is still active", systemdUnitName)
	}
	if err := removeExactFile(systemdUnitPath); err != nil {
		return fmt.Errorf("remove %s: %w", systemdUnitPath, err)
	}
	if err := removeExactFile(systemdResolver); err != nil {
		return fmt.Errorf("remove %s: %w", systemdResolver, err)
	}
	if err := runCommand(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd after relay removal: %w", err)
	}
	_ = runCommand(ctx, "/usr/bin/systemctl", "restart", "systemd-resolved.service")
	_ = runCommand(ctx, "/usr/bin/systemctl", "reset-failed", systemdUnitName)
	for _, path := range []string{systemdHelperPath, systemdReceipt} {
		if err := removeExactFile(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	removeDirectoryIfEmpty(filepath.Dir(systemdHelperPath))
	removeDirectoryIfEmpty(filepath.Dir(systemdReceipt))
	return nil
}

func platformServiceRunning(ctx context.Context) (bool, error) {
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", "is-active", "--quiet", systemdUnitName)
	if err := command.Run(); err == nil {
		return true, nil
	} else if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect relay system service: %w", err)
	}
}

func platformConfigurationOwner(path string) (int, int, string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", "", err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 256<<10))
	if err != nil {
		return 0, 0, "", "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			return parseSystemdRelayArguments(strings.TrimPrefix(line, "ExecStart="))
		}
	}
	return 0, 0, "", "", fmt.Errorf("%s has no ExecStart", path)
}

func parseSystemdRelayArguments(value string) (int, int, string, string, error) {
	socketMarker, dnsMarker, uidMarker, gidMarker := " --socket ", " --dns-socket ", " --uid ", " --gid "
	socketIndex := strings.Index(value, socketMarker)
	dnsIndex := strings.Index(value, dnsMarker)
	uidIndex := strings.Index(value, uidMarker)
	gidIndex := strings.Index(value, gidMarker)
	if socketIndex < 0 || dnsIndex <= socketIndex || uidIndex <= dnsIndex || gidIndex <= uidIndex {
		return 0, 0, "", "", fmt.Errorf("invalid relay ExecStart")
	}
	socket := strings.TrimSpace(value[socketIndex+len(socketMarker) : dnsIndex])
	if unquoted, err := strconv.Unquote(socket); err == nil {
		socket = unquoted
	}
	dnsSocket := strings.TrimSpace(value[dnsIndex+len(dnsMarker) : uidIndex])
	if unquoted, err := strconv.Unquote(dnsSocket); err == nil {
		dnsSocket = unquoted
	}
	uid := strings.TrimSpace(value[uidIndex+len(uidMarker) : gidIndex])
	gidFields := strings.Fields(value[gidIndex+len(gidMarker):])
	if len(gidFields) == 0 {
		return 0, 0, "", "", fmt.Errorf("invalid relay ExecStart group")
	}
	return relayArgumentValues([]string{"--socket", socket, "--dns-socket", dnsSocket, "--uid", uid, "--gid", gidFields[0]})
}

func renderSystemdUnit(request SetupRequest) []byte {
	quotedSocket := strconv.Quote(request.TargetSocket)
	return []byte(strings.Join([]string{
		"[Unit]",
		"Description=Portless localhost relay",
		"After=local-fs.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s __relay --socket %s --dns-socket %s --uid %d --gid %d", systemdHelperPath, quotedSocket, strconv.Quote(request.DNSTargetSocket), request.UID, request.GID),
		"Restart=on-failure",
		"RestartSec=2",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n"))
}

func renderResolvedConfiguration() []byte {
	return []byte("[Resolve]\nDNS=" + DefaultDNSAddress + "\nDomains=~portless.test\n")
}

func prepareRelayLoopbackPool(context.Context, bool) error { return nil }

func removeRelayLoopbackPool(context.Context) error { return nil }

func relayLoopbackPoolStatus() (bool, string, error) {
	return true, "IPv4 127/8 is routed by the Linux loopback interface", nil
}
