//go:build darwin

package ingress

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const (
	launchdLabel      = "dev.portless.ingress"
	launchdHelperPath = "/Library/PrivilegedHelperTools/dev.portless.ingress"
	launchdPlistPath  = "/Library/LaunchDaemons/dev.portless.ingress.plist"
)

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
	return nil
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
