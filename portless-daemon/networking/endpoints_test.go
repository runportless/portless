package networking

import (
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestAllocationSpecsCreatePublicAndDirectedTCPNames(t *testing.T) {
	definition := model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "orders", Kind: model.ServiceProcess},
			resourceService("postgres", "postgres", "17", 5432),
			resourceService("redis", "valkey", "8", 6379),
			resourceService("cache", "valkey", "8", 6379),
		},
		Connections: []model.Connection{
			{Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, Binding: "postgres"},
			{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey"},
		},
	}
	specs, err := AllocationSpecs("store", "local", definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 5 {
		t.Fatalf("got %d specs: %#v", len(specs), specs)
	}
	wanted := map[string]int{
		"postgres.via-orders.local.store.portless.test": 5432,
		"redis.via-orders.local.store.portless.test":    6379,
		"postgres.local.store.portless.test":            5432,
		"redis.local.store.portless.test":               6379,
		"cache.local.store.portless.test":               6379,
	}
	for _, spec := range specs {
		if wanted[spec.DNSName] != spec.Port {
			t.Fatalf("unexpected spec: %#v", spec)
		}
	}
}

func resourceService(name, resourceType, version string, port int) model.ServiceDefinition {
	return model.ServiceDefinition{Name: name, Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: resourceType, Version: version}, Port: port}
}

func TestAllocationSpecsRequireGenericTCPPort(t *testing.T) {
	definition := model.ProjectModel{
		Services:    []model.ServiceDefinition{{Name: "worker"}, {Name: "nats"}},
		Connections: []model.Connection{{Source: "worker", Target: "nats", Protocol: model.ProtocolTCP}},
	}
	if _, err := AllocationSpecs("store", "local", definition); err == nil {
		t.Fatal("generic TCP service without a port was accepted")
	}
	definition.Services[1].Port = 4222
	if _, err := AllocationSpecs("store", "local", definition); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointLoopbackPoolIsBoundedAndUnique(t *testing.T) {
	addresses := EndpointLoopbackAddresses()
	if len(addresses) != EndpointPoolSize || addresses[0] != "127.77.0.2" || addresses[len(addresses)-1] != "127.77.0.65" {
		t.Fatalf("unexpected endpoint pool: first=%q last=%q count=%d", addresses[0], addresses[len(addresses)-1], len(addresses))
	}
	seen := map[string]bool{}
	for _, address := range addresses {
		if seen[address] {
			t.Fatalf("duplicate endpoint address %s", address)
		}
		seen[address] = true
	}
}
