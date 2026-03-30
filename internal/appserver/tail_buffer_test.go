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

func TestTailBufferWriteByteAndTinyLimits(t *testing.T) {
	var buffer tailBuffer
	if err := buffer.WriteByte('x'); err != nil {
		t.Fatalf("WriteByte: %v", err)
	}
	if buffer.maxBytes != defaultOutputBufferLimitBytes {
		t.Fatalf("expected default maxBytes to be applied, got %d", buffer.maxBytes)
	}
	if got := buffer.String(); got != "x" {
		t.Fatalf("unexpected buffer contents after WriteByte: %q", got)
	}

	if got := trimToTrailingBytes([]byte("hello"), 0); string(got) != "hello" {
		t.Fatalf("expected non-positive trim budget to keep original bytes, got %q", got)
	}

	tiny := tailBuffer{maxBytes: len(tailBufferTruncationMarker) - 1}
	if _, err := tiny.WriteString(strings.Repeat("z", len(tailBufferTruncationMarker)+3)); err != nil {
		t.Fatalf("WriteString tiny: %v", err)
	}
	if got := tiny.String(); got != string(tailBufferTruncationMarker[:len(tailBufferTruncationMarker)-1]) {
		t.Fatalf("unexpected tiny buffer contents: %q", got)
	}

	leading := tailBuffer{maxBytes: len(tailBufferTruncationMarker) + 4}
	if _, err := leading.WriteString(strings.Repeat("x", 32) + "\nabc"); err != nil {
		t.Fatalf("WriteString leading: %v", err)
	}
	if strings.Contains(leading.String(), "\n\nabc") {
		t.Fatalf("expected trimming to remove the extra leading newline, got %q", leading.String())
	}
}

func TestMinInt(t *testing.T) {
	if got := minInt(3, 7); got != 3 {
		t.Fatalf("minInt(3, 7) = %d, want 3", got)
	}
	if got := minInt(9, -2); got != -2 {
		t.Fatalf("minInt(9, -2) = %d, want -2", got)
	}
}
