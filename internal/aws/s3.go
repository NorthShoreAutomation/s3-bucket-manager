package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

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

// DeleteBucket deletes an S3 bucket. The bucket must be empty.
// Uses the bucket's region to avoid 301 redirects for cross-region buckets.
func (c *Client) DeleteBucket(ctx context.Context, name, region string) error {
	opts := func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	}
	_, err := c.S3.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(name),
	}, opts)
	if err != nil {
		return fmt.Errorf("could not delete bucket %q: %w", name, err)
	}
	return nil
}

// BucketStats holds pre-computed stats from CloudWatch.
type BucketStats struct {
	ObjectCount int64
	SizeBytes   int64
}

// GetBucketStats fetches object count and size from CloudWatch daily metrics.
// These are updated once per day by S3 — fast and accurate for dashboard display.
func (c *Client) GetBucketStats(ctx context.Context, bucket string) (BucketStats, error) {
	now := time.Now()
	start := now.Add(-48 * time.Hour) // look back 2 days to ensure we get a data point

	objectCount := c.getCloudWatchMetric(ctx, bucket, "NumberOfObjects", "AllStorageTypes", start, now)
	sizeBytes := c.getCloudWatchMetric(ctx, bucket, "BucketSizeBytes", "StandardStorage", start, now)

	return BucketStats{
		ObjectCount: int64(objectCount),
		SizeBytes:   int64(sizeBytes),
	}, nil
}

func (c *Client) getCloudWatchMetric(ctx context.Context, bucket, metricName, storageType string, start, end time.Time) float64 {
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
	})
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
func (c *Client) GetBucketObjectCount(ctx context.Context, bucket string) (int64, error) {
	stats, err := c.GetBucketStats(ctx, bucket)
	return stats.ObjectCount, err
}

// ListPrefixes returns top-level prefixes (folders) in a bucket.
func (c *Client) ListPrefixes(ctx context.Context, bucket string) ([]string, error) {
	output, err := c.S3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return nil, fmt.Errorf("could not list prefixes in %q: %w", bucket, err)
	}

	prefixes := make([]string, 0, len(output.CommonPrefixes))
	for _, p := range output.CommonPrefixes {
		prefixes = append(prefixes, aws.ToString(p.Prefix))
	}
	return prefixes, nil
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
		json.Unmarshal([]byte(aws.ToString(policyOutput.Policy)), &doc)
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
		c.S3.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{
			Bucket: aws.String(bucket),
		})
		c.S3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
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
