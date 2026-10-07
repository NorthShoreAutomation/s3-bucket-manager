package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type statsCloudWatch struct {
	CloudWatchAPI
	outputs map[string]*cloudwatch.GetMetricStatisticsOutput
	err     error
}

func (m *statsCloudWatch) GetMetricStatistics(_ context.Context, p *cloudwatch.GetMetricStatisticsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStatisticsOutput, error) {
	return m.outputs[sdkaws.ToString(p.MetricName)], m.err
}
func TestBucketStatsDistinguishesZeroMissingAndDenied(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	sample := func(v float64, at time.Time) *cloudwatch.GetMetricStatisticsOutput {
		return &cloudwatch.GetMetricStatisticsOutput{Datapoints: []cwtypes.Datapoint{{Timestamp: &at, Average: &v}}}
	}
	for _, tt := range []struct {
		name           string
		count, size    *cloudwatch.GetMetricStatisticsOutput
		err            error
		known, wantErr bool
	}{{"real zero", sample(0, now), sample(0, older), nil, true, false}, {"missing count", nil, sample(5, now), nil, false, false}, {"missing size", sample(5, now), &cloudwatch.GetMetricStatisticsOutput{}, nil, false, false}, {"denied", nil, nil, errors.New("access denied"), false, true}, {"invalid sample", &cloudwatch.GetMetricStatisticsOutput{Datapoints: []cwtypes.Datapoint{{Average: sdkaws.Float64(0)}}}, sample(0, now), nil, false, false}} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{CloudWatch: &statsCloudWatch{outputs: map[string]*cloudwatch.GetMetricStatisticsOutput{"NumberOfObjects": tt.count, "BucketSizeBytes": tt.size}, err: tt.err}}
			stats, err := c.GetBucketStats(context.Background(), "bucket", "region")
			if stats.Known != tt.known || (err != nil) != tt.wantErr {
				t.Fatalf("stats=%+v err=%v", stats, err)
			}
			if tt.name == "real zero" && (!stats.UpdatedAt.Equal(older) || stats.ObjectCount != 0 || stats.SizeBytes != 0) {
				t.Fatalf("zero/date incorrect: %+v", stats)
			}
		})
	}
}
func TestBucketStatsUsesNewestValidSamples(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	out := &cloudwatch.GetMetricStatisticsOutput{Datapoints: []cwtypes.Datapoint{{Timestamp: &old, Average: sdkaws.Float64(3)}, {Timestamp: &now, Average: sdkaws.Float64(9)}, {Average: sdkaws.Float64(99)}}}
	c := &Client{CloudWatch: &statsCloudWatch{outputs: map[string]*cloudwatch.GetMetricStatisticsOutput{"NumberOfObjects": out, "BucketSizeBytes": out}}}
	stats, err := c.GetBucketStats(context.Background(), "b", "")
	if err != nil || !stats.Known || stats.ObjectCount != 9 || !stats.UpdatedAt.Equal(now) {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

type paginatedBucketS3 struct {
	mockS3
	calls  int
	repeat bool
}

func (m *paginatedBucketS3) ListBuckets(_ context.Context, p *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	m.calls++
	if sdkaws.ToString(p.ContinuationToken) == "" {
		return &s3.ListBucketsOutput{Buckets: []s3types.Bucket{{Name: sdkaws.String("one"), BucketRegion: sdkaws.String("region")}}, ContinuationToken: sdkaws.String("next")}, nil
	}
	out := &s3.ListBucketsOutput{Buckets: []s3types.Bucket{{Name: sdkaws.String("two"), BucketRegion: sdkaws.String("region")}}}
	if m.repeat {
		out.ContinuationToken = sdkaws.String("next")
	}
	return out, nil
}
func TestListBucketsPaginatesAndKeepsDeniedMetadataUnknown(t *testing.T) {
	m := &paginatedBucketS3{mockS3: mockS3{getPublicAccessBlockErr: &smithy.GenericAPIError{Code: "AccessDenied"}}}
	c := &Client{S3: m}
	buckets, err := c.ListBuckets(context.Background())
	if err != nil || len(buckets) != 2 || m.calls != 2 {
		t.Fatalf("buckets=%+v calls=%d err=%v", buckets, m.calls, err)
	}
	for _, b := range buckets {
		if b.AccessKnown || b.IsPublic {
			t.Fatalf("denial claimed public: %+v", b)
		}
	}
}
func TestListBucketsRejectsRepeatedPaginationToken(t *testing.T) {
	m := &paginatedBucketS3{repeat: true}
	c := &Client{S3: m}
	_, err := c.ListBuckets(context.Background())
	if err == nil || m.calls > 2 {
		t.Fatalf("pagination did not stop: calls=%d err=%v", m.calls, err)
	}
}

func TestBucketObjectCountDoesNotTurnMissingSamplesIntoZero(t *testing.T) {
	c := &Client{CloudWatch: &statsCloudWatch{}}
	_, err := c.GetBucketObjectCount(context.Background(), "bucket", "")
	if err == nil {
		t.Fatal("missing metrics returned a certain zero")
	}
}
