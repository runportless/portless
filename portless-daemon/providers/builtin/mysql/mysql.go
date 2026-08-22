package mysql

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

// Plugin provides MySQL discovery, container planning, and connection binding.
type Plugin struct{}

// New returns the built-in MySQL resource plugin.
func New() providers.Plugin { return Plugin{} }

// Descriptor returns MySQL plugin registration metadata.
func (Plugin) Descriptor() providers.Descriptor {
	return providers.Descriptor{ID: "mysql", DefaultVersion: "8.4"}
}

// Detect finds MySQL dependencies and their consumer environment variables.
func (Plugin) Detect(ctx context.Context, workspace providers.Workspace, consumers []providers.Consumer) (providers.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "mysql", Explanation: "MySQL configuration or dependency found",
		Markers: []string{"mysql://", "mysql2://", "jdbc:mysql:", "com.mysql", "mysql-connector", "github.com/go-sql-driver/mysql", "gorm.io/driver/mysql", `"mysql2"`, `'mysql2'`, "pymysql", "mysqlclient", "aiomysql"},
		DefaultEnvironment: func(consumer providers.Consumer) string {
			return common.FrameworkEnvironment(consumer, "SPRING_DATASOURCE_URL", "DATABASE_URL")
		},
		ExplicitEnvironment: func(content string, consumer providers.Consumer) string {
			return common.FirstEnvironment(content, "SPRING_DATASOURCE_URL", "DATABASE_URL", "MYSQL_URL")
		},
	})
}

// Plan returns the managed MySQL container, credentials, volume, and readiness recipe.
func (Plugin) Plan(definition model.ResourceDefinition) (providers.ContainerPlan, error) {
	return providers.ContainerPlan{
		Image: "docker.io/library/mysql:" + definition.Version, ClientPort: 3306,
		Environment: []providers.EnvironmentVariable{
			{Name: "MYSQL_DATABASE", Value: "portless"},
			{Name: "MYSQL_USER", Value: "portless"},
			{Name: "MYSQL_PASSWORD", SecretBytes: 24},
			{Name: "MYSQL_ROOT_PASSWORD", SecretBytes: 24},
		},
		Volumes: []providers.Volume{{Key: "data", Path: "/var/lib/mysql"}},
		Readiness: providers.Readiness{
			Kind: "exec", Command: []string{"sh", "-c", `mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD" --silent`},
			Timeout: 2 * time.Minute, Interval: time.Second,
		},
	}, nil
}

// Bind creates a MySQL or Spring JDBC connection configuration with masked safe values.
func (Plugin) Bind(context providers.BindingContext) (providers.BindingResult, error) {
	spring := strings.HasPrefix(context.Environment, "SPRING_DATASOURCE")
	if !context.Active {
		values := map[string]string{context.Environment: "not active"}
		if spring {
			values["SPRING_DATASOURCE_USERNAME"] = "not active"
			values["SPRING_DATASOURCE_PASSWORD"] = "not active"
		}
		return providers.BindingResult{SafeValues: values}, nil
	}
	user := context.TargetEnvironment["MYSQL_USER"]
	database := context.TargetEnvironment["MYSQL_DATABASE"]
	password := context.TargetEnvironment["MYSQL_PASSWORD"]
	if user == "" || database == "" || password == "" {
		return providers.BindingResult{}, errors.New("managed MySQL credentials are incomplete")
	}
	address := net.JoinHostPort(context.Host, strconv.Itoa(context.Port))
	if spring {
		values := map[string]string{
			context.Environment:          "jdbc:mysql://" + address + "/" + database,
			"SPRING_DATASOURCE_USERNAME": user,
			"SPRING_DATASOURCE_PASSWORD": password,
		}
		safe := map[string]string{
			context.Environment:          values[context.Environment],
			"SPRING_DATASOURCE_USERNAME": user,
			"SPRING_DATASOURCE_PASSWORD": "••••••••",
		}
		return providers.BindingResult{Values: values, SafeValues: safe}, nil
	}
	value := "mysql://" + url.QueryEscape(user) + ":" + url.QueryEscape(password) + "@" + address + "/" + url.PathEscape(database)
	safe := "mysql://••••@" + address + "/" + url.PathEscape(database)
	return providers.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: safe}}, nil
}
