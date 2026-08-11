package application

import (
	"reflect"
	"testing"

	"github.com/portless-run/portless/internal/model"
)

func TestStartOrderPlacesTargetsBeforeSources(t *testing.T) {
	definition := model.ProjectModel{
		Services:    []model.ServiceDefinition{{Name: "gateway"}, {Name: "orders"}, {Name: "postgres"}},
		Connections: []model.Connection{{Source: "gateway", Target: "orders"}, {Source: "orders", Target: "postgres"}},
	}
	order, err := startOrder(definition)
	if err != nil {
		t.Fatal(err)
	}
	if expected := []string{"postgres", "orders", "gateway"}; !reflect.DeepEqual(order, expected) {
		t.Fatalf("order = %#v, want %#v", order, expected)
	}
}

func TestStartOrderRejectsCycles(t *testing.T) {
	definition := model.ProjectModel{Services: []model.ServiceDefinition{{Name: "a"}, {Name: "b"}}, Connections: []model.Connection{{Source: "a", Target: "b"}, {Source: "b", Target: "a"}}}
	if _, err := startOrder(definition); err == nil {
		t.Fatal("dependency cycle was accepted")
	}
}

func TestExecutionOrderSkipsRemoteServicesAndTheirUnusedContainers(t *testing.T) {
	definition := model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "payments", Kind: model.ServiceProcess},
			{Name: "checkout-db", Kind: model.ServiceContainer},
			{Name: "payments-db", Kind: model.ServiceContainer},
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "payments"},
			{Source: "checkout", Target: "checkout-db"},
			{Source: "payments", Target: "payments-db"},
		},
	}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal},
		{Service: "payments", Provider: model.ProviderRemote},
		{Service: "checkout-db", Provider: model.ProviderContainer},
		{Service: "payments-db", Provider: model.ProviderContainer},
	}
	order, err := executionOrder(definition, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if expected := []string{"checkout-db", "checkout"}; !reflect.DeepEqual(order, expected) {
		t.Fatalf("order = %#v, want %#v", order, expected)
	}
}
