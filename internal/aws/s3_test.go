package aws

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
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
	putObjectErr               error
	putObjectContentLength     *int64
	listObjectsV2Output        *s3.ListObjectsV2Output
	getBucketPolicyOutput      *s3.GetBucketPolicyOutput
	getBucketPolicyErr         error
	putBucketPolicyErr         error
	putBucketPolicyCalls       int
	deleteBucketPolicyErr      error
	deleteObjectBatches        [][]string
	deleteObjectsOutput        *s3.DeleteObjectsOutput

	// multipart tracking for TestUploadStream
	createMultipartCalled   atomic.Int32
	uploadPartCalled        atomic.Int32
	completeMultipartBucket string
	completeMultipartKey    string
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

func (m *mockS3) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, nil
}

func (m *mockS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.putObjectContentLength = params.ContentLength
	return &s3.PutObjectOutput{}, m.putObjectErr
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
	m.putBucketPolicyCalls++
	return nil, m.putBucketPolicyErr
}

func (m *mockS3) DeleteBucketPolicy(ctx context.Context, params *s3.DeleteBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketPolicyOutput, error) {
	return nil, m.deleteBucketPolicyErr
}

func (m *mockS3) DeleteObjects(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	keys := make([]string, 0, len(input.Delete.Objects))
	for _, object := range input.Delete.Objects {
		keys = append(keys, aws.ToString(object.Key))
	}
	m.deleteObjectBatches = append(m.deleteObjectBatches, keys)
	if m.deleteObjectsOutput != nil {
		return m.deleteObjectsOutput, nil
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func (m *mockS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{}, nil
}

func (m *mockS3) CreateMultipartUpload(ctx context.Context, params *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	m.createMultipartCalled.Add(1)
	return &s3.CreateMultipartUploadOutput{
		Bucket:   params.Bucket,
		Key:      params.Key,
		UploadId: aws.String("test-upload-id"),
	}, nil
}

func (m *mockS3) UploadPart(ctx context.Context, params *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	m.uploadPartCalled.Add(1)
	return &s3.UploadPartOutput{ETag: aws.String("etag")}, nil
}

func (m *mockS3) CompleteMultipartUpload(ctx context.Context, params *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	if params.Bucket != nil {
		m.completeMultipartBucket = *params.Bucket
	}
	if params.Key != nil {
		m.completeMultipartKey = *params.Key
	}
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (m *mockS3) AbortMultipartUpload(ctx context.Context, params *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	return &s3.AbortMultipartUploadOutput{}, nil
}

func TestSetPrefixPrivateDoesNotRewritePolicyWithoutMatchingStatement(t *testing.T) {
	mock := &mockS3{getBucketPolicyOutput: &s3.GetBucketPolicyOutput{Policy: aws.String(`{
		"Version":"2012-10-17",
		"Statement":[{
			"Sid":"unrelated",
			"Effect":"Allow",
			"Principal":"*",
			"Action":"s3:GetObject",
			"Resource":"arn:aws:s3:::bucket-a/other/*"
		}]
	}`)}}
	client := &Client{S3: mock}

	if err := client.SetPrefixPrivate(context.Background(), "bucket-a", "private-folder/", "us-west-2"); err != nil {
		t.Fatalf("expected already-private prefix cleanup to succeed, got %v", err)
	}
	if mock.putBucketPolicyCalls != 0 {
		t.Fatalf("expected unrelated bucket policy not to be rewritten, got %d writes", mock.putBucketPolicyCalls)
	}
}

func TestSetPrefixesPrivatePropagatesPolicyReadFailure(t *testing.T) {
	mock := &mockS3{getBucketPolicyErr: fmt.Errorf("access denied")}
	client := &Client{S3: mock}

	err := client.SetPrefixesPrivate(context.Background(), "bucket-a", []string{"folder/"}, "us-west-2")
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected bucket-policy read failure to propagate, got %v", err)
	}
}

func TestListContentsOmitsObjectThatDuplicatesFolderPrefix(t *testing.T) {
	mock := &mockS3{listObjectsV2Output: &s3.ListObjectsV2Output{
		CommonPrefixes: []s3types.CommonPrefix{{Prefix: aws.String("parent/folder/")}},
		Contents: []s3types.Object{
			{Key: aws.String("parent/folder/"), Size: aws.Int64(0)},
			{Key: aws.String("parent/file.txt"), Size: aws.Int64(10)},
		},
	}}
	client := &Client{S3: mock}

	items, err := client.ListContents(context.Background(), "bucket-a", "parent/", "us-west-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].IsFolder || items[0].Key != "parent/folder/" || items[1].Key != "parent/file.txt" {
		t.Fatalf("expected one folder and one file without duplicate marker, got %#v", items)
	}
}

func TestDeleteObjectKeysBatchesAtS3Limit(t *testing.T) {
	mock := &mockS3{}
	client := &Client{S3: mock}
	keys := make([]string, 1001)
	for i := range keys {
		keys[i] = fmt.Sprintf("file-%d", i)
	}

	deleted, err := client.DeleteObjectKeys(context.Background(), "bucket-a", keys, "us-west-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1001 || len(mock.deleteObjectBatches) != 2 || len(mock.deleteObjectBatches[0]) != 1000 || len(mock.deleteObjectBatches[1]) != 1 {
		t.Fatalf("expected 1000/1 batched deletion, deleted=%d batches=%#v", deleted, mock.deleteObjectBatches)
	}
}

func TestDeletePrefixReturnsPerObjectDeleteErrors(t *testing.T) {
	mock := &mockS3{
		listObjectsV2Output: &s3.ListObjectsV2Output{Contents: []s3types.Object{{Key: aws.String("folder/blocked.txt")}}},
		deleteObjectsOutput: &s3.DeleteObjectsOutput{Errors: []s3types.Error{{Key: aws.String("folder/blocked.txt"), Message: aws.String("access denied")}}},
	}
	client := &Client{S3: mock}

	err := client.DeletePrefix(context.Background(), "bucket-a", "folder/", "us-west-2", nil)
	if err == nil || !strings.Contains(err.Error(), "blocked.txt") {
		t.Fatalf("expected per-object delete error, got %v", err)
	}
}

func TestUploadStream(t *testing.T) {
	const (
		bucket   = "test-bucket"
		key      = "uploads/big-file.bin"
		region   = "us-west-2"
		partSize = 5 * 1024 * 1024 // 5 MiB
		bodySize = 6 * 1024 * 1024 // 6 MiB — forces at least two parts
	)

	mock := &mockS3{}
	client := &Client{S3: mock, Region: "us-east-1"}

	body := bytes.NewReader(make([]byte, bodySize))
	err := client.UploadStream(context.Background(), bucket, key, region, body, partSize, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.createMultipartCalled.Load() != 1 {
		t.Errorf("expected CreateMultipartUpload to be called once, got %d", mock.createMultipartCalled.Load())
	}
	if mock.uploadPartCalled.Load() < 2 {
		t.Errorf("expected at least 2 UploadPart calls for 6 MiB body with 5 MiB part size, got %d", mock.uploadPartCalled.Load())
	}
	if mock.completeMultipartBucket != bucket {
		t.Errorf("expected CompleteMultipartUpload bucket %q, got %q", bucket, mock.completeMultipartBucket)
	}
	if mock.completeMultipartKey != key {
		t.Errorf("expected CompleteMultipartUpload key %q, got %q", key, mock.completeMultipartKey)
	}
}

func TestUploadObjectSizedSetsContentLength(t *testing.T) {
	const size int64 = 1234
	mock := &mockS3{}
	client := &Client{S3: mock, Region: "us-east-1"}

	if err := client.UploadObjectSized(context.Background(), "test-bucket", "file.txt", "us-west-2", bytes.NewReader(make([]byte, size)), size); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.putObjectContentLength == nil {
		t.Fatal("expected ContentLength to be set")
	}
	if got := *mock.putObjectContentLength; got != size {
		t.Fatalf("ContentLength = %d, want %d", got, size)
	}
}

func TestListBuckets(t *testing.T) {
	now := time.Now()
	mock := &mockS3{
		listBucketsOutput: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{
				{Name: aws.String("my-bucket"), CreationDate: &now, BucketRegion: aws.String("us-west-2")},
				{Name: aws.String("other-bucket"), CreationDate: &now, BucketRegion: aws.String("us-west-2")},
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

func TestListBucketsFallsBackWhenBucketRegionEmpty(t *testing.T) {
	// Older buckets / older AWS API responses may omit BucketRegion. The client
	// must fall back to HeadBucket/GetBucketLocation so the user still gets a
	// correct region without supplying --region manually.
	now := time.Now()
	mock := &mockS3{
		listBucketsOutput: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{
				// BucketRegion deliberately nil to simulate an empty response field.
				{Name: aws.String("legacy-bucket"), CreationDate: &now},
			},
		},
		// manager.GetBucketRegion will return "" because the stub HeadBucket
		// skips SDK middleware; Client.GetBucketRegion then falls through to
		// GetBucketLocation, which the mock supplies with us-east-2.
		getBucketLocationOutput: &s3.GetBucketLocationOutput{
			LocationConstraint: s3types.BucketLocationConstraintUsEast2,
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
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].Region != "us-east-2" {
		t.Errorf("expected region 'us-east-2' from fallback, got %q", buckets[0].Region)
	}
}

func TestGetBucketRegionFallsBackToGetBucketLocation(t *testing.T) {
	mock := &mockS3{
		getBucketLocationOutput: &s3.GetBucketLocationOutput{
			LocationConstraint: s3types.BucketLocationConstraintEuCentral1,
		},
	}
	client := &Client{S3: mock, Region: "us-east-1"}

	region, err := client.GetBucketRegion(context.Background(), "some-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "eu-central-1" {
		t.Errorf("expected 'eu-central-1', got %q", region)
	}
}

func TestGetBucketRegionUsEast1EmptyLocationConstraint(t *testing.T) {
	// us-east-1 buckets return an empty LocationConstraint; must normalize.
	mock := &mockS3{
		getBucketLocationOutput: &s3.GetBucketLocationOutput{},
	}
	client := &Client{S3: mock, Region: "us-east-1"}

	region, err := client.GetBucketRegion(context.Background(), "some-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "us-east-1" {
		t.Errorf("expected 'us-east-1', got %q", region)
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

	accesses, err := client.GetPrefixAccessStatus(context.Background(), "my-bucket", "us-west-2", []string{"installers/", "data/"})
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
