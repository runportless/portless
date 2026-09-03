package controlplane

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestRecoverySkipsOutgoingProxiesForMockAndRemoteProviders(t *testing.T) {
	for _, provider := range []model.ProviderKind{model.ProviderMock, model.ProviderRemote} {
		t.Run(string(provider), func(t *testing.T) {
			ctx := t.Context()
			data, source := t.TempDir(), t.TempDir()
			store, err := database.Open(filepath.Join(data, "portless.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			process := func(name, dependency string) model.ServiceDefinition {
				return model.ServiceDefinition{
					Name: name, Kind: model.ServiceProcess, Required: true,
					Command:          []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"},
					WorkingDirectory: source, ServiceDirectory: source, PortEnvironment: "PORT",
					Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1", "PORTLESS_APPLICATION_HEALTH_DEPENDENCY": dependency},
					Health:      model.HealthCheck{Kind: "http", Path: "/health", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
				}
			}
			definition := model.ProjectModel{
				SuggestedName: "billing", PrimaryService: "checkout",
				Services: []model.ServiceDefinition{process("client", "CHECKOUT_URL"), process("checkout", "ORDERS_URL"), process("orders", "")},
				Connections: []model.Connection{
					{Source: "client", Target: "checkout", Protocol: model.ProtocolHTTP, Environment: "CHECKOUT_URL", Required: true},
					{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true},
				},
			}
			if _, err := store.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "billing", Services: []string{"client", "checkout", "orders"}}}); err != nil {
				t.Fatal(err)
			}
			originalBinding := model.ComponentBinding{Service: "checkout", Provider: model.ProviderLocal, Source: "billing"}
			if _, err := store.CreateEnvironment(ctx, "billing", "local", definition,
				[]model.SourceBinding{{Name: "billing", Path: source, Status: "ready", Definition: definition}},
				[]model.ComponentBinding{{Service: "client", Provider: model.ProviderLocal, Source: "billing"}, originalBinding, {Service: "orders", Provider: model.ProviderLocal, Source: "billing"}}); err != nil {
				t.Fatal(err)
			}
			app := New(store, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-one", Executable: os.Args[0]})
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, name := range []string{"client", "checkout", "orders"} {
					_ = app.processes.Stop(cleanupCtx, "billing/local", name, time.Second)
				}
				app.Close(cleanupCtx)
			}()
			var operation model.Operation
			if provider == model.ProviderMock {
				if _, err := app.CreateMockScenario(ctx, "billing", "local", model.MockScenario{Name: "sold-out"}, "test"); err != nil {
					t.Fatal(err)
				}
				if _, err := app.PutMockRoute(ctx, "billing", "local", "sold-out", model.MockRoute{Name: "health", Service: "checkout", Method: "GET", Path: "/health", Status: http.StatusNoContent, Enabled: true}, "test"); err != nil {
					t.Fatal(err)
				}
				operation, err = app.SetMockScenarioEnabled(ctx, "billing", "local", "sold-out", true, "test", "enable")
			} else {
				remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(http.StatusNoContent)
				}))
				defer remote.Close()
				operation, err = app.ChangeBinding(ctx, "billing", "local", "checkout", model.ComponentBinding{
					Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"},
				}, "test", "remote")
			}
			if err != nil {
				t.Fatal(err)
			}
			if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
				t.Fatalf("configure provider = %#v", operation)
			}
			operation, err = app.Up(ctx, "billing", "local", "test", "start", UpOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
				t.Fatalf("start = %#v", operation)
			}
			before, err := store.Environment(ctx, "billing", "local")
			if err != nil {
				t.Fatal(err)
			}
			incoming, err := store.ConnectionRuntime(ctx, "billing/local", "client", "checkout")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ConnectionRuntime(ctx, "billing/local", "checkout", "orders"); !errors.Is(err, database.ErrNotFound) {
				t.Fatalf("non-local caller unexpectedly owns an outgoing proxy: %v", err)
			}
			if !app.environmentRuntimeVerified(ctx, before) {
				t.Error("healthy runtime verification required an outgoing proxy for a non-local caller")
			}
			if ready, problems := app.CanHandoff(ctx); !ready {
				t.Errorf("non-local caller blocked a safe handoff: %v", problems)
			}

			// Recover both a normally running environment and the persisted unknown
			// state caused by attempting to recover a non-local caller's proxy.
			for _, instance := range []string{"daemon-two", "daemon-three"} {
				if instance == "daemon-three" {
					if err := store.SetServiceStatus(ctx, "billing/local", "checkout", model.ServiceUnknown, "dependency proxy checkout:orders could not be recovered: saved proxy port is missing"); err != nil {
						t.Fatal(err)
					}
					app.proxy.RemoveTarget("billing/local", "checkout")
					app.reconcileEnvironmentStatus(ctx, "billing/local")
				}
				app.Close(ctx)
				app = New(store, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: instance, Executable: os.Args[0]})
				report, err := app.Reconcile(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Unverifiable) != 0 || len(report.Recovered) != 1 {
					t.Fatalf("%s recovery = %#v", instance, report)
				}
				after, err := store.Environment(ctx, "billing", "local")
				if err != nil {
					t.Fatal(err)
				}
				if after.Status != model.EnvironmentHealthy || !app.environmentRuntimeVerified(ctx, after) {
					t.Fatalf("%s did not recover a verified healthy environment: %#v", instance, after)
				}
				for _, name := range []string{"client", "orders"} {
					previous, current := runtimeFor(before, name), runtimeFor(after, name)
					if current.PID != previous.PID || current.Generation != previous.Generation {
						t.Fatalf("%s was restarted during recovery: before=%#v after=%#v", name, previous, current)
					}
				}
				assertIngressStatus(t, app, http.StatusNoContent)
				restoredIncoming, err := store.ConnectionRuntime(ctx, "billing/local", "client", "checkout")
				if err != nil || restoredIncoming.ListenPort != incoming.ListenPort || restoredIncoming.OwnerInstanceID != instance {
					t.Fatalf("incoming caller proxy was not adopted: %#v, %v", restoredIncoming, err)
				}
				if _, err := store.ConnectionRuntime(ctx, "billing/local", "checkout", "orders"); !errors.Is(err, database.ErrNotFound) {
					t.Fatalf("recovery created an unnecessary outgoing proxy: %v", err)
				}
				if ready, problems := app.CanHandoff(ctx); !ready {
					t.Fatalf("%s blocked the next safe handoff: %v", instance, problems)
				}
			}

			if provider == model.ProviderMock {
				operation, err = app.SetMockScenarioEnabled(ctx, "billing", "local", "sold-out", false, "test", "disable")
			} else {
				operation, err = app.ChangeBinding(ctx, "billing", "local", "checkout", originalBinding, "test", "restore")
			}
			if err != nil {
				t.Fatal(err)
			}
			if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
				t.Fatalf("restore real provider after recovery = %#v", operation)
			}
			assertIngressStatus(t, app, http.StatusOK)
			outgoing, err := store.ConnectionRuntime(ctx, "billing/local", "checkout", "orders")
			if err != nil || outgoing.State != "ready" || outgoing.OwnerInstanceID != "daemon-three" {
				t.Fatalf("restored local caller did not acquire its outgoing proxy: %#v, %v", outgoing, err)
			}
			if provider == model.ProviderMock {
				if err := app.DeleteMockScenario(ctx, "billing", "local", "sold-out", "test"); err != nil {
					t.Fatalf("delete disabled scenario after recovery: %v", err)
				}
			}

			// A real caller must still fail closed when its proxy ownership is stale.
			outgoing.OwnerInstanceID = "unverified-owner"
			if err := store.SaveConnectionRuntime(ctx, "billing/local", outgoing); err != nil {
				t.Fatal(err)
			}
			after, err := store.Environment(ctx, "billing", "local")
			if err != nil {
				t.Fatal(err)
			}
			if app.environmentRuntimeVerified(ctx, after) {
				t.Error("local caller with stale proxy ownership was considered verified")
			}
			if ready, problems := app.CanHandoff(ctx); ready || !strings.Contains(strings.Join(problems, "; "), "checkout:orders: saved proxy ownership is stale") {
				t.Fatalf("local caller ownership safeguard was bypassed: ready=%v, problems=%v", ready, problems)
			}
		})
	}
}
