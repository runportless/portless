//go:build e2e

package e2e_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
)

func TestCLIDownAllWorksFromAmbiguousCheckout(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "down-all-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start local environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	if output, err := runCLIAt(binary, home, checkout, "env", "clone", "qa-local"); err != nil {
		t.Fatalf("clone environment: %v\n%s", err, output)
	}
	clone := explicitEnvironmentStatus(t, binary, home, checkout, "down-all-e2e/qa-local")
	if clone.Status != model.EnvironmentStopped {
		t.Fatalf("cloned environment status = %s, want stopped", clone.Status)
	}

	if output, err := runCLIAt(binary, home, checkout, "down", "--timeout", "2m"); err == nil {
		t.Fatalf("ordinary down unexpectedly selected one of two environments:\n%s", output)
	} else if !strings.Contains(output, "this checkout belongs to multiple environments") ||
		!strings.Contains(output, "down-all-e2e/local") || !strings.Contains(output, "down-all-e2e/qa-local") {
		t.Fatalf("ordinary down did not explain the ambiguity: %v\n%s", err, output)
	}

	output, err := runCLIAt(binary, home, checkout, "down", "--all", "--timeout", "2m")
	if err != nil {
		t.Fatalf("down --all failed from an ambiguous checkout: %v\n%s", err, output)
	}
	if !strings.Contains(output, "down-all-e2e/local") || !strings.Contains(output, "stopped") {
		t.Fatalf("down --all output did not include the active environment:\n%s", output)
	}
	for _, selector := range []string{"down-all-e2e/local", "down-all-e2e/qa-local"} {
		environment := explicitEnvironmentStatus(t, binary, home, checkout, selector)
		if environment.Status != model.EnvironmentStopped {
			t.Fatalf("%s status = %s, want stopped", selector, environment.Status)
		}
		for _, service := range environment.Services {
			if service.PID != 0 {
				t.Fatalf("%s service %s survived down --all: %#v", selector, service.Name, service)
			}
			if selector == "down-all-e2e/local" && service.Status != model.ServiceStopped {
				t.Fatalf("active service %s status after down --all = %s, want stopped", service.Name, service.Status)
			}
			if selector == "down-all-e2e/qa-local" && service.Status != model.ServicePlanned && service.Status != model.ServiceStopped {
				t.Fatalf("never-started service %s has active status after down --all: %#v", service.Name, service)
			}
		}
	}
}

func TestCLIServiceLifecycleOnlyMutatesTheSelectedService(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "service-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	before := environmentStatus(t, binary, home, checkout)
	checkoutBefore := requireService(t, before, "checkout")
	inventoryBefore := requireService(t, before, "inventory")
	ordersBefore := requireService(t, before, "orders")

	if output, err := runCLIAt(binary, home, checkout, "service", "stop", "orders", "--timeout", "2m"); err != nil {
		t.Fatalf("stop orders: %v\n%s", err, output)
	}
	stopped := environmentStatus(t, binary, home, checkout)
	assertSameServiceProcess(t, checkoutBefore, requireService(t, stopped, "checkout"))
	assertSameServiceProcess(t, inventoryBefore, requireService(t, stopped, "inventory"))
	ordersStopped := requireService(t, stopped, "orders")
	if ordersStopped.Status != model.ServiceStopped || ordersStopped.PID != 0 {
		t.Fatalf("orders after stop = %#v", ordersStopped)
	}

	failed := applicationRequest(t, home, "checkout.local.service-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	failedBody, readErr := io.ReadAll(failed.Body)
	failed.Body.Close()
	if readErr != nil || failed.StatusCode != http.StatusBadGateway || !strings.Contains(string(failedBody), "orders:") {
		t.Fatalf("checkout did not expose the stopped dependency: status=%s err=%v body=%s", failed.Status, readErr, failedBody)
	}

	if output, err := runCLIAt(binary, home, checkout, "service", "start", "orders", "--timeout", "2m"); err != nil {
		t.Fatalf("start orders: %v\n%s", err, output)
	}
	started := environmentStatus(t, binary, home, checkout)
	assertSameServiceProcess(t, checkoutBefore, requireService(t, started, "checkout"))
	assertSameServiceProcess(t, inventoryBefore, requireService(t, started, "inventory"))
	ordersStarted := requireService(t, started, "orders")
	if ordersStarted.Status != model.ServiceReady || ordersStarted.PID == 0 || ordersStarted.Generation <= ordersBefore.Generation {
		t.Fatalf("orders was not started as a new generation: before=%#v after=%#v", ordersBefore, ordersStarted)
	}

	if output, err := runCLIAt(binary, home, checkout, "service", "restart", "checkout", "--timeout", "2m"); err != nil {
		t.Fatalf("restart checkout: %v\n%s", err, output)
	}
	restarted := environmentStatus(t, binary, home, checkout)
	checkoutRestarted := requireService(t, restarted, "checkout")
	if checkoutRestarted.Status != model.ServiceReady || checkoutRestarted.PID == 0 || checkoutRestarted.Generation <= checkoutBefore.Generation {
		t.Fatalf("checkout was not restarted as a new generation: before=%#v after=%#v", checkoutBefore, checkoutRestarted)
	}
	assertSameServiceProcess(t, inventoryBefore, requireService(t, restarted, "inventory"))
	assertSameServiceProcess(t, ordersStarted, requireService(t, restarted, "orders"))

	recovered := applicationRequest(t, home, "checkout.local.service-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	recovered.Body.Close()
	if recovered.StatusCode != http.StatusOK {
		t.Fatalf("checkout did not recover after service lifecycle operations: %s", recovered.Status)
	}
}

func requireService(t *testing.T, environment model.Environment, name string) model.Service {
	t.Helper()
	for _, service := range environment.Services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service %s not found in %#v", name, environment.Services)
	return model.Service{}
}

func assertSameServiceProcess(t *testing.T, before, after model.Service) {
	t.Helper()
	if after.Status != model.ServiceReady || after.PID != before.PID || after.Generation != before.Generation {
		t.Fatalf("service %s was unexpectedly replaced: before=%#v after=%#v", before.Name, before, after)
	}
}
