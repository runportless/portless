// Package builtin composes the trusted in-process resource plugin registry.
package builtin

import (
	"github.com/portless-run/portless/portless-daemon/providers"
	"github.com/portless-run/portless/portless-daemon/providers/builtin/mysql"
	"github.com/portless-run/portless/portless-daemon/providers/builtin/nats"
	"github.com/portless-run/portless/portless-daemon/providers/builtin/postgres"
	"github.com/portless-run/portless/portless-daemon/providers/builtin/valkey"
)

func Plugins() []providers.Plugin {
	return []providers.Plugin{postgres.New(), valkey.New(), mysql.New(), nats.New()}
}

func Registry() *providers.Registry {
	return providers.MustRegistry(Plugins()...)
}
