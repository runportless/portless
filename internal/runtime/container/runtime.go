package container

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/resource"
)

type RuntimeName string

const (
	RuntimeAuto   RuntimeName = "auto"
	RuntimeDocker RuntimeName = "docker"
	RuntimePodman RuntimeName = "podman"
)

func ParseRuntimeName(value string) (RuntimeName, error) {
	name := RuntimeName(strings.ToLower(strings.TrimSpace(value)))
	switch name {
	case RuntimeAuto, RuntimeDocker, RuntimePodman:
		return name, nil
	default:
		return "", fmt.Errorf("container runtime must be auto, docker, or podman")
	}
}

type ProbeResult struct {
	Name    RuntimeName `json:"name"`
	State   string      `json:"state"`
	Version string      `json:"version,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

type Status struct {
	Preference RuntimeName   `json:"preference"`
	Selected   RuntimeName   `json:"selected,omitempty"`
	State      string        `json:"state"`
	Version    string        `json:"version,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	Candidates []ProbeResult `json:"candidates"`
}

type StartResult struct {
	ContainerName string
	Port          int
	Environment   map[string]string
	StartedAt     time.Time
	LogDirectory  string
}

type ResetResult struct {
	Runtime    RuntimeName `json:"runtime"`
	Containers int         `json:"containers"`
	Volumes    int         `json:"volumes"`
	Networks   int         `json:"networks"`
}

type Runtime interface {
	Name() RuntimeName
	Probe(context.Context) ProbeResult
	StartHost(context.Context) ProbeResult
	Start(context.Context, string, string, model.ServiceDefinition, resource.ContainerPlan, int64, string) (StartResult, error)
	StopEnvironment(context.Context, string, bool) error
	StopService(context.Context, string, string) error
	ResetInstallation(context.Context) (ResetResult, error)
}

type Adopter interface {
	Adopt(context.Context, string, string, model.ServiceDefinition, resource.ContainerPlan, int64, string) (StartResult, error)
}

type Verifier interface {
	Verify(context.Context, string, model.ServiceDefinition, resource.ContainerPlan, int64, string) error
}

type Closer interface {
	Close()
}

var ErrRuntimeUnavailable = errors.New("container runtime is unavailable")
