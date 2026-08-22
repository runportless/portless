package postgres

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/providers"
	"github.com/runportless/portless/portless-daemon/providers/builtin/common"
)

// Plugin provides PostgreSQL discovery, container planning, and connection binding.
type Plugin struct{}

// New returns the built-in PostgreSQL resource plugin.
func New() providers.Plugin { return Plugin{} }

// Descriptor returns PostgreSQL plugin registration metadata and aliases.
func (Plugin) Descriptor() providers.Descriptor {
	return providers.Descriptor{ID: "postgres", Aliases: []string{"postgresql"}, DefaultVersion: "17"}
}

// Detect finds PostgreSQL dependencies and their consumer environment variables.
func (Plugin) Detect(ctx context.Context, workspace providers.Workspace, consumers []providers.Consumer) (providers.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "postgres", Explanation: "PostgreSQL configuration or dependency found",
		Markers: []string{"postgresql://", "postgres://", "jdbc:postgresql:", "org.postgresql", "github.com/jackc/pgx", "gorm.io/driver/postgres", `"pg"`, `'pg'`, "psycopg", "asyncpg"},
		DefaultEnvironment: func(consumer providers.Consumer) string {
			return common.FrameworkEnvironment(consumer, "SPRING_DATASOURCE_URL", "DATABASE_URL")
		},
		ExplicitEnvironment: func(content string, consumer providers.Consumer) string {
			return common.FirstEnvironment(content, "SPRING_DATASOURCE_URL", "DATABASE_URL", "POSTGRESQL_URL", "POSTGRES_URL")
		},
	})
}

// Plan returns the managed PostgreSQL container, credentials, volume, and readiness recipe.
func (Plugin) Plan(definition model.ResourceDefinition) (providers.ContainerPlan, error) {
	return providers.ContainerPlan{
		Image: "docker.io/library/postgres:" + definition.Version, ClientPort: 5432,
		Environment: []providers.EnvironmentVariable{
			{Name: "POSTGRES_DB", Value: "portless"},
			{Name: "POSTGRES_USER", Value: "portless"},
			{Name: "POSTGRES_PASSWORD", SecretBytes: 24},
		},
		Volumes:   []providers.Volume{{Key: "data", Path: "/var/lib/postgresql/data"}},
		Readiness: providers.Readiness{Kind: "exec", Command: []string{"pg_isready", "-U", "portless", "-d", "portless"}, Timeout: 2 * time.Minute, Interval: time.Second},
	}, nil
}

// Bind creates a PostgreSQL or Spring JDBC configuration with masked safe values.
func (Plugin) Bind(context providers.BindingContext) (providers.BindingResult, error) {
	if !context.Active {
		return inactive(context.Environment, strings.HasPrefix(context.Environment, "SPRING_DATASOURCE")), nil
	}
	user := context.TargetEnvironment["POSTGRES_USER"]
	database := context.TargetEnvironment["POSTGRES_DB"]
	password := context.TargetEnvironment["POSTGRES_PASSWORD"]
	if user == "" || database == "" || password == "" {
		return providers.BindingResult{}, errors.New("managed PostgreSQL credentials are incomplete")
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
		return providers.BindingResult{Values: values, SafeValues: safe}, nil
	}
	value := "postgresql://" + url.QueryEscape(user) + ":" + url.QueryEscape(password) + "@" + address + "/" + url.PathEscape(database)
	safe := "postgresql://••••@" + address + "/" + url.PathEscape(database)
	return providers.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: safe}}, nil
}

func inactive(environment string, spring bool) providers.BindingResult {
	values := map[string]string{environment: "not active"}
	if spring {
		values["SPRING_DATASOURCE_USERNAME"] = "not active"
		values["SPRING_DATASOURCE_PASSWORD"] = "not active"
	}
	return providers.BindingResult{SafeValues: values}
}

func clone(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
