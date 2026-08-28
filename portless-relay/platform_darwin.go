//go:build darwin

package relay

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/networking"
)

const (
	launchdLabel             = "dev.portless.relay"
	launchdHelperPath        = "/Library/PrivilegedHelperTools/dev.portless.relay"
	launchdPlistPath         = "/Library/LaunchDaemons/dev.portless.relay.plist"
	launchdReceipt           = "/var/db/portless/relay.json"
	launchdResolver          = "/etc/resolver/portless.test"
	launchdLocalhostResolver = "/etc/resolver/portless.localhost"
)

type darwinPlatform struct {
	commands commandRunner
}

func newHostPlatform() hostPlatform { return darwinPlatform{commands: execCommandRunner{}} }

func (darwinPlatform) installation() platformInstallation {
	return platformInstallation{
		Name: "launchd", Service: launchdLabel, HelperPath: launchdHelperPath,
		ConfigurationPath: launchdPlistPath, ReceiptPath: launchdReceipt, ResolverPath: launchdResolver,
		LocalhostResolverPath: launchdLocalhostResolver,
	}
}

func (platform darwinPlatform) install(ctx context.Context, request SetupRequest) (resultErr error) {
	previousState, err := platform.serviceState(ctx)
	if err != nil {
		return err
	}
	receiptExists, err := pathExists(launchdReceipt)
	if err != nil {
		return fmt.Errorf("inspect relay receipt before provisioning loopback addresses: %w", err)
	}
	var ownedLoopbackAddresses []string
	if receiptExists {
		receipt, receiptErr := readInstallationReceipt(platform.installation())
		if receiptErr != nil {
			return fmt.Errorf("inspect relay receipt before provisioning loopback addresses: %w", receiptErr)
		}
		ownedLoopbackAddresses = receipt.LoopbackAddresses
	}
	transaction, err := beginArtifactTransaction(launchdHelperPath, launchdPlistPath, launchdResolver, launchdLocalhostResolver, launchdReceipt)
	if err != nil {
		return err
	}
	committed := false
	serviceTouched := false
	artifactsChanged := false
	addedAddresses := []string{}
	removedAddresses := []string{}
	defer func() {
		if resultErr == nil || committed {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, platform.rollbackInstall(rollbackContext, transaction, previousState, serviceTouched, artifactsChanged, addedAddresses, removedAddresses))
	}()
	if previousState == relayServiceRunning {
		serviceTouched = true
		if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "bootout", "system/"+launchdLabel); err != nil {
			return fmt.Errorf("stop existing relay launch daemon: %w", err)
		}
		if err := waitForLaunchdUnloaded(ctx, 5*time.Second, platform.serviceState); err != nil {
			return err
		}
	}
	addedAddresses, err = prepareRelayLoopbackPool(ctx, platform.commands, ownedLoopbackAddresses)
	if err != nil {
		return err
	}
	obsoleteAddresses := loopbackAddressDifference(ownedLoopbackAddresses, managedRelayLoopbackAddresses())
	removedAddresses, err = removeRelayLoopbackPool(ctx, platform.commands, obsoleteAddresses)
	if err != nil {
		return err
	}
	if err := copyExecutableAtomically(request.Executable, launchdHelperPath); err != nil {
		return fmt.Errorf("install relay helper executable: %w", err)
	}
	artifactsChanged = true
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
	if err := writeRootFileAtomically(launchdLocalhostResolver, renderDarwinLocalhostResolverConfiguration(), 0o644); err != nil {
		return fmt.Errorf("install scoped localhost resolver: %w", err)
	}
	if err := writeInstallationReceipt(platform.installation(), request); err != nil {
		return err
	}
	if err := waitForRelayAddressesAvailable(ctx, 2*time.Second); err != nil {
		return err
	}
	serviceTouched = true
	if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "bootstrap", "system", launchdPlistPath); err != nil {
		return err
	}
	if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
		return err
	}
	flushDarwinResolver(ctx, platform.commands)
	committed = true
	return transaction.commit()
}

func (platform darwinPlatform) rollbackInstall(ctx context.Context, transaction *artifactTransaction, previousState relayServiceState, serviceTouched, artifactsChanged bool, addedAddresses, removedAddresses []string) error {
	var rollbackErr error
	if serviceTouched {
		state, err := platform.serviceState(ctx)
		if err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		} else if state == relayServiceRunning {
			if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "bootout", "system/"+launchdLabel); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("stop failed relay launch daemon during rollback: %w", err))
			}
		}
	}
	rollbackErr = errors.Join(rollbackErr, transaction.rollback())
	if len(addedAddresses) > 0 {
		if _, err := removeRelayLoopbackPool(ctx, platform.commands, addedAddresses); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if err := addRelayLoopbackAddresses(ctx, platform.commands, removedAddresses); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore Portless loopback addresses: %w", err))
	}
	if serviceTouched && previousState == relayServiceRunning {
		if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "bootstrap", "system", launchdPlistPath); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore relay launch daemon: %w", err))
		} else if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restart restored relay launch daemon: %w", err))
		}
	}
	if artifactsChanged || len(addedAddresses) > 0 || len(removedAddresses) > 0 {
		flushDarwinResolver(ctx, platform.commands)
	}
	return rollbackErr
}

func renderDarwinResolverConfiguration() []byte {
	host, port, _ := net.SplitHostPort(DefaultDNSAddress)
	return []byte(fmt.Sprintf("nameserver %s\nport %s\n", host, port))
}

func renderDarwinLocalhostResolverConfiguration() []byte {
	return append([]byte("domain localhost\n"), renderDarwinResolverConfiguration()...)
}

func (platform darwinPlatform) restart(ctx context.Context) error {
	if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "kickstart", "-k", "system/"+launchdLabel); err != nil {
		return fmt.Errorf("restart relay launch daemon: %w", err)
	}
	return nil
}

func (platform darwinPlatform) uninstall(ctx context.Context, spec uninstallSpec) error {
	state, err := platform.serviceState(ctx)
	if err != nil {
		return err
	}
	if state == relayServiceRunning {
		if err := runHostCommand(ctx, platform.commands, "/bin/launchctl", "bootout", "system/"+launchdLabel); err != nil {
			return fmt.Errorf("stop relay launch daemon: %w", err)
		}
	}
	if err := waitForLaunchdUnloaded(ctx, 5*time.Second, platform.serviceState); err != nil {
		return err
	}
	if len(spec.loopbackAddresses) > 0 {
		if _, err := removeRelayLoopbackPool(ctx, platform.commands, spec.loopbackAddresses); err != nil {
			return err
		}
	}
	for _, path := range []string{launchdPlistPath, launchdHelperPath, launchdReceipt, launchdResolver, launchdLocalhostResolver} {
		if err := removeExactFile(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	flushDarwinResolver(ctx, platform.commands)
	removeDirectoryIfEmpty(filepath.Dir(launchdReceipt))
	return nil
}

func waitForLaunchdUnloaded(ctx context.Context, timeout time.Duration, probe func(context.Context) (relayServiceState, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		state, err := probe(ctx)
		if err != nil {
			return err
		}
		if state == relayServiceStopped {
			return nil
		}
		if state == relayServiceUnknown {
			return errors.New("relay launch daemon state is unknown")
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

func (platform darwinPlatform) serviceState(ctx context.Context) (relayServiceState, error) {
	output, err := platform.commands.combinedOutput(ctx, "/bin/launchctl", "print", "system/"+launchdLabel)
	if err == nil {
		return relayServiceRunning, nil
	}
	detail := strings.TrimSpace(string(output))
	if _, ok := commandExitCode(err); ok && launchdServiceMissing(detail) {
		return relayServiceStopped, nil
	}
	if detail != "" {
		return relayServiceUnknown, fmt.Errorf("inspect relay launch daemon: %w: %s", err, detail)
	}
	return relayServiceUnknown, fmt.Errorf("inspect relay launch daemon: %w", err)
}

func launchdServiceMissing(output string) bool {
	normalized := strings.ToLower(output)
	return strings.Contains(normalized, "could not find service") || strings.Contains(normalized, "service not found")
}

func renderLaunchdPlist(request SetupRequest) ([]byte, error) {
	// encoding/xml cannot directly express alternating plist key/value elements,
	// so render the small fixed document with escaped dynamic values.
	escape := func(value string) (string, error) {
		var encoded strings.Builder
		if err := xml.EscapeText(&encoded, []byte(value)); err != nil {
			return "", err
		}
		return encoded.String(), nil
	}
	helper, err := escape(launchdHelperPath)
	if err != nil {
		return nil, fmt.Errorf("encode relay helper path: %w", err)
	}
	targetSocket, err := escape(request.TargetSocket)
	if err != nil {
		return nil, fmt.Errorf("encode relay target socket: %w", err)
	}
	dnsTargetSocket, err := escape(request.DNSTargetSocket)
	if err != nil {
		return nil, fmt.Errorf("encode relay DNS target socket: %w", err)
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
`, launchdLabel, helper, targetSocket, dnsTargetSocket, request.UID, request.GID)
	return []byte(content), nil
}

func flushDarwinResolver(ctx context.Context, runner commandRunner) {
	_ = runHostCommand(ctx, runner, "/usr/bin/dscacheutil", "-flushcache")
	_ = runHostCommand(ctx, runner, "/usr/bin/killall", "-HUP", "mDNSResponder")
}

func (platform darwinPlatform) prepareRuntime(ctx context.Context) error {
	receipt, err := readInstallationReceipt(platform.installation())
	if err != nil {
		return fmt.Errorf("verify relay installation before provisioning loopback addresses: %w", err)
	}
	if !receiptUsesCurrentLoopbackPool(receipt) {
		return errors.New("relay loopback address manifest is stale; run `portless relay install` to repair it")
	}
	added, err := prepareRelayLoopbackPool(ctx, platform.commands, receipt.LoopbackAddresses)
	if err == nil {
		return nil
	}
	rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, rollbackErr := removeRelayLoopbackPool(rollbackContext, platform.commands, added)
	return errors.Join(err, rollbackErr)
}

func prepareRelayLoopbackPool(ctx context.Context, runner commandRunner, ownedAddresses []string) ([]string, error) {
	configured, err := configuredRelayLoopbackAddresses()
	if err != nil {
		return nil, fmt.Errorf("inspect Portless loopback address pool: %w", err)
	}
	if conflict := firstUnownedLoopbackAddress(configured, managedRelayLoopbackAddresses(), ownedAddresses); conflict != "" {
		return nil, fmt.Errorf("reserved Portless loopback address %s is already configured without a matching ownership receipt; remove the conflicting lo0 alias and retry", conflict)
	}
	added := make([]string, 0, len(managedRelayLoopbackAddresses()))
	for _, address := range managedRelayLoopbackAddresses() {
		if configured[address] {
			continue
		}
		if err := runHostCommand(ctx, runner, "/sbin/ifconfig", "lo0", "alias", address, "netmask", "255.255.255.255"); err != nil {
			return added, fmt.Errorf("provision Portless loopback address %s: %w", address, err)
		}
		added = append(added, address)
	}
	ready, detail, err := (darwinPlatform{}).loopbackPoolStatus()
	if err != nil {
		return added, err
	}
	if !ready {
		return added, fmt.Errorf("Portless loopback address pool is incomplete: %s", detail)
	}
	return added, nil
}

func firstUnownedLoopbackAddress(configured map[string]bool, desiredAddresses, ownedAddresses []string) string {
	owned := make(map[string]struct{}, len(ownedAddresses))
	for _, address := range ownedAddresses {
		owned[address] = struct{}{}
	}
	for _, address := range desiredAddresses {
		if configured[address] {
			if _, ok := owned[address]; !ok {
				return address
			}
		}
	}
	return ""
}

func loopbackAddressDifference(addresses, excluded []string) []string {
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, address := range excluded {
		excludedSet[address] = struct{}{}
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if _, ok := excludedSet[address]; !ok {
			result = append(result, address)
		}
	}
	return result
}

func addRelayLoopbackAddresses(ctx context.Context, runner commandRunner, addresses []string) error {
	configured, err := configuredRelayLoopbackAddresses()
	if err != nil {
		return fmt.Errorf("inspect Portless loopback address pool before provisioning: %w", err)
	}
	for _, address := range addresses {
		if configured[address] {
			continue
		}
		if err := runHostCommand(ctx, runner, "/sbin/ifconfig", "lo0", "alias", address, "netmask", "255.255.255.255"); err != nil {
			return fmt.Errorf("provision Portless loopback address %s: %w", address, err)
		}
		configured[address] = true
	}
	return nil
}

func removeRelayLoopbackPool(ctx context.Context, runner commandRunner, addresses []string) ([]string, error) {
	configured, err := configuredRelayLoopbackAddresses()
	if err != nil {
		return nil, fmt.Errorf("inspect Portless loopback address pool before removal: %w", err)
	}
	removed := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if !configured[address] {
			continue
		}
		if err := runHostCommand(ctx, runner, "/sbin/ifconfig", "lo0", "-alias", address); err != nil {
			return removed, fmt.Errorf("remove Portless loopback address %s: %w", address, err)
		}
		removed = append(removed, address)
	}
	return removed, nil
}

func (darwinPlatform) loopbackPoolStatus() (bool, string, error) {
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
	dnsHost, _, _ := net.SplitHostPort(DefaultDNSAddress)
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
