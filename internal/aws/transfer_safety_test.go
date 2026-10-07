package aws

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type conditionalS3 struct {
	mockS3
	put      *s3.PutObjectInput
	complete *s3.CompleteMultipartUploadInput
}

func (m *conditionalS3) PutObject(_ context.Context, p *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.put = p
	_, _ = io.Copy(io.Discard, p.Body)
	return &s3.PutObjectOutput{}, nil
}
func (m *conditionalS3) CompleteMultipartUpload(_ context.Context, p *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	m.complete = p
	return &s3.CompleteMultipartUploadOutput{}, nil
}
func TestUploadStreamConditional(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		for _, existing := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "single", true: "multipart"}[multipart], map[bool]string{false: "new", true: "existing"}[existing]}, "/"), func(t *testing.T) {
				m := &conditionalS3{}
				c := &Client{S3: m}
				data := []byte("body")
				if multipart {
					data = bytes.Repeat([]byte("x"), 6<<20)
				}
				var condition *string
				if existing {
					condition = sdkaws.String("\"old-etag\"")
				}
				if err := c.UploadStreamConditional(context.Background(), "bucket", "key", "", bytes.NewReader(data), 5<<20, 1, condition); err != nil {
					t.Fatal(err)
				}
				var match, none *string
				if multipart {
					if m.complete == nil {
						t.Fatal("missing multipart completion")
					}
					match, none = m.complete.IfMatch, m.complete.IfNoneMatch
				} else {
					if m.put == nil {
						t.Fatal("missing put")
					}
					match, none = m.put.IfMatch, m.put.IfNoneMatch
				}
				if existing {
					if sdkaws.ToString(match) != "\"old-etag\"" || none != nil {
						t.Fatalf("wrong existing condition: %v %v", match, none)
					}
				} else if sdkaws.ToString(none) != "*" || match != nil {
					t.Fatalf("missing no-overwrite condition: %v %v", match, none)
				}
			})
		}
	}
}
func TestObjectWriteCondition(t *testing.T) {
	for _, tt := range []struct {
		name    string
		out     *s3.HeadObjectOutput
		err     error
		want    string
		wantErr bool
	}{{"absent", nil, &smithy.GenericAPIError{Code: "NotFound"}, "", false}, {"denied", nil, &smithy.GenericAPIError{Code: "AccessDenied"}, "", true}, {"exists", &s3.HeadObjectOutput{ETag: sdkaws.String("tag")}, nil, "tag", false}, {"unknown etag", &s3.HeadObjectOutput{}, nil, "", true}} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{S3: &mockS3{headObjectOutput: tt.out, headObjectErr: tt.err}}
			got, err := c.ObjectWriteCondition(context.Background(), "b", "k", "")
			if (err != nil) != tt.wantErr || sdkaws.ToString(got) != tt.want {
				t.Fatalf("condition=%v err=%v", got, err)
			}
		})
	}
}
func TestWriteDownloadSafely(t *testing.T) {
	for _, scenario := range []string{"complete", "existing", "failure", "cancelled", "late conflict", "overwrite", "changed overwrite"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "file")
			overwrite := scenario == "overwrite" || scenario == "changed overwrite"
			if scenario == "existing" || overwrite {
				if err := os.WriteFile(dst, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			n, err := WriteDownloadSafely(ctx, dst, overwrite, func(w io.WriterAt) (int64, error) {
				called = true
				_, e := w.WriteAt([]byte("new content"), 0)
				if e != nil {
					return 0, e
				}
				switch scenario {
				case "failure":
					return 0, errors.New("network failed")
				case "cancelled":
					cancel()
				case "late conflict":
					_ = os.WriteFile(dst, []byte("original"), 0600)
				case "changed overwrite":
					_ = os.WriteFile(dst, []byte("changed"), 0600)
				}
				return 11, nil
			})
			success := scenario == "complete" || scenario == "overwrite"
			if (err == nil) != success {
				t.Fatalf("n=%d err=%v", n, err)
			}
			if scenario == "existing" && called {
				t.Fatal("download started despite conflict")
			}
			data, readErr := os.ReadFile(dst)
			if success && string(data) != "new content" {
				t.Fatalf("published %q", data)
			}
			if (scenario == "existing" || scenario == "late conflict") && string(data) != "original" {
				t.Fatalf("replaced conflict: %q", data)
			}
			if scenario == "changed overwrite" && string(data) != "changed" {
				t.Fatalf("replaced changed destination %q", data)
			}
			if (scenario == "failure" || scenario == "cancelled") && !os.IsNotExist(readErr) {
				t.Fatal("published incomplete output")
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".s3m-download-") {
					t.Fatal("temporary output leaked")
				}
			}
		})
	}
}

type cancelledMultipartS3 struct {
	mockS3
	cancel        context.CancelFunc
	cleanupCalled bool
	cleanupErr    error
}

func (m *cancelledMultipartS3) UploadPart(ctx context.Context, p *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	m.cancel()
	return nil, ctx.Err()
}
func (m *cancelledMultipartS3) AbortMultipartUpload(ctx context.Context, p *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	if ctx.Err() == nil {
		m.cleanupCalled = true
		return nil, m.cleanupErr
	}
	return nil, ctx.Err()
}
func TestCancelledMultipartUsesUncancelledCleanup(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup succeeds", true: "cleanup fails"}[cleanupFails], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &cancelledMultipartS3{cancel: cancel}
			if cleanupFails {
				m.cleanupErr = errors.New("cleanup denied")
			}
			c := &Client{S3: m}
			err := c.UploadStreamConditional(ctx, "bucket", "key", "", bytes.NewReader(bytes.Repeat([]byte("x"), 6<<20)), 5<<20, 1, nil)
			if !errors.Is(err, context.Canceled) || !m.cleanupCalled {
				t.Fatalf("missing cleanup or cancellation: %v, cleanup %v", err, m.cleanupCalled)
			}
			if cleanupFails && !strings.Contains(err.Error(), "cleanup failed") {
				t.Fatalf("lost cleanup failure: %v", err)
			}
		})
	}
}
