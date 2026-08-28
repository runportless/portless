package relay

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandExposesOnlyFixedPrivateModes(t *testing.T) {
	var stderr bytes.Buffer
	if code := Command("__not-a-relay-mode", nil, &stderr); code != 2 || !strings.Contains(stderr.String(), "unknown private command") {
		t.Fatalf("unknown command code=%d stderr=%q", code, stderr.String())
	}
	for _, mode := range []string{"__relay", "__install-relay", "__restart-relay", "__uninstall-relay"} {
		stderr.Reset()
		if code := Command(mode, []string{"--not-a-portless-flag"}, &stderr); code != 2 {
			t.Errorf("%s invalid flag code=%d stderr=%q", mode, code, stderr.String())
		}
	}
}
