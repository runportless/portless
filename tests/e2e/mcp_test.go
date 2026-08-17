//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStdioScopeAndDurableLifecycle(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "mcp-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start MCP fixture environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}

	readSession, readDiagnostics := connectMCPCommand(t, binary, home, checkout, "mcp", "serve")
	readTools := mcpToolNames(t, readSession)
	if len(readTools) != 15 || containsString(readTools, "portless_stop_environment") {
		t.Fatalf("default tools = %v", readTools)
	}
	listed, err := readSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_list_environments", Arguments: map[string]any{"limit": 20},
	})
	if err != nil || listed.IsError {
		t.Fatalf("list environments: result=%#v err=%v diagnostics=%s", listed, err, readDiagnostics.String())
	}
	listedJSON := marshalMCPResult(t, listed)
	if !strings.Contains(listedJSON, `"scope":"workspace:`) || !strings.Contains(listedJSON, `"project":"mcp-e2e"`) {
		t.Fatalf("workspace-scoped environments = %s", listedJSON)
	}
	environment, err := readSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_get_environment", Arguments: map[string]any{"environment": "mcp-e2e/local"},
	})
	if err != nil || environment.IsError {
		t.Fatalf("get environment: result=%#v err=%v", environment, err)
	}
	environmentJSON := marshalMCPResult(t, environment)
	for _, privateField := range []string{`"pid"`, `"upstreamPort"`, `"address"`, `"runtimeTarget"`} {
		if strings.Contains(environmentJSON, privateField) {
			t.Fatalf("MCP environment leaked %s: %s", privateField, environmentJSON)
		}
	}
	if err := readSession.Close(); err != nil {
		t.Fatal(err)
	}

	operatorSession, operatorDiagnostics := connectMCPCommand(t, binary, home, checkout,
		"--env", "mcp-e2e/local", "mcp", "serve", "--allow-lifecycle")
	if tools := mcpToolNames(t, operatorSession); len(tools) != 18 || !containsString(tools, "portless_change_service_state") {
		t.Fatalf("lifecycle tools = %v", tools)
	}
	arguments := map[string]any{
		"environment": "mcp-e2e/local", "service": "checkout", "action": "restart",
		"waitSeconds": 120, "idempotencyKey": "mcp-e2e-restart",
	}
	first, err := operatorSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_change_service_state", Arguments: arguments,
	})
	if err != nil || first.IsError {
		t.Fatalf("first MCP restart: result=%#v err=%v diagnostics=%s", first, err, operatorDiagnostics.String())
	}
	repeated, err := operatorSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_change_service_state", Arguments: arguments,
	})
	if err != nil || repeated.IsError {
		t.Fatalf("repeated MCP restart: result=%#v err=%v diagnostics=%s", repeated, err, operatorDiagnostics.String())
	}
	firstOperation := decodeMCPLifecycle(t, first)
	repeatedOperation := decodeMCPLifecycle(t, repeated)
	if firstOperation.Operation.Number < 1 || firstOperation.Operation.Number != repeatedOperation.Operation.Number {
		t.Fatalf("idempotent operation numbers: first=%#v repeated=%#v", firstOperation, repeatedOperation)
	}
	if firstOperation.Operation.Actor != "MCP" || repeatedOperation.Operation.Actor != "MCP" {
		t.Fatalf("operation actors: first=%q repeated=%q", firstOperation.Operation.Actor, repeatedOperation.Operation.Actor)
	}
	if err := operatorSession.Close(); err != nil {
		t.Fatal(err)
	}
}

func connectMCPCommand(t *testing.T, binary, home, directory string, arguments ...string) (*mcp.ClientSession, *bytes.Buffer) {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = isolatedEnvironment(home)
	diagnostics := new(bytes.Buffer)
	command.Stderr = diagnostics
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "portless-e2e", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 5 * time.Second}, nil)
	if err != nil {
		t.Fatalf("connect MCP command: %v\n%s", err, diagnostics.String())
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, diagnostics
}

func mcpToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	names := []string{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func marshalMCPResult(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func decodeMCPLifecycle(t *testing.T, result *mcp.CallToolResult) struct {
	Operation struct {
		Number int64  `json:"number"`
		Actor  string `json:"actor"`
		State  string `json:"state"`
	} `json:"operation"`
} {
	t.Helper()
	var output struct {
		Operation struct {
			Number int64  `json:"number"`
			Actor  string `json:"actor"`
			State  string `json:"state"`
		} `json:"operation"`
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
