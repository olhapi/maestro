package appserver

import (
	"bytes"
	"unicode/utf8"
)

const defaultOutputBufferLimitBytes = 256 * 1024

var tailBufferTruncationMarker = []byte("\n...[truncated]\n")

type tailBuffer struct {
	buf      []byte
	maxBytes int
}

func (b *tailBuffer) WriteString(value string) (int, error) {
	if b.maxBytes <= 0 {
		b.maxBytes = defaultOutputBufferLimitBytes
	}
	b.buf = append(b.buf, value...)
	b.trim()
	return len(value), nil
}

func (b *tailBuffer) WriteByte(value byte) error {
	_, err := b.WriteString(string(value))
	return err
}

func (b *tailBuffer) String() string {
	return string(b.buf)
}

func (b *tailBuffer) trim() {
	if b.maxBytes <= 0 || len(b.buf) <= b.maxBytes {
		return
	}
	budget := b.maxBytes - len(tailBufferTruncationMarker)
	if budget <= 0 {
		b.buf = append([]byte(nil), tailBufferTruncationMarker[:minInt(len(tailBufferTruncationMarker), b.maxBytes)]...)
		return
	}
	tail := trimToTrailingBytes(b.buf, budget)
	if bytes.HasPrefix(tail, []byte("\n")) {
		tail = tail[1:]
	}
	b.buf = append(append(make([]byte, 0, len(tailBufferTruncationMarker)+len(tail)), tailBufferTruncationMarker...), tail...)
	if len(b.buf) > b.maxBytes {
		b.buf = trimToTrailingBytes(b.buf, b.maxBytes)
	}
}

func trimToTrailingBytes(value []byte, maxBytes int) []byte {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return append([]byte(nil), value...)
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.Valid(value[start:]) {
		start++
	}
	return append([]byte(nil), value[start:]...)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
