package builtin

import (
	"github.com/runportless/portless/portless-daemon/projects/discovery/builtin/fastapi"
	"github.com/runportless/portless/portless-daemon/projects/discovery/builtin/golang"
	"github.com/runportless/portless/portless-daemon/projects/discovery/builtin/node"
	"github.com/runportless/portless/portless-daemon/projects/discovery/builtin/springboot"
	"github.com/runportless/portless/portless-daemon/projects/discovery/builtin/topology"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
)

// Detectors returns the built-in framework detectors in registry order.
func Detectors() []spec.ServiceDetector {
	return []spec.ServiceDetector{
		springboot.New(),
		node.NestJS(),
		node.Express(),
		node.Fastify(),
		node.NextJS(),
		golang.New(),
		fastapi.New(),
	}
}

// Analyzers returns the built-in service topology analyzers.
func Analyzers() []spec.TopologyAnalyzer {
	return []spec.TopologyAnalyzer{topology.New()}
}
