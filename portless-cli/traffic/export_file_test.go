package traffic

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExportFilePublishesOnlyCompletedDownloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.json")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("interrupted export")
	err := writePrivateFile(path, func(w io.Writer) error { _, _ = io.WriteString(w, "partial"); return failure }, true)
	if !errors.Is(err, failure) {
		t.Fatalf("write error=%v", err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "original" {
		t.Fatal("failed export replaced the existing file")
	}
	if err := writePrivateFile(path, func(w io.Writer) error { _, err := io.WriteString(w, "complete"); return err }, true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("export permissions are not private")
	}
	newPath := filepath.Join(filepath.Dir(path), "new.json")
	err = writePrivateFile(newPath, func(w io.Writer) error {
		if err := os.WriteFile(newPath, []byte("concurrent"), 0600); err != nil {
			return err
		}
		_, err := io.WriteString(w, "complete")
		return err
	}, false)
	if err == nil {
		t.Fatal("non-forced export replaced a concurrently created file")
	}
	if content, err := os.ReadFile(newPath); err != nil || string(content) != "concurrent" {
		t.Fatal("concurrent file was overwritten")
	}
}
