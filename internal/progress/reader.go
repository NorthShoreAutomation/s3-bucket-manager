package progress

import "io"

// Reader wraps an io.Reader and invokes onProgress after every non-zero
// Read with the running cumulative byte count. It is goroutine-free and
// performs no throttling; callers MUST rate-limit any UI work themselves.
//
// A nil onProgress callback is supported and turns the wrapper into a
// pass-through.
type Reader struct {
	r          io.Reader
	done       int64
	onProgress func(done int64)
}

// NewReader returns a Reader that wraps r. The callback may be nil.
func NewReader(r io.Reader, onProgress func(done int64)) *Reader {
	return &Reader{r: r, onProgress: onProgress}
}

// Read implements io.Reader.
func (p *Reader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.done += int64(n)
		if p.onProgress != nil {
			p.onProgress(p.done)
		}
	}
	return n, err
}

// Done returns the total bytes that have flowed through this Reader.
func (p *Reader) Done() int64 { return p.done }
