//go:build linux

package ingress

import (
	"context"
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
	systemdUnitName   = "portless-ingress.service"
	systemdHelperPath = "/usr/local/libexec/portless/portless-ingress"
	systemdUnitPath   = "/etc/systemd/system/portless-ingress.service"
	systemdReceipt    = "/var/lib/portless/ingress.json"
)

func currentPlatformInstallation() platformInstallation {
	return platformInstallation{
		Name: "systemd", Service: systemdUnitName, HelperPath: systemdHelperPath,
		ConfigurationPath: systemdUnitPath, ReceiptPath: systemdReceipt,
	}
}

func installPlatform(ctx context.Context, request SetupRequest) error {
	if err := copyExecutableAtomically(request.Executable, systemdHelperPath); err != nil {
		return fmt.Errorf("install ingress helper executable: %w", err)
	}
	if err := writeRootFileAtomically(systemdUnitPath, renderSystemdUnit(request), 0o644); err != nil {
		return fmt.Errorf("install ingress system service: %w", err)
	}
	_ = runCommand(ctx, "/usr/bin/systemctl", "stop", systemdUnitName)
	if err := waitForPortAvailable(ctx, 2*time.Second); err != nil {
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
	return writeInstallationReceipt(request)
}

func uninstallPlatform(ctx context.Context) error {
	running, err := platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		if err := runCommand(ctx, "/usr/bin/systemctl", "disable", "--now", systemdUnitName); err != nil {
			return fmt.Errorf("stop and disable ingress system service: %w", err)
		}
	} else {
		_ = runCommand(ctx, "/usr/bin/systemctl", "disable", "--now", systemdUnitName)
	}
	running, err = platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("ingress system service %s is still active", systemdUnitName)
	}
	if err := removeExactFile(systemdUnitPath); err != nil {
		return fmt.Errorf("remove %s: %w", systemdUnitPath, err)
	}
	if err := runCommand(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd after ingress removal: %w", err)
	}
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
		return false, fmt.Errorf("inspect ingress system service: %w", err)
	}
}

func platformConfigurationOwner(path string) (int, int, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 256<<10))
	if err != nil {
		return 0, 0, "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			return parseSystemdRelayArguments(strings.TrimPrefix(line, "ExecStart="))
		}
	}
	return 0, 0, "", fmt.Errorf("%s has no ExecStart", path)
}

func parseSystemdRelayArguments(value string) (int, int, string, error) {
	socketMarker, uidMarker, gidMarker := " --socket ", " --uid ", " --gid "
	socketIndex := strings.Index(value, socketMarker)
	uidIndex := strings.Index(value, uidMarker)
	gidIndex := strings.Index(value, gidMarker)
	if socketIndex < 0 || uidIndex <= socketIndex || gidIndex <= uidIndex {
		return 0, 0, "", fmt.Errorf("invalid ingress ExecStart")
	}
	socket := strings.TrimSpace(value[socketIndex+len(socketMarker) : uidIndex])
	if unquoted, err := strconv.Unquote(socket); err == nil {
		socket = unquoted
	}
	uid := strings.TrimSpace(value[uidIndex+len(uidMarker) : gidIndex])
	gidFields := strings.Fields(value[gidIndex+len(gidMarker):])
	if len(gidFields) == 0 {
		return 0, 0, "", fmt.Errorf("invalid ingress ExecStart group")
	}
	return relayArgumentValues([]string{"--socket", socket, "--uid", uid, "--gid", gidFields[0]})
}

func renderSystemdUnit(request SetupRequest) []byte {
	quotedSocket := strconv.Quote(request.TargetSocket)
	return []byte(strings.Join([]string{
		"[Unit]",
		"Description=Portless clean localhost ingress",
		"After=local-fs.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s __ingress --socket %s --uid %d --gid %d", systemdHelperPath, quotedSocket, request.UID, request.GID),
		"Restart=on-failure",
		"RestartSec=2",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n"))
}
