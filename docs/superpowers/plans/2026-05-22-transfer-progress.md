# Transfer Progress for Upload & Download — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a progress bar, transfer-rate readout, percentage-complete, and ETA to the TUI's local-file upload (`p`) and object download (`g`) flows, reusing the same rendering pattern as the existing URL-upload progress UI.

**Architecture:** Extract a shared `internal/progress` package holding a counting `io.Reader`, an atomic `Snapshot`, and rate/duration formatters. Replace the duplicate `countingReader` in `internal/httpcopy` and the local formatters in `internal/tui/urlupload.go` to use the shared package. Add transfer state + a 250&nbsp;ms ticker to `bucketsModel` and wire the upload and download keypaths through `progress.Reader`. Widen `aws.Client.DownloadObject` to also return the object size so the bar can show percentage and ETA.

**Tech Stack:** Go 1.26, `aws-sdk-go-v2`, Charmbracelet `bubbletea` / `bubbles/progress` / `bubbles/spinner`.

---

## Spec reference

This plan implements `docs/superpowers/specs/2026-05-22-transfer-progress-design.html`. Re-read that spec if anything below is ambiguous.

## File map

**Create:**
- `internal/progress/snapshot.go` — `Snapshot` struct.
- `internal/progress/reader.go` — `Reader` (counting `io.Reader`) + `NewReader`.
- `internal/progress/format.go` — `FormatRate`, `FormatDuration`, `ParseRateBytesPerSec`.
- `internal/progress/reader_test.go`
- `internal/progress/format_test.go`

**Modify:**
- `internal/httpcopy/copy.go` — replace local `countingReader` with `progress.Reader`.
- `internal/tui/urlupload.go` — delete the local `formatRate`/`formatDuration`/`parseRateBytesPerSec`; call into `progress`.
- `internal/tui/urlupload_test.go` — remove tests now living in `progress/format_test.go`.
- `internal/aws/s3.go` — widen `DownloadObject` to `(io.ReadCloser, int64, error)`.
- `internal/tui/buckets.go` — add transfer state, ticker, rendering, wire `p` (upload) and `g` (download).
- `CHANGELOG.md` — note the change under Unreleased.

> **Note on `formatSize`:** `formatSize` lives in `internal/tui/styles.go` and is used by many TUI files. **Do not move it.** The new `progress` package only owns the rate/duration helpers.

---

### Task 1: Create `progress.Snapshot`

**Files:**
- Create: `internal/progress/snapshot.go`

- [ ] **Step 1: Create the file**

```go
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
```

- [ ] **Step 2: Compile**

Run: `go build ./internal/progress/...`
Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/progress/snapshot.go
git commit -m "feat(progress): add Snapshot type"
```

---

### Task 2: Create `progress.Reader` with TDD

**Files:**
- Create: `internal/progress/reader.go`
- Test: `internal/progress/reader_test.go`

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/progress/...`
Expected: build failure (`NewReader` undefined).

- [ ] **Step 3: Implement `Reader`**

Create `internal/progress/reader.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/progress/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/progress/reader.go internal/progress/reader_test.go
git commit -m "feat(progress): add counting Reader with progress callback"
```

---

### Task 3: Port format helpers into `progress` package

**Files:**
- Create: `internal/progress/format.go`
- Create: `internal/progress/format_test.go`

The helpers `formatRate`, `formatDuration`, and `parseRateBytesPerSec` currently live in `internal/tui/urlupload.go` (lines ~395–458). This task copies them into `internal/progress` under exported names. The originals stay in place for now; Task 4 removes them.

- [ ] **Step 1: Write the failing tests**

Create `internal/progress/format_test.go`:

```go
package progress

import (
	"testing"
	"time"
)

func TestFormatRate(t *testing.T) {
	tests := []struct {
		name string
		bps  float64
		want string
	}{
		{"zero", 0, "—/s"},
		{"negative", -1, "—/s"},
		{"bytes", 512, "512 B/s"},
		{"kilobytes", 2048, "2.0 KB/s"},
		{"megabytes", 5 * (1 << 20), "5.0 MB/s"},
		{"gigabytes", 3 * (1 << 30), "3.0 GB/s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatRate(tc.bps); got != tc.want {
				t.Errorf("FormatRate(%v) = %q, want %q", tc.bps, got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"negative", -1 * time.Second, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 4*time.Minute + 12*time.Second, "4m12s"},
		{"hours", 1*time.Hour + 23*time.Minute, "1h23m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDuration(tc.d); got != tc.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestParseRateBytesPerSecRoundTrip(t *testing.T) {
	tests := []float64{512, 2048, 5 * (1 << 20), 3 * (1 << 30)}
	for _, bps := range tests {
		got := ParseRateBytesPerSec(FormatRate(bps))
		// Allow 5% slack since FormatRate truncates to 1 decimal.
		diff := got - bps
		if diff < 0 {
			diff = -diff
		}
		if diff/bps > 0.05 {
			t.Errorf("round-trip %v -> %q -> %v drift exceeds 5%%", bps, FormatRate(bps), got)
		}
	}
}

func TestParseRateBytesPerSecEmpty(t *testing.T) {
	if ParseRateBytesPerSec("") != 0 {
		t.Errorf("expected 0 for empty input")
	}
	if ParseRateBytesPerSec("—/s") != 0 {
		t.Errorf("expected 0 for em-dash placeholder")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/progress/...`
Expected: build failure (`FormatRate`/`FormatDuration`/`ParseRateBytesPerSec` undefined).

- [ ] **Step 3: Implement the helpers**

Create `internal/progress/format.go`:

```go
package progress

import (
	"fmt"
	"strings"
	"time"
)

// FormatRate formats bytes/s into a human-readable string like "12.3 MB/s".
// Zero and negative values render as "—/s".
func FormatRate(bps float64) string {
	if bps <= 0 {
		return "—/s"
	}
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case bps >= gb:
		return fmt.Sprintf("%.1f GB/s", bps/gb)
	case bps >= mb:
		return fmt.Sprintf("%.1f MB/s", bps/mb)
	case bps >= kb:
		return fmt.Sprintf("%.1f KB/s", bps/kb)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

// ParseRateBytesPerSec is the inverse of FormatRate, used so the ETA
// calculation can be driven by the displayed rate rather than a raw float.
// Returns 0 on unparseable input or the em-dash placeholder.
func ParseRateBytesPerSec(r string) float64 {
	if r == "—/s" || r == "" {
		return 0
	}
	var val float64
	var unit string
	if _, err := fmt.Sscanf(r, "%f %s", &val, &unit); err != nil {
		return 0
	}
	switch {
	case strings.HasPrefix(unit, "GB"):
		return val * (1 << 30)
	case strings.HasPrefix(unit, "MB"):
		return val * (1 << 20)
	case strings.HasPrefix(unit, "KB"):
		return val * (1 << 10)
	default:
		return val
	}
}

// FormatDuration renders a duration as "1h23m", "4m12s", or "45s".
// Negative durations render as "0s".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/progress/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/progress/format.go internal/progress/format_test.go
git commit -m "feat(progress): add FormatRate, FormatDuration, ParseRateBytesPerSec"
```

---

### Task 4: Switch `urlupload.go` to use the shared helpers

**Files:**
- Modify: `internal/tui/urlupload.go` (delete local `formatRate`, `parseRateBytesPerSec`, `formatDuration`; replace call sites)
- Modify: `internal/tui/urlupload_test.go` (remove `TestFormatRate`, `TestFormatDuration` — they now live in `progress/format_test.go`)

- [ ] **Step 1: Add the import**

In `internal/tui/urlupload.go`, add to the import block:

```go
"github.com/dcorbell/s3m/internal/progress"
```

- [ ] **Step 2: Replace call sites in `urlupload.go`**

In `statusLine` (around line 366), change:

```go
done := formatSize(snap.done)
rate := formatRate(m.currentRate)
```

to:

```go
done := formatSize(snap.done)
rate := progress.FormatRate(m.currentRate)
```

(Note: `formatSize` stays as-is — it lives in `internal/tui/styles.go` and is shared within the `tui` package.)

A few lines further down, change:

```go
rateVal := parseRateBytesPerSec(rate)
if rateVal > 0 {
    remaining := float64(snap.total-snap.done) / rateVal
    eta = " — ETA " + formatDuration(time.Duration(remaining*float64(time.Second)))
}
```

to:

```go
rateVal := progress.ParseRateBytesPerSec(rate)
if rateVal > 0 {
    remaining := float64(snap.total-snap.done) / rateVal
    eta = " — ETA " + progress.FormatDuration(time.Duration(remaining*float64(time.Second)))
}
```

- [ ] **Step 3: Delete the local helpers**

Delete `formatRate`, `parseRateBytesPerSec`, and `formatDuration` from `internal/tui/urlupload.go` (currently lines ~394–458 — verify the exact range when editing). Do **not** touch `formatSize` in `styles.go`.

- [ ] **Step 4: Update `urlupload_test.go`**

Delete `TestFormatRate` (around line 107) and `TestFormatDuration` (around line 152) from `internal/tui/urlupload_test.go` — they are replaced by the equivalent tests in `internal/progress/format_test.go`. Other tests in this file stay.

- [ ] **Step 5: Verify everything still builds and passes**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/urlupload.go internal/tui/urlupload_test.go
git commit -m "refactor(tui): use shared progress helpers in urlupload"
```

---

### Task 5: Switch `httpcopy` to use `progress.Reader`

**Files:**
- Modify: `internal/httpcopy/copy.go` (replace `countingReader` usage; remove the type)

- [ ] **Step 1: Replace the wrapper construction**

In `internal/httpcopy/copy.go` around line 151, change:

```go
// Wrap response body with a counting reader for progress callbacks.
cr := &countingReader{
    r:     resp.Body,
    total: bytesTotal,
    key:   key,
    onProgress: func(done int64) {
        emit(Progress{
            Phase:      "uploading",
            BytesDone:  done,
            BytesTotal: bytesTotal,
            Filename:   key,
        })
    },
    enabled: opt.Progress != nil,
}
```

to:

```go
// Wrap response body with a counting reader for progress callbacks.
var onRead func(int64)
if opt.Progress != nil {
    onRead = func(done int64) {
        emit(Progress{
            Phase:      "uploading",
            BytesDone:  done,
            BytesTotal: bytesTotal,
            Filename:   key,
        })
    }
}
cr := progress.NewReader(resp.Body, onRead)
```

Update the `finalDone` line a few lines below from `finalDone := cr.done` to `finalDone := cr.Done()`.

Add to the import block at the top of the file:

```go
"github.com/dcorbell/s3m/internal/progress"
```

- [ ] **Step 2: Delete the local `countingReader` type**

Delete the `countingReader` struct and its `Read` method from `internal/httpcopy/copy.go` (currently lines ~276–297).

- [ ] **Step 3: Run httpcopy tests**

Run: `go test ./internal/httpcopy/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/httpcopy/copy.go
git commit -m "refactor(httpcopy): use shared progress.Reader"
```

---

### Task 6: Widen `DownloadObject` to return object size

**Files:**
- Modify: `internal/aws/s3.go` (`DownloadObject` at line 512)
- Modify: `internal/tui/buckets.go` (the one call site at line 1820)
- Modify: `internal/aws/s3_test.go` if it exercises `DownloadObject`

- [ ] **Step 1: Check whether s3_test.go exercises `DownloadObject`**

Run: `grep -n "DownloadObject" internal/aws/s3_test.go`
If matches exist, plan to update them in Step 4.

- [ ] **Step 2: Change the signature in `s3.go`**

Replace the existing `DownloadObject` (lines 510–526) with:

```go
// DownloadObject returns the body of an S3 object as an io.ReadCloser
// along with the object's size in bytes (or -1 when the server did not
// supply a Content-Length). The caller is responsible for closing the
// returned reader.
func (c *Client) DownloadObject(ctx context.Context, bucket, key, region string) (io.ReadCloser, int64, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	output, err := c.S3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("could not download %q: %w", key, err)
	}
	size := int64(-1)
	if output.ContentLength != nil {
		size = *output.ContentLength
	}
	return output.Body, size, nil
}
```

- [ ] **Step 3: Update the caller in `buckets.go`**

In `internal/tui/buckets.go` around line 1820, change:

```go
body, err := m.client.DownloadObject(ctx, bucket.name, item.Key, bucket.region)
if err != nil {
    return errMsg{err: fmt.Errorf("could not download %s: %w", item.Name, err)}
}
defer body.Close()
```

to:

```go
body, _, err := m.client.DownloadObject(ctx, bucket.name, item.Key, bucket.region)
if err != nil {
    return errMsg{err: fmt.Errorf("could not download %s: %w", item.Name, err)}
}
defer body.Close()
```

(We discard the size for now — Task 8 wires it through.)

- [ ] **Step 4: Update s3_test.go if needed**

If Step 1 found callers, update them to capture the new return value (`body, _, err := ...` or `body, size, err := ...` as appropriate).

- [ ] **Step 5: Build & test**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/aws/s3.go internal/tui/buckets.go
# include s3_test.go if it was touched
git commit -m "refactor(aws): return object size from DownloadObject"
```

---

### Task 7: Add transfer state and rendering scaffolding to `bucketsModel`

**Files:**
- Modify: `internal/tui/buckets.go`

This task adds the model fields, the tick message, the renderer, and a helper that starts/stops a transfer — without wiring it to the upload or download flows yet. Tasks 8 and 9 wire those flows.

- [ ] **Step 1: Locate the `bucketsModel` struct**

Find the struct definition in `internal/tui/buckets.go` (search for `type bucketsModel struct`). Read its existing fields so you know where to add the new ones — keep grouping consistent with the file's style.

- [ ] **Step 2: Add transfer state fields**

Add a new block of fields to `bucketsModel`:

```go
// Transfer progress (upload via p, download via g).
// transferSnap is non-nil while a transfer is in flight.
transferSnap     *atomic.Pointer[progress.Snapshot]
transferBar      progress.Model // bubbles progress bar
transferLabel    string         // e.g. "Uploading photo.jpg"
transferTotal    int64          // bytes; -1 when unknown
transferLastDone int64
transferLastTime time.Time
transferRate     float64
transferCancel   context.CancelFunc
```

Add the necessary imports if they aren't already in the file:

```go
"context"
"sync/atomic"
"time"

bubblesprogress "github.com/charmbracelet/bubbles/progress"
"github.com/dcorbell/s3m/internal/progress"
```

> **Naming collision warning:** Bubbletea's progress widget package is also called `progress`. Import it under the alias `bubblesprogress` to avoid clashing with our `internal/progress`. The struct field `transferBar` then has type `bubblesprogress.Model`.

Update the `transferBar` field type accordingly: `transferBar bubblesprogress.Model`.

- [ ] **Step 3: Initialize the bar in the constructor**

Find the `bucketsModel` constructor (search for `func newBucketsModel` or wherever `bucketsModel{}` is built). Initialize `transferBar` the same way `urlupload.go` does (around line 111):

```go
transferBar := bubblesprogress.New(
    bubblesprogress.WithDefaultGradient(),
    bubblesprogress.WithoutPercentage(),
)
```

…and assign it to the model. Leave all other fields at their zero values (`transferSnap == nil` is the "no transfer in flight" sentinel).

- [ ] **Step 4: Add the tick message**

Near the other message types in `buckets.go`, add:

```go
// transferTickMsg fires every 250ms while a transfer is in flight and
// drives the progress bar / rate display.
type transferTickMsg struct{}

func transferTick() tea.Cmd {
    return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
        return transferTickMsg{}
    })
}
```

- [ ] **Step 5: Add the tick handler**

In the model's `Update` method, add a case for `transferTickMsg` (place it near other tick / async message cases):

```go
case transferTickMsg:
    if m.transferSnap == nil {
        return m, nil
    }
    snap := m.transferSnap.Load()
    if snap != nil {
        now := time.Now()
        elapsed := now.Sub(m.transferLastTime).Seconds()
        delta := snap.Done - m.transferLastDone
        if elapsed > 0.01 && delta > 0 {
            m.transferRate = float64(delta) / elapsed
        }
        m.transferLastDone = snap.Done
        m.transferLastTime = now
        var barCmd tea.Cmd
        if snap.Total > 0 {
            pct := float64(snap.Done) / float64(snap.Total)
            if pct > 1.0 {
                pct = 1.0
            }
            barCmd = m.transferBar.SetPercent(pct)
        }
        return m, tea.Batch(barCmd, transferTick())
    }
    return m, transferTick()
```

Also add a case so the progress bar's own animation frames are processed:

```go
case bubblesprogress.FrameMsg:
    model, cmd := m.transferBar.Update(msg)
    m.transferBar = model.(bubblesprogress.Model)
    return m, cmd
```

- [ ] **Step 6: Add the render helper**

Add this helper method on `bucketsModel`:

```go
// renderTransferProgress returns the multi-line progress block shown
// during an in-flight upload or download. Returns "" when no transfer
// is active.
func (m bucketsModel) renderTransferProgress() string {
    if m.transferSnap == nil {
        return ""
    }
    snap := m.transferSnap.Load()
    if snap == nil {
        return ""
    }

    label := m.transferLabel
    bar := m.transferBar.View()

    done := formatSize(snap.Done)
    rate := progress.FormatRate(m.transferRate)

    var status string
    if snap.Total > 0 {
        total := formatSize(snap.Total)
        pct := float64(snap.Done) / float64(snap.Total) * 100
        eta := ""
        if rateVal := progress.ParseRateBytesPerSec(rate); rateVal > 0 {
            remaining := float64(snap.Total-snap.Done) / rateVal
            eta = " — ETA " + progress.FormatDuration(time.Duration(remaining*float64(time.Second)))
        }
        status = fmt.Sprintf("%s / %s (%.0f%%) — %s%s", done, total, pct, rate, eta)
    } else {
        status = fmt.Sprintf("%s — %s", done, rate)
    }

    return fmt.Sprintf("  %s\n  %s\n  %s\n", label, bar, dimStyle.Render(status))
}
```

- [ ] **Step 7: Wire the renderer into the browse View**

Find the browse view rendering (search for where `deleteProgress` is rendered — likely in a `viewBrowse` or `View()` method). Replace the current single-line "Uploading X…" / "Downloading X…" output with a block that prefers the new renderer when `m.transferSnap != nil`, falling back to the existing `deleteProgress` line otherwise:

```go
if tp := m.renderTransferProgress(); tp != "" {
    s += tp
} else if m.deleteProgress != "" {
    s += "  " + m.spinner.View() + " " + m.deleteProgress + "\n"
}
```

(Adjust to match the surrounding code's exact style — the goal is: while a transfer is in flight, render the bar+status block instead of the spinner-line placeholder.)

Also: while `m.transferSnap != nil`, the help line at the bottom should show `[esc] cancel` instead of the standard browse help. Search for where the browse help is rendered (likely a constant like `helpStyle.Render("...")`) and gate it on `m.transferSnap != nil`:

```go
help := /* existing browse help string */
if m.transferSnap != nil {
    help = "  [esc] cancel"
}
s += helpStyle.Render(help)
```

- [ ] **Step 8: Build & test**

Run: `go test ./...`
Expected: all packages PASS (no behavior change yet — nothing sets `transferSnap`).

- [ ] **Step 9: Commit**

```bash
git add internal/tui/buckets.go
git commit -m "feat(tui): add transfer-progress scaffolding to bucketsModel"
```

---

### Task 8: Wire the upload (`p`) path through `progress.Reader`

**Files:**
- Modify: `internal/tui/buckets.go` (the upload goroutine starting at line ~1677)

- [ ] **Step 1: Locate the upload goroutine**

Find the block that fires when the file picker returns a selection — it currently looks like (around lines 1670–1690):

```go
if selected != "" {
    // File selected — start upload
    m.showFilePicker = false
    m.loading = true
    bucket := m.items[m.cursor]
    prefix := m.browsePrefix
    filename := filepath.Base(selected)
    m.deleteProgress = fmt.Sprintf("Uploading %s...", filename)
    return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
        ctx := context.Background()
        f, err := os.Open(selected)
        if err != nil {
            return errMsg{err: fmt.Errorf("could not open %s: %w", selected, err)}
        }
        defer f.Close()
        key := prefix + filename
        err = m.client.UploadObject(ctx, bucket.name, key, bucket.region, f)
        if err != nil {
            return errMsg{err: err}
        }
        return uploadDoneMsg{filename: filename}
    })
}
```

- [ ] **Step 2: Stat the file, wrap it, and start the ticker**

Replace the block with:

```go
if selected != "" {
    m.showFilePicker = false
    m.loading = true
    bucket := m.items[m.cursor]
    prefix := m.browsePrefix
    filename := filepath.Base(selected)

    // Stat to learn the total size for percentage / ETA.
    var totalSize int64 = -1
    if info, statErr := os.Stat(selected); statErr == nil {
        totalSize = info.Size()
    }

    // Set up the progress snapshot and cancellation context.
    snap := &atomic.Pointer[progress.Snapshot]{}
    snap.Store(&progress.Snapshot{Done: 0, Total: totalSize})
    ctx, cancel := context.WithCancel(context.Background())

    m.transferSnap = snap
    m.transferLabel = fmt.Sprintf("Uploading %s", filename)
    m.transferTotal = totalSize
    m.transferLastDone = 0
    m.transferLastTime = time.Now()
    m.transferRate = 0
    m.transferCancel = cancel
    // Clear the legacy spinner line; the transfer renderer takes over.
    m.deleteProgress = ""

    return m, tea.Batch(transferTick(), func() tea.Msg {
        f, err := os.Open(selected)
        if err != nil {
            return errMsg{err: fmt.Errorf("could not open %s: %w", selected, err)}
        }
        defer f.Close()

        reader := progress.NewReader(f, func(done int64) {
            snap.Store(&progress.Snapshot{Done: done, Total: totalSize})
        })

        key := prefix + filename
        if err := m.client.UploadObject(ctx, bucket.name, key, bucket.region, reader); err != nil {
            return errMsg{err: err}
        }
        return uploadDoneMsg{filename: filename}
    })
}
```

- [ ] **Step 3: Clear transfer state when the upload finishes**

Find the `case uploadDoneMsg:` handler in `Update`. Immediately after the handler's existing logic, add:

```go
m.transferSnap = nil
m.transferCancel = nil
m.transferLabel = ""
```

(Place it before the handler returns. If the handler currently returns early in multiple paths, ensure all of them clear the state — easiest by clearing at the top of the case.)

Do the same in the `case errMsg:` handler so a failed upload also clears the bar — but be careful: `errMsg` is also used for non-transfer errors (folder ops, bucket creation, etc.). The safe form is:

```go
case errMsg:
    if m.transferSnap != nil {
        m.transferSnap = nil
        m.transferCancel = nil
        m.transferLabel = ""
    }
    // ... existing errMsg handling continues
```

- [ ] **Step 4: Wire esc to cancel an in-flight transfer**

In the browse keymap (search for `case "esc":` inside `updateBrowse`), add an early branch:

```go
case "esc":
    if m.transferSnap != nil && m.transferCancel != nil {
        m.transferCancel()
        // Do not clear transferSnap here — let the errMsg handler do it
        // once the goroutine returns with context.Canceled.
        return m, nil
    }
    // ... existing esc handling continues
```

Also, in the `errMsg` handler from Step 3, detect cancellation and replace the error message with a friendly notice:

```go
case errMsg:
    wasTransfer := m.transferSnap != nil
    if wasTransfer {
        m.transferSnap = nil
        m.transferCancel = nil
        m.transferLabel = ""
    }
    if wasTransfer && errors.Is(msg.err, context.Canceled) {
        m.detailMessage = "Cancelled"
        m.loading = false
        return m, nil
    }
    // ... existing errMsg handling continues
```

Add `"errors"` to the imports if not already present.

- [ ] **Step 5: Build & test**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 6: Manual smoke (optional but encouraged)**

Run `make build` (or `go run .`) and try uploading a moderately large local file via the TUI's browse view. Verify:
- The bar advances.
- The rate / percentage / ETA line updates roughly every 250&nbsp;ms.
- Pressing `esc` mid-upload returns to the browse list with "Cancelled".

- [ ] **Step 7: Commit**

```bash
git add internal/tui/buckets.go
git commit -m "feat(tui): show progress bar & rate during local-file upload"
```

---

### Task 9: Wire the download (`g`) path through `progress.Reader`

**Files:**
- Modify: `internal/tui/buckets.go` (the download goroutine starting at line ~1814)

- [ ] **Step 1: Locate the download goroutine**

Find the `case "g":` branch in `updateBrowse` (around lines 1807–1836). It currently runs `DownloadObject` then `io.Copy` to a file.

- [ ] **Step 2: Replace with the progress-wrapped version**

Replace the body of the `if m.browseCursor < len(m.browseItems) && !m.browseItems[m.browseCursor].IsFolder { ... }` block with:

```go
item := m.browseItems[m.browseCursor]
bucket := m.items[m.cursor]

snap := &atomic.Pointer[progress.Snapshot]{}
snap.Store(&progress.Snapshot{Done: 0, Total: -1})
ctx, cancel := context.WithCancel(context.Background())

m.transferSnap = snap
m.transferLabel = fmt.Sprintf("Downloading %s", item.Name)
m.transferTotal = -1
m.transferLastDone = 0
m.transferLastTime = time.Now()
m.transferRate = 0
m.transferCancel = cancel
m.deleteProgress = ""

return m, tea.Batch(transferTick(), func() tea.Msg {
    cwd, err := os.Getwd()
    if err != nil {
        return errMsg{err: fmt.Errorf("could not get working directory: %w", err)}
    }
    body, size, err := m.client.DownloadObject(ctx, bucket.name, item.Key, bucket.region)
    if err != nil {
        return errMsg{err: fmt.Errorf("could not download %s: %w", item.Name, err)}
    }
    defer body.Close()

    // Now that we know the size, update the snapshot's Total. Done is 0
    // for this initial write, which is correct.
    snap.Store(&progress.Snapshot{Done: 0, Total: size})

    reader := progress.NewReader(body, func(done int64) {
        snap.Store(&progress.Snapshot{Done: done, Total: size})
    })

    outPath := filepath.Join(cwd, item.Name)
    f, err := os.Create(outPath)
    if err != nil {
        return errMsg{err: fmt.Errorf("could not create file %s: %w", outPath, err)}
    }
    defer f.Close()
    if _, err := io.Copy(f, reader); err != nil {
        return errMsg{err: fmt.Errorf("could not write file %s: %w", outPath, err)}
    }
    return downloadDoneMsg{filename: item.Name, path: outPath}
})
```

> **Note:** Because `m.transferTotal` is set to `-1` before the goroutine learns the real size, the very first one or two ticks may render the unknown-total fallback (`"12 KB — 8 MB/s"`). That's harmless and corrects itself after the first `snap.Store` inside the goroutine.

- [ ] **Step 3: Ensure `downloadDoneMsg` clears transfer state**

Find the `case downloadDoneMsg:` handler in `Update` and add at the top (mirror what Task 8 Step 3 did for upload):

```go
m.transferSnap = nil
m.transferCancel = nil
m.transferLabel = ""
```

The `errMsg` clear from Task 8 already covers download failure / cancellation.

- [ ] **Step 4: Build & test**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 5: Manual smoke (optional but encouraged)**

Download a moderately large object from a bucket via `g`. Verify the bar advances, the rate/ETA line updates, and `esc` cancels cleanly.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/buckets.go
git commit -m "feat(tui): show progress bar & rate during object download"
```

---

### Task 10: Update CHANGELOG

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Read the current CHANGELOG.md**

Run: `head -30 CHANGELOG.md`
Identify the `## [Unreleased]` section's `### Added` and `### Changed` subsections (create them if missing).

- [ ] **Step 2: Add entries**

Under `### Added`:

```
- Progress bar, transfer rate, percentage complete, and ETA for local-file
  uploads (`p`) and object downloads (`g`) in the TUI browse view.
- `esc` cancels an in-flight upload or download.
- New `internal/progress` package providing a shared counting `io.Reader`
  and rate/duration formatters reused by URL upload, local upload, download,
  and the HTTP-copy path.
```

Under `### Changed`:

```
- `aws.Client.DownloadObject` now also returns the object's size, enabling
  percentage and ETA display.
```

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): note transfer-progress UI"
```

---

### Task 11: Final integration check

- [ ] **Step 1: Full test + build pass**

Run: `go test ./... && go build ./...`
Expected: both PASS.

- [ ] **Step 2: Lint pass (if the repo configures one)**

Run: `make lint` if it exists; otherwise `go vet ./...`.
Expected: no findings.

- [ ] **Step 3: No commit** (this task is verification only).

---

## Self-review notes

- **Spec coverage.** Tasks 1–3 cover the shared `progress` package. Task 4 swaps `urlupload.go`. Task 5 swaps `httpcopy`. Task 6 widens `DownloadObject`. Tasks 7–9 implement the UI + flows. Task 10 ships docs. All goals from the spec's `#goals` section are covered. The `#cancel` section is handled in Task 8 Step 4 (and inherited by Task 9 via the shared `errMsg` handler).
- **Out-of-scope confirmation.** No task touches `UploadStream`, folder uploads, or the URL-upload modal beyond the formatter import swap.
- **Type consistency.** `progress.Snapshot{Done, Total}` is used identically in Tasks 1, 7, 8, and 9. The model field `transferSnap` (atomic pointer) is read/written under the same name throughout. The progress bar field is named `transferBar` (typed `bubblesprogress.Model`) consistently.
