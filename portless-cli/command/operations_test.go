package command

import (
	"strings"
	"testing"
)

func TestInvocationKeysAreUnique(t *testing.T) {
	first, err := InvocationKey("cli-up")
	if err != nil {
		t.Fatal(err)
	}
	second, err := InvocationKey("cli-up")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "cli-up-") || len(first) != len("cli-up-")+32 {
		t.Fatalf("invocation keys = %q, %q", first, second)
	}
}
