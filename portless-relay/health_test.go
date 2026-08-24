package relay

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	portlessdns "github.com/runportless/portless/portless-daemon/dns"
)

func TestCheckAtRecognizesPortlessHealth(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "portless.localhost" || request.URL.Path != "/api/v1/health" {
			t.Errorf("unexpected request host=%q path=%q", request.Host, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ready":true,"apiVersion":"1.0.0"}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := checkAt(ctx, listener.Addr().String(), ControlOrigin); err != nil {
		t.Fatal(err)
	}
}

func TestResolverValidationRequiresOnlyThePortlessHealthAddress(t *testing.T) {
	if err := validateResolverAddresses("portless.test", []net.IPAddr{{IP: net.ParseIP("127.77.0.1")}}, portlessdns.HealthAddress); err != nil {
		t.Fatal(err)
	}
	for _, addresses := range [][]net.IPAddr{
		nil,
		{{IP: net.ParseIP("127.0.0.1")}},
		{{IP: net.ParseIP("127.77.0.1")}, {IP: net.ParseIP("203.0.113.10")}},
	} {
		if err := validateResolverAddresses("portless.test", addresses, portlessdns.HealthAddress); err == nil {
			t.Fatalf("unexpected resolver addresses were accepted: %#v", addresses)
		}
	}
}

func TestLocalhostResolverValidationAcceptsOnlyLoopbackAddresses(t *testing.T) {
	for _, addresses := range [][]net.IPAddr{
		{{IP: net.ParseIP("127.0.0.1")}},
		{{IP: net.ParseIP("::1")}},
		{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("::1")}},
	} {
		if err := validateLocalhostResolverAddresses("resolver.portless.localhost", addresses); err != nil {
			t.Fatalf("loopback addresses were rejected: %#v: %v", addresses, err)
		}
	}
	for _, addresses := range [][]net.IPAddr{
		nil,
		{{IP: net.ParseIP("203.0.113.10")}},
		{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("203.0.113.10")}},
	} {
		if err := validateLocalhostResolverAddresses("resolver.portless.localhost", addresses); err == nil {
			t.Fatalf("unexpected localhost addresses were accepted: %#v", addresses)
		}
	}
}

func TestCheckAtRejectsUnrelatedPort80Service(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("not portless"))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	if err := checkAt(context.Background(), listener.Addr().String(), ControlOrigin); err == nil {
		t.Fatal("unrelated HTTP service was accepted as Portless ingress")
	}
}

func TestCheckSocketRecognizesPrivateDaemonIngress(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "portless-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketPath := filepath.Join(root, "ingress.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "portless.localhost" || request.URL.Path != "/api/v1/health" {
			t.Errorf("unexpected request host=%q path=%q", request.Host, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ready":true,"apiVersion":"1.0.0"}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	if err := CheckSocket(context.Background(), socketPath); err != nil {
		t.Fatal(err)
	}
}
