package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/dcorbell/s3m/internal/model"
)

// ListBuckets returns every page and keeps unreadable public settings unknown.
// Region comes from the listing, with a lookup for older bucket responses.
func (c *Client) ListBuckets(ctx context.Context) ([]model.Bucket, error) {
	paginator := s3.NewListBucketsPaginator(c.S3, &s3.ListBucketsInput{})
	var source []s3types.Bucket
	tokens := make(map[string]bool)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not list buckets: %w", err)
		}
		source = append(source, page.Buckets...)
		token := aws.ToString(page.ContinuationToken)
		if token != "" {
			if tokens[token] {
				return nil, errors.New("bucket list pagination did not advance")
			}
			tokens[token] = true
		}
	}
	buckets := make([]model.Bucket, len(source))
	for i, b := range source {
		buckets[i] = model.Bucket{Name: aws.ToString(b.Name), CreationDate: aws.ToTime(b.CreationDate), Region: aws.ToString(b.BucketRegion)}
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(buckets)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for idx := range jobs {
				b := &buckets[idx]
				if b.Region == "" {
					if resolved, err := c.GetBucketRegion(ctx, b.Name); err == nil {
						b.Region = resolved
					}
				}
				b.IsPublic, b.AccessKnown, _ = c.PublicAccessStatus(ctx, b.Name, b.Region)
			}
		}()
	}
	for i := range buckets {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buckets, nil
}

// withBucketRegion returns an s3 option that pins the request to the bucket's
// region, avoiding 301 PermanentRedirect when the client default region differs.
func withBucketRegion(region string) func(*s3.Options) {
	return func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
}

// CreateBucket creates an S3 bucket with public access blocked by default.
func (c *Client) CreateBucket(ctx context.Context, name, region string) error {
	input := &s3.CreateBucketInput{
		Bucket: aws.String(name),
	}

	// us-east-1 must NOT have a LocationConstraint
	if region != "" && region != "us-east-1" {
		input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}

	_, err := c.S3.CreateBucket(ctx, input)
	if err != nil {
		return fmt.Errorf("could not create bucket %q: %w", name, err)
	}

	// Block all public access by default
	_, err = c.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(name),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(true),
			IgnorePublicAcls:      aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(true),
		},
	})
	if err != nil {
		return &PartialBucketCreationError{Bucket: name, Err: err}
	}

	return nil
}

// DeleteBucket deletes an S3 bucket. Retries on 409 (BucketNotEmpty) since S3
// is eventually consistent after emptying - objects may still appear briefly.
func (c *Client) DeleteBucket(ctx context.Context, name, region string) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	for attempt := range 5 {
		_, err := c.S3.DeleteBucket(ctx, &s3.DeleteBucketInput{
			Bucket: aws.String(name),
		}, opts)
		if err == nil {
			return nil
		}
		// Check if it's a 409 BucketNotEmpty - retry after a delay
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "BucketNotEmpty" && attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
			continue
		}
		return fmt.Errorf("could not delete bucket %q: %w", name, err)
	}
	return nil
}

// DeleteObject deletes a single object from a bucket.
func (c *Client) DeleteObject(ctx context.Context, bucket, key, region string) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	_, err := c.S3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, opts)
	if err != nil {
		return fmt.Errorf("could not delete %q: %w", key, err)
	}
	return nil
}

// DeleteObjectKeys deletes object keys in batches of up to S3's 1,000-key limit.
func (c *Client) DeleteObjectKeys(ctx context.Context, bucket string, keys []string, region string, onProgress func(deleted int64)) (int64, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	var deleted int64
	for start := 0; start < len(keys); start += 1000 {
		end := min(start+1000, len(keys))
		objects := make([]s3types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, s3types.ObjectIdentifier{Key: aws.String(key)})
		}
		output, err := c.S3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		}, opts)
		if err != nil {
			return deleted, fmt.Errorf("could not delete selected objects: %w", err)
		}
		if len(output.Errors) > 0 {
			return deleted, fmt.Errorf("could not delete %q: %s", aws.ToString(output.Errors[0].Key), aws.ToString(output.Errors[0].Message))
		}
		deleted += int64(len(objects))
		if onProgress != nil {
			onProgress(deleted)
		}
	}
	return deleted, nil
}

// CountObjects counts all objects under a given prefix (paginated, real-time).
func (c *Client) CountObjects(ctx context.Context, bucket, prefix, region string) (int64, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	var total int64
	var continuationToken *string
	for {
		output, err := c.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		}, opts)
		if err != nil {
			return 0, fmt.Errorf("could not count objects under %q: %w", prefix, err)
		}
		total += int64(aws.ToInt32(output.KeyCount))
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		continuationToken = output.NextContinuationToken
	}
	return total, nil
}

// DeletePrefix deletes all objects under a prefix. Reports progress via onProgress callback.
func (c *Client) DeletePrefix(ctx context.Context, bucket, prefix, region string, onProgress func(deleted int64)) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	var totalDeleted int64
	var continuationToken *string
	for {
		output, err := c.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		}, opts)
		if err != nil {
			return fmt.Errorf("could not list objects under %q: %w", prefix, err)
		}
		if len(output.Contents) == 0 {
			break
		}
		objects := make([]s3types.ObjectIdentifier, 0, len(output.Contents))
		for _, obj := range output.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: obj.Key})
		}
		deleteOutput, err := c.S3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		}, opts)
		if err != nil {
			return fmt.Errorf("could not delete objects under %q: %w", prefix, err)
		}
		if len(deleteOutput.Errors) > 0 {
			return fmt.Errorf("could not delete %q: %s", aws.ToString(deleteOutput.Errors[0].Key), aws.ToString(deleteOutput.Errors[0].Message))
		}
		totalDeleted += int64(len(objects))
		if onProgress != nil {
			onProgress(totalDeleted)
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		continuationToken = output.NextContinuationToken
	}
	return nil
}

// EmptyBucket deletes all objects (including versions) from a bucket.
// onProgress is called after each batch with the total number of objects deleted so far.
// Pass nil to skip progress reporting.
func (c *Client) EmptyBucket(ctx context.Context, name, region string, onProgress func(deleted int64)) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}

	var totalDeleted int64
	var keyMarker, versionMarker *string
	for {
		versions, err := c.S3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(name),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionMarker,
		}, opts)
		if err != nil {
			return fmt.Errorf("could not list objects in %q: %w", name, err)
		}

		var objects []s3types.ObjectIdentifier
		for _, v := range versions.Versions {
			objects = append(objects, s3types.ObjectIdentifier{
				Key:       v.Key,
				VersionId: v.VersionId,
			})
		}
		for _, dm := range versions.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{
				Key:       dm.Key,
				VersionId: dm.VersionId,
			})
		}

		if len(objects) > 0 {
			_, err = c.S3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(name),
				Delete: &s3types.Delete{
					Objects: objects,
					Quiet:   aws.Bool(true),
				},
			}, opts)
			if err != nil {
				return fmt.Errorf("could not delete objects in %q: %w", name, err)
			}
			totalDeleted += int64(len(objects))
			if onProgress != nil {
				onProgress(totalDeleted)
			}
		}

		if !aws.ToBool(versions.IsTruncated) {
			break
		}
		keyMarker = versions.NextKeyMarker
		versionMarker = versions.NextVersionIdMarker
	}
	return nil
}

// IsBucketEmpty does a real-time check (not CloudWatch) for objects in a bucket.
func (c *Client) IsBucketEmpty(ctx context.Context, name, region string) (bool, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	output, err := c.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(name),
		MaxKeys: aws.Int32(1),
	}, opts)
	if err != nil {
		return false, fmt.Errorf("could not check bucket contents: %w", err)
	}
	return aws.ToInt32(output.KeyCount) == 0, nil
}

// BucketStats holds pre-computed stats from CloudWatch.
type BucketStats struct {
	ObjectCount int64
	SizeBytes   int64
	Known       bool
	UpdatedAt   time.Time
}

// GetBucketStats fetches object count and size from CloudWatch daily metrics.
// Queries CloudWatch in the bucket's region since S3 metrics are published there.
func (c *Client) GetBucketStats(ctx context.Context, bucket, region string) (BucketStats, error) {
	now := time.Now()
	start := now.Add(-48 * time.Hour) // look back 2 days to ensure we get a data point

	objectCount, countErr := c.getCloudWatchMetric(ctx, bucket, "NumberOfObjects", "AllStorageTypes", region, start, now)
	sizeBytes, sizeErr := c.getCloudWatchMetric(ctx, bucket, "BucketSizeBytes", "StandardStorage", region, start, now)
	stats := BucketStats{ObjectCount: int64(objectCount.value), SizeBytes: int64(sizeBytes.value), Known: objectCount.known && sizeBytes.known}
	if stats.Known {
		stats.UpdatedAt = objectCount.at
		if sizeBytes.at.Before(stats.UpdatedAt) {
			stats.UpdatedAt = sizeBytes.at
		}
	}
	return stats, errors.Join(countErr, sizeErr)
}

type cloudWatchSample struct {
	value float64
	at    time.Time
	known bool
}

func (c *Client) getCloudWatchMetric(ctx context.Context, bucket, metricName, storageType, region string, start, end time.Time) (cloudWatchSample, error) {
	if c.CloudWatch == nil {
		return cloudWatchSample{}, errors.New("daily metrics are unavailable")
	}
	output, err := c.CloudWatch.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace: aws.String("AWS/S3"), MetricName: aws.String(metricName), Dimensions: []cwtypes.Dimension{{Name: aws.String("BucketName"), Value: aws.String(bucket)}, {Name: aws.String("StorageType"), Value: aws.String(storageType)}}, StartTime: &start, EndTime: &end, Period: aws.Int32(86400), Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
	}, func(o *cloudwatch.Options) {
		if region != "" {
			o.Region = region
		}
	})
	if err != nil {
		return cloudWatchSample{}, fmt.Errorf("could not read %s daily metric: %w", metricName, err)
	}
	var latest cloudWatchSample
	if output == nil {
		return latest, nil
	}
	for _, dp := range output.Datapoints {
		if dp.Timestamp == nil || dp.Timestamp.IsZero() || dp.Average == nil || math.IsNaN(*dp.Average) || math.IsInf(*dp.Average, 0) || *dp.Average < 0 {
			continue
		}
		if !latest.known || dp.Timestamp.After(latest.at) {
			latest = cloudWatchSample{value: *dp.Average, at: *dp.Timestamp, known: true}
		}
	}
	return latest, nil
}

// GetBucketObjectCount returns the object count from CloudWatch (convenience wrapper).
func (c *Client) GetBucketObjectCount(ctx context.Context, bucket, region string) (int64, error) {
	stats, err := c.GetBucketStats(ctx, bucket, region)
	if err == nil && !stats.Known {
		err = errors.New("daily bucket metrics have no usable samples")
	}
	return stats.ObjectCount, err
}

// GetBucketRegion returns the bucket's actual region using a HeadBucket-based
// lookup that works cross-region and with credentials that lack
// s3:GetBucketLocation (e.g. bucket-scoped access keys). Falls back to
// GetBucketLocation only if HeadBucket yields no region header.
func (c *Client) GetBucketRegion(ctx context.Context, bucket string) (string, error) {
	region, err := manager.GetBucketRegion(ctx, c.S3, bucket)
	if err == nil && region != "" {
		return region, nil
	}

	// Fallback: some edge cases (anonymous-blocked buckets, proxies) hide the
	// x-amz-bucket-region header. Try GetBucketLocation as a best-effort.
	if locOutput, locErr := c.S3.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucket),
	}); locErr == nil {
		r := string(locOutput.LocationConstraint)
		if r == "" {
			r = "us-east-1"
		}
		return r, nil
	}

	if err != nil {
		return "", fmt.Errorf("could not get region for %q: %w", bucket, err)
	}
	return "", fmt.Errorf("could not get region for %q", bucket)
}

// ListPrefixes returns top-level prefixes (folders) in a bucket.
func (c *Client) ListPrefixes(ctx context.Context, bucket, region string) ([]string, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	output, err := c.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String("/"),
	}, opts)
	if err != nil {
		return nil, fmt.Errorf("could not list prefixes in %q: %w", bucket, err)
	}

	prefixes := make([]string, 0, len(output.CommonPrefixes))
	for _, p := range output.CommonPrefixes {
		prefixes = append(prefixes, aws.ToString(p.Prefix))
	}
	return prefixes, nil
}

// CreatePrefix persists an empty folder marker object so the prefix appears in S3 listings.
func (c *Client) CreatePrefix(ctx context.Context, bucket, prefix, region string) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}

	_, err := c.S3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(prefix),
		Body:   strings.NewReader(""),
	}, opts)
	if err != nil {
		return fmt.Errorf("could not create prefix %q: %w", prefix, err)
	}
	return nil
}

// BrowseItem represents a folder or file in a bucket listing.
type BrowseItem struct {
	Name         string // display name (just the last segment, no full prefix)
	Key          string // full S3 key or prefix
	IsFolder     bool
	Size         int64
	LastModified time.Time
}

// ListContents returns folders and files at a given prefix in a bucket.
func (c *Client) ListContents(ctx context.Context, bucket, prefix, region string) ([]BrowseItem, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}

	var items []BrowseItem
	var continuationToken *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: continuationToken,
		}
		output, err := c.S3.ListObjectsV2(ctx, input, opts)
		if err != nil {
			return nil, fmt.Errorf("could not list contents of %q: %w", bucket, err)
		}

		// Folders (common prefixes)
		folderKeys := make(map[string]struct{}, len(output.CommonPrefixes))
		for _, cp := range output.CommonPrefixes {
			fullPrefix := aws.ToString(cp.Prefix)
			folderKeys[fullPrefix] = struct{}{}
			name := strings.TrimPrefix(fullPrefix, prefix)
			items = append(items, BrowseItem{
				Name:     name,
				Key:      fullPrefix,
				IsFolder: true,
			})
		}

		// Files (objects at this level)
		for _, obj := range output.Contents {
			key := aws.ToString(obj.Key)
			// Skip the prefix itself if it appears as an object
			if key == prefix {
				continue
			}
			// Explicit folder-marker objects can also be returned as a common
			// prefix. Render them once as folders, not again as files.
			if _, isFolderMarker := folderKeys[key]; isFolderMarker {
				continue
			}
			name := strings.TrimPrefix(key, prefix)
			items = append(items, BrowseItem{
				Name:         name,
				Key:          key,
				IsFolder:     false,
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}

		if !aws.ToBool(output.IsTruncated) {
			break
		}
		continuationToken = output.NextContinuationToken
	}

	return items, nil
}

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

// AutoPartSize returns an appropriate S3 multipart part size for the given
// content length, keeping the part count comfortably under the 10,000-part
// S3 cap. Mirrors httpcopy.ComputePartSize so disk uploads and URL uploads
// size parts identically. Pass 0 or negative for unknown size.
func AutoPartSize(contentLength int64) int64 {
	const (
		minPart      = 64 << 20  // 64 MiB, S3 minimum for non-final parts
		fallbackPart = 256 << 20 // 256 MiB when size is unknown
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

// DownloadFile downloads an S3 object to dest using concurrent ranged GETs
// via manager.Downloader. partSize and concurrency are optional; pass 0 to
// use manager defaults. Returns bytes written. Unlike DownloadObject (single
// GetObject), this parallelizes large downloads and has no 5 GiB ceiling.
//
//nolint:staticcheck // Retain the installed multipart manager for this phase.
func (c *Client) DownloadFile(ctx context.Context, bucket, key, region string, dest io.WriterAt, partSize int64, concurrency int) (int64, error) {
	downloader := manager.NewDownloader(c.S3, func(d *manager.Downloader) {
		if partSize > 0 {
			d.PartSize = partSize
		}
		if concurrency > 0 {
			d.Concurrency = concurrency
		}
	})
	var downloadOpts []func(*manager.Downloader)
	if region != "" {
		downloadOpts = append(downloadOpts, manager.WithDownloaderClientOptions(func(o *s3.Options) {
			o.Region = region
		}))
	}
	n, err := downloader.Download(ctx, dest, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, downloadOpts...)
	if err != nil {
		return 0, fmt.Errorf("could not download %q: %w", key, err)
	}
	return n, nil
}

// GetObjectSize returns the size of an S3 object in bytes via HeadObject.
// Callers use it to seed progress-bar totals before a DownloadFile.
func (c *Client) GetObjectSize(ctx context.Context, bucket, key, region string) (int64, error) {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	output, err := c.S3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, opts)
	if err != nil {
		return 0, fmt.Errorf("could not head %q: %w", key, err)
	}
	return aws.ToInt64(output.ContentLength), nil
}

// UploadObject uploads a file to S3 at the given key.
// Single-PUT only: S3 caps a single PutObject at 5 GiB. Callers uploading
// user-chosen files must use UploadStream (multipart) instead.
func (c *Client) UploadObject(ctx context.Context, bucket, key, region string, body io.Reader) error {
	return c.UploadObjectSized(ctx, bucket, key, region, body, -1)
}

// UploadObjectSized uploads a file to S3 at the given key, setting
// Content-Length when size is known.
func (c *Client) UploadObjectSized(ctx context.Context, bucket, key, region string, body io.Reader, size int64) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if size >= 0 {
		input.ContentLength = aws.Int64(size)
	}
	_, err := c.S3.PutObject(ctx, input, opts)
	if err != nil {
		return fmt.Errorf("could not upload %q: %w", key, err)
	}
	return nil
}

// UploadStream uploads an io.Reader to S3 using multipart upload via manager.Uploader.
// partSize and concurrency are optional; pass 0 to use manager defaults.
//
// The feature/s3/manager package is marked deprecated in favor of
// feature/s3/transfermanager (discussion aws-sdk-go-v2#3306), but transfermanager
// is still in preview as of aws-sdk-go-v2 v1.41.x. Revisit once it reaches GA.
//
// Failed multipart transfers retry cleanup using a bounded context independent
// of cancellation. A bucket lifecycle rule remains useful if cleanup is denied.
//
//nolint:staticcheck // manager.Uploader is the current stable multipart API
func (c *Client) UploadStream(ctx context.Context, bucket, key, region string, body io.Reader, partSize int64, concurrency int) error {
	return c.uploadStream(ctx, bucket, key, region, body, partSize, concurrency, nil, false)
}

// UploadStreamConditional rejects newly created or changed destinations.
// A nil condition requires absence; otherwise it must match the reviewed ETag.
func (c *Client) UploadStreamConditional(ctx context.Context, bucket, key, region string, body io.Reader, partSize int64, concurrency int, condition *string) error {
	return c.uploadStream(ctx, bucket, key, region, body, partSize, concurrency, condition, true)
}

//nolint:staticcheck // manager remains the installed stable multipart API
func (c *Client) uploadStream(ctx context.Context, bucket, key, region string, body io.Reader, partSize int64, concurrency int, condition *string, conditional bool) error {
	uploader := manager.NewUploader(c.S3, func(u *manager.Uploader) {
		if partSize > 0 {
			u.PartSize = partSize
		}
		if concurrency > 0 {
			u.Concurrency = concurrency
		}
	})
	input := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: body}
	if conditional {
		if condition == nil {
			input.IfNoneMatch = aws.String("*")
		} else {
			input.IfMatch = condition
		}
	}
	opts := manager.WithUploaderRequestOptions(func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	})
	_, err := uploader.Upload(ctx, input, opts)
	if err != nil {
		var failed manager.MultiUploadFailure
		if errors.As(err, &failed) && failed.UploadID() != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_, cleanupErr := c.S3.AbortMultipartUpload(cleanupCtx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(failed.UploadID())}, func(o *s3.Options) {
				if region != "" {
					o.Region = region
				}
			})
			cancel()
			if cleanupErr != nil {
				return fmt.Errorf("could not upload %q: %w; multipart cleanup failed: %v", key, err, cleanupErr)
			}
		}
		return fmt.Errorf("could not upload %q: %w", key, err)
	}
	return nil
}

// rawBucketPolicy keeps unrelated policy fields and statements intact.
type rawBucketPolicy struct {
	document   map[string]json.RawMessage
	statements []json.RawMessage
}

func (c *Client) readBucketPolicy(ctx context.Context, bucket, region string) (rawBucketPolicy, bool, error) {
	out, err := c.S3.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)}, withBucketRegion(region))
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NoSuchBucketPolicy" {
			return rawBucketPolicy{document: map[string]json.RawMessage{"Version": json.RawMessage(`"2012-10-17"`)}}, false, nil
		}
		return rawBucketPolicy{}, false, fmt.Errorf("could not read bucket policy for %q: %w", bucket, err)
	}
	if out == nil || out.Policy == nil {
		return rawBucketPolicy{}, false, fmt.Errorf("bucket policy response for %q was empty", bucket)
	}
	var doc map[string]json.RawMessage
	if err = json.Unmarshal([]byte(*out.Policy), &doc); err != nil || doc == nil {
		return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q is not a valid JSON object", bucket)
	}
	raw, ok := doc["Statement"]
	if !ok {
		return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q has no statements", bucket)
	}
	var statements []json.RawMessage
	if err = json.Unmarshal(raw, &statements); err != nil {
		var single map[string]json.RawMessage
		if json.Unmarshal(raw, &single) != nil || single == nil {
			return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q has invalid statements", bucket)
		}
		statements = []json.RawMessage{raw}
	}
	if statements == nil {
		return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q has null statements", bucket)
	}
	for _, statement := range statements {
		var fields map[string]json.RawMessage
		if json.Unmarshal(statement, &fields) != nil || len(fields) == 0 {
			return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q has an invalid statement", bucket)
		}
		effect := policyString(fields["Effect"])
		if effect != "Allow" && effect != "Deny" {
			return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q has an invalid statement effect", bucket)
		}
		if sid, exists := fields["Sid"]; exists {
			var value string
			if json.Unmarshal(sid, &value) != nil {
				return rawBucketPolicy{}, false, fmt.Errorf("bucket policy for %q has an invalid statement identifier", bucket)
			}
		}
	}
	return rawBucketPolicy{document: doc, statements: statements}, true, nil
}
func (policy rawBucketPolicy) encode(statements []json.RawMessage) (string, error) {
	encoded, err := json.Marshal(statements)
	if err != nil {
		return "", err
	}
	policy.document["Statement"] = encoded
	document, err := json.Marshal(policy.document)
	return string(document), err
}
func policyString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func policyStatementFields(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	return fields
}
func policyContains(raw json.RawMessage, value string) bool {
	if policyString(raw) == value {
		return true
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func policyPublicPrincipal(raw json.RawMessage) bool {
	if policyString(raw) == "*" {
		return true
	}
	var principal map[string]json.RawMessage
	if json.Unmarshal(raw, &principal) != nil {
		return false
	}
	return policyContains(principal["AWS"], "*")
}
func publicPrefixSID(prefix string) string { return "s3m-public-" + strings.TrimSuffix(prefix, "/") }

// GetPrefixAccessStatus reports explicit s3m-managed read grants, not reachability.
func (c *Client) GetPrefixAccessStatus(ctx context.Context, bucket, region string, prefixes []string) ([]model.PrefixAccess, error) {
	policy, _, err := c.readBucketPolicy(ctx, bucket, region)
	if err != nil {
		return nil, err
	}
	accesses := make([]model.PrefixAccess, 0, len(prefixes))
	for _, prefix := range prefixes {
		access := model.PrefixAccess{Prefix: prefix}
		for _, raw := range policy.statements {
			statement := policyStatementFields(raw)
			if policyString(statement["Sid"]) == publicPrefixSID(prefix) && policyString(statement["Effect"]) == "Allow" && policyPublicPrincipal(statement["Principal"]) && policyContains(statement["Action"], "s3:GetObject") && policyContains(statement["Resource"], fmt.Sprintf("arn:aws:s3:::%s/%s*", bucket, prefix)) {
				access.IsPublic = true
				break
			}
		}
		accesses = append(accesses, access)
	}
	return accesses, nil
}

// SetPrefixPublic reads and validates policy before changing public settings.
func (c *Client) SetPrefixPublic(ctx context.Context, bucket, prefix, region string) error {
	policy, _, err := c.readBucketPolicy(ctx, bucket, region)
	if err != nil {
		return err
	}
	sid := publicPrefixSID(prefix)
	filtered := make([]json.RawMessage, 0, len(policy.statements)+1)
	for _, statement := range policy.statements {
		if policyString(policyStatementFields(statement)["Sid"]) != sid {
			filtered = append(filtered, statement)
		}
	}
	grant, err := json.Marshal(map[string]string{"Sid": sid, "Effect": "Allow", "Principal": "*", "Action": "s3:GetObject", "Resource": fmt.Sprintf("arn:aws:s3:::%s/%s*", bucket, prefix)})
	if err != nil {
		return fmt.Errorf("could not build public grant: %w", err)
	}
	filtered = append(filtered, grant)
	encoded, err := policy.encode(filtered)
	if err != nil {
		return fmt.Errorf("could not build bucket policy: %w", err)
	}
	_, err = c.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{Bucket: aws.String(bucket), PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{BlockPublicAcls: aws.Bool(true), BlockPublicPolicy: aws.Bool(false), IgnorePublicAcls: aws.Bool(true), RestrictPublicBuckets: aws.Bool(false)}}, withBucketRegion(region))
	if err != nil {
		return fmt.Errorf("could not update public access settings: %w", err)
	}
	_, err = c.S3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucket), Policy: aws.String(encoded)}, withBucketRegion(region))
	if err != nil {
		return fmt.Errorf("public access block settings changed, but the requested grant for %q was not applied: %w", prefix, err)
	}
	return nil
}

// SetPrefixPrivate removes only the named s3m-managed public grant.
func (c *Client) SetPrefixPrivate(ctx context.Context, bucket, prefix, region string) error {
	return c.SetPrefixesPrivate(ctx, bucket, []string{prefix}, region)
}

// SetPrefixesPrivate preserves every unrelated statement and reports partial changes.
func (c *Client) SetPrefixesPrivate(ctx context.Context, bucket string, prefixes []string, region string) error {
	policy, exists, err := c.readBucketPolicy(ctx, bucket, region)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	sids := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		sids[publicPrefixSID(prefix)] = true
	}
	filtered := make([]json.RawMessage, 0, len(policy.statements))
	removed := false
	for _, statement := range policy.statements {
		if sids[policyString(policyStatementFields(statement)["Sid"])] {
			removed = true
		} else {
			filtered = append(filtered, statement)
		}
	}
	if !removed {
		return nil
	}
	if len(filtered) == 0 {
		_, err = c.S3.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: aws.String(bucket)}, withBucketRegion(region))
		if err != nil {
			return fmt.Errorf("could not remove managed public grants: %w", err)
		}
		_, err = c.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{Bucket: aws.String(bucket), PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{BlockPublicAcls: aws.Bool(true), BlockPublicPolicy: aws.Bool(true), IgnorePublicAcls: aws.Bool(true), RestrictPublicBuckets: aws.Bool(true)}}, withBucketRegion(region))
		if err != nil {
			return fmt.Errorf("managed public grants were removed, but public access blocks could not be restored: %w", err)
		}
		return nil
	}
	encoded, err := policy.encode(filtered)
	if err != nil {
		return fmt.Errorf("could not build bucket policy: %w", err)
	}
	_, err = c.S3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucket), Policy: aws.String(encoded)}, withBucketRegion(region))
	if err != nil {
		return fmt.Errorf("could not remove selected managed public grants: %w", err)
	}
	return nil
}
