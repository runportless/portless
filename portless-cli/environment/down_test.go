package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func TestDownAllStartsEveryActiveEnvironmentBeforeWaiting(t *testing.T) {
	environments := []model.Environment{
		{Project: "zeta", Name: "qa", Status: model.EnvironmentHealthy},
		{Project: "archive", Name: "local", Status: model.EnvironmentStopped},
		{Project: "alpha", Name: "local", Status: model.EnvironmentDegraded},
	}
	postPaths := []string{}
	waitPaths := []string{}
	removeVolumes := []bool{}
	operations := map[string]model.Operation{
		"/api/v1/environments/alpha/local/down": {Project: "alpha", Environment: "local", Number: 11, Type: "down", State: "running"},
		"/api/v1/environments/zeta/qa/down":     {Project: "zeta", Environment: "qa", Number: 12, Type: "down", State: "running"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/environments":
			if request.URL.Query().Get("limit") != "1000" {
				t.Errorf("inventory limit = %q, want 1000", request.URL.Query().Get("limit"))
			}
			writeDownTestJSON(t, writer, http.StatusOK, map[string]any{"environments": environments, "total": len(environments)})
		case request.Method == http.MethodPost:
			operation, found := operations[request.URL.Path]
			if !found {
				t.Errorf("unexpected down request %s", request.URL.Path)
				http.NotFound(writer, request)
				return
			}
			var input struct {
				RemoveVolumes bool `json:"removeVolumes"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Errorf("decode down request: %v", err)
			}
			postPaths = append(postPaths, request.URL.Path)
			removeVolumes = append(removeVolumes, input.RemoveVolumes)
			writeDownTestJSON(t, writer, http.StatusAccepted, operation)
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/operations/"):
			if len(postPaths) != len(operations) {
				t.Errorf("wait began after %d shutdown requests, want %d", len(postPaths), len(operations))
			}
			waitPaths = append(waitPaths, request.URL.Path)
			completed := operationForWaitPath(operations, request.URL.Path)
			if completed.Number == 0 {
				t.Errorf("unexpected operation request %s", request.URL.Path)
				http.NotFound(writer, request)
				return
			}
			completed.State = "succeeded"
			writeDownTestJSON(t, writer, http.StatusOK, completed)
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.String())
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	application, output, errorsOutput := newTestCommands(t)
	client := apiclient.New(server.URL, "test-token", server.Client())
	err := application.downAll(context.Background(), client, downOptions{all: true, wait: true, timeout: time.Second})
	if err != nil {
		t.Fatalf("downAll returned error: %v", err)
	}
	if actual, expected := strings.Join(postPaths, ","), "/api/v1/environments/alpha/local/down,/api/v1/environments/zeta/qa/down"; actual != expected {
		t.Fatalf("down request order = %q, want %q", actual, expected)
	}
	if len(waitPaths) != 2 {
		t.Fatalf("operation waits = %d, want 2: %#v", len(waitPaths), waitPaths)
	}
	if len(removeVolumes) != 2 || removeVolumes[0] || removeVolumes[1] {
		t.Fatalf("removeVolumes bodies = %#v, want [false false]", removeVolumes)
	}
	if strings.Contains(strings.Join(postPaths, ","), "archive/local") {
		t.Fatalf("already-stopped environment received a down request: %#v", postPaths)
	}
	if actual, expected := output.String(), "alpha/local  stopped\nzeta/qa  stopped\n"; actual != expected {
		t.Fatalf("stdout = %q, want %q", actual, expected)
	}
	if errorsOutput.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", errorsOutput.String())
	}
}

func TestDownAllAggregatesRequestAndOperationFailures(t *testing.T) {
	environments := []model.Environment{
		{Project: "alpha", Name: "local", Status: model.EnvironmentHealthy},
		{Project: "beta", Name: "qa", Status: model.EnvironmentDegraded},
		{Project: "gamma", Name: "dev", Status: model.EnvironmentHealthy},
	}
	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/environments":
			writeDownTestJSON(t, writer, http.StatusOK, map[string]any{"environments": environments, "total": len(environments)})
		case request.Method == http.MethodPost:
			postCount++
			switch request.URL.Path {
			case "/api/v1/environments/alpha/local/down":
				writeDownTestJSON(t, writer, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "RUNTIME_UNAVAILABLE", "message": "container runtime is offline"}})
			case "/api/v1/environments/beta/qa/down":
				writeDownTestJSON(t, writer, http.StatusAccepted, model.Operation{Project: "beta", Environment: "qa", Number: 21, Type: "down", State: "running"})
			case "/api/v1/environments/gamma/dev/down":
				writeDownTestJSON(t, writer, http.StatusAccepted, model.Operation{Project: "gamma", Environment: "dev", Number: 22, Type: "down", State: "running"})
			default:
				http.NotFound(writer, request)
			}
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/environments/beta/qa/operations/21":
			if postCount != 3 {
				t.Errorf("wait began after %d shutdown requests, want 3", postCount)
			}
			writeDownTestJSON(t, writer, http.StatusOK, model.Operation{Project: "beta", Environment: "qa", Number: 21, Type: "down", State: "failed", Error: "process refused to stop"})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/environments/gamma/dev/operations/22":
			if postCount != 3 {
				t.Errorf("wait began after %d shutdown requests, want 3", postCount)
			}
			writeDownTestJSON(t, writer, http.StatusOK, model.Operation{Project: "gamma", Environment: "dev", Number: 22, Type: "down", State: "succeeded"})
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.String())
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	application, output, errorsOutput := newTestCommands(t)
	client := apiclient.New(server.URL, "test-token", server.Client())
	err := application.downAll(context.Background(), client, downOptions{all: true, wait: true, timeout: time.Second})
	var reported *command.ReportedError
	if !errors.As(err, &reported) {
		t.Fatalf("downAll error = %v, want reportedCommandError", err)
	}
	if postCount != 3 {
		t.Fatalf("down requests = %d, want 3", postCount)
	}
	if actual, expected := output.String(), "gamma/dev  stopped\n"; actual != expected {
		t.Fatalf("stdout = %q, want %q", actual, expected)
	}
	for _, expected := range []string{"alpha/local failed during request", "container runtime is offline", "beta/qa failed during operation", "process refused to stop"} {
		if !strings.Contains(errorsOutput.String(), expected) {
			t.Errorf("stderr does not contain %q:\n%s", expected, errorsOutput.String())
		}
	}
}

func TestDownAllNoWaitWritesAggregateJSON(t *testing.T) {
	getOperation := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/environments":
			writeDownTestJSON(t, writer, http.StatusOK, map[string]any{
				"environments": []model.Environment{
					{Project: "store", Name: "local", Status: model.EnvironmentHealthy},
					{Project: "store", Name: "old", Status: model.EnvironmentStopped},
				},
				"total": 2,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/environments/store/local/down":
			writeDownTestJSON(t, writer, http.StatusAccepted, model.Operation{Project: "store", Environment: "local", Number: 31, Type: "down", State: "running"})
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/operations/"):
			getOperation = true
			http.Error(writer, "unexpected wait", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	application, output, errorsOutput := newTestCommands(t)
	application.JSONOutput = true
	client := apiclient.New(server.URL, "test-token", server.Client())
	if err := application.downAll(context.Background(), client, downOptions{all: true, wait: false, timeout: time.Second}); err != nil {
		t.Fatalf("downAll returned error: %v", err)
	}
	if getOperation {
		t.Fatal("--no-wait polled an operation")
	}
	var result downAllOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, output.String())
	}
	if result.Action != "down" || !result.All || result.Wait || result.Targets != 1 {
		t.Fatalf("unexpected aggregate output: %#v", result)
	}
	if len(result.Operations) != 1 || result.Operations[0].State != "running" || len(result.Failures) != 0 {
		t.Fatalf("unexpected operations or failures: %#v", result)
	}
	if errorsOutput.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", errorsOutput.String())
	}
}

func TestDownAllVolumesIncludesStoppedEnvironments(t *testing.T) {
	requested := false
	removeVolumes := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/environments":
			writeDownTestJSON(t, writer, http.StatusOK, map[string]any{
				"environments": []model.Environment{{Project: "store", Name: "old", Status: model.EnvironmentStopped}},
				"total":        1,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/environments/store/old/down":
			requested = true
			var input struct {
				RemoveVolumes bool `json:"removeVolumes"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Errorf("decode down request: %v", err)
			}
			removeVolumes = input.RemoveVolumes
			writeDownTestJSON(t, writer, http.StatusAccepted, model.Operation{Project: "store", Environment: "old", Number: 41, Type: "down", State: "running"})
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.String())
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	application, output, errorsOutput := newTestCommands(t)
	client := apiclient.New(server.URL, "test-token", server.Client())
	if err := application.downAll(context.Background(), client, downOptions{all: true, volumes: true, wait: false, timeout: time.Second}); err != nil {
		t.Fatalf("downAll returned error: %v", err)
	}
	if !requested || !removeVolumes {
		t.Fatalf("stopped environment request = %t, removeVolumes = %t; want both true", requested, removeVolumes)
	}
	if !strings.Contains(output.String(), "store/old  down operation 41 running") {
		t.Fatalf("stdout does not report the accepted cleanup operation: %s", output.String())
	}
	if errorsOutput.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", errorsOutput.String())
	}
}

func TestDownAllReportsWhenEverythingIsAlreadyStopped(t *testing.T) {
	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			postCount++
		}
		writeDownTestJSON(t, writer, http.StatusOK, map[string]any{
			"environments": []model.Environment{{Project: "store", Name: "old", Status: model.EnvironmentStopped}},
			"total":        1,
		})
	}))
	t.Cleanup(server.Close)

	application, output, errorsOutput := newTestCommands(t)
	client := apiclient.New(server.URL, "test-token", server.Client())
	if err := application.downAll(context.Background(), client, downOptions{all: true, wait: true, timeout: time.Second}); err != nil {
		t.Fatalf("downAll returned error: %v", err)
	}
	if postCount != 0 {
		t.Fatalf("already-stopped inventory triggered %d shutdown requests", postCount)
	}
	if actual, expected := output.String(), "All environments are already stopped.\n"; actual != expected {
		t.Fatalf("stdout = %q, want %q", actual, expected)
	}
	if errorsOutput.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", errorsOutput.String())
	}
}

func TestDownAllRefusesTruncatedInventory(t *testing.T) {
	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			postCount++
		}
		writeDownTestJSON(t, writer, http.StatusOK, map[string]any{
			"environments": []model.Environment{{Project: "store", Name: "local", Status: model.EnvironmentHealthy}},
			"total":        downAllInventoryLimit + 1,
		})
	}))
	t.Cleanup(server.Close)

	client := apiclient.New(server.URL, "test-token", server.Client())
	_, err := loadDownAllTargets(context.Background(), client, false)
	if err == nil || !strings.Contains(err.Error(), "refused a partial machine-wide shutdown") {
		t.Fatalf("loadDownAllTargets error = %v, want truncated-inventory refusal", err)
	}
	if postCount != 0 {
		t.Fatalf("truncated inventory triggered %d shutdown requests", postCount)
	}
}

func operationForWaitPath(operations map[string]model.Operation, waitPath string) model.Operation {
	for downPath, operation := range operations {
		expected := strings.TrimSuffix(downPath, "/down") + "/operations/" + strconv.FormatInt(operation.Number, 10)
		if waitPath == expected {
			return operation
		}
	}
	return model.Operation{}
}

func writeDownTestJSON(t *testing.T, writer http.ResponseWriter, status int, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

func newTestCommands(t *testing.T) (*Commands, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(&command.Context{Out: out, Err: errOut, Paths: paths}), out, errOut
}
