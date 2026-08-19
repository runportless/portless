package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCobraRootHelpShowsCommandTree(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"Environment:", "Observe:", "Projects:", "Traffic:", "Administration:", "Help:", "completion", "config", "daemon", "doctor", "env", "mcp", "mock", "project", "record", "relay", "reset", "runtime", "uninstall", "--env", "--json", "--no-color"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("help does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "Available Commands:") || strings.Contains(output.String(), "Additional Commands:") {
		t.Fatalf("root help contains an ungrouped command section:\n%s", output.String())
	}
	if strings.Contains(output.String(), "\n  use ") {
		t.Fatalf("root help still exposes the ambiguous top-level use command:\n%s", output.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("automatic color escaped into redirected help output:\n%q", output.String())
	}
}

func TestCobraRootCommandsAreGroupedByTask(t *testing.T) {
	application, _, _ := newTestCLI(t)
	root := application.rootCommand()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	expected := map[string]string{
		"up": rootGroupRun, "down": rootGroupRun, "status": rootGroupRun,
		"open": rootGroupRun, "url": rootGroupRun, "ui": rootGroupRun,
		"logs": rootGroupInspect, "traffic": rootGroupInspect, "timeline": rootGroupInspect,
		"service": rootGroupInspect, "connection": rootGroupInspect,
		"project": rootGroupConfigure, "env": rootGroupConfigure,
		"record": rootGroupTest, "fault": rootGroupTest, "mock": rootGroupTest,
		"runtime": rootGroupSystem, "setup": rootGroupSystem, "relay": rootGroupSystem, "daemon": rootGroupSystem, "mcp": rootGroupSystem,
		"doctor": rootGroupSystem, "config": rootGroupSystem, "reset": rootGroupSystem, "uninstall": rootGroupSystem,
		"completion": rootGroupOther, "help": rootGroupOther,
	}

	seen := make(map[string]bool, len(expected))
	for _, command := range root.Commands() {
		if !command.IsAvailableCommand() && command.Name() != "help" {
			continue
		}
		want, ok := expected[command.Name()]
		if !ok {
			t.Errorf("unexpected top-level command %q in group %q", command.Name(), command.GroupID)
			continue
		}
		seen[command.Name()] = true
		if command.GroupID != want {
			t.Errorf("command %q group = %q, want %q", command.Name(), command.GroupID, want)
		}
	}
	for command := range expected {
		if !seen[command] {
			t.Errorf("expected top-level command %q was not registered", command)
		}
	}
	if !root.AllChildCommandsHaveGroup() {
		t.Fatal("at least one available top-level command has no group")
	}
}

func newTestCLI(t *testing.T) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	return newTestCLIAt(t, t.TempDir())
}

func newTestCLIAt(t *testing.T, root string) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	errorsOutput := &bytes.Buffer{}
	application, err := New(output, errorsOutput, root)
	if err != nil {
		t.Fatal(err)
	}
	return application, output, errorsOutput
}
