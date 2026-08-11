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

func TestIngressTrafficRedactionRecordingAndFault(t *testing.T) {
	ctx := context.Background()
	controlStore, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	_, err = controlStore.CreateProject(ctx, "billing", "/tmp/proxy-fixture", model.ProjectModel{SuggestedName: "billing", PrimaryService: "gateway", Services: []model.ServiceDefinition{{Name: "gateway"}}})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Upstream", "gateway")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("created"))
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	manager := NewManager(controlStore, broker)
	manager.SetTarget("billing", "gateway", port)

	request := httptest.NewRequest(http.MethodPost, "http://gateway.billing.localhost/orders", nil)
	request.Header.Set("Authorization", "Bearer should-not-leak")
	request.Header.Set("X-Trace", "visible")
	response := httptest.NewRecorder()
	manager.ServeIngress(response, request, "billing", "gateway")
	if response.Code != http.StatusCreated || response.Header().Get("X-Upstream") != "gateway" {
		t.Fatalf("response code=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	traffic := broker.RecentTraffic("billing", 10)
	if len(traffic) != 1 || traffic[0].Headers["Authorization"] != "[REDACTED]" || traffic[0].Headers["X-Trace"] != "visible" {
		t.Fatalf("unexpected traffic %#v", traffic)
	}

	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Name: "checkout-debug"}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://gateway.billing.localhost/recorded", nil), "billing", "gateway")
	recorded, err := controlStore.RecordedTraffic(ctx, "billing", "checkout-debug", 10)
	if err != nil || len(recorded) != 1 || recorded[0].Recording != "checkout-debug" {
		t.Fatalf("recorded traffic=%#v err=%v", recorded, err)
	}

	if _, err := controlStore.CreateFault(ctx, model.FaultRule{Project: "billing", Name: "gateway-down", Source: "external", Target: "gateway", StatusCode: http.StatusServiceUnavailable, Probability: 1}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://gateway.billing.localhost/faulted", nil), "billing", "gateway")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("X-Portless-Fault") != "gateway-down" {
		t.Fatalf("fault response code=%d headers=%v", response.Code, response.Header())
	}
}
