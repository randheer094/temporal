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

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	const maxBytes = 64

	w, err := NewWithMaxSize(path, maxBytes)
	if err != nil {
		t.Fatal("NewWithMaxSize failed:", err)
	}

	// Write enough to cross maxBytes and force one rotation, then keep
	// writing so the active file has fresh content too.
	const line = "0123456789ABCDEF\n" // 17 bytes
	for i := 0; i < 6; i++ {
		w.Write(line)
	}
	w.Close()

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup not created: %v", err)
	}
	if len(backup) < maxBytes {
		t.Errorf("backup size = %d, want >= %d", len(backup), maxBytes)
	}

	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("active file missing after rotation: %v", err)
	}
	// The post-rotation writes ended up here.
	if !strings.Contains(string(active), "0123456789ABCDEF") {
		t.Errorf("active file content unexpected: %q", string(active))
	}
}

func TestRotationOverwritesPreviousBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	if err := os.WriteFile(path+".1", []byte("stale-backup\n"), 0640); err != nil {
		t.Fatal("seed backup failed:", err)
	}

	const maxBytes = 32
	w, err := NewWithMaxSize(path, maxBytes)
	if err != nil {
		t.Fatal("NewWithMaxSize failed:", err)
	}
	for i := 0; i < 4; i++ {
		w.Write("0123456789ABCDEF\n")
	}
	w.Close()

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if strings.Contains(string(backup), "stale-backup") {
		t.Errorf("rotation did not replace stale backup: %q", string(backup))
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
