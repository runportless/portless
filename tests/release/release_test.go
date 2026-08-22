package release_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		filepath.Join(".github", "dependabot.yml"),
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

func TestReleaseWorkflowActionsAreCommitPinned(t *testing.T) {
	root := repositoryRoot(t)
	commit := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, relative := range []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join(".github", "workflows", "release.yml"),
	} {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "uses: ") {
				continue
			}
			action, reference, ok := strings.Cut(strings.TrimPrefix(line, "uses: "), "@")
			if !ok {
				t.Errorf("%s action %q has no reference", relative, action)
				continue
			}
			reference, _, _ = strings.Cut(reference, " ")
			if !commit.MatchString(reference) {
				t.Errorf("%s action %s is not pinned to a full commit: %q", relative, action, reference)
			}
		}
	}
}

func TestReleaseWorkflowRequiresApprovalOnlyForFinalPublication(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Needs       string `yaml:"needs"`
			Environment string `yaml:"environment"`
			Steps       []struct {
				Name string `yaml:"name"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}

	release := workflow.Jobs["release"]
	publish := workflow.Jobs["publish"]
	homebrew := workflow.Jobs["homebrew"]
	if release.Environment != "" {
		t.Fatalf("release staging job unexpectedly uses environment %q", release.Environment)
	}
	if publish.Needs != "release" || publish.Environment != "release" {
		t.Fatalf("publish job must depend on release staging and use the release environment: %#v", publish)
	}
	if homebrew.Needs != "publish" {
		t.Fatalf("Homebrew proposal must wait for publication, got needs %q", homebrew.Needs)
	}
	if !workflowHasStep(publish.Steps, "Publish release") {
		t.Fatal("publish job does not contain the final release publication step")
	}
	if workflowHasStep(release.Steps, "Publish release") {
		t.Fatal("release staging job publishes without the approval environment")
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
		"# typed: strict",
		"# frozen_string_literal: true",
		"# Portless installs the local application-environment control plane.",
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
	goDependency := strings.Index(formula, `depends_on "go" => :build`)
	macOSDependency := strings.Index(formula, "depends_on :macos")
	if goDependency == -1 || macOSDependency == -1 || goDependency > macOSDependency {
		t.Fatal("rendered formula must declare the Go build dependency before the macOS dependency")
	}
}

func TestHomebrewFormulaRendererAcceptsPrereleaseVersion(t *testing.T) {
	root := repositoryRoot(t)
	output := filepath.Join(t.TempDir(), "Formula", "portless.rb")
	checksum := strings.Repeat("cd", 32)
	command := exec.Command(filepath.Join(root, "scripts", "render-homebrew-formula.sh"), "0.1.0-alpha.1", checksum, output)
	command.Dir = root
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render prerelease formula: %v\n%s", err, result)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	formula := string(content)
	for _, expected := range []string{
		"releases/download/v0.1.0-alpha.1/portless_0.1.0-alpha.1_source.tar.gz",
		`version "0.1.0-alpha.1"`,
	} {
		if !strings.Contains(formula, expected) {
			t.Errorf("rendered prerelease formula does not contain %q", expected)
		}
	}
}

func TestHomebrewFormulaRendererRejectsInvalidVersionAndChecksum(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "scripts", "render-homebrew-formula.sh")
	for _, arguments := range [][]string{
		{"v1.2.3", strings.Repeat("ab", 32), filepath.Join(t.TempDir(), "portless.rb")},
		{"1.2.3-alpha.01", strings.Repeat("ab", 32), filepath.Join(t.TempDir(), "portless.rb")},
		{"1.2", strings.Repeat("ab", 32), filepath.Join(t.TempDir(), "portless.rb")},
		{"1.2.3", "not-a-checksum", filepath.Join(t.TempDir(), "portless.rb")},
	} {
		command := exec.Command(script, arguments...)
		command.Dir = root
		if err := command.Run(); err == nil {
			t.Fatalf("renderer accepted invalid arguments %#v", arguments)
		}
	}
}

func workflowHasStep(steps []struct {
	Name string `yaml:"name"`
}, name string) bool {
	for _, step := range steps {
		if step.Name == name {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate release test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
