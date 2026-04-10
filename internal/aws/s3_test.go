package aws

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/dcorbell/s3m/internal/model"
)

// mockS3 implements S3API for testing.
type mockS3 struct {
	S3API
	listBucketsOutput          *s3.ListBucketsOutput
	listBucketsErr             error
	createBucketOutput         *s3.CreateBucketOutput
	createBucketErr            error
	deleteBucketErr            error
	getBucketLocationOutput    *s3.GetBucketLocationOutput
	getPublicAccessBlockOutput *s3.GetPublicAccessBlockOutput
	getPublicAccessBlockErr    error
	putPublicAccessBlockErr    error
	listObjectsV2Output        *s3.ListObjectsV2Output
	getBucketPolicyOutput      *s3.GetBucketPolicyOutput
	getBucketPolicyErr         error
	putBucketPolicyErr         error
	deleteBucketPolicyErr      error
}

func (m *mockS3) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return m.listBucketsOutput, m.listBucketsErr
}

func (m *mockS3) CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	return m.createBucketOutput, m.createBucketErr
}

func (m *mockS3) DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	return nil, m.deleteBucketErr
}

func (m *mockS3) GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return m.getBucketLocationOutput, nil
}

func (m *mockS3) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return m.listObjectsV2Output, nil
}

func (m *mockS3) GetPublicAccessBlock(ctx context.Context, params *s3.GetPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error) {
	return m.getPublicAccessBlockOutput, m.getPublicAccessBlockErr
}

func (m *mockS3) PutPublicAccessBlock(ctx context.Context, params *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	return nil, m.putPublicAccessBlockErr
}

func (m *mockS3) DeletePublicAccessBlock(ctx context.Context, params *s3.DeletePublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.DeletePublicAccessBlockOutput, error) {
	return nil, nil
}

func (m *mockS3) GetBucketPolicy(ctx context.Context, params *s3.GetBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	return m.getBucketPolicyOutput, m.getBucketPolicyErr
}

func (m *mockS3) PutBucketPolicy(ctx context.Context, params *s3.PutBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error) {
	return nil, m.putBucketPolicyErr
}

func (m *mockS3) DeleteBucketPolicy(ctx context.Context, params *s3.DeleteBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketPolicyOutput, error) {
	return nil, m.deleteBucketPolicyErr
}

func TestListBuckets(t *testing.T) {
	now := time.Now()
	mock := &mockS3{
		listBucketsOutput: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{
				{Name: aws.String("my-bucket"), CreationDate: &now},
				{Name: aws.String("other-bucket"), CreationDate: &now},
			},
		},
		getBucketLocationOutput: &s3.GetBucketLocationOutput{
			LocationConstraint: s3types.BucketLocationConstraintUsWest2,
		},
		getPublicAccessBlockOutput: &s3.GetPublicAccessBlockOutput{
			PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
				BlockPublicAcls:       aws.Bool(true),
				BlockPublicPolicy:     aws.Bool(true),
				IgnorePublicAcls:      aws.Bool(true),
				RestrictPublicBuckets: aws.Bool(true),
			},
		},
	}
	client := &Client{S3: mock, Region: "us-east-1"}

	buckets, err := client.ListBuckets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(buckets))
	}
	if buckets[0].Name != "my-bucket" {
		t.Errorf("expected 'my-bucket', got %q", buckets[0].Name)
	}
	if buckets[0].Region != "us-west-2" {
		t.Errorf("expected region 'us-west-2', got %q", buckets[0].Region)
	}
}

func TestCreateBucket(t *testing.T) {
	mock := &mockS3{
		createBucketOutput: &s3.CreateBucketOutput{},
	}
	client := &Client{S3: mock, Region: "us-west-2"}

	err := client.CreateBucket(context.Background(), "new-bucket", "us-west-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateBucketUsEast1(t *testing.T) {
	// us-east-1 must NOT send a LocationConstraint
	mock := &mockS3{
		createBucketOutput: &s3.CreateBucketOutput{},
	}
	client := &Client{S3: mock, Region: "us-east-1"}

	err := client.CreateBucket(context.Background(), "new-bucket", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListPrefixes(t *testing.T) {
	mock := &mockS3{
		listObjectsV2Output: &s3.ListObjectsV2Output{
			CommonPrefixes: []s3types.CommonPrefix{
				{Prefix: aws.String("installers/")},
				{Prefix: aws.String("data/")},
			},
		},
	}
	client := &Client{S3: mock}

	prefixes, err := client.ListPrefixes(context.Background(), "my-bucket", "us-west-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("expected 2 prefixes, got %d", len(prefixes))
	}
	if prefixes[0] != "installers/" {
		t.Errorf("expected 'installers/', got %q", prefixes[0])
	}
}

// mockCloudWatch implements CloudWatchAPI for testing.
type mockCloudWatch struct {
	CloudWatchAPI
	output *cloudwatch.GetMetricStatisticsOutput
}

func (m *mockCloudWatch) GetMetricStatistics(ctx context.Context, params *cloudwatch.GetMetricStatisticsInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStatisticsOutput, error) {
	return m.output, nil
}

func TestGetBucketStats(t *testing.T) {
	now := time.Now()
	avg42 := 42.0
	avg1024 := 1024.0
	cwMock := &mockCloudWatch{
		output: &cloudwatch.GetMetricStatisticsOutput{
			Datapoints: []cwtypes.Datapoint{
				{Timestamp: &now, Average: &avg42},
			},
		},
	}
	// Override to return size on second call
	client := &Client{CloudWatch: cwMock}

	stats, err := client.GetBucketStats(context.Background(), "my-bucket", "us-west-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both metrics hit the same mock, so both return 42
	if stats.ObjectCount != 42 {
		t.Errorf("expected 42 objects, got %d", stats.ObjectCount)
	}

	// Test with size value
	cwMock.output = &cloudwatch.GetMetricStatisticsOutput{
		Datapoints: []cwtypes.Datapoint{
			{Timestamp: &now, Average: &avg1024},
		},
	}
	stats, _ = client.GetBucketStats(context.Background(), "my-bucket", "us-west-2")
	if stats.SizeBytes != 1024 {
		t.Errorf("expected 1024 bytes, got %d", stats.SizeBytes)
	}
}

func TestGetPrefixAccessStatus(t *testing.T) {
	policyJSON := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Sid": "s3m-public-installers",
				"Effect": "Allow",
				"Principal": "*",
				"Action": "s3:GetObject",
				"Resource": "arn:aws:s3:::my-bucket/installers/*"
			}
		]
	}`
	mock := &mockS3{
		getBucketPolicyOutput: &s3.GetBucketPolicyOutput{
			Policy: aws.String(policyJSON),
		},
	}
	client := &Client{S3: mock}

	accesses, err := client.GetPrefixAccessStatus(context.Background(), "my-bucket", []string{"installers/", "data/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accesses) != 2 {
		t.Fatalf("expected 2 accesses, got %d", len(accesses))
	}

	var installers, data model.PrefixAccess
	for _, a := range accesses {
		if a.Prefix == "installers/" {
			installers = a
		}
		if a.Prefix == "data/" {
			data = a
		}
	}
	if !installers.IsPublic {
		t.Error("expected installers/ to be public")
	}
	if data.IsPublic {
		t.Error("expected data/ to be private")
	}
}
