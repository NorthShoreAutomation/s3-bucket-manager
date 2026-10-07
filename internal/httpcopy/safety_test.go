package httpcopy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type safeFake struct {
	fakeUploader
	conditional bool
	condition   *string
}

func (f *safeFake) UploadStreamConditional(ctx context.Context, b, k, r string, body io.Reader, p int64, n int, c *string) error {
	f.conditional = true
	f.condition = c
	return f.UploadStream(ctx, b, k, r, body, p, n)
}
func TestResolveReviewsFilenameWithoutUpload(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return makeResponse(200, "body", http.Header{"Content-Disposition": []string{`attachment; filename="report.txt"`}}), nil
	})}
	resolved, err := Resolve(context.Background(), Options{URL: "https://example.com/download", Key: "folder/", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Key != "folder/report.txt" || resolved.BytesTotal != 4 {
		t.Fatalf("wrong destination: %+v", resolved)
	}
}
func TestConditionalRunNeverFallsBackToUnsafeUploader(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return makeResponse(200, "body", nil), nil })}
	unsafe := &fakeUploader{}
	_, err := Run(context.Background(), unsafe, Options{URL: "https://example.com/file", Conditional: true, HTTPClient: client})
	if err == nil || unsafe.called {
		t.Fatal("conditional copy used unsafe upload")
	}
	safe := &safeFake{}
	tag := "old"
	_, err = Run(context.Background(), safe, Options{URL: "https://example.com/file", Conditional: true, Condition: &tag, HTTPClient: client})
	if err != nil || !safe.conditional || safe.condition != &tag {
		t.Fatalf("conditional upload: %v", err)
	}
}

func TestSourceFailureDoesNotExposeSignedQuery(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network unavailable") })}
	_, err := Resolve(context.Background(), Options{URL: "https://example.com/file?secret-token=credential", HTTPClient: client})
	if err == nil || strings.Contains(err.Error(), "credential") {
		t.Fatalf("source query leaked: %v", err)
	}
}
