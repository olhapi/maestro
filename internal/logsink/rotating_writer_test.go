package logsink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatesBySize(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "symphony.log")
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
