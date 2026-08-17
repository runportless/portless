package model

import "testing"

func TestDeriveEnvironmentStatusReflectsIndividualServiceTransitions(t *testing.T) {
	tests := []struct {
		name     string
		services []Service
		want     EnvironmentStatus
	}{
		{name: "starting", services: []Service{{ServiceDefinition: ServiceDefinition{Name: "checkout", Required: true}, Status: ServiceStarting}, {ServiceDefinition: ServiceDefinition{Name: "orders", Required: true}, Status: ServicePlanned}}, want: EnvironmentStarting},
		{name: "stopping", services: []Service{{ServiceDefinition: ServiceDefinition{Name: "checkout", Required: true}, Status: ServiceStopping}, {ServiceDefinition: ServiceDefinition{Name: "orders", Required: true}, Status: ServiceReady}}, want: EnvironmentStopping},
		{name: "partially ready", services: []Service{{ServiceDefinition: ServiceDefinition{Name: "checkout", Required: true}, Status: ServiceReady}, {ServiceDefinition: ServiceDefinition{Name: "orders", Required: true}, Status: ServicePlanned}}, want: EnvironmentDegraded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _ := DeriveEnvironmentStatus(test.services, "")
			if status != test.want {
				t.Fatalf("status = %s, want %s", status, test.want)
			}
		})
	}
}
