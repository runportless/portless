package valkey

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
	return resource.Descriptor{ID: "valkey", Aliases: []string{"redis"}, DefaultVersion: "8"}
}

func (Plugin) Detect(ctx context.Context, workspace resource.Workspace, consumers []resource.Consumer) (resource.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "redis", Explanation: "Redis-compatible configuration or dependency found; Valkey proposed",
		Markers: []string{"redis://", "rediss://", "valkey://", `"redis"`, `'redis'`, `"ioredis"`, `'ioredis'`, "spring-boot-starter-data-redis", "github.com/redis/go-redis", "github.com/valkey-io", "redis[hiredis]"},
		DefaultEnvironment: func(consumer resource.Consumer) string {
			return common.FrameworkEnvironment(consumer, "SPRING_DATA_REDIS_URL", "REDIS_URL")
		},
		ExplicitEnvironment: func(content string, consumer resource.Consumer) string {
			return common.FirstEnvironment(content, "SPRING_DATA_REDIS_URL", "REDIS_URL", "VALKEY_URL")
		},
	})
}

func (Plugin) Plan(definition model.ResourceDefinition) (resource.ContainerPlan, error) {
	return resource.ContainerPlan{
		Image: "docker.io/valkey/valkey:" + definition.Version, ClientPort: 6379,
		Volumes:   []resource.Volume{{Key: "data", Path: "/data"}},
		Readiness: resource.Readiness{Kind: "exec", Command: []string{"valkey-cli", "ping"}, Timeout: time.Minute, Interval: time.Second},
	}, nil
}

func (Plugin) Bind(context resource.BindingContext) (resource.BindingResult, error) {
	if !context.Active {
		return resource.BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	value := "redis://" + net.JoinHostPort(context.Host, strconv.Itoa(context.Port))
	return resource.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: value}}, nil
}
