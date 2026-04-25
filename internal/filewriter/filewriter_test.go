package filewriter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	w, err := New(path)
	if err != nil {
		t.Fatal("New failed:", err)
	}

	w.Write("hello\n")
	w.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("ReadFile failed:", err)
	}
	if string(content) != "hello\n" {
		t.Errorf("got %q, want %q", string(content), "hello\n")
	}
}

func TestPreservesOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	w, err := New(path)
	if err != nil {
		t.Fatal("New failed:", err)
	}

	const n = 200
	for i := 0; i < n; i++ {
		w.Write(fmt.Sprintf("%d\n", i))
	}
	w.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("ReadFile failed:", err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	for i, line := range lines {
		want := fmt.Sprintf("%d", i)
		if line != want {
			t.Fatalf("line %d: got %q, want %q", i, line, want)
		}
	}
}

func TestAppendsToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	if err := os.WriteFile(path, []byte("existing\n"), 0644); err != nil {
		t.Fatal("seed file failed:", err)
	}

	w, err := New(path)
	if err != nil {
		t.Fatal("New failed:", err)
	}
	w.Write("new\n")
	w.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("ReadFile failed:", err)
	}
	if string(content) != "existing\nnew\n" {
		t.Errorf("got %q, want %q", string(content), "existing\nnew\n")
	}
}

func TestNewReturnsErrorForUnwritablePath(t *testing.T) {
	// A path under a regular file (not a directory) cannot be opened.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatal("seed failed:", err)
	}
	_, err := New(filepath.Join(notADir, "child.log"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
