package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// TestParseS3Dest
// ---------------------------------------------------------------------------

func TestParseS3Dest(t *testing.T) {
	tests := []struct {
		input      string
		wantBucket string
		wantKey    string
		wantErr    bool
	}{
		{
			input:      "s3://bucket/key",
			wantBucket: "bucket",
			wantKey:    "key",
		},
		{
			input:      "s3://bucket/path/with/slashes/",
			wantBucket: "bucket",
			wantKey:    "path/with/slashes/",
		},
		{
			input:      "s3://bucket/",
			wantBucket: "bucket",
			wantKey:    "",
		},
		{
			// Wrong scheme — must error.
			input:   "https://bucket/",
			wantErr: true,
		},
		{
			// Empty bucket — s3:///key has no bucket between the two slashes.
			input:   "s3:///key",
			wantErr: true,
		},
		{
			// Missing slash separator between bucket and key.
			input:   "s3://bucket",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			bucket, key, err := parseS3Dest(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseS3Dest(%q): expected error, got bucket=%q key=%q", tc.input, bucket, key)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseS3Dest(%q): unexpected error: %v", tc.input, err)
			}
			if bucket != tc.wantBucket {
				t.Errorf("parseS3Dest(%q): bucket = %q, want %q", tc.input, bucket, tc.wantBucket)
			}
			if key != tc.wantKey {
				t.Errorf("parseS3Dest(%q): key = %q, want %q", tc.input, key, tc.wantKey)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestParseByteSize
//
// MB and MiB are treated as the same binary unit (1 048 576 bytes) for
// simplicity, since S3 documentation conflates them.  This is intentional and
// documented in the command's Long description.
// ---------------------------------------------------------------------------

func TestParseByteSize(t *testing.T) {
	const (
		kib = 1024
		mib = 1024 * 1024
		gib = 1024 * 1024 * 1024
	)

	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{input: "128MiB", want: 128 * mib},
		{input: "64mb", want: 64 * mib}, // case-insensitive; MB == MiB (binary)
		{input: "64MB", want: 64 * mib}, // uppercase MB
		{input: "1GB", want: 1 * gib},
		{input: "1GiB", want: 1 * gib},
		{input: "1234", want: 1234}, // raw bytes, no suffix
		{input: "", want: 0},        // empty string → 0
		{input: "0", want: 0},       // zero
		{input: "1KiB", want: 1 * kib},
		{input: "1kb", want: 1 * kib},
		{input: "512B", want: 512},
		{input: "garbage", wantErr: true},
		{input: "12xyz", wantErr: true},
		{input: "MiB", wantErr: true}, // suffix with no leading number
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseByteSize(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseByteSize(%q): expected error, got %d", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseByteSize(%q): unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("parseByteSize(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestHTTPCopyCmd_rejectsBadArgs
// ---------------------------------------------------------------------------

// newTestRoot builds a minimal root command that mirrors the real one just
// enough to attach httpCopyCmd and capture errors without needing AWS creds.
func newTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "s3m", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&profile, "profile", "", "")
	root.PersistentFlags().StringVar(&region, "region", "", "")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "")
	root.AddCommand(httpCopyCmd)
	return root
}

func TestHTTPCopyCmd_rejectsBadArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrStr string
	}{
		{
			name:       "malformed destination — no s3 scheme",
			args:       []string{"http-copy", "https://x.com/f", "bucket/key"},
			wantErrStr: `s3://`,
		},
		{
			name:       "wrong arg count — too few",
			args:       []string{"http-copy", "https://x.com/f"},
			wantErrStr: `accepts 2 arg`,
		},
		{
			name:       "negative concurrency",
			args:       []string{"http-copy", "--concurrency", "-1", "https://x.com/f", "s3://bucket/key"},
			wantErrStr: `--concurrency`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newTestRoot()
			var errBuf bytes.Buffer
			root.SetArgs(tc.args)
			root.SetErr(&errBuf)

			err := root.Execute()

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrStr)
			}
			if tc.wantErrStr != "" {
				errMsg := err.Error()
				if !containsIgnoreCase(errMsg, tc.wantErrStr) {
					t.Errorf("error %q does not contain %q", errMsg, tc.wantErrStr)
				}
			}
		})
	}
}

func containsIgnoreCase(s, substr string) bool {
	return bytes.Contains(
		bytes.ToLower([]byte(s)),
		bytes.ToLower([]byte(substr)),
	)
}
