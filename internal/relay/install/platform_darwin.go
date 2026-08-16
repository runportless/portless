//go:build darwin

package install

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/networking"
	"github.com/portless-run/portless/internal/relay"
)

const (
	launchdLabel      = "dev.portless.relay"
	launchdHelperPath = "/Library/PrivilegedHelperTools/dev.portless.relay"
	launchdPlistPath  = "/Library/LaunchDaemons/dev.portless.relay.plist"
	launchdReceipt    = "/var/db/portless/relay.json"
	launchdResolver   = "/etc/resolver/portless.test"
)

func currentPlatformInstallation() platformInstallation {
	return platformInstallation{
		Name: "launchd", Service: launchdLabel, HelperPath: launchdHelperPath,
		ConfigurationPath: launchdPlistPath, ReceiptPath: launchdReceipt, ResolverPath: launchdResolver,
	}
}

func installPlatform(ctx context.Context, request SetupRequest) (resultErr error) {
	receiptExists, err := pathExists(launchdReceipt)
	if err != nil {
		return fmt.Errorf("inspect relay receipt before provisioning loopback addresses: %w", err)
	}
	ownsLoopbackPool := false
	if receiptExists {
		receipt, receiptErr := readInstallationReceipt(currentPlatformInstallation())
		if receiptErr != nil {
			return fmt.Errorf("inspect relay receipt before provisioning loopback addresses: %w", receiptErr)
		}
		ownsLoopbackPool = receipt.SchemaVersion >= 3
	}
	if err := prepareRelayLoopbackPool(ctx, !ownsLoopbackPool); err != nil {
		return err
	}
	if !ownsLoopbackPool {
		defer func() {
			if resultErr != nil {
				if !receiptExists {
					_ = runCommand(context.Background(), "/bin/launchctl", "bootout", "system/"+launchdLabel)
					for _, path := range []string{launchdPlistPath, launchdHelperPath, launchdResolver} {
						_ = removeExactFile(path)
					}
				}
				_ = removeRelayLoopbackPool(context.Background())
				flushDarwinResolver(context.Background())
			}
		}()
	}
	if err := copyExecutableAtomically(request.Executable, launchdHelperPath); err != nil {
		return fmt.Errorf("install relay helper executable: %w", err)
	}
	plist, err := renderLaunchdPlist(request)
	if err != nil {
		return err
	}
	if err := writeRootFileAtomically(launchdPlistPath, plist, 0o644); err != nil {
		return fmt.Errorf("install relay launch daemon: %w", err)
	}
	if err := writeRootFileAtomically(launchdResolver, renderDarwinResolverConfiguration(), 0o644); err != nil {
		return fmt.Errorf("install scoped portless.test resolver: %w", err)
	}
	_ = runCommand(ctx, "/bin/launchctl", "bootout", "system/"+launchdLabel)
	if err := waitForRelayAddressesAvailable(ctx, 2*time.Second); err != nil {
		return err
	}
	if err := runCommand(ctx, "/bin/launchctl", "bootstrap", "system", launchdPlistPath); err != nil {
		return err
	}
	if err := runCommand(ctx, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
		return err
	}
	flushDarwinResolver(ctx)
	return writeInstallationReceipt(request)
}

func renderDarwinResolverConfiguration() []byte {
	host, port, _ := net.SplitHostPort(relay.DefaultDNSAddress)
	return []byte(fmt.Sprintf("nameserver %s\nport %s\n", host, port))
}

func restartPlatform(ctx context.Context) error {
	if err := runCommand(ctx, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
		return fmt.Errorf("restart relay launch daemon: %w", err)
	}
	return nil
}

func uninstallPlatform(ctx context.Context, removeLoopbackPool bool) error {
	loaded, err := platformServiceRunning(ctx)
	if err != nil {
		return err
	}
	if loaded {
		if err := runCommand(ctx, "/bin/launchctl", "bootout", "system/"+launchdLabel); err != nil {
			return fmt.Errorf("stop relay launch daemon: %w", err)
		}
	} else {
		_ = runCommand(ctx, "/bin/launchctl", "bootout", "system/"+launchdLabel)
	}
	if err := waitForLaunchdUnloaded(ctx, 5*time.Second, platformServiceRunning); err != nil {
		return err
	}
	if removeLoopbackPool {
		if err := removeRelayLoopbackPool(ctx); err != nil {
			return err
		}
	}
	for _, path := range []string{launchdPlistPath, launchdHelperPath, launchdReceipt, launchdResolver} {
		if err := removeExactFile(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	flushDarwinResolver(ctx)
	removeDirectoryIfEmpty(filepath.Dir(launchdReceipt))
	return nil
}

func waitForLaunchdUnloaded(ctx context.Context, timeout time.Duration, probe func(context.Context) (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		loaded, err := probe(ctx)
		if err != nil {
			return err
		}
		if !loaded {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("relay launch daemon %s is still loaded after %s", launchdLabel, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func platformServiceRunning(ctx context.Context) (bool, error) {
	command := exec.CommandContext(ctx, "/bin/launchctl", "print", "system/"+launchdLabel)
	if err := command.Run(); err == nil {
		return true, nil
	} else if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect relay launch daemon: %w", err)
	}
}

func platformConfigurationOwner(path string) (int, int, string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", "", err
	}
	defer file.Close()
	var document struct {
		Arguments []string `xml:"dict>array>string"`
	}
	if err := xml.NewDecoder(io.LimitReader(file, 256<<10)).Decode(&document); err != nil {
		return 0, 0, "", "", err
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
    <string>__relay</string>
    <string>--socket</string>
    <string>%s</string>
	<string>--dns-socket</string>
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
`, launchdLabel, escape(launchdHelperPath), escape(request.TargetSocket), escape(request.DNSTargetSocket), request.UID, request.GID)
	return []byte(content), nil
}

func flushDarwinResolver(ctx context.Context) {
	_ = runCommand(ctx, "/usr/bin/dscacheutil", "-flushcache")
	_ = runCommand(ctx, "/usr/bin/killall", "-HUP", "mDNSResponder")
}

func prepareRelayLoopbackPool(ctx context.Context, rejectExisting bool) error {
	configured, err := configuredRelayLoopbackAddresses()
	if err != nil {
		return fmt.Errorf("inspect Portless loopback address pool: %w", err)
	}
	if rejectExisting {
		for _, address := range managedRelayLoopbackAddresses() {
			if configured[address] {
				return fmt.Errorf("reserved Portless loopback address %s is already configured; remove the conflicting lo0 alias and retry", address)
			}
		}
	}
	for _, address := range managedRelayLoopbackAddresses() {
		if configured[address] {
			continue
		}
		if err := runCommand(ctx, "/sbin/ifconfig", "lo0", "alias", address, "netmask", "255.255.255.255"); err != nil {
			return fmt.Errorf("provision Portless loopback address %s: %w", address, err)
		}
	}
	ready, detail, err := relayLoopbackPoolStatus()
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("Portless loopback address pool is incomplete: %s", detail)
	}
	return nil
}

func removeRelayLoopbackPool(ctx context.Context) error {
	configured, err := configuredRelayLoopbackAddresses()
	if err != nil {
		return fmt.Errorf("inspect Portless loopback address pool before removal: %w", err)
	}
	for _, address := range managedRelayLoopbackAddresses() {
		if !configured[address] {
			continue
		}
		if err := runCommand(ctx, "/sbin/ifconfig", "lo0", "-alias", address); err != nil {
			return fmt.Errorf("remove Portless loopback address %s: %w", address, err)
		}
	}
	return nil
}

func relayLoopbackPoolStatus() (bool, string, error) {
	configured, err := configuredRelayLoopbackAddresses()
	if err != nil {
		return false, "", err
	}
	count := 0
	for _, address := range networking.EndpointLoopbackAddresses() {
		if configured[address] {
			count++
		}
	}
	dnsHost, _, _ := net.SplitHostPort(relay.DefaultDNSAddress)
	dnsReady := configured[dnsHost]
	detail := fmt.Sprintf("%d/%d endpoint addresses configured on lo0; DNS address %s", count, networking.EndpointPoolSize, map[bool]string{true: "ready", false: "missing"}[dnsReady])
	return count == networking.EndpointPoolSize && dnsReady, detail, nil
}

func configuredRelayLoopbackAddresses() (map[string]bool, error) {
	loopback, err := net.InterfaceByName("lo0")
	if err != nil {
		return nil, err
	}
	addresses, err := loopback.Addrs()
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		ip, _, parseErr := net.ParseCIDR(address.String())
		if parseErr == nil && ip.To4() != nil {
			result[ip.String()] = true
		}
	}
	return result, nil
}
