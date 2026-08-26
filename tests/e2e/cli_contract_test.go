//go:build e2e

package e2e_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLIHelpContracts(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")

	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name: "root",
			expected: []string{
				"Usage:\n  portless [flags]", "Environment:", "Observe:", "Projects:",
				"Traffic:", "Administration:", "Help:", "mcp", "--env string", "--json", "--no-color",
			},
		},
		{name: "mcp group", args: []string{"mcp"}, expected: []string{"Expose the local Portless control plane to MCP clients", "portless mcp [command]", "serve"}},
		{name: "traffic group", args: []string{"traffic"}, expected: []string{"Inspect local application traffic", "portless traffic [command]", "list", "show", "traces", "trace"}},
		{name: "fault group", args: []string{"fault"}, expected: []string{"Introduce scoped failures", "portless fault [command]", "add", "delete", "disable", "enable", "list"}},
		{name: "record group", args: []string{"record"}, expected: []string{"Capture bounded local traffic recordings", "portless record [command]", "start", "stop", "export", "delete"}},
		{name: "environment group", args: []string{"env"}, expected: []string{"Manage project environments", "portless env [command]", "select", "current", "clone", "bind", "checkout"}},
		{name: "environment checkout group", args: []string{"env", "checkout"}, expected: []string{"Manage source checkouts for the selected environment", "portless env checkout [command]", "list", "set", "remove"}},
		{name: "missing environment checkout", args: []string{"env", "checkout", "set"}, expected: []string{"Configure a source checkout", "portless env checkout set <source>"}},
		{name: "missing environment selection", args: []string{"env", "select"}, expected: []string{"Select an environment", "portless env select <project/environment>"}},
		{name: "project source group", args: []string{"project", "source"}, expected: []string{"Manage project sources", "portless project source [command]", "add", "delete"}},
		{name: "missing project source", args: []string{"project", "source", "add"}, expected: []string{"Discover a checkout and add its services", "portless project source add <name>"}},
		{name: "missing project source deletion", args: []string{"project", "source", "delete"}, expected: []string{"Delete a logical source from the current project", "portless project source delete <name>"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := runCLIAt(binary, home, checkout, test.args...)
			if err != nil {
				t.Fatalf("help command failed: %v\n%s", err, output)
			}
			if strings.Contains(output, "accepts ") || strings.Contains(output, "requires at least") {
				t.Fatalf("raw Cobra argument error leaked instead of help:\n%s", output)
			}
			for _, expected := range test.expected {
				if !strings.Contains(output, expected) {
					t.Errorf("output does not contain %q:\n%s", expected, output)
				}
			}
		})
	}
}

func TestCLIHumanAndJSONOutputContracts(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")

	human, err := runCLIAt(binary, home, checkout, "daemon", "status")
	if err != nil {
		t.Fatalf("human daemon status: %v\n%s", err, human)
	}
	if !strings.Contains(human, "Portless daemon is stopped.") || strings.HasPrefix(strings.TrimSpace(human), "{") {
		t.Fatalf("default output was not human-readable:\n%s", human)
	}

	encoded, err := runCLIAt(binary, home, checkout, "--json", "daemon", "status")
	if err != nil {
		t.Fatalf("JSON daemon status: %v\n%s", err, encoded)
	}
	var status struct {
		State              string   `json:"state"`
		HandoffState       string   `json:"handoffState"`
		HandoffProblems    []string `json:"handoffProblems"`
		RecoveryProblems   []string `json:"recoveryProblems"`
		ActiveEnvironments []string `json:"activeEnvironments"`
		Problems           []string `json:"problems"`
	}
	if err := json.Unmarshal([]byte(encoded), &status); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, encoded)
	}
	if status.State != "stopped" || status.HandoffState != "not-required" || status.HandoffProblems == nil || status.RecoveryProblems == nil || status.ActiveEnvironments == nil || status.Problems == nil {
		t.Fatalf("unexpected stopped daemon JSON contract: %#v", status)
	}
	if strings.Contains(encoded, "\x1b[") {
		t.Fatalf("ANSI color escaped into JSON output: %q", encoded)
	}

	unknown, err := runCLIAt(binary, home, checkout, "definitely-not-a-command")
	if err == nil {
		t.Fatalf("unknown command unexpectedly succeeded:\n%s", unknown)
	}
	if !strings.Contains(unknown, `portless: unknown command "definitely-not-a-command"`) || !strings.Contains(unknown, "Usage:\n  portless [flags]") {
		t.Fatalf("unknown command did not include a useful error and root help:\n%s", unknown)
	}
}
