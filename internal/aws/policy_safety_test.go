package aws

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type policySafeS3 struct {
	mockS3
	blocks, puts, deletes int
	policy                string
	blockErr              error
}

func (m *policySafeS3) PutPublicAccessBlock(context.Context, *s3.PutPublicAccessBlockInput, ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	m.blocks++
	return &s3.PutPublicAccessBlockOutput{}, m.blockErr
}
func (m *policySafeS3) PutBucketPolicy(_ context.Context, p *s3.PutBucketPolicyInput, _ ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error) {
	m.puts++
	m.policy = sdkaws.ToString(p.Policy)
	return &s3.PutBucketPolicyOutput{}, m.putBucketPolicyErr
}
func (m *policySafeS3) DeleteBucketPolicy(context.Context, *s3.DeleteBucketPolicyInput, ...func(*s3.Options)) (*s3.DeleteBucketPolicyOutput, error) {
	m.deletes++
	return &s3.DeleteBucketPolicyOutput{}, m.deleteBucketPolicyErr
}
func TestAccessPolicyUnreadableMakesNoMutation(t *testing.T) {
	for _, kind := range []string{"denied", "malformed", "missing response"} {
		for _, makePublic := range []bool{true, false} {
			t.Run(kind+map[bool]string{true: " public", false: " private"}[makePublic], func(t *testing.T) {
				m := &policySafeS3{}
				switch kind {
				case "denied":
					m.getBucketPolicyErr = &smithy.GenericAPIError{Code: "AccessDenied"}
				case "malformed":
					m.getBucketPolicyOutput = &s3.GetBucketPolicyOutput{Policy: sdkaws.String(`{"Statement":broken}`)}
				}
				c := &Client{S3: m}
				var err error
				if makePublic {
					err = c.SetPrefixPublic(context.Background(), "bucket", "folder/", "")
				} else {
					err = c.SetPrefixPrivate(context.Background(), "bucket", "folder/", "")
				}
				if err == nil || m.blocks+m.puts+m.deletes != 0 {
					t.Fatalf("err=%v writes=%d", err, m.blocks+m.puts+m.deletes)
				}
			})
		}
	}
}

const unrelatedPolicy = `{"Version":"2012-10-17","Id":"retain-document-id","Statement":[{"Sid":"unrelated","Effect":"Allow","Principal":{"AWS":["arn:aws:iam::123456789012:root"]},"Action":["s3:GetObject","s3:PutObject"],"Resource":["arn:aws:s3:::bucket/other/*"],"Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-example"}}}]}`

func TestPublicPolicyPreservesUnrelatedJSON(t *testing.T) {
	m := &policySafeS3{mockS3: mockS3{getBucketPolicyOutput: &s3.GetBucketPolicyOutput{Policy: sdkaws.String(unrelatedPolicy)}}}
	c := &Client{S3: m}
	if err := c.SetPrefixPublic(context.Background(), "bucket", "folder/", ""); err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = json.Unmarshal([]byte(unrelatedPolicy), &before)
	_ = json.Unmarshal([]byte(m.policy), &after)
	if after["Id"] != before["Id"] || !reflect.DeepEqual(before["Statement"].([]any)[0], after["Statement"].([]any)[0]) {
		t.Fatalf("unrelated policy changed: %s", m.policy)
	}
}
func TestPrivatePolicyPreservesUnrelatedJSON(t *testing.T) {
	var doc map[string]any
	_ = json.Unmarshal([]byte(unrelatedPolicy), &doc)
	doc["Statement"] = append(doc["Statement"].([]any), map[string]any{"Sid": "s3m-public-folder", "Effect": "Allow", "Principal": "*", "Action": "s3:GetObject", "Resource": "arn:aws:s3:::bucket/folder/*"})
	data, _ := json.Marshal(doc)
	m := &policySafeS3{mockS3: mockS3{getBucketPolicyOutput: &s3.GetBucketPolicyOutput{Policy: sdkaws.String(string(data))}}}
	c := &Client{S3: m}
	if err := c.SetPrefixPrivate(context.Background(), "bucket", "folder/", ""); err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	_ = json.Unmarshal([]byte(m.policy), &after)
	var before map[string]any
	_ = json.Unmarshal([]byte(unrelatedPolicy), &before)
	if !reflect.DeepEqual(before, after) || m.blocks != 0 || m.deletes != 0 {
		t.Fatalf("unrelated policy changed: %s", m.policy)
	}
}
func TestAccessPolicyMutationFailuresAreReported(t *testing.T) {
	only := `{"Version":"2012-10-17","Statement":[{"Sid":"s3m-public-folder","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket/folder/*"}]}`
	for _, kind := range []string{"public policy fails", "delete fails", "block fails"} {
		t.Run(kind, func(t *testing.T) {
			m := &policySafeS3{mockS3: mockS3{getBucketPolicyOutput: &s3.GetBucketPolicyOutput{Policy: sdkaws.String(only)}}}
			switch kind {
			case "public policy fails":
				m.putBucketPolicyErr = errors.New("denied")
			case "delete fails":
				m.deleteBucketPolicyErr = errors.New("denied")
			case "block fails":
				m.blockErr = errors.New("denied")
			}
			c := &Client{S3: m}
			var err error
			if kind == "public policy fails" {
				err = c.SetPrefixPublic(context.Background(), "bucket", "new/", "")
			} else {
				err = c.SetPrefixPrivate(context.Background(), "bucket", "folder/", "")
			}
			if err == nil {
				t.Fatal("mutation failure reported success")
			}
			if kind == "public policy fails" && (m.blocks != 1 || !strings.Contains(err.Error(), "changed")) {
				t.Fatalf("partial change not reported: %v", err)
			}
			if kind == "delete fails" && m.blocks != 0 {
				t.Fatal("continued after failed delete")
			}
			if kind == "block fails" && !strings.Contains(err.Error(), "removed") {
				t.Fatalf("partial deletion not reported: %v", err)
			}
		})
	}
}
func TestPrefixAccessStatusUnreadableIsUnknown(t *testing.T) {
	c := &Client{S3: &policySafeS3{mockS3: mockS3{getBucketPolicyErr: &smithy.GenericAPIError{Code: "AccessDenied"}}}}
	access, err := c.GetPrefixAccessStatus(context.Background(), "bucket", "", []string{"folder/"})
	if err == nil || len(access) != 0 {
		t.Fatal("denial became private")
	}
}

func TestOnlyExplicitMissingPolicyPermitsNewGrant(t *testing.T) {
	m := &policySafeS3{mockS3: mockS3{getBucketPolicyErr: &smithy.GenericAPIError{Code: "NoSuchBucketPolicy"}}}
	c := &Client{S3: m}
	if err := c.SetPrefixPublic(context.Background(), "bucket", "folder/", ""); err != nil || m.blocks != 1 || m.puts != 1 {
		t.Fatalf("explicit missing policy: %v", err)
	}
}
func TestPrefixStatusMatchesManagedArraysAndIgnoresUnrelatedGrant(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"s3m-public-folder","Effect":"Allow","Principal":{"AWS":"*"},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket/folder/*"]},{"Sid":"unrelated","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket/other/*"}]}`
	m := &policySafeS3{mockS3: mockS3{getBucketPolicyOutput: &s3.GetBucketPolicyOutput{Policy: sdkaws.String(policy)}}}
	c := &Client{S3: m}
	access, err := c.GetPrefixAccessStatus(context.Background(), "bucket", "", []string{"folder/", "other/"})
	if err != nil || len(access) != 2 || !access[0].IsPublic || access[1].IsPublic {
		t.Fatalf("managed status: %+v / %v", access, err)
	}
}
func TestSingleStatementPolicyIsRetained(t *testing.T) {
	policy := `{"Version":"2012-10-17","Statement":{"Sid":"unrelated","Effect":"Deny","Principal":"*","Action":"s3:DeleteObject","Resource":"arn:aws:s3:::bucket/*","Condition":{"StringNotEquals":{"aws:PrincipalOrgID":"o-example"}}}}`
	m := &policySafeS3{mockS3: mockS3{getBucketPolicyOutput: &s3.GetBucketPolicyOutput{Policy: sdkaws.String(policy)}}}
	c := &Client{S3: m}
	if err := c.SetPrefixPublic(context.Background(), "bucket", "folder/", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.policy, "StringNotEquals") || !strings.Contains(m.policy, "unrelated") {
		t.Fatalf("single statement lost: %s", m.policy)
	}
}
