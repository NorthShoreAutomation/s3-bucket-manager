package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/httpcopy"
)

var (
	httpCopyPartSize    string
	httpCopyConcurrency int
)

var httpCopyCmd = &cobra.Command{
	Use:   "http-copy <url> s3://<bucket>/<key-or-prefix>[/]",
	Short: "Stream a file from an HTTP URL directly into S3",
	Long: `Stream a remote file (or WeTransfer share link) directly into an S3 bucket
via multipart upload — no temporary local storage required.

The destination key argument accepts two forms:
  s3://bucket/path/to/file.zip   — verbatim key
  s3://bucket/path/to/prefix/   — trailing slash: derive filename from response

The --part-size flag accepts human-readable sizes such as 128MiB, 64MB, 1GiB,
or a plain integer (bytes). MB and MiB are treated as equivalent binary units
(1 MiB = 1 048 576 bytes) for simplicity, since S3 documentation conflates them.`,
	Args: cobra.ExactArgs(2),
	RunE: runHTTPCopy,
}

// s3DestPattern matches s3://<bucket>/<key> where bucket is non-empty and a
// slash separator is required between bucket and key (key may be empty).
var s3DestPattern = regexp.MustCompile(`^s3://([a-zA-Z0-9.\-_]+)/(.*)$`)

// parseS3Dest splits an s3://bucket/key string into its components.
// Returns an error when the format is wrong or the bucket part is empty.
func parseS3Dest(dest string) (bucket, key string, err error) {
	m := s3DestPattern.FindStringSubmatch(dest)
	if m == nil {
		return "", "", fmt.Errorf("destination must be in the form s3://<bucket>/<key>, got %q", dest)
	}
	return m[1], m[2], nil
}

// parseByteSize parses a human-readable byte size string into an int64.
// Recognized suffixes (case-insensitive): B, KB, MB, GB, KiB, MiB, GiB.
// MB and MiB are treated as the same binary unit (1 048 576 bytes).
// A bare integer is returned as-is. An empty string returns 0.
func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Find where the numeric prefix ends.
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid byte size %q: must start with a number", s)
	}

	num, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", s, err)
	}

	suffix := strings.ToLower(strings.TrimSpace(s[i:]))
	switch suffix {
	case "", "b":
		return num, nil
	case "kb", "kib":
		return num * 1024, nil
	case "mb", "mib":
		return num * 1024 * 1024, nil
	case "gb", "gib":
		return num * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("invalid byte size %q: unrecognized suffix %q", s, suffix)
	}
}

// formatBytes renders a byte count as a compact human-readable string.
func formatBytes(b int64) string {
	const (
		gib = 1 << 30
		mib = 1 << 20
		kib = 1 << 10
	)
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/mib)
	case b >= kib:
		return fmt.Sprintf("%.1f KiB", float64(b)/kib)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// formatDuration renders a duration in h/m/s form, dropping leading zero components.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func runHTTPCopy(cmd *cobra.Command, args []string) error {
	rawURL := args[0]
	dest := args[1]

	bucket, key, err := parseS3Dest(dest)
	if err != nil {
		return err
	}

	if httpCopyConcurrency < 0 {
		return fmt.Errorf("--concurrency must be 0 or greater, got %d", httpCopyConcurrency)
	}

	partSize, err := parseByteSize(httpCopyPartSize)
	if err != nil {
		return fmt.Errorf("--part-size: %w", err)
	}

	ctx := context.Background()
	client, err := awsClient.NewClient(ctx, profile, region)
	if err != nil {
		return fmt.Errorf("could not connect to AWS. Check your credentials in ~/.aws/credentials.\n  Detail: %w", err)
	}

	// Throttled progress reporting: one line per second written to stderr.
	var (
		lastPrint   time.Time
		startTime   = time.Now()
		lastDone    int64
		lastPrinted int64
	)

	progress := func(p httpcopy.Progress) {
		now := time.Now()

		if p.Phase == "done" {
			elapsed := now.Sub(startTime)
			fmt.Fprintf(os.Stderr, "\r\033[K[done] %s  %s  elapsed %s\n",
				p.Filename,
				formatBytes(p.BytesDone),
				formatDuration(elapsed),
			)
			return
		}

		if p.Phase == "resolving" {
			if p.ResolvedURL != "" {
				fmt.Fprintf(os.Stderr, "\r\033[K[resolving] %s\n", p.ResolvedURL)
			}
			return
		}

		// Throttle to one update per second.
		if now.Sub(lastPrint) < time.Second {
			return
		}

		elapsed := now.Sub(startTime)
		interval := now.Sub(lastPrint)
		if lastPrint.IsZero() {
			interval = elapsed
		}

		// Compute throughput over the last interval.
		var speed int64
		if interval > 0 {
			speed = int64(float64(p.BytesDone-lastDone) / interval.Seconds())
		}

		lastDone = p.BytesDone
		lastPrint = now
		lastPrinted = p.BytesDone

		filename := p.Filename
		if filename == "" {
			filename = key
		}

		if p.BytesTotal < 0 {
			// Content-Length unknown — no percent or ETA.
			fmt.Fprintf(os.Stderr, "\r\033[K[uploading] %s  %s  %s/s",
				filename,
				formatBytes(p.BytesDone),
				formatBytes(speed),
			)
			return
		}

		var pct float64
		if p.BytesTotal > 0 {
			pct = float64(p.BytesDone) / float64(p.BytesTotal) * 100
		}

		var etaStr string
		if speed > 0 && p.BytesTotal > p.BytesDone {
			remaining := time.Duration(float64(p.BytesTotal-p.BytesDone)/float64(speed)) * time.Second
			etaStr = "  ETA " + formatDuration(remaining)
		}

		fmt.Fprintf(os.Stderr, "\r\033[K[uploading] %s  %s / %s (%.1f%%)  %s/s%s",
			filename,
			formatBytes(p.BytesDone),
			formatBytes(p.BytesTotal),
			pct,
			formatBytes(speed),
			etaStr,
		)
	}

	_ = lastPrinted // used only for tracking; suppress unused warning

	finalKey, err := httpcopy.Run(ctx, client, httpcopy.Options{
		URL:         rawURL,
		Bucket:      bucket,
		Key:         key,
		Region:      region,
		PartSize:    partSize,
		Concurrency: httpCopyConcurrency,
		Progress:    progress,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Uploaded to s3://%s/%s\n", bucket, finalKey)
	return nil
}

func init() {
	httpCopyCmd.Flags().StringVar(&httpCopyPartSize, "part-size", "0",
		`Multipart upload part size, e.g. 128MiB, 64MB, 1GiB, or bytes (0 = auto)`)
	httpCopyCmd.Flags().IntVar(&httpCopyConcurrency, "concurrency", 0,
		"Number of concurrent upload parts (0 = uploader default)")

	rootCmd.AddCommand(httpCopyCmd)
}
