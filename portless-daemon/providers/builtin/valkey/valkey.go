package valkey

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/providers"
	"github.com/portless-run/portless/portless-daemon/providers/builtin/common"
)

type Plugin struct{}

func New() providers.Plugin { return Plugin{} }

func (Plugin) Descriptor() providers.Descriptor {
	return providers.Descriptor{ID: "valkey", Aliases: []string{"redis"}, DefaultVersion: "8"}
}

func (Plugin) Detect(ctx context.Context, workspace providers.Workspace, consumers []providers.Consumer) (providers.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "redis", Explanation: "Redis-compatible configuration or dependency found; Valkey proposed",
		Markers: []string{"redis://", "rediss://", "valkey://", `"redis"`, `'redis'`, `"ioredis"`, `'ioredis'`, "spring-boot-starter-data-redis", "github.com/redis/go-redis", "github.com/valkey-io", "redis[hiredis]"},
		DefaultEnvironment: func(consumer providers.Consumer) string {
			return common.FrameworkEnvironment(consumer, "SPRING_DATA_REDIS_URL", "REDIS_URL")
		},
		ExplicitEnvironment: func(content string, consumer providers.Consumer) string {
			return common.FirstEnvironment(content, "SPRING_DATA_REDIS_URL", "REDIS_URL", "VALKEY_URL")
		},
	})
}

func (Plugin) Plan(definition model.ResourceDefinition) (providers.ContainerPlan, error) {
	return providers.ContainerPlan{
		Image: "docker.io/valkey/valkey:" + definition.Version, ClientPort: 6379,
		Volumes:   []providers.Volume{{Key: "data", Path: "/data"}},
		Readiness: providers.Readiness{Kind: "exec", Command: []string{"valkey-cli", "ping"}, Timeout: time.Minute, Interval: time.Second},
	}, nil
}

func (Plugin) Bind(context providers.BindingContext) (providers.BindingResult, error) {
	if !context.Active {
		return providers.BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	value := "redis://" + net.JoinHostPort(context.Host, strconv.Itoa(context.Port))
	return providers.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: value}}, nil
}
