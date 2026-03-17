package appserver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTailBufferKeepsNewestOutputWithinLimit(t *testing.T) {
	var buffer tailBuffer
	buffer.maxBytes = 64
	for i := 0; i < 12; i++ {
		_, err := buffer.WriteString("line-" + strings.Repeat("x", 8) + "\n")
		if err != nil {
			t.Fatalf("WriteString: %v", err)
		}
	}
	got := buffer.String()
	if len(got) > buffer.maxBytes {
		t.Fatalf("expected bounded buffer, got %d bytes", len(got))
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if !strings.Contains(got, "line-xxxxxxxx") {
		t.Fatalf("expected newest output to survive, got %q", got)
	}
}

func TestTailBufferPreservesUtf8Boundaries(t *testing.T) {
	var buffer tailBuffer
	buffer.maxBytes = 21
	_, err := buffer.WriteString(strings.Repeat("é", 20))
	if err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	got := buffer.String()
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid utf-8, got %q", got)
	}
}
