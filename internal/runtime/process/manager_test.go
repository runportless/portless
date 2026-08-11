package process

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func TestProcessLifecycleUsesAllocatedPortAndGroupStop(t *testing.T) {
	manager := NewManager(nil)
	definition := model.ServiceDefinition{
		Name: "gateway", Kind: model.ServiceProcess, Required: true,
		Command:         []string{os.Args[0], "-test.run=TestProcessHelper", "--"},
		Environment:     map[string]string{"PORTLESS_TEST_HELPER": "1"},
		PortEnvironment: "PORT",
		Health:          model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
	}
	scope := model.EnvironmentSelector("billing", "local")
	result, err := manager.Start(context.Background(), scope, definition, 1, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.Port == 0 || result.PID == 0 || !manager.IsRunning(scope, "gateway") {
		t.Fatalf("invalid start result %#v", result)
	}
	if err := manager.Stop(context.Background(), scope, "gateway", time.Second); err != nil {
		t.Fatal(err)
	}
	if manager.IsRunning(scope, "gateway") {
		t.Fatal("process remained running after stop")
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("PORTLESS_TEST_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(2)
	}
	defer listener.Close()
	for {
		connection, err := listener.Accept()
		if err != nil {
			os.Exit(0)
		}
		_ = connection.Close()
	}
}
