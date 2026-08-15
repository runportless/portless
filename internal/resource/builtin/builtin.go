// Package builtin composes the trusted in-process resource plugin registry.
package builtin

import (
	"github.com/portless-run/portless/internal/resource"
	"github.com/portless-run/portless/internal/resource/builtin/mysql"
	"github.com/portless-run/portless/internal/resource/builtin/nats"
	"github.com/portless-run/portless/internal/resource/builtin/postgres"
	"github.com/portless-run/portless/internal/resource/builtin/valkey"
)

func Plugins() []resource.Plugin {
	return []resource.Plugin{postgres.New(), valkey.New(), mysql.New(), nats.New()}
}

func Registry() *resource.Registry {
	return resource.MustRegistry(Plugins()...)
}
