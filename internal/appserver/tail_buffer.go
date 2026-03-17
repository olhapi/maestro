package appserver

import (
	"strings"
	"unicode/utf8"
)

const defaultOutputBufferLimitBytes = 256 * 1024

var tailBufferTruncationMarker = "\n...[truncated]\n"

type tailBuffer struct {
	text     string
	maxBytes int
}

func (b *tailBuffer) WriteString(value string) (int, error) {
	if b.maxBytes <= 0 {
		b.maxBytes = defaultOutputBufferLimitBytes
	}
	b.text += value
	b.trim()
	return len(value), nil
}

func (b *tailBuffer) WriteByte(value byte) error {
	_, err := b.WriteString(string(value))
	return err
}

func (b *tailBuffer) String() string {
	return b.text
}

func (b *tailBuffer) trim() {
	if b.maxBytes <= 0 || len(b.text) <= b.maxBytes {
		return
	}
	budget := b.maxBytes - len(tailBufferTruncationMarker)
	if budget <= 0 {
		b.text = tailBufferTruncationMarker[:minInt(len(tailBufferTruncationMarker), b.maxBytes)]
		return
	}
	tail := trimToTrailingBytes(b.text, budget)
	if strings.HasPrefix(tail, "\n") {
		tail = strings.TrimPrefix(tail, "\n")
	}
	b.text = tailBufferTruncationMarker + tail
	if len(b.text) > b.maxBytes {
		b.text = trimToTrailingBytes(b.text, b.maxBytes)
	}
}

func trimToTrailingBytes(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
