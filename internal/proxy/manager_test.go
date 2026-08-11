package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/store"
)

func TestIngressTrafficRedactionRecordingAndFaultAreEnvironmentScoped(t *testing.T) {
	ctx := context.Background()
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Upstream", "checkout")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("created"))
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	manager := NewManager(controlStore, broker)
	manager.SetTarget(scope, "checkout", port)

	request := httptest.NewRequest(http.MethodPost, "http://checkout.local.billing.localhost/orders", nil)
	request.Header.Set("Authorization", "Bearer should-not-leak")
	request.Header.Set("X-Trace", "visible")
	response := httptest.NewRecorder()
	manager.ServeIngress(response, request, scope, "checkout")
	if response.Code != http.StatusCreated || response.Header().Get("X-Upstream") != "checkout" {
		t.Fatalf("response code=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	traffic := broker.RecentTraffic(scope, 10)
	if len(traffic) != 1 || traffic[0].Project != "billing" || traffic[0].Environment != "local" || traffic[0].Headers["Authorization"] != "[REDACTED]" || traffic[0].Headers["X-Trace"] != "visible" {
		t.Fatalf("unexpected traffic %#v", traffic)
	}

	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "checkout-debug"}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/recorded", nil), scope, "checkout")
	recorded, err := controlStore.RecordedTraffic(ctx, scope, "checkout-debug", 10)
	if err != nil || len(recorded) != 1 || recorded[0].Recording != "checkout-debug" {
		t.Fatalf("recorded traffic=%#v err=%v", recorded, err)
	}

	if _, err := controlStore.CreateFault(ctx, model.FaultRule{Project: "billing", Environment: "local", Name: "checkout-down", Source: "external", Target: "checkout", StatusCode: http.StatusServiceUnavailable, Probability: 1}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/faulted", nil), scope, "checkout")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("X-Portless-Fault") != "checkout-down" {
		t.Fatalf("fault response code=%d headers=%v", response.Code, response.Header())
	}
}

func TestRemoteTargetForwardsHTTPAndEnforcesReadOnlyPolicy(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	var receivedPath, receivedHost string
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedPath, receivedHost = request.URL.Path, request.Host
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()
	manager := NewManager(controlStore, events.NewBroker())
	if err := manager.SetRemoteTarget(scope, "payments", model.RemoteTarget{URL: remote.URL + "/api", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://payments.local.billing.localhost/charges/42", nil), scope, "payments")
	parsed, _ := url.Parse(remote.URL)
	if response.Code != http.StatusNoContent || receivedPath != "/api/charges/42" || receivedHost != parsed.Host {
		t.Fatalf("remote response=%d path=%q host=%q", response.Code, receivedPath, receivedHost)
	}

	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodPost, "http://payments.local.billing.localhost/charges", nil), scope, "payments")
	if response.Code != http.StatusForbidden || response.Header().Get("X-Portless-Remote-Policy") != "read-only" {
		t.Fatalf("write policy response=%d headers=%v", response.Code, response.Header())
	}
}

func environmentStore(t *testing.T) *store.Store {
	t.Helper()
	controlStore, err := store.Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}, {Name: "payments", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(context.Background(), "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	bindings := []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}, {Service: "payments", Provider: model.ProviderLocal, Source: "payments"}}
	if _, err := controlStore.CreateEnvironment(context.Background(), "billing", "local", definition, nil, bindings); err != nil {
		t.Fatal(err)
	}
	return controlStore
}
