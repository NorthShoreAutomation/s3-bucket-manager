// Package progress provides shared building blocks for byte-counting
// progress reporting across upload, download, and HTTP-copy flows.
package progress

// Snapshot is an immutable view of the current state of a transfer. It is
// designed to be stored and swapped via sync/atomic.Pointer so a UI
// goroutine can read the latest values without locks.
type Snapshot struct {
	Done  int64 // bytes transferred so far
	Total int64 // total bytes; -1 when unknown
}
