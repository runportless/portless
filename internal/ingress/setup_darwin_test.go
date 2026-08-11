//go:build darwin

package ingress

import (
	"encoding/xml"
	"os"
	"path/filepath"
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

func TestPlatformConfigurationOwnerReadsLegacyLaunchdPlist(t *testing.T) {
	content, err := renderLaunchdPlist(SetupRequest{TargetSocket: "/Users/dev/a&b/ingress.sock", UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dev.portless.ingress.plist")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid, socket, err := platformConfigurationOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if uid != 501 || gid != 20 || socket != "/Users/dev/a&b/ingress.sock" {
		t.Fatalf("unexpected owner: uid=%d gid=%d socket=%q", uid, gid, socket)
	}
}
