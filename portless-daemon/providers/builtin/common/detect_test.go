package common

import (
	"strings"
	"testing"
)

func TestLogicalServiceHost(t *testing.T) {
	tests := []struct {
		name    string
		content string
		schemes []string
		want    string
	}{
		{name: "URL", content: "DATABASE_URL=postgresql://user:secret@orders-postgres:5432/portless", schemes: []string{"postgresql", "postgres"}, want: "orders-postgres"},
		{name: "JDBC", content: "spring.datasource.url=jdbc:mysql://shared-mysql:3306/portless", schemes: []string{"mysql", "mysql2"}, want: "shared-mysql"},
		{name: "localhost", content: "REDIS_URL=redis://localhost:6379", schemes: []string{"redis"}},
		{name: "external DNS", content: "NATS_URL=nats://broker.example.test:4222", schemes: []string{"nats"}},
		{name: "dynamic", content: "DATABASE_URL=postgresql://${POSTGRES_HOST}:5432/portless", schemes: []string{"postgresql"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := LogicalServiceHost(test.content, test.schemes...); got != test.want {
				t.Fatalf("LogicalServiceHost() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConsumerResourceNamePreservesReadableSuffixWithinDNSLimit(t *testing.T) {
	name := consumerResourceName(strings.Repeat("a", 63), "postgres")
	if len(name) != 63 || !strings.HasSuffix(name, "-postgres") {
		t.Fatalf("consumer resource name = %q", name)
	}
}
