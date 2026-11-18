package docker

import (
	"bytes"
	"testing"
)

func TestCappedBuffer(t *testing.T) {
	var buf cappedBuffer
	payload := bytes.Repeat([]byte("a"), stderrLimit+100)
	n, err := buf.Write(payload)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(payload) {
		t.Fatalf("Write() = %d, want %d", n, len(payload))
	}
	if buf.buf.Len() != stderrLimit {
		t.Fatalf("stored = %d, want %d", buf.buf.Len(), stderrLimit)
	}
	if _, err := buf.Write([]byte("more")); err != nil {
		t.Fatal(err)
	}
	if buf.buf.Len() != stderrLimit {
		t.Fatalf("stored after overflow = %d", buf.buf.Len())
	}
}
