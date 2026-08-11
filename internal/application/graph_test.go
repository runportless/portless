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
