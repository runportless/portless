package health

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestWaitHTTPRequiresTheDiscoveredEndpointToBecomeReady(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ready" {
			http.NotFound(response, request)
			return
		}
		if attempts.Add(1) < 3 {
			http.Error(response, "starting", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	port := listener.Addr().(*net.TCPAddr).Port
	check := model.HealthCheck{Kind: "http", Path: "/ready", Timeout: time.Second, Interval: 10 * time.Millisecond}
	if err := Wait(context.Background(), port, check); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() < 3 {
		t.Fatalf("readiness succeeded after only %d attempts", attempts.Load())
	}
}
