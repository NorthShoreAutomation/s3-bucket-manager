package progress

import (
	"sync"
	"testing"
)

type memWriterAt struct {
	buf []byte
}

func (m *memWriterAt) WriteAt(p []byte, off int64) (int, error) {
	copy(m.buf[off:], p)
	return len(p), nil
}

func TestWriterAtReportsRunningByteCount(t *testing.T) {
	dest := &memWriterAt{buf: make([]byte, 10)}
	var mu sync.Mutex
	var calls []int64
	w := NewWriterAt(dest, func(done int64) {
		mu.Lock()
		calls = append(calls, done)
		mu.Unlock()
	})

	// Simulate concurrent part writes at distinct offsets.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := w.WriteAt([]byte("ab"), int64(i*2)); err != nil {
				t.Errorf("WriteAt returned error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := w.Done(); got != 10 {
		t.Errorf("Done = %d, want 10", got)
	}
	if len(calls) != 5 {
		t.Errorf("expected 5 callbacks, got %d", len(calls))
	}
}

func TestWriterAtNilCallbackIsSafe(t *testing.T) {
	dest := &memWriterAt{buf: make([]byte, 5)}
	w := NewWriterAt(dest, nil)
	if _, err := w.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("WriteAt returned error: %v", err)
	}
	if got := string(dest.buf); got != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}
