package directorypicker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMacOSSelectionReturnsAnAbsoluteDirectory(t *testing.T) {
	directory := t.TempDir()
	var executable string
	var arguments []string
	picker := selector{
		platform: "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("unexpected lookup") },
		run: func(_ context.Context, command string, commandArguments ...string) ([]byte, int, error) {
			executable = command
			arguments = append([]string(nil), commandArguments...)
			return []byte(directory + string(os.PathSeparator) + "\n"), 0, nil
		},
	}

	selected, canceled, err := picker.selectDirectory(context.Background(), "Choose a source", directory)
	if err != nil {
		t.Fatal(err)
	}
	if canceled || selected != filepath.Clean(directory) {
		t.Fatalf("selection = %q canceled=%t", selected, canceled)
	}
	if executable != "osascript" {
		t.Fatalf("executable = %q", executable)
	}
	wantTail := []string{"Choose a source", filepath.Clean(directory)}
	if len(arguments) < 2 || !reflect.DeepEqual(arguments[len(arguments)-2:], wantTail) {
		t.Fatalf("arguments = %#v", arguments)
	}
}

func TestMacOSSelectionTreatsUserCancellationAsAResult(t *testing.T) {
	picker := selector{
		platform: "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("unexpected lookup") },
		run: func(context.Context, string, ...string) ([]byte, int, error) {
			return []byte("execution error: User canceled. (-128)"), 1, errors.New("exit status 1")
		},
	}

	selected, canceled, err := picker.selectDirectory(context.Background(), "Choose a source", "")
	if err != nil || !canceled || selected != "" {
		t.Fatalf("selection = %q canceled=%t err=%v", selected, canceled, err)
	}
}

func TestLinuxSelectionUsesZenityAndIgnoresAnInvalidInitialPath(t *testing.T) {
	directory := t.TempDir()
	var arguments []string
	picker := selector{
		platform: "linux",
		lookPath: func(name string) (string, error) {
			if name == "zenity" {
				return "/usr/bin/zenity", nil
			}
			return "", errors.New("not found")
		},
		run: func(_ context.Context, executable string, commandArguments ...string) ([]byte, int, error) {
			if executable != "/usr/bin/zenity" {
				t.Fatalf("executable = %q", executable)
			}
			arguments = append([]string(nil), commandArguments...)
			return []byte(directory + "\n"), 0, nil
		},
	}

	selected, canceled, err := picker.selectDirectory(context.Background(), "Choose a source", filepath.Join(directory, "missing"))
	if err != nil || canceled || selected != filepath.Clean(directory) {
		t.Fatalf("selection = %q canceled=%t err=%v", selected, canceled, err)
	}
	for _, argument := range arguments {
		if len(argument) >= len("--filename=") && argument[:len("--filename=")] == "--filename=" {
			t.Fatalf("invalid initial directory was passed to zenity: %#v", arguments)
		}
	}
}

func TestSelectionFailsWhenNoNativeChooserIsAvailable(t *testing.T) {
	picker := selector{
		platform: "linux",
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run: func(context.Context, string, ...string) ([]byte, int, error) {
			return nil, 0, errors.New("unexpected command")
		},
	}

	if _, _, err := picker.selectDirectory(context.Background(), "Choose a source", ""); err == nil {
		t.Fatal("selection unexpectedly succeeded")
	}
}
