package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/runportless/portless/portless-daemon/networking"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

type installationReceipt struct {
	SchemaVersion     int       `json:"schemaVersion"`
	Platform          string    `json:"platform"`
	Service           string    `json:"service"`
	OwnerUID          int       `json:"ownerUid"`
	OwnerGID          int       `json:"ownerGid"`
	TargetSocket      string    `json:"targetSocket"`
	DNSTargetSocket   string    `json:"dnsTargetSocket,omitempty"`
	LoopbackAddresses []string  `json:"loopbackAddresses,omitempty"`
	HelperPath        string    `json:"helperPath"`
	ConfigurationPath string    `json:"configurationPath"`
	InstalledAt       time.Time `json:"installedAt"`
}

const installationReceiptSchema = 3

func validateOwnership(status InstallationStatus, requestingUID int) error {
	if requestingUID <= 0 {
		return errors.New("the relay operation requires a non-root requesting user ID")
	}
	if status.OwnerUID <= 0 {
		return errors.New("the clean-URL relay owner could not be determined; inspect `portless relay status`")
	}
	if status.OwnerUID != requestingUID {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d", status.OwnerUID)
	}
	return nil
}

// ValidateOwnership verifies that status describes a relay owned by
// requestingUID.
func ValidateOwnership(status InstallationStatus, requestingUID int) error {
	return validateOwnership(status, requestingUID)
}

func validateUninstallOwnership(status InstallationStatus, requestingUID int, force bool) error {
	if force {
		return nil
	}
	if err := validateOwnership(status, requestingUID); err != nil {
		return fmt.Errorf("%w; repeat with --force to remove the installation", err)
	}
	return nil
}

// ValidateUninstallOwnership verifies removal ownership, allowing force to
// explicitly override an owner mismatch.
func ValidateUninstallOwnership(status InstallationStatus, requestingUID int, force bool) error {
	return validateUninstallOwnership(status, requestingUID, force)
}

func writeInstallationReceipt(details platformInstallation, request SetupRequest) error {
	receipt := installationReceipt{
		SchemaVersion: installationReceiptSchema, Platform: details.Name, Service: details.Service,
		OwnerUID: request.UID, OwnerGID: request.GID, TargetSocket: request.TargetSocket, DNSTargetSocket: request.DNSTargetSocket,
		LoopbackAddresses: managedRelayLoopbackAddresses(),
		HelperPath:        details.HelperPath, ConfigurationPath: details.ConfigurationPath, InstalledAt: time.Now().UTC(),
	}
	content, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := writeRootFileAtomically(details.ReceiptPath, content, 0o644); err != nil {
		return fmt.Errorf("write relay installation receipt: %w", err)
	}
	return nil
}

func readInstallationReceipt(details platformInstallation) (installationReceipt, error) {
	present, artifactErr := inspectArtifact(details.ReceiptPath, 0o644, details.ArtifactUID, details.ArtifactGID)
	if artifactErr != nil {
		return installationReceipt{}, artifactErr
	}
	if !present {
		return installationReceipt{}, os.ErrNotExist
	}
	file, err := os.Open(details.ReceiptPath)
	if err != nil {
		return installationReceipt{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return installationReceipt{}, fmt.Errorf("inspect relay installation receipt size: %w", err)
	}
	if info.Size() > 64<<10 {
		return installationReceipt{}, errors.New("relay installation receipt is unexpectedly larger than 64 KiB")
	}
	var receipt installationReceipt
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return installationReceipt{}, fmt.Errorf("read relay installation receipt: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return installationReceipt{}, errors.New("relay installation receipt contains trailing data")
	}
	if receipt.SchemaVersion != installationReceiptSchema {
		return installationReceipt{}, fmt.Errorf("unsupported relay installation receipt schema %d", receipt.SchemaVersion)
	}
	if receipt.Platform != details.Name || receipt.Service != details.Service || receipt.HelperPath != details.HelperPath || receipt.ConfigurationPath != details.ConfigurationPath {
		return installationReceipt{}, errors.New("relay installation receipt does not match this platform")
	}
	runtimeErr := relayruntime.ValidateIdentity(relayruntime.Identity{
		TargetSocket: receipt.TargetSocket, DNSTargetSocket: receipt.DNSTargetSocket,
		UID: receipt.OwnerUID, GID: receipt.OwnerGID,
	})
	if receipt.InstalledAt.IsZero() || runtimeErr != nil {
		return installationReceipt{}, errors.New("relay installation receipt contains invalid ownership or socket information")
	}
	if err := validateLoopbackManifest(receipt.LoopbackAddresses); err != nil {
		return installationReceipt{}, err
	}
	return receipt, nil
}

func validateRuntimeReceipt(receipt installationReceipt, config relayruntime.Identity) error {
	if !receiptUsesCurrentLoopbackPool(receipt) {
		return errors.New("relay ownership receipt uses a stale loopback address manifest; run `portless relay install` to repair it")
	}
	if receipt.OwnerUID != config.UID || receipt.OwnerGID != config.GID || receipt.TargetSocket != config.TargetSocket || receipt.DNSTargetSocket != config.DNSTargetSocket {
		return errors.New("relay runtime identity does not match its ownership receipt; run `portless relay install` to repair the service configuration")
	}
	return nil
}

func validateLoopbackManifest(addresses []string) error {
	if len(addresses) == 0 || len(addresses) > 254 {
		return errors.New("relay installation receipt contains an invalid loopback address pool")
	}
	seen := make(map[netip.Addr]struct{}, len(addresses))
	dnsAddress := netip.MustParseAddr("127.77.0.1")
	hasDNSAddress := false
	for _, value := range addresses {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is4() {
			return errors.New("relay installation receipt contains an invalid loopback address pool")
		}
		bytes := address.As4()
		if bytes[0] != 127 || bytes[1] != 77 || bytes[2] != 0 || bytes[3] == 0 || bytes[3] == 255 {
			return errors.New("relay installation receipt contains an invalid loopback address pool")
		}
		if _, duplicate := seen[address]; duplicate {
			return errors.New("relay installation receipt contains an invalid loopback address pool")
		}
		seen[address] = struct{}{}
		hasDNSAddress = hasDNSAddress || address == dnsAddress
	}
	if !hasDNSAddress {
		return errors.New("relay installation receipt contains an invalid loopback address pool")
	}
	return nil
}

func receiptUsesCurrentLoopbackPool(receipt installationReceipt) bool {
	expected := managedRelayLoopbackAddresses()
	if len(receipt.LoopbackAddresses) != len(expected) {
		return false
	}
	actual := make(map[string]struct{}, len(receipt.LoopbackAddresses))
	for _, address := range receipt.LoopbackAddresses {
		actual[address] = struct{}{}
	}
	for _, address := range expected {
		if _, ok := actual[address]; !ok {
			return false
		}
	}
	return true
}

func managedRelayLoopbackAddresses() []string {
	dnsHost, _, _ := net.SplitHostPort(relayruntime.DefaultDNSAddress)
	return append([]string{dnsHost}, networking.EndpointLoopbackAddresses()...)
}
