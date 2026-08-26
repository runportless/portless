package nats

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/providers"
	"github.com/runportless/portless/portless-daemon/providers/builtin/common"
)

// Plugin provides NATS discovery, container planning, and connection binding.
type Plugin struct{}

// New returns the built-in NATS resource plugin.
func New() providers.Plugin { return Plugin{} }

// Descriptor returns NATS plugin registration metadata.
func (Plugin) Descriptor() providers.Descriptor {
	return providers.Descriptor{ID: "nats", DefaultVersion: "2", ApplicationProtocol: model.ApplicationProtocolNATS}
}

// Detect finds NATS dependencies and their consumer environment variables.
func (Plugin) Detect(ctx context.Context, workspace providers.Workspace, consumers []providers.Consumer) (providers.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "nats", Explanation: "NATS configuration or dependency found",
		Markers:            []string{"nats://", "github.com/nats-io/nats.go", "io.nats", `"nats"`, `'nats'`, "nats-py"},
		DefaultEnvironment: func(providers.Consumer) string { return "NATS_URL" },
		ExplicitEnvironment: func(content string, consumer providers.Consumer) string {
			return common.FirstEnvironment(content, "NATS_URL", "NATS_ADDRESS")
		},
		ResourceName: func(content string) string {
			return common.LogicalServiceHost(content, "nats")
		},
	})
}

// Plan returns the managed NATS container and readiness recipe.
func (Plugin) Plan(definition model.ResourceDefinition) (providers.ContainerPlan, error) {
	return providers.ContainerPlan{
		Image: "docker.io/library/nats:" + definition.Version, ClientPort: 4222,
		Readiness: providers.Readiness{Kind: "tcp", Timeout: time.Minute, Interval: 500 * time.Millisecond},
	}, nil
}

// Bind creates the active NATS connection URL for a consumer.
func (Plugin) Bind(context providers.BindingContext) (providers.BindingResult, error) {
	if !context.Active {
		return providers.BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	value := "nats://" + net.JoinHostPort(context.Host, strconv.Itoa(context.Port))
	return providers.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: value}}, nil
}
