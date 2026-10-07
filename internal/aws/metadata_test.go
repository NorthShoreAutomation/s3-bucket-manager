package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestPublicAccessStatus(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		out                     *s3.GetPublicAccessBlockOutput
		err                     error
		allowed, known, wantErr bool
	}{{"denied", nil, &smithy.GenericAPIError{Code: "AccessDenied"}, false, false, true}, {"absent", nil, &smithy.GenericAPIError{Code: "NoSuchPublicAccessBlockConfiguration"}, true, true, false}, {"missing", nil, nil, false, false, true}, {"missing configuration", &s3.GetPublicAccessBlockOutput{}, nil, false, false, true}, {"blocked", &s3.GetPublicAccessBlockOutput{PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{BlockPublicAcls: sdkaws.Bool(true), IgnorePublicAcls: sdkaws.Bool(true), BlockPublicPolicy: sdkaws.Bool(true), RestrictPublicBuckets: sdkaws.Bool(true)}}, nil, false, true, false}, {"partial", &s3.GetPublicAccessBlockOutput{PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{BlockPublicPolicy: sdkaws.Bool(true)}}, nil, true, true, false}} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{S3: &mockS3{getPublicAccessBlockOutput: tt.out, getPublicAccessBlockErr: tt.err}}
			allowed, known, err := c.PublicAccessStatus(context.Background(), "bucket", "region")
			if allowed != tt.allowed || known != tt.known || (err != nil) != tt.wantErr {
				t.Fatalf("allowed=%v known=%v err=%v", allowed, known, err)
			}
		})
	}
}
