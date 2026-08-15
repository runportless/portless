package mysql

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
	return resource.Descriptor{ID: "mysql", DefaultVersion: "8.4"}
}

func (Plugin) Detect(ctx context.Context, workspace resource.Workspace, consumers []resource.Consumer) (resource.Findings, error) {
	return common.Detect(ctx, workspace, consumers, common.Detection{
		Name: "mysql", Explanation: "MySQL configuration or dependency found",
		Markers: []string{"mysql://", "mysql2://", "jdbc:mysql:", "com.mysql", "mysql-connector", "github.com/go-sql-driver/mysql", "gorm.io/driver/mysql", `"mysql2"`, `'mysql2'`, "pymysql", "mysqlclient", "aiomysql"},
		DefaultEnvironment: func(consumer resource.Consumer) string {
			return common.FrameworkEnvironment(consumer, "SPRING_DATASOURCE_URL", "DATABASE_URL")
		},
		ExplicitEnvironment: func(content string, consumer resource.Consumer) string {
			return common.FirstEnvironment(content, "SPRING_DATASOURCE_URL", "DATABASE_URL", "MYSQL_URL")
		},
	})
}

func (Plugin) Plan(definition model.ResourceDefinition) (resource.ContainerPlan, error) {
	return resource.ContainerPlan{
		Image: "docker.io/library/mysql:" + definition.Version, ClientPort: 3306,
		Environment: []resource.EnvironmentVariable{
			{Name: "MYSQL_DATABASE", Value: "portless"},
			{Name: "MYSQL_USER", Value: "portless"},
			{Name: "MYSQL_PASSWORD", SecretBytes: 24},
			{Name: "MYSQL_ROOT_PASSWORD", SecretBytes: 24},
		},
		Volumes: []resource.Volume{{Key: "data", Path: "/var/lib/mysql"}},
		Readiness: resource.Readiness{
			Kind: "exec", Command: []string{"sh", "-c", `mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD" --silent`},
			Timeout: 2 * time.Minute, Interval: time.Second,
		},
	}, nil
}

func (Plugin) Bind(context resource.BindingContext) (resource.BindingResult, error) {
	spring := strings.HasPrefix(context.Environment, "SPRING_DATASOURCE")
	if !context.Active {
		values := map[string]string{context.Environment: "not active"}
		if spring {
			values["SPRING_DATASOURCE_USERNAME"] = "not active"
			values["SPRING_DATASOURCE_PASSWORD"] = "not active"
		}
		return resource.BindingResult{SafeValues: values}, nil
	}
	user := context.TargetEnvironment["MYSQL_USER"]
	database := context.TargetEnvironment["MYSQL_DATABASE"]
	password := context.TargetEnvironment["MYSQL_PASSWORD"]
	if user == "" || database == "" || password == "" {
		return resource.BindingResult{}, errors.New("managed MySQL credentials are incomplete")
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
		return resource.BindingResult{Values: values, SafeValues: safe}, nil
	}
	value := "mysql://" + url.QueryEscape(user) + ":" + url.QueryEscape(password) + "@" + address + "/" + url.PathEscape(database)
	safe := "mysql://••••@" + address + "/" + url.PathEscape(database)
	return resource.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: safe}}, nil
}
