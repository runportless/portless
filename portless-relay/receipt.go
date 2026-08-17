package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/portless-run/portless/portless-daemon/networking"
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

func writeInstallationReceipt(request SetupRequest) error {
	details := currentPlatformInstallation()
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
	file, err := os.Open(details.ReceiptPath)
	if err != nil {
		return installationReceipt{}, err
	}
	defer file.Close()
	var receipt installationReceipt
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return installationReceipt{}, fmt.Errorf("read relay installation receipt: %w", err)
	}
	if receipt.SchemaVersion < 1 || receipt.SchemaVersion > installationReceiptSchema {
		return installationReceipt{}, fmt.Errorf("unsupported relay installation receipt schema %d", receipt.SchemaVersion)
	}
	if receipt.Platform != details.Name || receipt.Service != details.Service || receipt.HelperPath != details.HelperPath || receipt.ConfigurationPath != details.ConfigurationPath {
		return installationReceipt{}, errors.New("relay installation receipt does not match this platform")
	}
	if receipt.OwnerUID <= 0 || receipt.OwnerGID <= 0 || !filepath.IsAbs(receipt.TargetSocket) || filepath.Base(filepath.Clean(receipt.TargetSocket)) != "ingress.sock" {
		return installationReceipt{}, errors.New("relay installation receipt contains invalid ownership or socket information")
	}
	if receipt.SchemaVersion >= 2 && (!filepath.IsAbs(receipt.DNSTargetSocket) || filepath.Base(filepath.Clean(receipt.DNSTargetSocket)) != "dns.sock") {
		return installationReceipt{}, errors.New("relay installation receipt contains invalid DNS socket information")
	}
	if receipt.SchemaVersion >= 3 {
		expected := managedRelayLoopbackAddresses()
		if len(receipt.LoopbackAddresses) != len(expected) {
			return installationReceipt{}, errors.New("relay installation receipt contains an invalid loopback address pool")
		}
		for index := range expected {
			if receipt.LoopbackAddresses[index] != expected[index] {
				return installationReceipt{}, errors.New("relay installation receipt contains an invalid loopback address pool")
			}
		}
	}
	return receipt, nil
}

func managedRelayLoopbackAddresses() []string {
	dnsHost, _, _ := net.SplitHostPort(DefaultDNSAddress)
	return append([]string{dnsHost}, networking.EndpointLoopbackAddresses()...)
}
