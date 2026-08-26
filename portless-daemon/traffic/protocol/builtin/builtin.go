// Package builtin composes the protocol decoders shipped with Portless.
package builtin

import (
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
	"github.com/runportless/portless/portless-daemon/traffic/protocol/mysql"
	"github.com/runportless/portless/portless-daemon/traffic/protocol/nats"
	"github.com/runportless/portless/portless-daemon/traffic/protocol/postgres"
	"github.com/runportless/portless/portless-daemon/traffic/protocol/redis"
)

// Registry returns a decoder registry containing every built-in protocol.
func Registry() *protocol.Registry {
	registry := protocol.NewRegistry()
	mustRegister(registry, model.ApplicationProtocolPostgreSQL, postgres.New)
	mustRegister(registry, model.ApplicationProtocolRedis, redis.New)
	mustRegister(registry, model.ApplicationProtocolMySQL, mysql.New)
	mustRegister(registry, model.ApplicationProtocolNATS, nats.New)
	return registry
}

func mustRegister(registry *protocol.Registry, applicationProtocol model.ApplicationProtocol, factory protocol.Factory) {
	if err := registry.Register(applicationProtocol, factory); err != nil {
		panic(err)
	}
}
