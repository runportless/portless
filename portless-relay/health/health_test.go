package health

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	portlessdns "github.com/runportless/portless/portless-daemon/dns"
	"github.com/runportless/portless/portless-daemon/networking"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
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
	if err := checkAt(ctx, listener.Addr().String(), relayruntime.ControlOrigin); err != nil {
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

	if err := checkAt(context.Background(), listener.Addr().String(), relayruntime.ControlOrigin); err == nil {
		t.Fatal("unrelated HTTP service was accepted as Portless ingress")
	}
}

func TestDNSHealthCheckHonorsContextCancellation(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	queryReceived := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, portlessdns.MaxMessage)
		if _, _, readErr := server.ReadFrom(buffer); readErr == nil {
			queryReceived <- struct{}{}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = checkDNSRecordAt(ctx, server.LocalAddr().String(), networking.DNSZone, portlessdns.HealthAddress)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("DNS health cancellation err=%v duration=%s", err, time.Since(started))
	}
	select {
	case <-queryReceived:
	case <-time.After(time.Second):
		t.Fatal("DNS health check did not send its probe query")
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

func TestRelayHealthInspectionRunsIndependentProbesConcurrently(t *testing.T) {
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	probe := func(context.Context) error {
		entered <- struct{}{}
		<-release
		return nil
	}
	finished := make(chan Inspection, 1)
	go func() {
		finished <- Inspect(context.Background(), Probes{HTTP: probe, DNS: probe, Resolver: probe})
	}()
	for range 3 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("health probes did not all start concurrently")
		}
	}
	close(release)
	result := <-finished
	if result.HTTPError != nil || result.DNSError != nil || result.ResolverError != nil {
		t.Fatalf("unexpected health result: %#v", result)
	}
}

func TestWaitUntilReadyHonorsOverallTimeout(t *testing.T) {
	probe := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	started := time.Now()
	err := waitUntilReady(context.Background(), 40*time.Millisecond, Probes{HTTP: probe, DNS: probe, Resolver: probe})
	if err == nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("readiness timeout err=%v duration=%s", err, time.Since(started))
	}
}

func TestRelayHealthInspectionReturnsWhenAProbeDoesNotCooperate(t *testing.T) {
	release := make(chan struct{})
	blockedProbe := func(context.Context) error {
		<-release
		return nil
	}
	quickProbe := func(context.Context) error { return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := Inspect(ctx, Probes{HTTP: blockedProbe, DNS: quickProbe, Resolver: quickProbe})
	close(release)
	if !errors.Is(result.HTTPError, context.DeadlineExceeded) || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("uncooperative probe result=%#v duration=%s", result, time.Since(started))
	}
}
