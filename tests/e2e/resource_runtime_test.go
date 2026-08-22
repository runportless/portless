//go:build e2e

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
)

const managedResourceE2EEnvironment = "PORTLESS_MANAGED_RESOURCE_E2E"

func TestCLIManagedResourcePluginLifecycle(t *testing.T) {
	if os.Getenv(managedResourceE2EEnvironment) != "1" {
		t.Skip(managedResourceE2EEnvironment + "=1 is required for real container lifecycle coverage")
	}
	cases := []struct {
		name         string
		resource     string
		resourceType string
		marker       string
		environment  string
		prefix       string
	}{
		{name: "postgres", resource: "postgres", resourceType: "postgres", marker: "DATABASE_URL=postgresql://postgres/portless\n", environment: "DATABASE_URL", prefix: "postgresql://"},
		{name: "valkey", resource: "redis", resourceType: "valkey", marker: "REDIS_URL=redis://redis\n", environment: "REDIS_URL", prefix: "redis://"},
		{name: "mysql", resource: "mysql", resourceType: "mysql", marker: "MYSQL_URL=mysql://mysql/portless\n", environment: "MYSQL_URL", prefix: "mysql://"},
		{name: "nats", resource: "nats", resourceType: "nats", marker: "NATS_URL=nats://nats\n", environment: "NATS_URL", prefix: "nats://"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			binary := e2eBinary(t)
			home, checkout := isolatedFixture(t, "store-lite")
			defer cleanupInstallation(t, binary, home, checkout)
			appendResourceMarker(t, checkout, testCase.marker)
			selectRequestedRuntime(t, binary, home, checkout)

			project := "managed-" + testCase.name
			if output, err := runCLIAt(binary, home, checkout, "up", "--name", project, "--no-open", "--timeout", "4m"); err != nil {
				t.Fatalf("start %s resource environment: %v\n%s\ndaemon log:\n%s", testCase.name, err, output, readDaemonLog(home))
			}
			environment := environmentStatus(t, binary, home, checkout)
			if environment.Status != model.EnvironmentHealthy {
				t.Fatalf("%s environment status = %s: %#v", testCase.name, environment.Status, environment)
			}
			resource := requireService(t, environment, testCase.resource)
			assertReadyManagedResource(t, resource, testCase.resourceType)
			address := managedResourceProbeAddress(t, resource)
			probeManagedResource(t, testCase.resourceType, address)
			assertGeneratedResourceBinding(t, binary, home, checkout, "checkout", testCase.environment, testCase.prefix)

			if testCase.resourceType == "valkey" {
				if response := valkeyCommand(t, address, "SET", "portless-e2e", "preserved"); response != "OK" {
					t.Fatalf("Valkey SET response = %q", response)
				}
				if response := valkeyCommand(t, address, "SAVE"); response != "OK" {
					t.Fatalf("Valkey SAVE response = %q", response)
				}
			}

			beforeRestart := resource
			if output, err := runCLIAt(binary, home, checkout, "daemon", "restart", "--timeout", "30s"); err != nil {
				t.Fatalf("restart daemon with %s active: %v\n%s", testCase.name, err, output)
			}
			afterRestart := requireService(t, environmentStatus(t, binary, home, checkout), testCase.resource)
			if afterRestart.Status != model.ServiceReady || afterRestart.Generation != beforeRestart.Generation || afterRestart.UpstreamPort != beforeRestart.UpstreamPort {
				t.Fatalf("%s container was replaced instead of adopted: before=%#v after=%#v", testCase.name, beforeRestart, afterRestart)
			}

			if output, err := runCLIAt(binary, home, checkout, "down", "--timeout", "3m"); err != nil {
				t.Fatalf("stop %s without deleting volumes: %v\n%s", testCase.name, err, output)
			}
			if output, err := runCLIAt(binary, home, checkout, "up", "--managed", "--no-open", "--timeout", "4m"); err != nil {
				t.Fatalf("restart %s after ordinary down: %v\n%s", testCase.name, err, output)
			}
			resource = requireService(t, environmentStatus(t, binary, home, checkout), testCase.resource)
			assertReadyManagedResource(t, resource, testCase.resourceType)
			if testCase.resourceType == "valkey" {
				address = managedResourceProbeAddress(t, resource)
				if value := valkeyCommand(t, address, "GET", "portless-e2e"); value != "preserved" {
					t.Fatalf("Valkey volume did not survive ordinary down/up: %q", value)
				}
				if output, err := runCLIAt(binary, home, checkout, "down", "--volumes", "--yes", "--timeout", "3m"); err != nil {
					t.Fatalf("stop Valkey and delete its volume: %v\n%s", err, output)
				}
				if output, err := runCLIAt(binary, home, checkout, "up", "--managed", "--no-open", "--timeout", "4m"); err != nil {
					t.Fatalf("restart Valkey after volume deletion: %v\n%s", err, output)
				}
				resource = requireService(t, environmentStatus(t, binary, home, checkout), testCase.resource)
				if value := valkeyCommand(t, managedResourceProbeAddress(t, resource), "GET", "portless-e2e"); value != "" {
					t.Fatalf("Valkey data survived --volumes deletion: %q", value)
				}
			}
		})
	}
}

func TestCLIManagedResourceRebootRecovery(t *testing.T) {
	if os.Getenv(managedResourceE2EEnvironment) != "1" {
		t.Skip(managedResourceE2EEnvironment + "=1 is required for real container lifecycle coverage")
	}
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	appendResourceMarker(t, checkout, "REDIS_URL=redis://redis\n")
	selectRequestedRuntime(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "resource-reboot-e2e", "--no-open", "--timeout", "4m"); err != nil {
		t.Fatalf("start managed-resource reboot environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	before := environmentStatus(t, binary, home, checkout)
	redisBefore := requireService(t, before, "redis")
	if response := valkeyCommand(t, managedResourceProbeAddress(t, redisBefore), "SET", "reboot-proof", "preserved"); response != "OK" {
		t.Fatalf("Valkey SET response = %q", response)
	}
	if response := valkeyCommand(t, managedResourceProbeAddress(t, redisBefore), "SAVE"); response != "OK" {
		t.Fatalf("Valkey SAVE response = %q", response)
	}
	selector := "resource-reboot-e2e/local"
	processes := persistedProcessRuntimes(t, home, selector)
	resource := persistedServiceRuntime(t, home, selector, "redis")
	if resource.ContainerName == "" {
		t.Fatal("managed Valkey runtime has no persisted container name")
	}
	runtimeName := selectedManagedRuntime(t, binary, home, checkout)
	daemon := daemonStatus(t, binary, home, checkout)
	strandReadyProcessRuntimes(t, daemon.PID, processes)

	stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	stopCommand := exec.CommandContext(stopContext, runtimeName, "stop", resource.ContainerName)
	stopOutput, stopErr := stopCommand.CombinedOutput()
	cancel()
	if stopErr != nil {
		t.Fatalf("stop isolated managed container %s with %s: %v\n%s", resource.ContainerName, runtimeName, stopErr, stopOutput)
	}

	output, err := runCLIAt(binary, home, checkout, "up", "--no-open", "--timeout", "4m")
	if err != nil {
		t.Fatalf("up did not recover stopped managed resource: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	after := environmentStatus(t, binary, home, checkout)
	if after.Status != model.EnvironmentHealthy {
		t.Fatalf("managed-resource environment after reboot recovery = %s: %#v", after.Status, after)
	}
	redisAfter := requireService(t, after, "redis")
	if redisAfter.Status != model.ServiceReady || redisAfter.Generation != redisBefore.Generation+1 || redisAfter.UpstreamPort == 0 {
		t.Fatalf("Valkey container was not recreated at the next generation: before=%#v after=%#v", redisBefore, redisAfter)
	}
	if value := valkeyCommand(t, managedResourceProbeAddress(t, redisAfter), "GET", "reboot-proof"); value != "preserved" {
		t.Fatalf("Valkey volume did not survive reboot recovery: %q", value)
	}
	for _, previous := range before.Services {
		if previous.Kind != model.ServiceProcess {
			continue
		}
		current := requireService(t, after, previous.Name)
		if current.Status != model.ServiceReady || current.PID == 0 || current.PID == previous.PID || current.Generation != previous.Generation+1 {
			t.Fatalf("process service %s was not restarted after reboot: before=%#v after=%#v", previous.Name, previous, current)
		}
	}
	recoveredDaemon := daemonStatus(t, binary, home, checkout)
	if recoveredDaemon.RuntimeState != "ready" || !recoveredDaemon.HandoffReady || len(recoveredDaemon.RecoveryProblems) != 0 {
		t.Fatalf("daemon remained unhealthy after managed-resource recovery: %#v", recoveredDaemon)
	}
}

func TestCLIManagedResourcesAreIsolatedAcrossEnvironments(t *testing.T) {
	if os.Getenv(managedResourceE2EEnvironment) != "1" {
		t.Skip(managedResourceE2EEnvironment + "=1 is required for real container isolation coverage")
	}
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	appendResourceMarker(t, checkout, "REDIS_URL=redis://redis\n")
	selectRequestedRuntime(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "resource-isolation", "--no-open", "--timeout", "4m"); err != nil {
		t.Fatalf("start local resource environment: %v\n%s", err, output)
	}
	local := environmentStatus(t, binary, home, checkout)
	if len(local.Sources) != 1 {
		t.Fatalf("local source bindings = %#v", local.Sources)
	}
	secondCheckout := filepath.Join(filepath.Dir(checkout), "store-lite-isolated")
	if err := copyDirectory(checkout, secondCheckout); err != nil {
		t.Fatal(err)
	}
	if output, err := runCLIAt(binary, home, checkout, "env", "clone", "isolated"); err != nil {
		t.Fatalf("clone resource environment: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "--env", "resource-isolation/isolated", "env", "checkout", "set", local.Sources[0].Name, "--path", secondCheckout); err != nil {
		t.Fatalf("bind isolated checkout: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, secondCheckout, "--env", "resource-isolation/isolated", "up", "--managed", "--no-open", "--timeout", "4m"); err != nil {
		t.Fatalf("start isolated resource environment: %v\n%s", err, output)
	}

	local = explicitEnvironmentStatus(t, binary, home, checkout, "resource-isolation/local")
	isolated := explicitEnvironmentStatus(t, binary, home, checkout, "resource-isolation/isolated")
	localRedis := requireService(t, local, "redis")
	isolatedRedis := requireService(t, isolated, "redis")
	localAddress, isolatedAddress := managedResourceProbeAddress(t, localRedis), managedResourceProbeAddress(t, isolatedRedis)
	if localAddress == isolatedAddress || localRedis.UpstreamPort == isolatedRedis.UpstreamPort {
		t.Fatalf("resource environments share runtime endpoints: local=%#v isolated=%#v", localRedis, isolatedRedis)
	}
	if response := valkeyCommand(t, localAddress, "SET", "environment", "local"); response != "OK" {
		t.Fatalf("set local-only Valkey key: %q", response)
	}
	if value := valkeyCommand(t, isolatedAddress, "GET", "environment"); value != "" {
		t.Fatalf("isolated environment observed local data: %q", value)
	}

	localGeneration, isolatedGeneration := localRedis.Generation, isolatedRedis.Generation
	localPort, isolatedPort := localRedis.UpstreamPort, isolatedRedis.UpstreamPort
	if output, err := runCLIAt(binary, home, checkout, "daemon", "restart", "--timeout", "30s"); err != nil {
		t.Fatalf("restart daemon with isolated resources: %v\n%s", err, output)
	}
	localRedis = requireService(t, explicitEnvironmentStatus(t, binary, home, checkout, "resource-isolation/local"), "redis")
	isolatedRedis = requireService(t, explicitEnvironmentStatus(t, binary, home, checkout, "resource-isolation/isolated"), "redis")
	if localRedis.Generation != localGeneration || isolatedRedis.Generation != isolatedGeneration || localRedis.UpstreamPort != localPort || isolatedRedis.UpstreamPort != isolatedPort {
		t.Fatalf("daemon restart did not adopt isolated containers: local=%#v isolated=%#v", localRedis, isolatedRedis)
	}
	if output, err := runCLIAt(binary, home, checkout, "down", "--all", "--timeout", "3m"); err != nil {
		t.Fatalf("down --all with two resource environments: %v\n%s", err, output)
	}
}

func selectRequestedRuntime(t *testing.T, binary, home, checkout string) {
	t.Helper()
	preference := strings.TrimSpace(os.Getenv("PORTLESS_MANAGED_RESOURCE_RUNTIME"))
	if preference == "" {
		preference = "auto"
	}
	if preference != "auto" && preference != "docker" && preference != "podman" {
		t.Fatalf("PORTLESS_MANAGED_RESOURCE_RUNTIME must be auto, docker, or podman; got %q", preference)
	}
	if output, err := runCLIAt(binary, home, checkout, "runtime", "use", preference); err != nil {
		t.Fatalf("select %s runtime: %v\n%s", preference, err, output)
	}
	output, err := runCLIAt(binary, home, checkout, "--json", "runtime", "status")
	if err != nil {
		t.Fatalf("inspect selected runtime: %v\n%s", err, output)
	}
	var status struct {
		Selected string `json:"selected"`
		State    string `json:"state"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("decode runtime status: %v\n%s", err, output)
	}
	if status.Selected == "" || status.State != "ready" {
		t.Fatalf("managed-resource E2E runtime is unavailable: %#v", status)
	}
	if preference != "auto" && status.Selected != preference {
		t.Fatalf("runtime selected %q, want %q: %#v", status.Selected, preference, status)
	}
}

func selectedManagedRuntime(t *testing.T, binary, home, checkout string) string {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--json", "runtime", "status")
	if err != nil {
		t.Fatalf("inspect selected runtime: %v\n%s", err, output)
	}
	var status struct {
		Selected string `json:"selected"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("decode selected runtime: %v\n%s", err, output)
	}
	if status.State != "ready" || status.Selected == "" {
		t.Fatalf("managed runtime is not ready: %#v", status)
	}
	return status.Selected
}

func persistedServiceRuntime(t *testing.T, home, selector, service string) database.ServiceRuntimeRecord {
	t.Helper()
	controlStore, err := database.Open(filepath.Join(home, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, runtimeErr := controlStore.ServiceRuntime(context.Background(), selector, service)
	closeErr := controlStore.Close()
	if runtimeErr != nil {
		t.Fatal(runtimeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return runtime
}

func appendResourceMarker(t *testing.T, checkout, marker string) {
	t.Helper()
	path := filepath.Join(checkout, "apps", "checkout", ".env.example")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte(marker)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertReadyManagedResource(t *testing.T, service model.Service, resourceType string) {
	t.Helper()
	if service.Kind != model.ServiceResource || service.Resource == nil || service.Resource.Type != resourceType || service.Status != model.ServiceReady || service.UpstreamPort == 0 || service.Generation == 0 {
		t.Fatalf("managed %s service is not ready: %#v", resourceType, service)
	}
}

func managedResourceProbeAddress(t *testing.T, service model.Service) string {
	t.Helper()
	for _, endpoint := range service.Endpoints {
		if endpoint.Kind == model.EndpointPublic && endpoint.Protocol == model.ProtocolTCP {
			if endpoint.Host == "" || endpoint.Port == 0 || endpoint.URL == "" {
				t.Fatalf("public TCP endpoint for %s is incomplete: %#v", service.Name, endpoint)
			}
			// The ordinary E2E binary deliberately does not install machine DNS or the
			// privileged TCP relay. Probe the real published container port here; the
			// destructive relay suite separately verifies the advertised clean endpoint.
			return net.JoinHostPort("127.0.0.1", strconv.Itoa(service.UpstreamPort))
		}
	}
	t.Fatalf("service %s has no public TCP endpoint: %#v", service.Name, service.Endpoints)
	return ""
}

func assertGeneratedResourceBinding(t *testing.T, binary, home, checkout, service, key, prefix string) {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--json", "service", "config", service)
	if err != nil {
		t.Fatalf("show generated resource binding: %v\n%s", err, output)
	}
	var configuration model.ServiceConfiguration
	if err := json.Unmarshal([]byte(output), &configuration); err != nil {
		t.Fatalf("decode service configuration: %v\n%s", err, output)
	}
	for _, value := range configuration.Environment {
		if value.Key != key {
			continue
		}
		if !strings.HasPrefix(value.Value, prefix) || value.Source == "" {
			t.Fatalf("generated binding %s = %#v", key, value)
		}
		return
	}
	t.Fatalf("generated resource binding %s not found: %#v", key, configuration.Environment)
}

func probeManagedResource(t *testing.T, resourceType, address string) {
	t.Helper()
	switch resourceType {
	case "valkey":
		if response := valkeyCommand(t, address, "PING"); response != "PONG" {
			t.Fatalf("Valkey PING response = %q", response)
		}
	case "nats":
		connection, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			t.Fatalf("connect to NATS at %s: %v", address, err)
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(connection)
		line, err := reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "INFO ") {
			t.Fatalf("NATS greeting = %q, err=%v", line, err)
		}
		if _, err := io.WriteString(connection, "PING\r\n"); err != nil {
			t.Fatal(err)
		}
		for attempts := 0; attempts < 4; attempts++ {
			line, err = reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read NATS PONG: %v", err)
			}
			if strings.TrimSpace(line) == "PONG" {
				return
			}
		}
		t.Fatal("NATS did not return PONG")
	default:
		connection, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			t.Fatalf("connect to %s at %s: %v", resourceType, address, err)
		}
		connection.Close()
	}
}

func valkeyCommand(t *testing.T, address string, arguments ...string) string {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("connect to Valkey at %s: %v", address, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	var request strings.Builder
	fmt.Fprintf(&request, "*%d\r\n", len(arguments))
	for _, argument := range arguments {
		fmt.Fprintf(&request, "$%d\r\n%s\r\n", len(argument), argument)
	}
	if _, err := io.WriteString(connection, request.String()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read Valkey response: %v", err)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "$-1" {
		return ""
	}
	if strings.HasPrefix(line, "+") {
		return strings.TrimPrefix(line, "+")
	}
	if strings.HasPrefix(line, "-") {
		t.Fatalf("Valkey returned %s", line)
	}
	if strings.HasPrefix(line, "$") {
		length, err := strconv.Atoi(strings.TrimPrefix(line, "$"))
		if err != nil || length < 0 {
			t.Fatalf("invalid Valkey bulk response %q", line)
		}
		content := make([]byte, length+2)
		if _, err := io.ReadFull(reader, content); err != nil {
			t.Fatal(err)
		}
		return string(content[:length])
	}
	t.Fatalf("unsupported Valkey response %q", line)
	return ""
}
