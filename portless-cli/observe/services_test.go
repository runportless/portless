package observe

import (
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestReadinessDescription(t *testing.T) {
	tests := []struct {
		name   string
		health model.HealthCheck
		want   string
	}{
		{name: "HTTP", health: model.HealthCheck{Kind: "http", Path: "/health"}, want: "HTTP GET /health"},
		{name: "TCP", health: model.HealthCheck{Kind: "tcp"}, want: "TCP connect"},
		{name: "resource", health: model.HealthCheck{Kind: "exec"}, want: "provider command"},
		{name: "unknown", health: model.HealthCheck{}, want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readinessDescription(test.health); got != test.want {
				t.Fatalf("description = %q, want %q", got, test.want)
			}
		})
	}
}
