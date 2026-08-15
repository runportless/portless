package nats

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/resource"
	"github.com/portless-run/portless/internal/resource/builtin/common"
)

type Plugin struct{}

func New() resource.Plugin { return Plugin{} }

func (Plugin) Descriptor() resource.Descriptor {
	return resource.Descriptor{ID: "nats", DefaultVersion: "2"}
}

func (Plugin) Detect(ctx context.Context, workspace resource.Workspace, consumers []resource.Consumer) (resource.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "nats", Explanation: "NATS configuration or dependency found",
		Markers:            []string{"nats://", "github.com/nats-io/nats.go", "io.nats", `"nats"`, `'nats'`, "nats-py"},
		DefaultEnvironment: func(resource.Consumer) string { return "NATS_URL" },
		ExplicitEnvironment: func(content string, consumer resource.Consumer) string {
			return common.FirstEnvironment(content, "NATS_URL", "NATS_ADDRESS")
		},
	})
}

func (Plugin) Plan(definition model.ResourceDefinition) (resource.ContainerPlan, error) {
	return resource.ContainerPlan{
		Image: "docker.io/library/nats:" + definition.Version, ClientPort: 4222,
		Readiness: resource.Readiness{Kind: "tcp", Timeout: time.Minute, Interval: 500 * time.Millisecond},
	}, nil
}

func (Plugin) Bind(context resource.BindingContext) (resource.BindingResult, error) {
	if !context.Active {
		return resource.BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	value := "nats://" + net.JoinHostPort(context.Host, strconv.Itoa(context.Port))
	return resource.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: value}}, nil
}
