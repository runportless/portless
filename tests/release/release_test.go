package release_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCanonicalModuleAndReleaseConfiguration(t *testing.T) {
	root := repositoryRoot(t)
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(module, []byte("module github.com/runportless/portless\n")) {
		t.Fatalf("unexpected module declaration: %q", bytes.SplitN(module, []byte("\n"), 2)[0])
	}

	for _, relative := range []string{
		".goreleaser.yaml",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join(".github", "workflows", "release.yml"),
	} {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := yaml.Unmarshal(content, &document); err != nil {
			t.Errorf("parse %s: %v", relative, err)
		}
		if strings.Contains(string(content), "github.com/portless-run/portless") {
			t.Errorf("%s contains the retired repository identity", relative)
		}
	}
}

func TestHomebrewFormulaRenderer(t *testing.T) {
	root := repositoryRoot(t)
	output := filepath.Join(t.TempDir(), "Formula", "portless.rb")
	checksum := strings.Repeat("ab", 32)
	command := exec.Command(filepath.Join(root, "scripts", "render-homebrew-formula.sh"), "1.2.3", checksum, output)
	command.Dir = root
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render formula: %v\n%s", err, result)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	formula := string(content)
	for _, expected := range []string{
		"class Portless < Formula",
		"releases/download/v1.2.3/portless_1.2.3_source.tar.gz",
		`sha256 "` + checksum + `"`,
		`license "Apache-2.0"`,
		"github.com/runportless/portless/portless-cli.Version=#{version}",
		"github.com/runportless/portless/portless-cli.Distribution=homebrew",
		"brew uninstall runportless/tap/portless",
	} {
		if !strings.Contains(formula, expected) {
			t.Errorf("rendered formula does not contain %q", expected)
		}
	}
	if strings.Contains(formula, "@VERSION@") || strings.Contains(formula, "@SHA256@") {
		t.Fatal("rendered formula still contains a template placeholder")
	}
}

func TestHomebrewFormulaRendererRejectsUnstableVersionAndInvalidChecksum(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "scripts", "render-homebrew-formula.sh")
	for _, arguments := range [][]string{
		{"1.2.3-rc.1", strings.Repeat("ab", 32), filepath.Join(t.TempDir(), "portless.rb")},
		{"1.2.3", "not-a-checksum", filepath.Join(t.TempDir(), "portless.rb")},
	} {
		command := exec.Command(script, arguments...)
		command.Dir = root
		if err := command.Run(); err == nil {
			t.Fatalf("renderer accepted invalid arguments %#v", arguments)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate release test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
