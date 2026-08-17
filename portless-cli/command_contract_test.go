package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCobraUsageErrorsReturnExitCodeTwo(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  string
		usage string
	}{
		{name: "unknown command", args: []string{"not-a-command"}, want: "unknown command", usage: "portless [flags]"},
		{name: "missing repeatable source", args: []string{"project", "create", "billing"}, want: `required flag(s) "source" not set`, usage: "portless project create <name>"},
		{name: "missing provider", args: []string{"env", "bind", "checkout"}, want: "at least one of the flags", usage: "portless env bind <service>"},
		{name: "exclusive provider", args: []string{"env", "bind", "checkout", "--local", "checkout", "--container"}, want: "none of the others can be", usage: "portless env bind <service>"},
		{name: "invalid runtime", args: []string{"runtime", "use", "containerd"}, want: "runtime must be auto, docker, or podman", usage: "portless runtime use <auto|docker|podman>"},
		{name: "invalid recording duration", args: []string{"record", "start", "capture", "--duration", "0s"}, want: "--duration must be greater than zero", usage: "portless record start <name>"},
		{name: "negative fault duration", args: []string{"fault", "add", "slow", "checkout:orders", "--latency", "100", "--duration=-1s"}, want: "--duration must be zero or greater", usage: "portless fault add <name> <source:target>"},
		{name: "fault without effect", args: []string{"fault", "add", "slow", "checkout:orders"}, want: "define at least one effect", usage: "portless fault add <name> <source:target>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, _, errorsOutput := newTestCLI(t)
			if code := application.Run(context.Background(), test.args); code != 2 {
				t.Fatalf("Run returned %d, want 2; stderr: %s", code, errorsOutput.String())
			}
			if !strings.Contains(errorsOutput.String(), test.want) {
				t.Fatalf("stderr does not contain %q:\n%s", test.want, errorsOutput.String())
			}
			if !strings.Contains(errorsOutput.String(), "Usage:\n  "+test.usage) {
				t.Fatalf("stderr does not contain command usage %q:\n%s", test.usage, errorsOutput.String())
			}
		})
	}
}

func TestEveryPublicCommandHasAuditedBareBehavior(t *testing.T) {
	const (
		showHelp  = "help"
		runAction = "run"
	)
	expected := map[string]string{
		"portless":                 showHelp,
		"portless config":          showHelp,
		"portless config color":    runAction,
		"portless config reset":    runAction,
		"portless setup":           runAction,
		"portless relay":           showHelp,
		"portless relay install":   runAction,
		"portless relay status":    runAction,
		"portless relay restart":   runAction,
		"portless relay uninstall": runAction,
		"portless daemon":          showHelp,
		"portless daemon status":   runAction,
		"portless daemon stop":     runAction,
		"portless daemon restart":  runAction,
		"portless doctor":          runAction,
		"portless reset":           runAction,
		"portless uninstall":       runAction,
		"portless up":              runAction,
		"portless down":            runAction,
		"portless status":          runAction,
		"portless open":            runAction,
		"portless url":             runAction,
		"portless ui":              runAction,
		"portless logs":            runAction,
		"portless traffic":         showHelp,
		"portless traffic list":    runAction,
		"portless traffic show":    showHelp,
		"portless traffic traces":  runAction,
		"portless traffic trace":   showHelp,
		"portless service":         showHelp,
		"portless service list":    runAction,
		"portless service show":    showHelp,
		"portless service config":  showHelp,
		"portless service start":   showHelp,
		"portless service stop":    showHelp,
		"portless service restart": showHelp,
		"portless service debug":   showHelp,
		"portless service manage":  showHelp,
		"portless connection":      showHelp,
		"portless connection list": runAction,
		"portless connection show": showHelp,
		"portless timeline":        runAction,
		"portless record":          showHelp,
		"portless record list":     runAction,
		"portless record start":    showHelp,
		"portless record stop":     runAction,
		"portless record show":     showHelp,
		"portless record export":   showHelp,
		"portless record delete":   showHelp,
		"portless fault":           showHelp,
		"portless fault list":      runAction,
		"portless fault add":       showHelp,
		"portless fault show":      showHelp,
		"portless fault enable":    showHelp,
		"portless fault disable":   showHelp,
		"portless fault delete":    showHelp,
		"portless fault clear":     runAction,
		"portless project":         showHelp,
		"portless project list":    runAction,
		"portless project show":    runAction,
		"portless project create":  showHelp,
		"portless project export":  runAction,
		"portless project rename":  showHelp,
		"portless project forget":  runAction,
		"portless env":             showHelp,
		"portless env select":      showHelp,
		"portless env current":     runAction,
		"portless env clear":       runAction,
		"portless env list":        runAction,
		"portless env clone":       showHelp,
		"portless env bind":        showHelp,
		"portless env source":      showHelp,
		"portless env rescan":      runAction,
		"portless env forget":      runAction,
		"portless runtime":         showHelp,
		"portless runtime status":  runAction,
		"portless runtime start":   runAction,
		"portless runtime use":     showHelp,
	}
	expected["portless project source"] = showHelp
	expected["portless project source add"] = showHelp

	application, _, _ := newTestCLI(t)
	actual := map[string]*cobra.Command{}
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		actual[command.CommandPath()] = command
		for _, child := range command.Commands() {
			if child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			visit(child)
		}
	}
	visit(application.rootCommand())

	for path := range actual {
		if _, ok := expected[path]; !ok {
			t.Errorf("public command %q has no audited bare behavior", path)
		}
	}
	for path := range expected {
		if _, ok := actual[path]; !ok {
			t.Errorf("audited command %q no longer exists", path)
		}
	}

	for path, behavior := range expected {
		if behavior != showHelp {
			continue
		}
		t.Run(path, func(t *testing.T) {
			application, output, errorsOutput := newTestCLI(t)
			args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(path, "portless")))
			if code := application.Run(context.Background(), args); code != 0 {
				t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
			}
			if errorsOutput.Len() != 0 {
				t.Fatalf("bare command printed an error: %s", errorsOutput.String())
			}
			if !strings.Contains(output.String(), "Usage:") {
				t.Fatalf("bare command did not print help:\n%s", output.String())
			}
			if _, err := os.Stat(application.context.Paths.Control); !os.IsNotExist(err) {
				t.Fatalf("bare help contacted or started the daemon: %v", err)
			}
		})
	}

	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"completion"}); code != 0 {
		t.Fatalf("bare completion returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Available Commands:") || !strings.Contains(output.String(), "zsh") {
		t.Fatalf("bare completion did not print help:\n%s", output.String())
	}
}

func TestBareLeafCommandsWithRequiredArgumentsShowHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "env select", args: []string{"env", "select"}, want: "portless env select <project/environment>"},
		{name: "project source add", args: []string{"project", "source", "add"}, want: "portless project source add <name>"},
		{name: "record start", args: []string{"record", "start"}, want: "portless record start <name>"},
		{name: "partial fault add", args: []string{"fault", "add", "slow"}, want: "portless fault add <name> <source:target>"},
		{name: "runtime use", args: []string{"runtime", "use"}, want: "portless runtime use <auto|docker|podman>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, output, errorsOutput := newTestCLI(t)
			if code := application.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
			}
			if errorsOutput.Len() != 0 {
				t.Fatalf("bare command printed an error: %s", errorsOutput.String())
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("help does not contain %q:\n%s", test.want, output.String())
			}
			if _, err := os.Stat(application.context.Paths.Control); !os.IsNotExist(err) {
				t.Fatalf("bare command contacted or started the daemon: %v", err)
			}
		})
	}
}

func TestCobraGeneratesShellCompletion(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"completion", "zsh"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "#compdef portless") || !strings.Contains(output.String(), "_portless") {
		t.Fatalf("unexpected completion output:\n%s", output.String())
	}
}
