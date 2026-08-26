package examples_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/compiler"
	"github.com/runportless/portless/portless-daemon/projects/discovery"
)

func TestStoreExampleCompilesRealResourceBackedTopology(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "examples", "store")
	engine, err := discovery.NewDefault(discovery.Config{ScanTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := engine.Discover(context.Background(), root)
	if err != nil {
		t.Fatalf("discover Store example: %v", err)
	}

	project, sources, defaults, err := compiler.InitialProject("store", []model.SourceBinding{{
		Name: "store", Path: root, Definition: discovered.Model,
	}})
	if err != nil {
		t.Fatalf("compile Store example: %v", err)
	}
	if project.PrimaryService != "checkout" {
		t.Fatalf("primary service = %q", project.PrimaryService)
	}
	if got := serviceNames(project.Services); strings.Join(got, ",") != "checkout,inventory,inventory-postgres,orders,orders-postgres,orders-redis" {
		t.Fatalf("services = %v", got)
	}
	if got := connectionKeys(project.Connections); strings.Join(got, ",") != "checkout:inventory:http,checkout:orders:http,inventory:inventory-postgres:tcp,orders:orders-postgres:tcp,orders:orders-redis:tcp" {
		t.Fatalf("connections = %v", got)
	}
	if len(project.References) != 0 {
		t.Fatalf("unresolved references = %#v", project.References)
	}
	if got := sourceServices(sources); strings.Join(got, ",") != "store=checkout+inventory+orders" {
		t.Fatalf("sources = %v", got)
	}
	if got := providerDefaults(defaults); strings.Join(got, ",") != "checkout=local:store,inventory-postgres=container,inventory=local:store,orders-postgres=container,orders-redis=container,orders=local:store" {
		t.Fatalf("provider defaults = %v", got)
	}

	protocols := make(map[string]model.ApplicationProtocol)
	for _, connection := range project.Connections {
		protocols[connection.Source+":"+connection.Target] = connection.ApplicationProtocol
	}
	if protocols["inventory:inventory-postgres"] != model.ApplicationProtocolPostgreSQL || protocols["orders:orders-postgres"] != model.ApplicationProtocolPostgreSQL || protocols["orders:orders-redis"] != model.ApplicationProtocolRedis {
		t.Fatalf("application protocols = %#v", protocols)
	}
}
