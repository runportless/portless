package ingress

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		_, _ = writer.Write([]byte(`{"ready":true,"apiVersion":"1"}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := checkAt(ctx, listener.Addr().String(), ControlOrigin); err != nil {
		t.Fatal(err)
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

func TestValidateSetupRequestRequiresPrivateIngressSocket(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{Executable: executable, TargetSocket: filepath.Join(t.TempDir(), "ingress.sock"), UID: 501, GID: 20}
	if err := validateSetupRequest(request); err != nil {
		t.Fatal(err)
	}
	request.TargetSocket = filepath.Join(t.TempDir(), "somewhere-else.sock")
	if err := validateSetupRequest(request); err == nil || !strings.Contains(err.Error(), "ingress.sock") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
