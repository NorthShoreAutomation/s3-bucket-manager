package progress

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReaderReportsRunningByteCount(t *testing.T) {
	src := strings.NewReader("abcdefghij") // 10 bytes
	var calls []int64
	r := NewReader(src, func(done int64) { calls = append(calls, done) })

	buf := make([]byte, 3)
	for {
		_, err := r.Read(buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
	}

	if len(calls) == 0 {
		t.Fatalf("expected at least one progress callback")
	}
	if got := calls[len(calls)-1]; got != 10 {
		t.Errorf("final done = %d, want 10", got)
	}
	// Counter must be monotonically non-decreasing.
	for i := 1; i < len(calls); i++ {
		if calls[i] < calls[i-1] {
			t.Errorf("progress went backwards: %v", calls)
		}
	}
}

func TestReaderNilCallbackIsSafe(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte("hello")), nil)
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
}

func TestReaderZeroReadDoesNotEmit(t *testing.T) {
	// io.Reader is allowed to return (0, nil); the callback must not fire.
	var calls int
	r := NewReader(zeroThenEOF{}, func(int64) { calls++ })
	_, _ = io.ReadAll(r)
	if calls != 0 {
		t.Errorf("expected 0 callbacks on zero-byte reads, got %d", calls)
	}
}

type zeroThenEOF struct{ done bool }

func (z zeroThenEOF) Read(p []byte) (int, error) {
	if z.done {
		return 0, io.EOF
	}
	return 0, io.EOF
}
