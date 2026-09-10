package progress

import (
	"io"
	"sync/atomic"
)

// WriterAt wraps an io.WriterAt and invokes onProgress after every non-zero
// WriteAt with the running cumulative byte count. Unlike Reader, it is safe
// for concurrent use: the S3 download manager writes parts from multiple
// goroutines, so the counter is atomic. Callbacks may fire concurrently and
// totals may arrive out of order; callers MUST use atomic-safe sinks (e.g.
// atomic.Pointer[Snapshot]) and rate-limit any UI work themselves.
//
// A nil onProgress callback is supported and turns the wrapper into a
// pass-through.
type WriterAt struct {
	w          io.WriterAt
	done       atomic.Int64
	onProgress func(done int64)
}

// NewWriterAt returns a WriterAt that wraps w. The callback may be nil.
func NewWriterAt(w io.WriterAt, onProgress func(done int64)) *WriterAt {
	return &WriterAt{w: w, onProgress: onProgress}
}

// WriteAt implements io.WriterAt.
func (p *WriterAt) WriteAt(buf []byte, off int64) (int, error) {
	n, err := p.w.WriteAt(buf, off)
	if n > 0 {
		done := p.done.Add(int64(n))
		if p.onProgress != nil {
			p.onProgress(done)
		}
	}
	return n, err
}

// Done returns the total bytes written through this WriterAt.
func (p *WriterAt) Done() int64 { return p.done.Load() }
