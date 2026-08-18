package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func TestDoAuthenticatesAndEncodesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get(contract.ClientKindHeader) != string(contract.ClientKindMCP) {
			t.Fatalf("%s = %q", contract.ClientKindHeader, request.Header.Get(contract.ClientKindHeader))
		}
		if request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected headers: %#v", request.Header)
		}
		content, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != `{"name":"store"}` {
			t.Fatalf("body = %s", content)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"accepted":true}`)
	}))
	defer server.Close()

	client := New(server.URL, "secret", server.Client()).WithClientKind(contract.ClientKindMCP)
	var result struct {
		Accepted bool `json:"accepted"`
	}
	if err := client.do(context.Background(), http.MethodPost, "/projects", map[string]string{"name": "store"}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Accepted {
		t.Fatal("response was not decoded")
	}
}

func TestChangeBindingSendsIdempotencyKeyAndDecodesOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/v1/environments/billing/local/bindings/orders" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Idempotency-Key") != "provider-request" {
			t.Fatalf("Idempotency-Key = %q", request.Header.Get("Idempotency-Key"))
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(writer, `{"project":"billing","environment":"local","number":7,"type":"change-provider","state":"running","actor":"CLI","startedAt":"2026-08-18T12:00:00Z"}`)
	}))
	defer server.Close()
	client := New(server.URL, "secret", server.Client())
	operation, err := client.ChangeBinding(context.Background(), "billing", "local", "orders", contract.ComponentBinding{Provider: "remote"}, "provider-request")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Number != 7 || operation.Type != "change-provider" || operation.State != "running" {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestDoDecodesStructuredErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(writer, `{"error":{"code":"ACTIVE_ENVIRONMENTS","message":"stop them first","subject":{"project":"store"},"details":{"count":2},"remediation":[{"label":"Stop all","command":"portless down --all"}]}}`)
	}))
	defer server.Close()

	client := New(server.URL, "", server.Client())
	err := client.do(context.Background(), http.MethodGet, "/failure", nil, nil)
	var clientError *ClientError
	if !errors.As(err, &clientError) {
		t.Fatalf("error = %T %v", err, err)
	}
	if clientError.Status != http.StatusConflict || clientError.Code != "ACTIVE_ENVIRONMENTS" || clientError.Subject["project"] != "store" || len(clientError.Remediation) != 1 {
		t.Fatalf("unexpected client error: %#v", clientError)
	}
}

func TestDoSupportsEmptyAndRawResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/empty" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.WriteString(writer, "raw response\n")
	}))
	defer server.Close()

	client := New(server.URL, "", server.Client())
	if err := client.do(context.Background(), http.MethodPost, "/empty", nil, nil); err != nil {
		t.Fatal(err)
	}
	var content []byte
	if err := client.do(context.Background(), http.MethodGet, "/raw", nil, &content); err != nil {
		t.Fatal(err)
	}
	if string(content) != "raw response\n" {
		t.Fatalf("content = %q", content)
	}
}

func TestDoHonorsCancellationAndResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/wait" {
			<-request.Context().Done()
			return
		}
		_, _ = io.WriteString(writer, `{"value":"`+strings.Repeat("x", (16<<20)+1024)+`"}`)
	}))
	defer server.Close()

	client := New(server.URL, "", server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := client.do(ctx, http.MethodGet, "/wait", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation error = %v", err)
	}
	var response map[string]string
	if err := client.do(context.Background(), http.MethodGet, "/large", nil, &response); err == nil || !strings.Contains(err.Error(), "decode daemon response") {
		t.Fatalf("oversized response error = %v", err)
	}
}
