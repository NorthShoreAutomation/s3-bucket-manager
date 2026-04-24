// Package httpcopy orchestrates streaming a remote file (optionally behind a
// WeTransfer share URL) directly into an S3 bucket via multipart upload.
//
// The public surface is intentionally small: callers construct an Options,
// provide an Uploader (satisfied by *aws.Client), and call Run.  Progress
// events are delivered through a nil-safe callback — the caller is responsible
// for throttling its UI; httpcopy never spawns background goroutines or
// time-based timers.
package httpcopy

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/dcorbell/s3m/internal/httpresolve"
)

// Progress is a snapshot of the copy operation delivered to Options.Progress.
// BytesTotal is -1 when the server did not send a Content-Length.
type Progress struct {
	BytesDone   int64
	BytesTotal  int64  // -1 if unknown
	ResolvedURL string // set once after any URL resolution step
	Filename    string // final S3 key chosen
	Phase       string // "resolving" | "uploading" | "done"
}

// Options configures a single Run invocation.
type Options struct {
	URL         string // WeTransfer share URL or direct HTTP URL
	Bucket      string
	Key         string // if empty OR ends with "/", derive filename from response
	Region      string
	PartSize    int64          // bytes; 0 => auto-computed from Content-Length
	Concurrency int            // 0 => uploader default
	Progress    func(Progress) // nil-safe; called on state changes and data reads
	HTTPClient  *http.Client   // nil => internal long-timeout client
}

// Uploader is the interface satisfied by *aws.Client.  Declaring it here keeps
// httpcopy free of AWS SDK types at the package boundary and lets tests inject
// a fake.
type Uploader interface {
	UploadStream(ctx context.Context, bucket, key, region string, body io.Reader, partSize int64, concurrency int) error
}

// defaultClient returns an http.Client suited for large file transfers.
// No global timeout is set because 700 GB can take many hours; the transport
// sets short per-connection deadlines to detect stalled connections.
func defaultClient() *http.Client {
	return &http.Client{
		Timeout: 0, // no deadline — large files can take hours
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// Run resolves opt.URL, streams the response body to S3 via up, and returns
// the final S3 key.
//
// WeTransfer detection: if the URL host has suffix "wetransfer.com" the share
// link is resolved to a direct download URL via httpresolve.ResolveDirectLink.
// Otherwise the URL is used as-is.
//
// Key derivation (in precedence order when opt.Key is empty or ends with "/"):
//  1. Content-Disposition filename parameter from the direct HTTP response.
//  2. Fallback filename returned by the WeTransfer resolver (<transfer_id>.zip).
//  3. Last path segment of the direct URL.
//
// The resolved key is always sanitized by stripping any leading "/".
func Run(ctx context.Context, up Uploader, opt Options) (key string, err error) {
	client := opt.HTTPClient
	if client == nil {
		client = defaultClient()
	}

	emit := func(p Progress) {
		if opt.Progress != nil {
			opt.Progress(p)
		}
	}

	directURL := opt.URL
	resolverFilename := ""

	// Detect WeTransfer and resolve to a direct link when applicable.
	parsed, parseErr := url.Parse(opt.URL)
	if parseErr != nil {
		return "", fmt.Errorf("could not parse URL %q: %w", opt.URL, parseErr)
	}

	if httpresolve.IsWeTransferHost(parsed.Host) {
		emit(Progress{Phase: "resolving", BytesTotal: -1})

		resolved, fallback, resolveErr := httpresolve.ResolveDirectLink(ctx, client, opt.URL)
		if resolveErr != nil {
			return "", fmt.Errorf("could not resolve WeTransfer URL: %w", resolveErr)
		}

		directURL = resolved
		resolverFilename = fallback
		emit(Progress{Phase: "resolving", ResolvedURL: directURL, BytesTotal: -1})
	}

	// GET the direct download URL.
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, directURL, nil)
	if reqErr != nil {
		return "", fmt.Errorf("could not build download request for %q: %w", directURL, reqErr)
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		return "", fmt.Errorf("could not fetch %q: %w", directURL, doErr)
	}
	// resp.Body is closed after upload finishes — see deferred close below.

	// Reject non-2xx responses before touching the body pipeline.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := make([]byte, 512)
		n, _ := io.ReadFull(resp.Body, preview)
		resp.Body.Close() //nolint:errcheck
		return "", fmt.Errorf("download request returned status %d: %s", resp.StatusCode, preview[:n])
	}

	defer resp.Body.Close() //nolint:errcheck

	// Derive the final S3 key.
	key = deriveKey(opt.Key, resp.Header.Get("Content-Disposition"), resolverFilename, directURL)

	// Compute part size.
	partSize := opt.PartSize
	if partSize <= 0 {
		partSize = ComputePartSize(resp.ContentLength)
	}

	// Determine BytesTotal for progress; -1 when unknown.
	bytesTotal := resp.ContentLength
	if bytesTotal == 0 {
		bytesTotal = -1
	}

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

	if uploadErr := up.UploadStream(ctx, opt.Bucket, key, opt.Region, cr, partSize, opt.Concurrency); uploadErr != nil {
		return "", fmt.Errorf("could not upload to s3://%s/%s: %w", opt.Bucket, key, uploadErr)
	}

	finalDone := cr.done
	emit(Progress{
		Phase:      "done",
		BytesDone:  finalDone,
		BytesTotal: finalDone,
		Filename:   key,
	})

	return key, nil
}

// deriveKey computes the final S3 key from the caller's opt.Key, the
// Content-Disposition header, the WeTransfer resolver fallback filename, and
// the direct URL's last path segment.
//
// Precedence when opt.Key is empty or ends with "/":
//  1. Content-Disposition filename (authoritative source on the actual file).
//  2. WeTransfer resolver fallback (<transfer_id>.zip).
//  3. Last path segment of the direct URL.
//
// The base is then appended to the opt.Key prefix and the result is sanitized
// by stripping any leading "/".
func deriveKey(optKey, contentDisposition, resolverFilename, directURL string) string {
	// Verbatim key: non-empty and does not end with "/".
	if optKey != "" && !strings.HasSuffix(optKey, "/") {
		return optKey
	}

	base := filenameFromContentDisposition(contentDisposition)
	if base == "" {
		base = resolverFilename
	}
	if base == "" {
		base = lastPathSegment(directURL)
	}

	result := optKey + base
	return strings.TrimPrefix(result, "/")
}

// filenameFromContentDisposition parses a Content-Disposition header value and
// returns the filename parameter, sanitized to just the basename — any
// directory components are stripped to prevent a hostile server from steering
// the S3 key outside the caller's intended prefix (e.g., filename="../../foo").
// Returns an empty string when absent, malformed, or when the sanitized result
// is empty or "." / "..".
func filenameFromContentDisposition(header string) string {
	if header == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	raw := params["filename"]
	if raw == "" {
		return ""
	}
	base := path.Base(raw)
	if base == "" || base == "." || base == ".." || base == "/" {
		return ""
	}
	return base
}

// lastPathSegment returns the final non-empty path segment of rawURL, or the
// raw URL string itself when no path segment exists.
func lastPathSegment(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	segments := strings.Split(strings.TrimRight(parsed.Path, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] != "" {
			return segments[i]
		}
	}
	return rawURL
}

// ComputePartSize returns an appropriate S3 multipart upload part size for the
// given Content-Length.  The calculation ensures the total number of parts
// stays comfortably under the S3 limit of 10,000 parts.
//
//   - contentLength <= 0 (unknown): returns 256 MiB.
//   - Otherwise: max(64 MiB, ceil(contentLength / 9500)).
func ComputePartSize(contentLength int64) int64 {
	const (
		minPart      = 64 << 20  // 64 MiB — S3 minimum for non-final parts
		fallbackPart = 256 << 20 // 256 MiB when Content-Length is unknown
		safetyDiv    = 9500      // stay well under the 10,000-part S3 cap
	)

	if contentLength <= 0 {
		return fallbackPart
	}

	computed := (contentLength + safetyDiv - 1) / safetyDiv
	if computed < minPart {
		return minPart
	}
	return computed
}

// countingReader wraps an io.Reader and calls onProgress after each Read.
// It is deliberately goroutine-free and has no internal throttle; the caller
// is responsible for rate-limiting its UI updates.
type countingReader struct {
	r          io.Reader
	done       int64
	total      int64
	key        string
	onProgress func(int64)
	enabled    bool
}

func (c *countingReader) Read(p []byte) (n int, err error) {
	n, err = c.r.Read(p)
	if n > 0 {
		c.done += int64(n)
		if c.enabled {
			c.onProgress(c.done)
		}
	}
	return n, err
}
