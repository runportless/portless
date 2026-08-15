package builtin

import (
	"github.com/portless-run/portless/internal/project/discovery/builtin/fastapi"
	"github.com/portless-run/portless/internal/project/discovery/builtin/golang"
	"github.com/portless-run/portless/internal/project/discovery/builtin/node"
	"github.com/portless-run/portless/internal/project/discovery/builtin/springboot"
	"github.com/portless-run/portless/internal/project/discovery/builtin/topology"
	"github.com/portless-run/portless/internal/project/discovery/spec"
)

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

func Analyzers() []spec.TopologyAnalyzer {
	return []spec.TopologyAnalyzer{topology.New()}
}
