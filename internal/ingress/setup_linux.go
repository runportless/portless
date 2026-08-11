//go:build linux

package ingress

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	systemdUnitName   = "portless-ingress.service"
	systemdHelperPath = "/usr/local/libexec/portless/portless-ingress"
	systemdUnitPath   = "/etc/systemd/system/portless-ingress.service"
)

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
	return nil
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
