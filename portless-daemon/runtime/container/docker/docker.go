package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/portless-run/portless/portless-daemon/runtime/container"
	"github.com/portless-run/portless/portless-daemon/runtime/container/managed"
)

type engine struct {
	binary string
}

// New returns an ownership-safe managed runtime backed by Docker.
func New(installationKey, temporaryRoot string) container.Runtime {
	return managed.New(&engine{binary: "docker"}, installationKey, temporaryRoot)
}

// Name returns Docker's canonical runtime name.
func (e *engine) Name() container.RuntimeName { return container.RuntimeDocker }

// Binary returns the Docker CLI executable name.
func (e *engine) Binary() string { return e.binary }

// Probe reports Docker CLI and Engine availability.
func (e *engine) Probe(ctx context.Context) container.ProbeResult {
	result := container.ProbeResult{Name: e.Name()}
	path, err := exec.LookPath(e.binary)
	if err != nil {
		result.State = "missing"
		result.Reason = "Docker CLI is not installed or is not on PATH"
		return result
	}
	output, err := exec.CommandContext(ctx, path, "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err != nil {
		result.State = "failed"
		result.Reason = "Docker Engine is not ready: " + commandFailure(output, err)
		return result
	}
	result.State = "ready"
	result.Version = strings.TrimSpace(string(output))
	return result
}

// StartHost explains that Docker must be started outside Portless when unavailable.
func (e *engine) StartHost(ctx context.Context) container.ProbeResult {
	result := e.Probe(ctx)
	if result.State == "failed" {
		result.Reason = "Docker Engine cannot be started by Portless; start Docker and retry: " + result.Reason
	}
	return result
}

// ResourceExists reports whether Docker can inspect the named resource.
func (e *engine) ResourceExists(ctx context.Context, kind, name string) bool {
	return exec.CommandContext(ctx, e.binary, kind, "inspect", name).Run() == nil
}

// VolumeMount formats a Docker named-volume mount.
func (e *engine) VolumeMount(volume, path string) string { return volume + ":" + path }

func commandFailure(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message != "" {
		return message
	}
	return fmt.Sprint(err)
}
