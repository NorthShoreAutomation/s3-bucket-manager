package httpcopy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc allows constructing an http.RoundTripper from a plain function,
// matching the pattern used in internal/httpresolve/wetransfer_test.go.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// makeResponse is a convenience helper that builds an *http.Response.
func makeResponse(status int, body string, headers http.Header) *http.Response {
	h := headers
	if h == nil {
		h = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(strings.NewReader(body)),
		Header:        h,
		ContentLength: int64(len(body)),
	}
}

// fakeUploader captures the arguments passed to UploadStream for later inspection.
type fakeUploader struct {
	called      bool
	bucket      string
	key         string
	region      string
	body        []byte
	partSize    int64
	concurrency int
}

func (f *fakeUploader) UploadStream(_ context.Context, bucket, key, region string, body io.Reader, partSize int64, concurrency int) error {
	f.called = true
	f.bucket = bucket
	f.key = key
	f.region = region
	f.partSize = partSize
	f.concurrency = concurrency

	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.body = b
	return nil
}

// --------------------------------------------------------------------------
// TestComputePartSize
// --------------------------------------------------------------------------

func TestComputePartSize(t *testing.T) {
	const (
		mib = int64(1 << 20)
		gib = int64(1 << 30)
		tib = int64(1 << 40)
	)

	cases := []struct {
		name          string
		contentLength int64
		want          int64
		checkMin      int64 // when > 0, assert result >= checkMin
		checkMax      int64 // when > 0, assert result <= checkMax
	}{
		{
			name:          "zero_length_returns_fallback",
			contentLength: 0,
			want:          256 * mib,
		},
		{
			name:          "negative_length_returns_fallback",
			contentLength: -1,
			want:          256 * mib,
		},
		{
			name:          "tiny_file_floored_to_64MiB",
			contentLength: 1 * mib,
			want:          64 * mib,
		},
		{
			name:          "5GiB_floored_to_64MiB",
			contentLength: 5 * gib,
			// 5 GiB / 9500 ≈ 565 KiB, well below the 64 MiB floor
			want: 64 * mib,
		},
		{
			name:          "700GiB_within_bounds",
			contentLength: 700 * gib,
			// computed = (700*GiB + 9499) / 9500
			want:     (700*gib + 9499) / 9500,
			checkMin: 64 * mib,
			checkMax: 5 * gib,
		},
		{
			name:          "5TiB_produces_less_than_5GiB",
			contentLength: 5 * tib,
			// Just assert it stays under the 5 GiB S3 max-part-size limit.
			checkMax: 5 * gib,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputePartSize(tc.contentLength)

			if tc.want > 0 && got != tc.want {
				t.Errorf("ComputePartSize(%d) = %d; want %d", tc.contentLength, got, tc.want)
			}
			if tc.checkMin > 0 && got < tc.checkMin {
				t.Errorf("ComputePartSize(%d) = %d; want >= %d", tc.contentLength, got, tc.checkMin)
			}
			if tc.checkMax > 0 && got > tc.checkMax {
				t.Errorf("ComputePartSize(%d) = %d; want <= %d", tc.contentLength, got, tc.checkMax)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestRun_directURL
// --------------------------------------------------------------------------

func TestRun_directURL(t *testing.T) {
	const payloadSize = 10 * 1024 // 10 KiB
	payload := bytes.Repeat([]byte("x"), payloadSize)

	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(bytes.NewReader(payload)),
			Header:        make(http.Header),
			ContentLength: int64(payloadSize),
		}, nil
	})

	up := &fakeUploader{}
	opt := Options{
		URL:        "https://cdn.example.com/file.bin",
		Bucket:     "b",
		Key:        "prefix/",
		Region:     "us-east-1",
		HTTPClient: &http.Client{Transport: rt},
	}

	gotKey, err := Run(context.Background(), up, opt)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if gotKey != "prefix/file.bin" {
		t.Errorf("returned key = %q; want %q", gotKey, "prefix/file.bin")
	}
	if up.key != "prefix/file.bin" {
		t.Errorf("uploader key = %q; want %q", up.key, "prefix/file.bin")
	}
	if up.bucket != "b" {
		t.Errorf("uploader bucket = %q; want %q", up.bucket, "b")
	}
	if up.region != "us-east-1" {
		t.Errorf("uploader region = %q; want %q", up.region, "us-east-1")
	}
	if !bytes.Equal(up.body, payload) {
		t.Errorf("uploader received %d bytes; want %d", len(up.body), len(payload))
	}
}

// --------------------------------------------------------------------------
// TestRun_wetransferDelegatesToResolver
//
// Content-Disposition wins over the WeTransfer resolver fallback filename when
// both are present. This is documented here and in deriveKey.
// --------------------------------------------------------------------------

func TestRun_wetransferContentDispositionWinsOverResolverFallback(t *testing.T) {
	const (
		csrfHTML    = `<html><head><meta name="csrf-token" content="T"></head></html>`
		downloadURL = "https://download.example/dl"
		dlPayload   = "a hundred bytes of content padding here to make up roughly that count"
	)

	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		// WeTransfer CSRF handshake
		case r.Method == http.MethodGet && r.URL.String() == "https://wetransfer.com/":
			return makeResponse(http.StatusOK, csrfHTML, nil), nil

		// WeTransfer download API — returns a direct_link
		case r.Method == http.MethodPost &&
			strings.HasSuffix(r.URL.String(), "/download"):
			return makeResponse(http.StatusOK, `{"direct_link":"`+downloadURL+`"}`, nil), nil

		// Actual file download — responds with Content-Disposition
		case r.Method == http.MethodGet && r.URL.String() == downloadURL:
			h := http.Header{}
			h.Set("Content-Disposition", `attachment; filename="real.zip"`)
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader(dlPayload)),
				Header:        h,
				ContentLength: int64(len(dlPayload)),
			}, nil

		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
			return nil, nil
		}
	})

	up := &fakeUploader{}
	opt := Options{
		URL:        "https://foo.wetransfer.com/downloads/TID/HASH",
		Bucket:     "b",
		Key:        "",
		HTTPClient: &http.Client{Transport: rt},
	}

	gotKey, err := Run(context.Background(), up, opt)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Content-Disposition "real.zip" wins over resolver fallback "TID.zip".
	if gotKey != "real.zip" {
		t.Errorf("returned key = %q; want %q (Content-Disposition wins over resolver fallback)", gotKey, "real.zip")
	}
	if up.key != "real.zip" {
		t.Errorf("uploader key = %q; want %q", up.key, "real.zip")
	}
}

// --------------------------------------------------------------------------
// TestRun_non2xxFromDirect
// --------------------------------------------------------------------------

func TestRun_non2xxFromDirect(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return makeResponse(http.StatusNotFound, "nope", nil), nil
	})

	up := &fakeUploader{}
	_, err := Run(context.Background(), up, Options{
		URL:        "https://cdn.example.com/missing.bin",
		Bucket:     "b",
		Key:        "k",
		HTTPClient: &http.Client{Transport: rt},
	})

	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not mention 404", err.Error())
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q does not contain body preview", err.Error())
	}
	if up.called {
		t.Error("uploader should not have been called on non-2xx response")
	}
}

// --------------------------------------------------------------------------
// TestRun_keyOverrideIsVerbatim
// --------------------------------------------------------------------------

func TestRun_keyOverrideIsVerbatim(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Content-Disposition", `attachment; filename="ignored.zip"`)
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("data")),
			Header:        h,
			ContentLength: 4,
		}, nil
	})

	up := &fakeUploader{}
	const wantKey = "custom/path/thing.zip"
	_, err := Run(context.Background(), up, Options{
		URL:        "https://cdn.example.com/whatever.bin",
		Bucket:     "b",
		Key:        wantKey, // no trailing slash — verbatim
		HTTPClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if up.key != wantKey {
		t.Errorf("uploader key = %q; want %q", up.key, wantKey)
	}
}

// --------------------------------------------------------------------------
// TestRun_progressCallbackFires
// --------------------------------------------------------------------------

func TestRun_progressCallbackFires(t *testing.T) {
	const payloadSize = 64 * 1024 // 64 KiB
	payload := bytes.Repeat([]byte("p"), payloadSize)

	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(bytes.NewReader(payload)),
			Header:        make(http.Header),
			ContentLength: int64(payloadSize),
		}, nil
	})

	var events []Progress
	up := &fakeUploader{}
	_, err := Run(context.Background(), up, Options{
		URL:    "https://cdn.example.com/big.bin",
		Bucket: "b",
		Key:    "big.bin",
		Progress: func(p Progress) {
			events = append(events, p)
		},
		HTTPClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Must have at least one "uploading" event with BytesDone > 0.
	hasUploading := false
	for _, e := range events {
		if e.Phase == "uploading" && e.BytesDone > 0 {
			hasUploading = true
			break
		}
	}
	if !hasUploading {
		t.Errorf("no uploading progress event with BytesDone > 0; got events: %+v", events)
	}

	// Last event must be "done".
	if len(events) == 0 {
		t.Fatal("no progress events received")
	}
	last := events[len(events)-1]
	if last.Phase != "done" {
		t.Errorf("last event phase = %q; want %q", last.Phase, "done")
	}
}

// --------------------------------------------------------------------------
// TestRun_respectsCallerPartSize
// --------------------------------------------------------------------------

func TestRun_respectsCallerPartSize(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("tiny")),
			Header:        make(http.Header),
			ContentLength: 4,
		}, nil
	})

	const wantPartSize = 99 << 20 // 99 MiB
	up := &fakeUploader{}
	_, err := Run(context.Background(), up, Options{
		URL:        "https://cdn.example.com/tiny.bin",
		Bucket:     "b",
		Key:        "tiny.bin",
		PartSize:   wantPartSize,
		HTTPClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if up.partSize != wantPartSize {
		t.Errorf("uploader partSize = %d; want %d", up.partSize, wantPartSize)
	}
}

// --------------------------------------------------------------------------
// TestRun_contextCancel
// --------------------------------------------------------------------------

func TestRun_contextCancel(t *testing.T) {
	// Use an already-cancelled context so the HTTP request is rejected
	// before any network I/O occurs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Honour the context cancellation the same way real transports do.
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return makeResponse(http.StatusOK, "data", nil), nil
	})

	up := &fakeUploader{}
	_, err := Run(ctx, up, Options{
		URL:        "https://cdn.example.com/file.bin",
		Bucket:     "b",
		Key:        "file.bin",
		HTTPClient: &http.Client{Transport: rt},
	})

	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if up.called {
		t.Error("uploader should not have been called when context is cancelled")
	}
}

// TestRun_sanitizesContentDispositionFilename asserts that a hostile
// Content-Disposition filename containing path components cannot steer the S3
// key outside the caller's intended prefix. path.Base must strip any directory
// component before the filename is appended to opt.Key; bare "." / ".." must
// fall through to the URL basename.
func TestRun_sanitizesContentDispositionFilename(t *testing.T) {
	cases := []struct {
		name           string
		headerFilename string
		wantKey        string
	}{
		{"traversal", `attachment; filename="../../../etc/passwd"`, "uploads/passwd"},
		{"absolute", `attachment; filename="/etc/passwd"`, "uploads/passwd"},
		{"dotdot_only", `attachment; filename=".."`, "uploads/file.bin"}, // falls through to URL basename
		{"dot_only", `attachment; filename="."`, "uploads/file.bin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeUploader{}
			rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				h := http.Header{}
				h.Set("Content-Disposition", tc.headerFilename)
				return makeResponse(http.StatusOK, "x", h), nil
			})
			_, err := Run(context.Background(), up, Options{
				URL:        "https://cdn.example.com/file.bin",
				Bucket:     "b",
				Key:        "uploads/",
				HTTPClient: &http.Client{Transport: rt},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if up.key != tc.wantKey {
				t.Errorf("key = %q; want %q", up.key, tc.wantKey)
			}
			// Guard: the key must never contain ".." as a path segment, which would
			// escape the caller's intended prefix when an S3 consumer resolves paths.
			for _, seg := range strings.Split(up.key, "/") {
				if seg == ".." {
					t.Errorf("key %q contains a %q path segment", up.key, "..")
				}
			}
		})
	}
}
