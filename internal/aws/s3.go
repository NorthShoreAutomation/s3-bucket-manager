package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/dcorbell/s3m/internal/model"
)

// ListBuckets returns all S3 buckets with their region and public status.
// Per-bucket metadata is fetched concurrently for speed.
func (c *Client) ListBuckets(ctx context.Context) ([]model.Bucket, error) {
	output, err := c.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("could not list buckets: %w", err)
	}

	buckets := make([]model.Bucket, len(output.Buckets))
	var wg sync.WaitGroup
	for i, b := range output.Buckets {
		buckets[i] = model.Bucket{
			Name:         aws.ToString(b.Name),
			CreationDate: aws.ToTime(b.CreationDate),
		}
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()

			// Get region
			locOutput, err := c.S3.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
				Bucket: aws.String(name),
			})
			if err == nil {
				region := string(locOutput.LocationConstraint)
				if region == "" {
					region = "us-east-1"
				}
				buckets[idx].Region = region
			}

			// Check public access
			pabOutput, err := c.S3.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{
				Bucket: aws.String(name),
			})
			if err != nil {
				buckets[idx].IsPublic = true
			} else {
				cfg := pabOutput.PublicAccessBlockConfiguration
				allBlocked := aws.ToBool(cfg.BlockPublicAcls) &&
					aws.ToBool(cfg.BlockPublicPolicy) &&
					aws.ToBool(cfg.IgnorePublicAcls) &&
					aws.ToBool(cfg.RestrictPublicBuckets)
				buckets[idx].IsPublic = !allBlocked
			}
		}(i, aws.ToString(b.Name))
	}
	wg.Wait()
	return buckets, nil
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
		return fmt.Errorf("bucket created but could not block public access: %w", err)
	}

	return nil
}

// DeleteBucket deletes an S3 bucket. Retries on 409 (BucketNotEmpty) since S3
// is eventually consistent after emptying — objects may still appear briefly.
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
		// Check if it's a 409 BucketNotEmpty — retry after a delay
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
		_, err = c.S3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		}, opts)
		if err != nil {
			return fmt.Errorf("could not delete objects under %q: %w", prefix, err)
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
}

// GetBucketStats fetches object count and size from CloudWatch daily metrics.
// Queries CloudWatch in the bucket's region since S3 metrics are published there.
func (c *Client) GetBucketStats(ctx context.Context, bucket, region string) (BucketStats, error) {
	now := time.Now()
	start := now.Add(-48 * time.Hour) // look back 2 days to ensure we get a data point

	objectCount := c.getCloudWatchMetric(ctx, bucket, "NumberOfObjects", "AllStorageTypes", region, start, now)
	sizeBytes := c.getCloudWatchMetric(ctx, bucket, "BucketSizeBytes", "StandardStorage", region, start, now)

	return BucketStats{
		ObjectCount: int64(objectCount),
		SizeBytes:   int64(sizeBytes),
	}, nil
}

func (c *Client) getCloudWatchMetric(ctx context.Context, bucket, metricName, storageType, region string, start, end time.Time) float64 {
	opts := func(o *cloudwatch.Options) {
		if region != "" {
			o.Region = region
		}
	}
	output, err := c.CloudWatch.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/S3"),
		MetricName: aws.String(metricName),
		Dimensions: []cwtypes.Dimension{
			{Name: aws.String("BucketName"), Value: aws.String(bucket)},
			{Name: aws.String("StorageType"), Value: aws.String(storageType)},
		},
		StartTime:  &start,
		EndTime:    &end,
		Period:     aws.Int32(86400), // 1 day
		Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
	}, opts)
	if err != nil || len(output.Datapoints) == 0 {
		return 0
	}
	// Return the most recent data point
	latest := output.Datapoints[0]
	for _, dp := range output.Datapoints[1:] {
		if dp.Timestamp.After(*latest.Timestamp) {
			latest = dp
		}
	}
	if latest.Average != nil {
		return *latest.Average
	}
	return 0
}

// GetBucketObjectCount returns the object count from CloudWatch (convenience wrapper).
func (c *Client) GetBucketObjectCount(ctx context.Context, bucket, region string) (int64, error) {
	stats, err := c.GetBucketStats(ctx, bucket, region)
	return stats.ObjectCount, err
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
		for _, cp := range output.CommonPrefixes {
			fullPrefix := aws.ToString(cp.Prefix)
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

// policyDocument represents an S3 bucket policy.
type policyDocument struct {
	Version   string            `json:"Version"`
	Statement []policyStatement `json:"Statement"`
}

type policyStatement struct {
	Sid       string `json:"Sid"`
	Effect    string `json:"Effect"`
	Principal string `json:"Principal"`
	Action    string `json:"Action"`
	Resource  string `json:"Resource"`
}

// GetPrefixAccessStatus checks which prefixes are public based on bucket policy.
func (c *Client) GetPrefixAccessStatus(ctx context.Context, bucket string, prefixes []string) ([]model.PrefixAccess, error) {
	publicPrefixes := make(map[string]bool)

	policyOutput, err := c.S3.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	if err == nil && policyOutput.Policy != nil {
		var doc policyDocument
		if jsonErr := json.Unmarshal([]byte(aws.ToString(policyOutput.Policy)), &doc); jsonErr == nil {
			for _, stmt := range doc.Statement {
				if stmt.Effect == "Allow" && stmt.Principal == "*" && stmt.Action == "s3:GetObject" {
					// Extract prefix from resource ARN
					arnPrefix := fmt.Sprintf("arn:aws:s3:::%s/", bucket)
					if strings.HasPrefix(stmt.Resource, arnPrefix) {
						prefix := strings.TrimPrefix(stmt.Resource, arnPrefix)
						prefix = strings.TrimSuffix(prefix, "*")
						publicPrefixes[prefix] = true
					}
				}
			}
		}
	}

	accesses := make([]model.PrefixAccess, 0, len(prefixes))
	for _, p := range prefixes {
		accesses = append(accesses, model.PrefixAccess{
			Prefix:   p,
			IsPublic: publicPrefixes[p],
		})
	}
	return accesses, nil
}

// SetPrefixPublic makes a prefix publicly readable by adding a bucket policy statement.
func (c *Client) SetPrefixPublic(ctx context.Context, bucket, prefix string) error {
	// First ensure public access block allows public policies
	_, err := c.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(false), // Allow public policies
			IgnorePublicAcls:      aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(false), // Allow public access
		},
	})
	if err != nil {
		return fmt.Errorf("could not update public access settings: %w", err)
	}

	// Get existing policy or create new one
	doc := policyDocument{Version: "2012-10-17"}
	policyOutput, err := c.S3.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	if err == nil && policyOutput.Policy != nil {
		json.Unmarshal([]byte(aws.ToString(policyOutput.Policy)), &doc) //nolint:errcheck // best-effort parse; empty doc is fine on failure
	}

	// Build sid from prefix (e.g., "installers/" -> "s3m-public-installers")
	sid := "s3m-public-" + strings.TrimSuffix(prefix, "/")

	// Remove existing statement for this prefix if any
	filtered := make([]policyStatement, 0, len(doc.Statement))
	for _, stmt := range doc.Statement {
		if stmt.Sid != sid {
			filtered = append(filtered, stmt)
		}
	}

	// Add new public statement
	resource := fmt.Sprintf("arn:aws:s3:::%s/%s*", bucket, prefix)
	filtered = append(filtered, policyStatement{
		Sid:       sid,
		Effect:    "Allow",
		Principal: "*",
		Action:    "s3:GetObject",
		Resource:  resource,
	})
	doc.Statement = filtered

	policyJSON, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("could not build policy: %w", err)
	}

	_, err = c.S3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(string(policyJSON)),
	})
	if err != nil {
		return fmt.Errorf("could not set prefix %q as public: %w", prefix, err)
	}
	return nil
}

// SetPrefixPrivate removes the public access policy statement for a prefix.
func (c *Client) SetPrefixPrivate(ctx context.Context, bucket, prefix string) error {
	sid := "s3m-public-" + strings.TrimSuffix(prefix, "/")

	policyOutput, err := c.S3.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return nil // No policy means already private
	}

	var doc policyDocument
	if err := json.Unmarshal([]byte(aws.ToString(policyOutput.Policy)), &doc); err != nil {
		return nil
	}

	// Remove the statement for this prefix
	filtered := make([]policyStatement, 0, len(doc.Statement))
	for _, stmt := range doc.Statement {
		if stmt.Sid != sid {
			filtered = append(filtered, stmt)
		}
	}

	if len(filtered) == 0 {
		// No statements left, delete the policy and re-block public access
		c.S3.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{ //nolint:errcheck // best-effort cleanup when removing all public access
			Bucket: aws.String(bucket),
		})
		c.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{ //nolint:errcheck // best-effort restore of public access block
			Bucket: aws.String(bucket),
			PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
				BlockPublicAcls:       aws.Bool(true),
				BlockPublicPolicy:     aws.Bool(true),
				IgnorePublicAcls:      aws.Bool(true),
				RestrictPublicBuckets: aws.Bool(true),
			},
		})
		return nil
	}

	doc.Statement = filtered
	policyJSON, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("could not build policy: %w", err)
	}

	_, err = c.S3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(string(policyJSON)),
	})
	if err != nil {
		return fmt.Errorf("could not remove public access for prefix %q: %w", prefix, err)
	}
	return nil
}
