package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/runportless/portless/portless-daemon/runtime/container"
	"github.com/runportless/portless/portless-daemon/runtime/container/managed"
)

type engine struct {
	binary string
}

// New returns an ownership-safe managed runtime backed by Podman.
func New(installationKey, temporaryRoot string) container.Runtime {
	return managed.New(&engine{binary: "podman"}, installationKey, temporaryRoot)
}

// Name returns Podman's canonical runtime name.
func (e *engine) Name() container.RuntimeName { return container.RuntimePodman }

// Binary returns the Podman CLI executable name.
func (e *engine) Binary() string { return e.binary }

// Probe reports Podman CLI, service, and version availability.
func (e *engine) Probe(ctx context.Context) container.ProbeResult {
	result := container.ProbeResult{Name: e.Name()}
	path, err := exec.LookPath(e.binary)
	if err != nil {
		result.State = "missing"
		result.Reason = "Podman is not installed or is not on PATH"
		return result
	}
	if output, err := exec.CommandContext(ctx, path, "info", "--format", "json").CombinedOutput(); err != nil {
		result.State = "failed"
		result.Reason = "Podman is not ready: " + commandFailure(output, err)
		return result
	}
	output, err := exec.CommandContext(ctx, path, "version", "--format", "json").CombinedOutput()
	if err != nil {
		result.State = "failed"
		result.Reason = "Podman version could not be read: " + commandFailure(output, err)
		return result
	}
	var version struct {
		Client struct {
			Version string `json:"Version"`
		} `json:"Client"`
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(output, &version); err != nil {
		result.State = "failed"
		result.Reason = "Podman returned an invalid version response"
		return result
	}
	result.State = "ready"
	result.Version = version.Client.Version
	if result.Version == "" {
		result.Version = version.Version
	}
	return result
}

// StartHost attempts to start the configured Podman machine when needed.
func (e *engine) StartHost(ctx context.Context) container.ProbeResult {
	result := e.Probe(ctx)
	if result.State == "ready" || result.State == "missing" {
		return result
	}
	path, err := exec.LookPath(e.binary)
	if err != nil {
		return result
	}
	if output, err := exec.CommandContext(ctx, path, "machine", "start").CombinedOutput(); err != nil {
		result.Reason = "Podman machine could not be started: " + commandFailure(output, err)
		return result
	}
	return e.Probe(ctx)
}

// ResourceExists reports whether Podman knows the named resource.
func (e *engine) ResourceExists(ctx context.Context, kind, name string) bool {
	return exec.CommandContext(ctx, e.binary, kind, "exists", name).Run() == nil
}

// VolumeMount formats a relabeled Podman named-volume mount.
func (e *engine) VolumeMount(volume, path string) string { return volume + ":" + path + ":Z" }

func commandFailure(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message != "" {
		return message
	}
	return fmt.Sprint(err)
}
