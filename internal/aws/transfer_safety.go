package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// ObjectWriteCondition distinguishes a missing destination from an unreadable one.
// A nil condition means absent; a non-nil condition identifies the current object.
func (c *Client) ObjectWriteCondition(ctx context.Context, bucket, key, region string) (*string, error) {
	out, err := c.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: sdkaws.String(bucket), Key: sdkaws.String(key)}, func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "404") {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot check destination %q: %w", key, err)
	}
	if out == nil || sdkaws.ToString(out.ETag) == "" {
		return nil, fmt.Errorf("cannot verify destination %q: missing object identifier", key)
	}
	return out.ETag, nil
}

// WriteDownloadSafely publishes only completed output. The callback writes into
// a private temporary file. New files use atomic exclusive publication.
func WriteDownloadSafely(ctx context.Context, destination string, overwrite bool, download func(io.WriterAt) (int64, error)) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	before, err := os.Lstat(destination)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if err == nil {
		if !overwrite {
			return 0, fmt.Errorf("%w: destination exists: %s", os.ErrExist, destination)
		}
		if !before.Mode().IsRegular() {
			return 0, fmt.Errorf("destination is not a regular file: %s", destination)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".s3m-download-*")
	if err != nil {
		return 0, err
	}
	tempName := tmp.Name()
	defer os.Remove(tempName) //nolint:errcheck
	defer tmp.Close()         //nolint:errcheck
	n, err := download(tmp)
	if err != nil {
		return n, err
	}
	if err = ctx.Err(); err != nil {
		return n, err
	}
	if err = tmp.Sync(); err != nil {
		return n, err
	}
	if err = tmp.Close(); err != nil {
		return n, err
	}
	if err = ctx.Err(); err != nil {
		return n, err
	}
	if before != nil {
		current, statErr := os.Lstat(destination)
		if statErr != nil || !os.SameFile(before, current) || before.Size() != current.Size() || !before.ModTime().Equal(current.ModTime()) {
			return n, fmt.Errorf("destination changed during download: %s", destination)
		}
		if err = os.Rename(tempName, destination); err != nil {
			return n, fmt.Errorf("cannot publish download: %w", err)
		}
	} else if err = os.Link(tempName, destination); err != nil {
		return n, fmt.Errorf("cannot publish download without replacing another file: %w", err)
	}
	return n, nil
}

// DownloadToPath writes an object safely. Existing destinations need explicit consent.
func (c *Client) DownloadToPath(ctx context.Context, bucket, key, region, destination string, overwrite bool, partSize int64, concurrency int) (int64, error) {
	return WriteDownloadSafely(ctx, destination, overwrite, func(w io.WriterAt) (int64, error) {
		return c.DownloadFile(ctx, bucket, key, region, w, partSize, concurrency)
	})
}
