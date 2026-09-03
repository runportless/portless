package worktrees

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSnapshotsCurrentFilesWithoutChangingTheOriginal(t *testing.T) {
	ctx := context.Background()
	root := repositoryFixture(t)
	writeFixture(t, root, ".gitignore", "node_modules/\n.env.local\n")
	writeFixture(t, root, "apps/store/server.js", "committed")
	writeFixture(t, root, "deleted.txt", "committed")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Portless Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	writeFixture(t, root, "apps/store/server.js", "staged")
	runGit(t, root, "add", "apps/store/server.js")
	writeFixture(t, root, "apps/store/server.js", "working copy")
	writeFixture(t, root, "apps/store/untracked.txt", "untracked")
	writeFixture(t, root, "node_modules/local-package/index.js", "installed dependency")
	writeFixture(t, root, ".env.local", "local configuration")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "apps/store/server.js"), filepath.Join(root, "linked.js")); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "not-copied")
	if err := os.Symlink(external, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, ".git/hooks/post-checkout", "#!/bin/sh\ntouch '"+filepath.Join(root, "hook-ran")+"'\n")
	if err := os.Chmod(filepath.Join(root, ".git/hooks/post-checkout"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := runGit(t, root, "status", "--porcelain=v1")
	repository, err := Inspect(ctx, filepath.Join(root, "apps/store"))
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := Create(ctx, t.TempDir(), "store-qa", repository)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"apps/store/server.js": "working copy", "apps/store/untracked.txt": "untracked",
		"node_modules/local-package/index.js": "installed dependency", ".env.local": "local configuration", "linked.js": "working copy",
	} {
		content, err := os.ReadFile(filepath.Join(checkout, name))
		if err != nil || string(content) != want {
			t.Fatalf("snapshot %s = %q, %v; want %q", name, content, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(checkout, "deleted.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file was restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "hook-ran")); !os.IsNotExist(err) {
		t.Fatalf("Git checkout hook ran: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(checkout, "external")); err != nil || target != external {
		t.Fatalf("external link = %q, %v", target, err)
	}
	writeFixture(t, checkout, "node_modules/local-package/index.js", "independent dependency")
	content, err := os.ReadFile(filepath.Join(root, "node_modules/local-package/index.js"))
	if err != nil || string(content) != "installed dependency" {
		t.Fatalf("original dependency was changed: %q, %v", content, err)
	}
	if after := runGit(t, root, "status", "--porcelain=v1"); after != before {
		t.Fatalf("original index or checkout changed: before=%s after=%s", before, after)
	}
	if head := strings.TrimSpace(runGit(t, checkout, "rev-parse", "HEAD")); head != repository.Commit {
		t.Fatalf("worktree HEAD = %q, want %q", head, repository.Commit)
	}
	if _, err := git(ctx, checkout, "symbolic-ref", "HEAD"); err == nil {
		t.Fatal("automatic worktree should have detached HEAD without creating or switching a user branch")
	}
	// A clone of an already isolated environment can itself be isolated.
	linked, err := Inspect(ctx, filepath.Join(checkout, "apps/store"))
	if err != nil || linked.Root != checkout {
		t.Fatalf("inspect linked worktree = %#v, %v", linked, err)
	}
}

func TestPreparationFailuresLeaveNoWorktreeRegistration(t *testing.T) {
	ctx := context.Background()
	root := repositoryFixture(t)
	writeFixture(t, root, "app.txt", "source")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Portless Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	repository, err := Inspect(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "nested/.git", "gitdir: somewhere")
	directory := t.TempDir()
	if _, err := Create(ctx, directory, "store-qa", repository); err == nil || !strings.Contains(err.Error(), "nested Git repository") {
		t.Fatalf("nested repository error = %v", err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("failed checkout was retained: %v, %v", entries, err)
	}
	if list := runGit(t, root, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("failed checkout remains registered: %s", list)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Create(canceled, directory, "store-qa", repository); err == nil {
		t.Fatal("canceled preparation succeeded")
	}
	if _, err := Create(ctx, filepath.Join(root, "checkouts"), "store-qa", repository); err == nil {
		t.Fatal("created a recursively nested checkout")
	}
	if _, err := os.Stat(filepath.Join(root, "checkouts")); !os.IsNotExist(err) {
		t.Fatalf("invalid destination changed the source: %v", err)
	}
}

func TestInspectRequiresGitAndACommittedBase(t *testing.T) {
	if _, err := Inspect(context.Background(), t.TempDir()); err == nil {
		t.Fatal("non-Git source was accepted")
	}
	if _, err := Inspect(context.Background(), repositoryFixture(t)); err == nil || !strings.Contains(err.Error(), "at least one commit") {
		t.Fatalf("unborn repository error = %v", err)
	}
}

func repositoryFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "-q")
	return root
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	output, err := git(context.Background(), directory, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func writeFixture(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
