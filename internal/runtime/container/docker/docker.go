package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/runtime/container/managed"
)

type engine struct {
	binary string
}

func New(installationKey, temporaryRoot string) container.Runtime {
	return managed.New(&engine{binary: "docker"}, installationKey, temporaryRoot)
}

func (e *engine) Name() container.RuntimeName { return container.RuntimeDocker }
func (e *engine) Binary() string              { return e.binary }

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

func (e *engine) StartHost(ctx context.Context) container.ProbeResult {
	result := e.Probe(ctx)
	if result.State == "failed" {
		result.Reason = "Docker Engine cannot be started by Portless; start Docker and retry: " + result.Reason
	}
	return result
}

func (e *engine) ResourceExists(ctx context.Context, kind, name string) bool {
	return exec.CommandContext(ctx, e.binary, kind, "inspect", name).Run() == nil
}

func (e *engine) VolumeMount(volume, path string) string { return volume + ":" + path }

func commandFailure(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message != "" {
		return message
	}
	return fmt.Sprint(err)
}
