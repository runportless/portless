package logstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSinkAndReadStructuredLogs(t *testing.T) {
	root := t.TempDir()
	stdout, err := OpenSink(filepath.Join(root, "checkout", "1"), "checkout", "stdout", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdout.Write([]byte("ready\n")); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{
		filepath.Join(root, "checkout", "1"):                 0o700,
		filepath.Join(root, "checkout", "1", "stdout.jsonl"): 0o600,
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat %s: %v", path, statErr)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), mode)
		}
	}

	since := time.Now().UTC()
	time.Sleep(time.Millisecond)
	stderr, err := OpenSink(filepath.Join(root, "checkout", "2"), "checkout", "stderr", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Write([]byte("failed\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := Read(root, []string{"checkout"}, 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %#v", entries)
	}
	if entries[0].Service != "checkout" || entries[0].Stream != "stdout" || entries[0].Generation != 1 || entries[0].Message != "ready" {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Stream != "stderr" || entries[1].Generation != 2 || entries[1].Message != "failed" {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}

	entries, err = Read(root, []string{"checkout"}, 1, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Message != "failed" {
		t.Fatalf("expected the newest matching entry, got %#v", entries)
	}
}

func TestOpenSinkPrunesOldGenerations(t *testing.T) {
	root := t.TempDir()
	serviceRoot := filepath.Join(root, "orders")
	for generation := int64(1); generation <= retainedRuns+2; generation++ {
		sink, err := OpenSink(filepath.Join(serviceRoot, time.Unix(generation, 0).Format("5")), "orders", "stdout", generation)
		if err != nil {
			t.Fatal(err)
		}
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(serviceRoot, "1")); !os.IsNotExist(err) {
		t.Fatalf("expected generation 1 to be pruned, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(serviceRoot, "2")); !os.IsNotExist(err) {
		t.Fatalf("expected generation 2 to be pruned, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(serviceRoot, "3")); err != nil {
		t.Fatalf("expected retained generation 3: %v", err)
	}
	retention := Retention()
	if retention.RetainedRuns != retainedRuns || retention.MaxStreamBytes != maxLogFileBytes || retention.LastPrunedAt == nil {
		t.Fatalf("retention status after pruning = %#v", retention)
	}
}

func TestFullSinkDropsAdditionalLinesWithoutBreakingWriter(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "full-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	sink := &Sink{file: file, service: "checkout", stream: "stdout", generation: 1, written: maxLogFileBytes, full: true}
	content := []byte("still running\n")
	written, err := sink.Write(content)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(content) {
		t.Fatalf("expected writer to accept %d bytes, got %d", len(content), written)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected no additional bytes, got %d", info.Size())
	}
}
