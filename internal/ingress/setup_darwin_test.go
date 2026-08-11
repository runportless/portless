//go:build darwin

package ingress

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRenderLaunchdPlistEscapesSocketAndUsesFixedHelper(t *testing.T) {
	content, err := renderLaunchdPlist(SetupRequest{TargetSocket: "/Users/dev/a&b/ingress.sock", UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("invalid launchd plist XML: %v\n%s", err, content)
	}
	text := string(content)
	for _, expected := range []string{launchdLabel, launchdHelperPath, "/Users/dev/a&amp;b/ingress.sock", "<string>501</string>", "<string>20</string>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plist did not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "/Users/dev/a&b/") {
		t.Fatal("socket path was not XML escaped")
	}
}
