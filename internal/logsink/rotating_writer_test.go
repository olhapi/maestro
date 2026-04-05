package logsink

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatesBySize(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "maestro.log")
	w, err := New(p, 32, 3)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	defer w.Close()

	line := strings.Repeat("x", 20)
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected active log file: %v", err)
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatalf("expected rotated file .1: %v", err)
	}
}

func TestNewAppliesDefaultsAndMulti(t *testing.T) {
	d := t.TempDir()
	w, err := New(filepath.Join(d, "maestro.log"), 0, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if w.maxBytes != 10*1024*1024 {
		t.Fatalf("expected default maxBytes, got %d", w.maxBytes)
	}
	if w.maxFiles != 3 {
		t.Fatalf("expected default maxFiles, got %d", w.maxFiles)
	}

	var stdout bytes.Buffer
	var file bytes.Buffer
	if _, err := Multi(&stdout, &file).Write([]byte("hello")); err != nil {
		t.Fatalf("Multi.Write: %v", err)
	}
	if stdout.String() != "hello" || file.String() != "hello" {
		t.Fatalf("unexpected multiwriter output: stdout=%q file=%q", stdout.String(), file.String())
	}
}

func TestWriteWithoutRotation(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "maestro.log")
	w, err := New(p, 1024, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("small entry")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(p + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected no rotated file, stat err=%v", err)
	}
}

func TestCloseWithoutFileAndWriteAfterClose(t *testing.T) {
	if err := (&RotatingWriter{}).Close(); err != nil {
		t.Fatalf("expected zero-value Close to succeed, got %v", err)
	}

	d := t.TempDir()
	p := filepath.Join(d, "maestro.log")
	w, err := New(p, 1024, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := w.Write([]byte("after-close")); err == nil {
		t.Fatal("expected write against closed file to fail")
	}
}

func TestRotateMaintainsHistory(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "maestro.log")
	w, err := New(p, 8, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	for _, chunk := range []string{"first", "second", "third", "fourth"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write %q: %v", chunk, err)
		}
	}

	for _, rotated := range []string{p, p + ".1", p + ".2"} {
		if _, err := os.Stat(rotated); err != nil {
			t.Fatalf("expected rotated log file %s to exist: %v", rotated, err)
		}
	}
}

func TestNewRejectsBlockedParentPath(t *testing.T) {
	d := t.TempDir()
	parentFile := filepath.Join(d, "blocked")
	if err := os.WriteFile(parentFile, []byte("file"), 0o644); err != nil {
		t.Fatalf("WriteFile blocked parent: %v", err)
	}

	if _, err := New(filepath.Join(parentFile, "nested", "maestro.log"), 1024, 3); err == nil {
		t.Fatal("expected blocked parent path to fail")
	}
}

func TestRotateIfNeededReportsRenameFailures(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "maestro.log")
	w, err := New(p, 8, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(p+".1", []byte("rotated"), 0o644); err != nil {
		t.Fatalf("WriteFile rotated file: %v", err)
	}
	if err := os.Mkdir(p+".2", 0o755); err != nil {
		t.Fatalf("Mkdir blocking directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p+".2", "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile blocking child: %v", err)
	}

	if _, err := w.Write([]byte("trigger-rotation")); err == nil {
		t.Fatal("expected rotation rename failure")
	}
}
