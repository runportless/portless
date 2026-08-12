//go:build darwin

package ingress

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	launchdLabel      = "dev.portless.ingress"
	launchdHelperPath = "/Library/PrivilegedHelperTools/dev.portless.ingress"
	launchdPlistPath  = "/Library/LaunchDaemons/dev.portless.ingress.plist"
	launchdReceipt    = "/var/db/portless/ingress.json"
)

func currentPlatformInstallation() platformInstallation {
	return platformInstallation{
		Name: "launchd", Service: launchdLabel, HelperPath: launchdHelperPath,
		ConfigurationPath: launchdPlistPath, ReceiptPath: launchdReceipt,
	}
}

func installPlatform(ctx context.Context, request SetupRequest) error {
	if err := copyExecutableAtomically(request.Executable, launchdHelperPath); err != nil {
		return fmt.Errorf("install ingress helper executable: %w", err)
	}
	plist, err := renderLaunchdPlist(request)
	if err != nil {
		return err
	}
	if err := writeRootFileAtomically(launchdPlistPath, plist, 0o644); err != nil {
		return fmt.Errorf("install ingress launch daemon: %w", err)
	}
	_ = runCommand(ctx, "/bin/launchctl", "bootout", "system/"+launchdLabel)
	if err := waitForPortAvailable(ctx, 2*time.Second); err != nil {
		return err
	}
	if err := runCommand(ctx, "/bin/launchctl", "bootstrap", "system", launchdPlistPath); err != nil {
		return err
	}
	if err := runCommand(ctx, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
		return err
	}
	return writeInstallationReceipt(request)
}

func restartPlatform(ctx context.Context) error {
	if err := runCommand(ctx, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
		return fmt.Errorf("restart ingress launch daemon: %w", err)
	}
	return nil
}

func uninstallPlatform(ctx context.Context) error {
	loaded, err := platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if loaded {
		if err := runCommand(ctx, "/bin/launchctl", "bootout", "system/"+launchdLabel); err != nil {
			return fmt.Errorf("stop ingress launch daemon: %w", err)
		}
	} else {
		_ = runCommand(ctx, "/bin/launchctl", "bootout", "system/"+launchdLabel)
	}
	loaded, err = platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if loaded {
		return fmt.Errorf("ingress launch daemon %s is still loaded", launchdLabel)
	}
	for _, path := range []string{launchdPlistPath, launchdHelperPath, launchdReceipt} {
		if err := removeExactFile(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	removeDirectoryIfEmpty(filepath.Dir(launchdReceipt))
	return nil
}

func platformServiceRunning(ctx context.Context) (bool, error) {
	command := exec.CommandContext(ctx, "/bin/launchctl", "print", "system/"+launchdLabel)
	if err := command.Run(); err == nil {
		return true, nil
	} else if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect ingress launch daemon: %w", err)
	}
}

func platformConfigurationOwner(path string) (int, int, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer file.Close()
	var document struct {
		Arguments []string `xml:"dict>array>string"`
	}
	if err := xml.NewDecoder(io.LimitReader(file, 256<<10)).Decode(&document); err != nil {
		return 0, 0, "", err
	}
	return relayArgumentValues(document.Arguments)
}

func renderLaunchdPlist(request SetupRequest) ([]byte, error) {
	// encoding/xml cannot directly express alternating plist key/value elements,
	// so render the small fixed document with escaped dynamic values.
	escape := func(value string) string {
		var encoded strings.Builder
		_ = xml.EscapeText(&encoded, []byte(value))
		return encoded.String()
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>__ingress</string>
    <string>--socket</string>
    <string>%s</string>
    <string>--uid</string>
    <string>%d</string>
    <string>--gid</string>
    <string>%d</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>2</integer>
</dict>
</plist>
`, launchdLabel, escape(launchdHelperPath), escape(request.TargetSocket), request.UID, request.GID)
	return []byte(content), nil
}
