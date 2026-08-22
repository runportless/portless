package controlplane

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/networking"
)

func TestActiveProviderChangeRestartsOnlySelectedService(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	process := func(name string) model.ServiceDefinition {
		return model.ServiceDefinition{
			Name: name, Kind: model.ServiceProcess, Required: true,
			Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, WorkingDirectory: source, ServiceDirectory: source,
			PortEnvironment: "PORT", Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			Health: model.HealthCheck{Kind: "http", Path: "/health", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
		}
	}
	definition := model.ProjectModel{
		SuggestedName: "billing", PrimaryService: "checkout",
		Services:    []model.ServiceDefinition{process("checkout"), process("orders")},
		Connections: []model.Connection{{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "app", Services: []string{"checkout", "orders"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "app", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "app"}, {Service: "orders", Provider: model.ProviderLocal, Source: "app"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), "billing/local", "checkout", time.Second)
	defer app.processes.Stop(context.Background(), "billing/local", "orders", time.Second)

	up, err := app.Up(ctx, "billing", "local", "test", "provider-up", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if up = waitForOperation(t, app, up); up.State != "succeeded" {
		t.Fatalf("up operation = %#v", up)
	}
	beforeCheckout := serviceSnapshot(t, app, "checkout")
	beforeOrders := serviceSnapshot(t, app, "orders")
	unavailable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unavailableURL := unavailable.URL
	unavailable.Close()
	failed, err := app.ChangeBinding(ctx, "billing", "local", "orders", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: unavailableURL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health",
	}}, "test", "orders-unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if failed = waitForOperation(t, app, failed); failed.State != "failed" {
		t.Fatalf("unavailable remote operation = %#v", failed)
	}
	afterFailedCheckout := serviceSnapshot(t, app, "checkout")
	afterFailedOrders := serviceSnapshot(t, app, "orders")
	if afterFailedCheckout.PID != beforeCheckout.PID || afterFailedCheckout.Generation != beforeCheckout.Generation || afterFailedOrders.PID != beforeOrders.PID || afterFailedOrders.Generation != beforeOrders.Generation {
		t.Fatalf("failed preflight changed runtimes: checkout=%#v orders=%#v", afterFailedCheckout, afterFailedOrders)
	}
	failedEnvironment, err := app.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if bindingForEnvironment(failedEnvironment, "orders").Provider != model.ProviderLocal || failedEnvironment.Status != model.EnvironmentHealthy {
		t.Fatalf("failed preflight changed environment: %#v", failedEnvironment)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = writer.Write([]byte("remote orders"))
	}))
	defer remote.Close()

	change, err := app.ChangeBinding(ctx, "billing", "local", "orders", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health",
	}}, "test", "orders-remote")
	if err != nil {
		t.Fatal(err)
	}
	if change = waitForOperation(t, app, change); change.State != "succeeded" {
		t.Fatalf("local-to-remote operation = %#v", change)
	}
	afterRemoteCheckout := serviceSnapshot(t, app, "checkout")
	afterRemoteOrders := serviceSnapshot(t, app, "orders")
	if afterRemoteCheckout.PID != beforeCheckout.PID || afterRemoteCheckout.Generation != beforeCheckout.Generation || afterRemoteCheckout.Status != model.ServiceReady {
		t.Fatalf("unrelated checkout runtime changed: before=%#v after=%#v", beforeCheckout, afterRemoteCheckout)
	}
	if afterRemoteOrders.PID != 0 || afterRemoteOrders.Status != model.ServiceReady || afterRemoteOrders.Generation != beforeOrders.Generation {
		t.Fatalf("remote orders runtime = %#v", afterRemoteOrders)
	}
	response := httptest.NewRecorder()
	app.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://orders.local.billing.localhost/orders", nil), "billing/local", "orders")
	if response.Code != http.StatusOK || response.Body.String() != "remote orders" {
		t.Fatalf("remote ingress = %d %q", response.Code, response.Body.String())
	}

	change, err = app.ChangeBinding(ctx, "billing", "local", "orders", model.ComponentBinding{Provider: model.ProviderLocal, Source: "app"}, "test", "orders-local")
	if err != nil {
		t.Fatal(err)
	}
	if change = waitForOperation(t, app, change); change.State != "succeeded" {
		t.Fatalf("remote-to-local operation = %#v", change)
	}
	afterLocalCheckout := serviceSnapshot(t, app, "checkout")
	afterLocalOrders := serviceSnapshot(t, app, "orders")
	if afterLocalCheckout.PID != beforeCheckout.PID || afterLocalCheckout.Generation != beforeCheckout.Generation || afterLocalCheckout.Status != model.ServiceReady {
		t.Fatalf("checkout changed during remote-to-local handoff: before=%#v after=%#v", beforeCheckout, afterLocalCheckout)
	}
	if afterLocalOrders.PID == 0 || afterLocalOrders.Status != model.ServiceReady || afterLocalOrders.Generation != beforeOrders.Generation+1 {
		t.Fatalf("restored local orders runtime = %#v", afterLocalOrders)
	}
}

func TestActiveMockHandoffKeepsOtherStoreServicesRunning(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	process := func(name string) model.ServiceDefinition {
		return model.ServiceDefinition{
			Name: name, Kind: model.ServiceProcess, Required: true,
			Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, WorkingDirectory: source, ServiceDirectory: source,
			PortEnvironment: "PORT", Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			Health: model.HealthCheck{Kind: "http", Path: "/health", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
		}
	}
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "checkout",
		Services: []model.ServiceDefinition{process("checkout"), process("orders"), process("inventory")},
		Connections: []model.Connection{
			{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true},
			{Source: "checkout", Target: "inventory", Protocol: model.ProtocolHTTP, Environment: "INVENTORY_URL", Required: true},
		},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "store", Services: []string{"checkout", "orders", "inventory"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition,
		[]model.SourceBinding{{Name: "store", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "store"}, {Service: "orders", Provider: model.ProviderLocal, Source: "store"}, {Service: "inventory", Provider: model.ProviderLocal, Source: "store"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	for _, name := range []string{"checkout", "orders", "inventory"} {
		defer app.processes.Stop(context.Background(), "store/local", name, time.Second)
	}
	operation, err := app.Up(ctx, "store", "local", "test", "mock-store-up", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("up operation = %#v", operation)
	}
	checkoutBefore := scopedServiceSnapshot(t, app, "store", "local", "checkout")
	ordersBefore := scopedServiceSnapshot(t, app, "store", "local", "orders")
	inventoryBefore := scopedServiceSnapshot(t, app, "store", "local", "inventory")
	if _, err := app.CreateMockProfile(ctx, "store", "local", model.MockProfile{Name: "sold-out", Service: "inventory"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PutMockRoute(ctx, "store", "local", "sold-out", model.MockRoute{Name: "lookup", Method: "GET", Path: "/inventory/{sku}", Status: http.StatusConflict, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"available":false}`, Enabled: true}, "test"); err != nil {
		t.Fatal(err)
	}
	operation, err = app.ChangeBinding(ctx, "store", "local", "inventory", model.ComponentBinding{Provider: model.ProviderMock, Mock: &model.MockTarget{Profile: "sold-out"}}, "test", "inventory-mock")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("mock handoff = %#v", operation)
	}
	checkoutMock := scopedServiceSnapshot(t, app, "store", "local", "checkout")
	ordersMock := scopedServiceSnapshot(t, app, "store", "local", "orders")
	inventoryMock := scopedServiceSnapshot(t, app, "store", "local", "inventory")
	if checkoutMock.PID != checkoutBefore.PID || ordersMock.PID != ordersBefore.PID || inventoryMock.PID != 0 || inventoryMock.Status != model.ServiceReady || inventoryMock.Generation != inventoryBefore.Generation {
		t.Fatalf("mock handoff changed the wrong runtimes: checkout=%#v orders=%#v inventory=%#v", checkoutMock, ordersMock, inventoryMock)
	}
	response := httptest.NewRecorder()
	app.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://inventory.local.store.localhost/inventory/coffee", nil), "store/local", "inventory")
	if response.Code != http.StatusConflict || response.Body.String() != `{"available":false}` {
		t.Fatalf("mock ingress = %d %q", response.Code, response.Body.String())
	}
	exchanges := app.TrafficExchanges("store", "local", 1)
	if len(exchanges) != 1 || exchanges[0].TargetProvider != model.ProviderMock || exchanges[0].MockProfile != "sold-out" || exchanges[0].MockRoute != "lookup" {
		t.Fatalf("mock exchange = %#v", exchanges)
	}
	operation, err = app.StopService(ctx, "store", "local", "inventory", "test", "stop-mock-inventory")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("stop mock service = %#v", operation)
	}
	if _, active := app.mocks.Address("store/local", "inventory"); active {
		t.Fatal("mock listener remained active after stopping the service")
	}
	if _, err := app.PutMockRoute(ctx, "store", "local", "sold-out", model.MockRoute{Name: "lookup", Method: "GET", Path: "/inventory/{sku}", Status: http.StatusGone, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"available":false,"updated":true}`, Enabled: true}, "test"); err != nil {
		t.Fatal(err)
	}
	if stopped := scopedServiceSnapshot(t, app, "store", "local", "inventory"); stopped.Status != model.ServiceStopped {
		t.Fatalf("editing a stopped mock route restarted the service: %#v", stopped)
	}
	if _, active := app.mocks.Address("store/local", "inventory"); active {
		t.Fatal("editing a stopped mock route recreated its listener")
	}
	operation, err = app.StartService(ctx, "store", "local", "inventory", "test", "start-mock-inventory")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("start mock service = %#v", operation)
	}
	response = httptest.NewRecorder()
	app.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://inventory.local.store.localhost/inventory/coffee", nil), "store/local", "inventory")
	if response.Code != http.StatusGone || response.Body.String() != `{"available":false,"updated":true}` {
		t.Fatalf("restarted mock ingress = %d %q", response.Code, response.Body.String())
	}
	operation, err = app.ChangeBinding(ctx, "store", "local", "inventory", model.ComponentBinding{Provider: model.ProviderLocal, Source: "store"}, "test", "inventory-local")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("local handoff = %#v", operation)
	}
	checkoutAfter := scopedServiceSnapshot(t, app, "store", "local", "checkout")
	ordersAfter := scopedServiceSnapshot(t, app, "store", "local", "orders")
	inventoryAfter := scopedServiceSnapshot(t, app, "store", "local", "inventory")
	if checkoutAfter.PID != checkoutBefore.PID || ordersAfter.PID != ordersBefore.PID || inventoryAfter.PID == 0 || inventoryAfter.Generation != inventoryBefore.Generation+1 {
		t.Fatalf("local restore changed the wrong runtimes: checkout=%#v orders=%#v inventory=%#v", checkoutAfter, ordersAfter, inventoryAfter)
	}
}

func scopedServiceSnapshot(t *testing.T, app *Service, project, environment, name string) model.Service {
	t.Helper()
	snapshot, err := app.Environment(context.Background(), project, environment)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range snapshot.Services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service %s was not found in %s/%s", name, project, environment)
	return model.Service{}
}

func TestUpStartsFocusedServiceUnderPortlessWithDebugger(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	definition := model.ProjectModel{
		SuggestedName: "billing", PrimaryService: "checkout",
		Services: []model.ServiceDefinition{{
			Name: "checkout", Kind: model.ServiceProcess, Framework: "nestjs", Required: true,
			Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, WorkingDirectory: source, ServiceDirectory: source,
			Debug:           &model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNestCLI, Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}},
			PortEnvironment: "PORT", Environment: map[string]string{"LOG_LEVEL": "debug", "PORTLESS_APPLICATION_TEST_HELPER": "1"},
			Health: model.HealthCheck{Kind: "http", Path: "/health", Timeout: time.Second, Interval: 20 * time.Millisecond},
		}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "billing", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "billing", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "billing"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-one", Executable: os.Args[0]})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), "billing/local", "checkout", time.Second)

	operation, err := app.Up(ctx, "billing", "local", "test", "debug-up", UpOptions{DebugServices: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("up operation = %#v", operation)
	}
	service := serviceSnapshot(t, app, "checkout")
	if service.Status != model.ServiceReady || service.LaunchMode != model.LaunchDebug || service.Debugger == nil || service.Debugger.State != "listening" || service.PID == 0 {
		t.Fatalf("debug service = %#v", service)
	}
	environment, err := app.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if environment.Status != model.EnvironmentHealthy || environment.Reason != "" {
		t.Fatalf("debug environment health = %s (%q), want healthy with no reason", environment.Status, environment.Reason)
	}
	assertIngressStatus(t, app, http.StatusOK)
	response, err := http.Get("http://" + net.JoinHostPort(service.Debugger.Host, strconv.Itoa(service.Debugger.Port)) + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("debugger status = %s", response.Status)
	}
	debugPort := service.Debugger.Port
	managed, err := app.ManageService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if managed = waitForOperation(t, app, managed); managed.State != "succeeded" {
		t.Fatalf("manage operation = %#v", managed)
	}
	service = serviceSnapshot(t, app, "checkout")
	if service.LaunchMode != model.LaunchManaged || service.Debugger != nil || service.Status != model.ServiceReady || service.Generation != 2 {
		t.Fatalf("managed service = %#v", service)
	}
	probe, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(probe, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(debugPort)+"/json/list", nil)
	if response, requestErr := http.DefaultClient.Do(request); requestErr == nil {
		response.Body.Close()
		t.Fatal("old debugger remained reachable after returning to managed mode")
	}
}

func TestReconcileAdoptsDebugProcessAndDebugger(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	definition := model.ProjectModel{
		SuggestedName: "billing", PrimaryService: "checkout",
		Services: []model.ServiceDefinition{{
			Name: "checkout", Kind: model.ServiceProcess, Framework: "nestjs", Required: true,
			Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, WorkingDirectory: source, ServiceDirectory: source,
			Debug:           &model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNestCLI, Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}},
			Environment:     map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			PortEnvironment: "PORT",
			Health:          model.HealthCheck{Kind: "http", Path: "/health", Timeout: time.Second, Interval: 20 * time.Millisecond},
		}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "billing", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "billing", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "billing"}}); err != nil {
		t.Fatal(err)
	}

	first := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-one", Executable: os.Args[0]})
	operation, err := first.Up(ctx, "billing", "local", "test", "debug-up", UpOptions{DebugServices: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, first, operation); operation.State != "succeeded" {
		t.Fatalf("up operation = %#v", operation)
	}
	before := serviceSnapshot(t, first, "checkout")
	first.Close(ctx)

	second := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-two", Executable: os.Args[0]})
	defer second.Close(ctx)
	defer second.processes.Stop(context.Background(), "billing/local", "checkout", time.Second)
	report, err := second.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unverifiable) != 0 || len(report.Recovered) != 1 || report.Recovered[0] != "billing/local" {
		t.Fatalf("reconciliation report = %#v", report)
	}
	recovered := serviceSnapshot(t, second, "checkout")
	if recovered.LaunchMode != model.LaunchDebug || recovered.Debugger == nil || recovered.Debugger.State != "listening" || recovered.Debugger.Port != before.Debugger.Port || recovered.PID != before.PID {
		t.Fatalf("debug process changed across daemon restart: before=%#v after=%#v", before, recovered)
	}
	assertIngressStatus(t, second, http.StatusOK)
	if ok, reasons := second.CanHandoff(ctx); !ok {
		t.Fatalf("recovered debug environment cannot hand off: %v", reasons)
	}
}

func TestIndividualServiceLifecycleIsIdempotentAndCountsRestarts(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing",
		Services: []model.ServiceDefinition{{
			Name: "checkout", Kind: model.ServiceProcess, Required: true,
			Command:         []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"},
			Environment:     map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			PortEnvironment: "PORT",
			Health:          model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
		}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, sources, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), model.EnvironmentSelector("billing", "local"), "checkout", time.Second)

	started, err := app.StartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	concurrent, err := app.StartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	started = waitForOperation(t, app, started)
	concurrent = waitForOperation(t, app, concurrent)
	if started.State != "succeeded" {
		t.Fatalf("start operation = %#v", started)
	}
	if concurrent.State != "succeeded" {
		t.Fatalf("concurrent idempotent start = %#v", concurrent)
	}
	service := serviceSnapshot(t, app, "checkout")
	if service.Status != model.ServiceReady || service.Generation != 1 || service.RestartCount != 0 {
		t.Fatalf("started service = %#v", service)
	}

	again, err := app.StartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	again = waitForOperation(t, app, again)
	if again.State != "succeeded" || serviceSnapshot(t, app, "checkout").Generation != 1 {
		t.Fatalf("idempotent start changed the running generation: %#v", again)
	}

	restarted, err := app.RestartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	restarted = waitForOperation(t, app, restarted)
	service = serviceSnapshot(t, app, "checkout")
	if restarted.State != "succeeded" || service.Status != model.ServiceReady || service.Generation != 2 || service.RestartCount != 1 {
		t.Fatalf("restarted service = %#v, operation = %#v", service, restarted)
	}

	stopped, err := app.StopService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	stopped = waitForOperation(t, app, stopped)
	service = serviceSnapshot(t, app, "checkout")
	if stopped.State != "succeeded" || service.Status != model.ServiceStopped || service.Generation != 2 || service.RestartCount != 1 {
		t.Fatalf("stopped service = %#v, operation = %#v", service, stopped)
	}

	stoppedAgain, err := app.StopService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitForOperation(t, app, stoppedAgain); completed.State != "succeeded" {
		t.Fatalf("idempotent stop = %#v", completed)
	}
}

func TestEffectiveConnectionsAndConfigurationExplainRemoteTargetsAndMaskCredentials(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess, Environment: map[string]string{"DATABASE_URL": "postgresql://user:secret@localhost/billing", "JDBC_URL": "jdbc:postgresql://user:secret@localhost/billing", "LOG_LEVEL": "debug"}},
			{Name: "payments", Kind: model.ServiceProcess},
		},
		Connections: []model.Connection{{Source: "checkout", Target: "payments", Protocol: model.ProtocolHTTP, Environment: "PAYMENTS_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	remoteURL := "https://payments.qa.example.test"
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "hybrid", definition, nil, []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remoteURL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}},
	}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	connections, err := app.Connections(ctx, "billing", "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].TargetProvider != model.ProviderRemote || connections[0].RuntimeTarget != remoteURL || connections[0].InjectedEnvironment["PAYMENTS_URL"] == "" {
		t.Fatalf("effective connections = %#v", connections)
	}
	configuration, err := app.ServiceConfiguration(ctx, "billing", "hybrid", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]model.ConfigurationValue)
	for _, value := range configuration.Environment {
		values[value.Key] = value
	}
	if values["DATABASE_URL"].Classification != "masked" || values["DATABASE_URL"].Value != "••••••••" {
		t.Fatalf("credential URL was exposed: %#v", values["DATABASE_URL"])
	}
	if values["JDBC_URL"].Classification != "masked" || values["JDBC_URL"].Value != "••••••••" {
		t.Fatalf("JDBC credential URL was exposed: %#v", values["JDBC_URL"])
	}
	if values["LOG_LEVEL"].Value != "debug" || values["LOG_LEVEL"].Classification != "public" {
		t.Fatalf("public configuration was not preserved: %#v", values["LOG_LEVEL"])
	}
	if values["PAYMENTS_URL"].Classification != "generated" {
		t.Fatalf("generated connection value was not explained: %#v", values["PAYMENTS_URL"])
	}
}

func TestStableTCPServiceAndConnectionEndpointsAreExposedBeforeStartup(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "orders",
		Services: []model.ServiceDefinition{
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432},
		},
		Connections: []model.Connection{{Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, Binding: "postgres", Environment: "DATABASE_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	environment, err := app.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	var postgres model.Service
	for _, service := range environment.Services {
		if service.Name == "postgres" {
			postgres = service
			break
		}
	}
	if len(postgres.Endpoints) != 1 || postgres.Endpoints[0].Host != "postgres.local.store.portless.test" || postgres.Endpoints[0].Port != 5432 {
		t.Fatalf("public Postgres endpoint = %#v", postgres.Endpoints)
	}
	connections, err := app.Connections(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].Endpoint == nil || connections[0].Endpoint.Host != "postgres.via-orders.local.store.portless.test" || connections[0].Endpoint.Port != 5432 {
		t.Fatalf("directed Postgres endpoint = %#v", connections)
	}
	if connections[0].InjectedEnvironment["DATABASE_URL"] != "not active" {
		t.Fatalf("stopped connection was reported as injected: %#v", connections[0])
	}
}

func TestPrivateTCPIngressUsesEphemeralLoopbackWithoutPublishingStableEndpoints(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "orders",
		Services: []model.ServiceDefinition{
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432},
		},
		Connections: []model.Connection{{Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, Environment: "DATABASE_ADDRESS", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", PrivateTCPIngress: true})
	defer app.Close(ctx)
	app.proxy.SetTargetProvider("store/local", "postgres", 49152, model.ProviderContainer)

	environment, err := controlStore.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	values, err := app.prepareProcessEnvironment(ctx, environment, definition.Services[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	host, encodedPort, err := net.SplitHostPort(values["DATABASE_ADDRESS"])
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("private TCP binding = %q: host=%q err=%v", values["DATABASE_ADDRESS"], host, err)
	}
	port, err := strconv.Atoi(encodedPort)
	if err != nil || port == 0 {
		t.Fatalf("private TCP port = %q: %v", encodedPort, err)
	}
	runtime, err := controlStore.ConnectionRuntime(ctx, "store/local", "orders", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ListenIP != "127.0.0.1" || runtime.ListenPort != port || runtime.DNSName != "" {
		t.Fatalf("private TCP runtime = %#v", runtime)
	}
	if err := app.ensurePublicTCPProxies(ctx, environment); err != nil {
		t.Fatal(err)
	}
	allocations, err := controlStore.NetworkAllocations(ctx, "store/local")
	if err != nil {
		t.Fatal(err)
	}
	for _, allocation := range allocations {
		if allocation.Kind == networking.AllocationPublic && app.proxy.HasEdgeAtAddress("store/local", "external", allocation.Target, allocation.Address()) {
			t.Fatalf("private E2E mode published stable endpoint %s", allocation.Address())
		}
	}
	connections, err := app.Connections(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].Endpoint == nil || connections[0].Endpoint.Address != net.JoinHostPort(host, encodedPort) || connections[0].InjectedEnvironment["DATABASE_ADDRESS"] != values["DATABASE_ADDRESS"] {
		t.Fatalf("private effective connection = %#v", connections)
	}
}
