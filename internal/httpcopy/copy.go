// Package httpcopy orchestrates streaming a remote file (optionally behind a
// WeTransfer share URL) directly into an S3 bucket via multipart upload.
//
// The public surface is intentionally small: callers construct an Options,
// provide an Uploader (satisfied by *aws.Client), and call Run.  Progress
// events are delivered through a nil-safe callback. The caller is responsible
// for throttling its UI; httpcopy never spawns background goroutines or
// time-based timers.
package httpcopy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/dcorbell/s3m/internal/httpresolve"
	"github.com/dcorbell/s3m/internal/progress"
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
	Conditional bool           // require conditional destination protection
	Condition   *string        // nil requires absence; otherwise reviewed ETag
}

// Uploader is the interface satisfied by *aws.Client.  Declaring it here keeps
// httpcopy free of AWS SDK types at the package boundary and lets tests inject
// a fake.
type Uploader interface {
	UploadStream(ctx context.Context, bucket, key, region string, body io.Reader, partSize int64, concurrency int) error
}

// ConditionalUploader protects the destination against concurrent replacement.
type ConditionalUploader interface {
	UploadStreamConditional(context.Context, string, string, string, io.Reader, int64, int, *string) error
}

// Resolved is safe to display before starting an upload.
type Resolved struct {
	URL        string
	Key        string
	BytesTotal int64
}

// Resolve obtains the source filename and size without writing to S3.
func Resolve(ctx context.Context, opt Options) (Resolved, error) {
	resp, directURL, key, err := prepare(ctx, opt)
	if err != nil {
		return Resolved{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	return Resolved{URL: directURL, Key: key, BytesTotal: resp.ContentLength}, nil
}

// defaultClient returns an http.Client suited for large file transfers.
// No global timeout is set because 700 GB can take many hours; the transport
// sets short per-connection deadlines to detect stalled connections.
func defaultClient() *http.Client {
	return &http.Client{
		Timeout: 0, // no deadline - large files can take hours
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
	emit := func(p Progress) {
		if opt.Progress != nil {
			opt.Progress(p)
		}
	}
	resp, _, key, err := prepare(ctx, opt)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

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

	var uploadErr error
	if opt.Conditional {
		safe, ok := up.(ConditionalUploader)
		if !ok {
			return "", fmt.Errorf("uploader cannot protect the reviewed destination")
		}
		uploadErr = safe.UploadStreamConditional(ctx, opt.Bucket, key, opt.Region, cr, partSize, opt.Concurrency, opt.Condition)
	} else {
		uploadErr = up.UploadStream(ctx, opt.Bucket, key, opt.Region, cr, partSize, opt.Concurrency)
	}
	if uploadErr != nil {
		return "", fmt.Errorf("could not upload to s3://%s/%s: %w", opt.Bucket, key, uploadErr)
	}

	finalDone := cr.Done()
	emit(Progress{
		Phase:      "done",
		BytesDone:  finalDone,
		BytesTotal: finalDone,
		Filename:   key,
	})

	return key, nil
}

// prepare opens the source only long enough to obtain its authoritative name.
func prepare(ctx context.Context, opt Options) (*http.Response, string, string, error) {
	client := opt.HTTPClient
	if client == nil {
		client = defaultClient()
	}
	directURL := opt.URL
	fallback := ""
	parsed, err := url.Parse(opt.URL)
	if err != nil {
		return nil, "", "", fmt.Errorf("could not parse source URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", "", fmt.Errorf("source URL must use HTTP or HTTPS")
	}
	if parsed.Host == "" {
		return nil, "", "", fmt.Errorf("source URL requires a host")
	}
	if httpresolve.IsWeTransferHost(parsed.Host) {
		if opt.Progress != nil {
			opt.Progress(Progress{Phase: "resolving", BytesTotal: -1})
		}
		directURL, fallback, err = httpresolve.ResolveDirectLink(ctx, client, opt.URL)
		if err != nil {
			return nil, "", "", fmt.Errorf("could not resolve WeTransfer URL: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("could not build source request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			err = requestErr.Err
		}
		return nil, "", "", fmt.Errorf("could not fetch source: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := make([]byte, 512)
		n, _ := io.ReadFull(resp.Body, preview)
		resp.Body.Close()
		return nil, "", "", fmt.Errorf("download request returned status %d: %s", resp.StatusCode, preview[:n])
	}
	key := deriveKey(opt.Key, resp.Header.Get("Content-Disposition"), fallback, directURL)
	if key == "" || strings.HasSuffix(key, "/") {
		resp.Body.Close()
		return nil, "", "", fmt.Errorf("source has no filename; enter a destination filename")
	}
	if opt.Progress != nil {
		opt.Progress(Progress{Phase: "resolving", ResolvedURL: directURL, Filename: key, BytesTotal: resp.ContentLength})
	}
	return resp, directURL, key, nil
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
// returns the filename parameter, sanitized to just the basename - any
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
		minPart      = 64 << 20  // 64 MiB - S3 minimum for non-final parts
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
