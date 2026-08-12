package supervisor

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func TestRunnerAuthenticatesStatusAndStopsProcess(t *testing.T) {
	socketRoot, err := os.MkdirTemp("/tmp", "portless-supervisor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	data := t.TempDir()
	manifestPath := filepath.Join(data, "manifest.json")
	manifest := Manifest{
		SocketPath: filepath.Join(socketRoot, "service.sock"), StatePath: filepath.Join(data, "state.json"),
		RunKey: "private-test-key", Scope: "billing/local", Service: "checkout", Generation: 3, Port: port,
		Definition: model.ServiceDefinition{
			Name: "checkout", Kind: model.ServiceProcess,
			Command:     []string{os.Args[0], "-test.run=TestSupervisorServiceHelper", "--"},
			Environment: map[string]string{"PORTLESS_SUPERVISOR_TEST_HELPER": "1"}, PortEnvironment: "PORT",
		},
		LogsRoot: filepath.Join(data, "logs"),
	}
	if err := WriteManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), manifestPath) }()

	var status Status
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		status, err = LiveStatus(probeCtx, manifest.SocketPath, manifest.RunKey)
		cancel()
		if err == nil && status.State == "ready" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if status.State != "ready" || status.PID == 0 || status.SupervisorPID == 0 || status.Port != port {
		t.Fatalf("runner status = %#v, err = %v", status, err)
	}
	if _, err := LiveStatus(context.Background(), manifest.SocketPath, "wrong-key"); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("wrong key error = %v", err)
	}
	var response *http.Response
	for time.Now().Before(deadline) {
		response, err = http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("service never became reachable: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("service status = %s", response.Status)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped, err := Stop(stopCtx, manifest.SocketPath, manifest.StatePath, manifest.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != "stopped" || !stopped.Expected {
		t.Fatalf("stopped status = %#v", stopped)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit after stopping its process")
	}
}

func TestSupervisorServiceHelper(t *testing.T) {
	if os.Getenv("PORTLESS_SUPERVISOR_TEST_HELPER") != "1" {
		return
	}
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("PORT"), Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		os.Exit(2)
	}
}
