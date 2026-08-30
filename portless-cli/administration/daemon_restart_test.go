package administration

import (
	"strings"
	"testing"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
)

func TestDaemonRestartErrorExplainsHowToStopTheBlockingEnvironment(t *testing.T) {
	input := &apiclient.ClientError{
		Status:  409,
		Code:    "HANDOFF_UNAVAILABLE",
		Message: "active environments cannot be safely handed off",
		Details: map[string]any{
			"activeEnvironments": []any{"store/local", "store/qa-local"},
			"problems":           []any{"store/qa-local/external:orders-redis: public TCP endpoint is not listening"},
		},
	}
	if actual := actionableDaemonRestartError(input); actual != input {
		t.Fatalf("restart error identity changed: %T %v", actual, actual)
	}
	if input.Message != "Safe daemon restart is blocked by environment store/qa-local. Stop it, then retry." {
		t.Fatalf("restart message = %q", input.Message)
	}

	application, _, errorOutput := newTestCommands(t)
	application.PrintError(input)
	output := errorOutput.String()
	for _, expected := range []string{
		"Safe daemon restart is blocked by environment store/qa-local. Stop it, then retry.",
		"next: portless down --env store/qa-local",
		"next: portless daemon restart",
		"next: portless doctor",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("restart error does not contain %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "portless down --env store/local") {
		t.Fatalf("restart error stops a handoff-ready environment:\n%s", output)
	}
}
