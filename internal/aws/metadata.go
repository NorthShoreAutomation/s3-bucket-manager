package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// PublicAccessStatus reports bucket-level block settings, not object reachability.
// allowed means the four public-access protections are not all enabled.
// Errors remain unknown; only an explicit missing configuration counts as known.
func (c *Client) PublicAccessStatus(ctx context.Context, bucket, region string) (allowed, known bool, err error) {
	out, err := c.S3.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: sdkaws.String(bucket)}, withBucketRegion(region))
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NoSuchPublicAccessBlockConfiguration" {
			return true, true, nil
		}
		return false, false, fmt.Errorf("cannot read public settings for %q: %w", bucket, err)
	}
	if out == nil || out.PublicAccessBlockConfiguration == nil {
		return false, false, fmt.Errorf("public settings response for %q was empty", bucket)
	}
	p := out.PublicAccessBlockConfiguration
	allBlocked := sdkaws.ToBool(p.BlockPublicAcls) && sdkaws.ToBool(p.IgnorePublicAcls) && sdkaws.ToBool(p.BlockPublicPolicy) && sdkaws.ToBool(p.RestrictPublicBuckets)
	return !allBlocked, true, nil
}
