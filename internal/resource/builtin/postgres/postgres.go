package postgres

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/resource"
	"github.com/portless-run/portless/internal/resource/builtin/common"
)

type Plugin struct{}

func New() resource.Plugin { return Plugin{} }

func (Plugin) Descriptor() resource.Descriptor {
	return resource.Descriptor{ID: "postgres", Aliases: []string{"postgresql"}, DefaultVersion: "17"}
}

func (Plugin) Detect(ctx context.Context, workspace resource.Workspace, consumers []resource.Consumer) (resource.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "postgres", Explanation: "PostgreSQL configuration or dependency found",
		Markers: []string{"postgresql://", "postgres://", "jdbc:postgresql:", "org.postgresql", "github.com/jackc/pgx", "gorm.io/driver/postgres", `"pg"`, `'pg'`, "psycopg", "asyncpg"},
		DefaultEnvironment: func(consumer resource.Consumer) string {
			return common.FrameworkEnvironment(consumer, "SPRING_DATASOURCE_URL", "DATABASE_URL")
		},
		ExplicitEnvironment: func(content string, consumer resource.Consumer) string {
			return common.FirstEnvironment(content, "SPRING_DATASOURCE_URL", "DATABASE_URL", "POSTGRESQL_URL", "POSTGRES_URL")
		},
	})
}

func (Plugin) Plan(definition model.ResourceDefinition) (resource.ContainerPlan, error) {
	return resource.ContainerPlan{
		Image: "docker.io/library/postgres:" + definition.Version, ClientPort: 5432,
		Environment: []resource.EnvironmentVariable{
			{Name: "POSTGRES_DB", Value: "portless"},
			{Name: "POSTGRES_USER", Value: "portless"},
			{Name: "POSTGRES_PASSWORD", SecretBytes: 24},
		},
		Volumes:   []resource.Volume{{Key: "data", Path: "/var/lib/postgresql/data"}},
		Readiness: resource.Readiness{Kind: "exec", Command: []string{"pg_isready", "-U", "portless", "-d", "portless"}, Timeout: 2 * time.Minute, Interval: time.Second},
	}, nil
}

func (Plugin) Bind(context resource.BindingContext) (resource.BindingResult, error) {
	if !context.Active {
		return inactive(context.Environment, strings.HasPrefix(context.Environment, "SPRING_DATASOURCE")), nil
	}
	user := context.TargetEnvironment["POSTGRES_USER"]
	database := context.TargetEnvironment["POSTGRES_DB"]
	password := context.TargetEnvironment["POSTGRES_PASSWORD"]
	if user == "" || database == "" || password == "" {
		return resource.BindingResult{}, errors.New("managed PostgreSQL credentials are incomplete")
	}
	address := net.JoinHostPort(context.Host, strconv.Itoa(context.Port))
	if strings.HasPrefix(context.Environment, "SPRING_DATASOURCE") {
		values := map[string]string{
			context.Environment:          "jdbc:postgresql://" + address + "/" + database,
			"SPRING_DATASOURCE_USERNAME": user,
			"SPRING_DATASOURCE_PASSWORD": password,
		}
		safe := clone(values)
		safe["SPRING_DATASOURCE_PASSWORD"] = "••••••••"
		return resource.BindingResult{Values: values, SafeValues: safe}, nil
	}
	value := "postgresql://" + url.QueryEscape(user) + ":" + url.QueryEscape(password) + "@" + address + "/" + url.PathEscape(database)
	safe := "postgresql://••••@" + address + "/" + url.PathEscape(database)
	return resource.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: safe}}, nil
}

func inactive(environment string, spring bool) resource.BindingResult {
	values := map[string]string{environment: "not active"}
	if spring {
		values["SPRING_DATASOURCE_USERNAME"] = "not active"
		values["SPRING_DATASOURCE_PASSWORD"] = "not active"
	}
	return resource.BindingResult{SafeValues: values}
}

func clone(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
