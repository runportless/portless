package controlplane

import (
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
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
			{Name: "checkout-db", Kind: model.ServiceResource},
			{Name: "payments-db", Kind: model.ServiceResource},
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

func TestExecutionLayersGroupIndependentTargetsBeforeTheirSources(t *testing.T) {
	definition := model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "inventory", Kind: model.ServiceProcess},
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "inventory-db", Kind: model.ServiceResource},
			{Name: "orders-db", Kind: model.ServiceResource},
			{Name: "orders-cache", Kind: model.ServiceResource},
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "inventory"},
			{Source: "checkout", Target: "orders"},
			{Source: "inventory", Target: "inventory-db"},
			{Source: "orders", Target: "orders-db"},
			{Source: "orders", Target: "orders-cache"},
		},
	}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal},
		{Service: "inventory", Provider: model.ProviderLocal},
		{Service: "orders", Provider: model.ProviderLocal},
		{Service: "inventory-db", Provider: model.ProviderContainer},
		{Service: "orders-db", Provider: model.ProviderContainer},
		{Service: "orders-cache", Provider: model.ProviderContainer},
	}
	layers, err := executionLayers(definition, bindings)
	if err != nil {
		t.Fatal(err)
	}
	expected := [][]string{{"inventory-db", "orders-cache", "orders-db"}, {"inventory", "orders"}, {"checkout"}}
	if !reflect.DeepEqual(layers, expected) {
		t.Fatalf("layers = %#v, want %#v", layers, expected)
	}
}
