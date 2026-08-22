package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/lifecycle"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

// Inspection compares an authenticated running daemon with its discovery
// record, current installation, protocol, API, and executable build.
type Inspection struct {
	Record          identity.Record
	Identity        lifecycle.Identity
	Compatible      bool
	CurrentBuild    bool
	ExpectedBuildID string
	Problems        []string
}

// ErrLegacyDaemon indicates that a daemon record predates authenticated
// lifecycle identity metadata.
var ErrLegacyDaemon = errors.New("daemon predates the authenticated lifecycle protocol")

// inspectDaemon authenticates the daemon and verifies that its response matches
// the private discovery record and this Portless installation. Compatibility is
// reported separately so a verified older build can be stopped safely.
func (m *Manager) inspectDaemon(ctx context.Context) (Inspection, error) {
	paths := m.layout
	record, err := identity.Read(paths)
	if err != nil {
		return Inspection{}, err
	}
	if record.TokenPath != paths.AuthToken {
		return Inspection{}, fmt.Errorf("daemon token path %s does not match this installation", record.TokenPath)
	}
	if record.ProtocolVersion == "" || record.InstallationID == "" || record.InstanceID == "" || record.BuildID == "" || record.StartedAt.IsZero() {
		return Inspection{}, fmt.Errorf("%w: identity metadata is missing", ErrLegacyDaemon)
	}
	token, err := installation.ReadPrivateTextFile(paths.AuthToken)
	if err != nil {
		return Inspection{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	expectedInstallationID, err := installation.InstallationID(paths)
	if err != nil {
		return Inspection{}, err
	}
	identity, err := m.fetchDaemonIdentity(ctx, record.Port, token)
	if err != nil {
		return Inspection{}, err
	}
	if identity.Product != lifecycle.Product {
		return Inspection{}, fmt.Errorf("unexpected daemon product %q", identity.Product)
	}
	if identity.PID != record.PID || identity.ProtocolVersion != record.ProtocolVersion || identity.APIVersion != record.APIVersion ||
		identity.InstallationID != record.InstallationID || identity.InstanceID != record.InstanceID || identity.BuildID != record.BuildID ||
		!identity.StartedAt.Equal(record.StartedAt) {
		return Inspection{}, errors.New("authenticated daemon identity does not match the discovery record")
	}
	if identity.InstallationID != expectedInstallationID {
		return Inspection{}, errors.New("authenticated daemon belongs to a different Portless installation")
	}
	expectedBuildID, err := installation.CurrentBuildID()
	if err != nil {
		return Inspection{}, err
	}
	inspection := Inspection{
		Record: record, Identity: identity, Compatible: true,
		CurrentBuild: identity.BuildID == expectedBuildID, ExpectedBuildID: expectedBuildID,
	}
	if identity.ProtocolVersion != lifecycle.ProtocolVersion {
		inspection.Compatible = false
		inspection.Problems = append(inspection.Problems, fmt.Sprintf("daemon protocol %s, CLI protocol %s", identity.ProtocolVersion, lifecycle.ProtocolVersion))
	}
	if identity.APIVersion != contract.APIVersion {
		inspection.Compatible = false
		inspection.Problems = append(inspection.Problems, fmt.Sprintf("daemon API %s, CLI API %s", identity.APIVersion, contract.APIVersion))
	}
	if identity.BuildID != expectedBuildID {
		inspection.Problems = append(inspection.Problems, "daemon executable differs from the current CLI executable")
	}
	return inspection, nil
}

// checkDaemon verifies an existing compatible daemon without starting or
// modifying it.
func (m *Manager) checkDaemon(ctx context.Context) (identity.Record, error) {
	inspection, err := m.inspectDaemon(ctx)
	if err != nil {
		return identity.Record{}, err
	}
	if !inspection.Compatible || !inspection.CurrentBuild {
		return identity.Record{}, incompatibleDaemonError(inspection)
	}
	record := inspection.Record
	// The discovery record is an atomic startup snapshot. Runtime recovery and
	// handoff safety are live properties, so diagnostics must use the freshly
	// authenticated identity response rather than stale JSON on disk.
	record.State = inspection.Identity.State
	record.HandoffReady = inspection.Identity.HandoffReady
	record.RecoveryProblems = append([]string(nil), inspection.Identity.RecoveryProblems...)
	return record, nil
}

func (m *Manager) fetchDaemonIdentity(ctx context.Context, port int, token string) (lifecycle.Identity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, lifecycle.IdentityPath), nil)
	if err != nil {
		return lifecycle.Identity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	// Identity includes a live handoff-safety check. Container ownership probes
	// can require a few local engine round trips, so this timeout must cover more
	// than a simple health endpoint while remaining tightly bounded.
	client := m.hooks.HTTPClient(15 * time.Second)
	response, err := client.Do(request)
	if err != nil {
		return lifecycle.Identity{}, fmt.Errorf("connect to recorded daemon identity endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return lifecycle.Identity{}, fmt.Errorf("recorded daemon identity endpoint returned %s", response.Status)
	}
	var identity lifecycle.Identity
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&identity); err != nil {
		return lifecycle.Identity{}, fmt.Errorf("decode recorded daemon identity: %w", err)
	}
	return identity, nil
}

func incompatibleDaemonError(inspection Inspection) error {
	if inspection.Compatible && !inspection.CurrentBuild {
		return errors.New("Portless daemon is compatible but runs a different executable build")
	}
	return fmt.Errorf("Portless daemon is not compatible with this CLI: %s", strings.Join(inspection.Problems, "; "))
}

func unverifiedDaemonError(record identity.Record, cause error) error {
	return fmt.Errorf("cannot authenticate the recorded Portless daemon at PID %d: %v; refusing to replace or signal it (run `portless daemon restart --force` only after confirming active environments may be interrupted)", record.PID, cause)
}
