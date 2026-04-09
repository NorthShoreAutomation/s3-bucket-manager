package aws

import (
	"context"
	"encoding/json"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/dcorbell/s3m/internal/model"
)

const (
	managedTagKey   = "s3m:managed"
	managedTagValue = "true"
	policyName      = "s3m-bucket-access"
)

// ListManagedUsers returns IAM users tagged with s3m:managed=true.
func (c *Client) ListManagedUsers(ctx context.Context) ([]model.User, error) {
	output, err := c.IAM.ListUsers(ctx, &iam.ListUsersInput{})
	if err != nil {
		return nil, fmt.Errorf("could not list users: %w", err)
	}

	var users []model.User
	for _, u := range output.Users {
		if !c.isManaged(ctx, awssdk.ToString(u.UserName)) {
			continue
		}

		keysOutput, _ := c.IAM.ListAccessKeys(ctx, &iam.ListAccessKeysInput{
			UserName: u.UserName,
		})
		keyCount := 0
		if keysOutput != nil {
			keyCount = len(keysOutput.AccessKeyMetadata)
		}

		users = append(users, model.User{
			Name:       awssdk.ToString(u.UserName),
			ARN:        awssdk.ToString(u.Arn),
			CreateDate: awssdk.ToTime(u.CreateDate),
			KeyCount:   keyCount,
		})
	}
	return users, nil
}

// isManaged checks if a user has the s3m:managed=true tag.
func (c *Client) isManaged(ctx context.Context, username string) bool {
	tagsOutput, err := c.IAM.ListUserTags(ctx, &iam.ListUserTagsInput{
		UserName: awssdk.String(username),
	})
	if err != nil {
		return false
	}
	for _, tag := range tagsOutput.Tags {
		if awssdk.ToString(tag.Key) == managedTagKey && awssdk.ToString(tag.Value) == managedTagValue {
			return true
		}
	}
	return false
}

// CreateManagedUser creates an IAM user, tags it, creates an access key, and attaches
// a policy granting access to the specified buckets.
func (c *Client) CreateManagedUser(ctx context.Context, username string, buckets []string) (*model.AccessKey, error) {
	// Create the user
	createOutput, err := c.IAM.CreateUser(ctx, &iam.CreateUserInput{
		UserName: awssdk.String(username),
	})
	if err != nil {
		return nil, fmt.Errorf("could not create user %q: %w", username, err)
	}

	// Tag as managed
	_, err = c.IAM.TagUser(ctx, &iam.TagUserInput{
		UserName: awssdk.String(username),
		Tags: []iamtypes.Tag{
			{Key: awssdk.String(managedTagKey), Value: awssdk.String(managedTagValue)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("could not tag user %q: %w", username, err)
	}

	// Attach bucket access policy
	policyDoc := c.buildBucketPolicy(buckets)
	policyJSON, err := json.Marshal(policyDoc)
	if err != nil {
		return nil, fmt.Errorf("could not build policy: %w", err)
	}
	_, err = c.IAM.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       awssdk.String(username),
		PolicyName:     awssdk.String(policyName),
		PolicyDocument: awssdk.String(string(policyJSON)),
	})
	if err != nil {
		return nil, fmt.Errorf("could not attach policy to user %q: %w", username, err)
	}

	// Create access key
	keyOutput, err := c.IAM.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{
		UserName: awssdk.String(username),
	})
	if err != nil {
		return nil, fmt.Errorf("could not create access key for %q: %w", username, err)
	}

	_ = createOutput // used for the CreateUser call

	return &model.AccessKey{
		AccessKeyID:     awssdk.ToString(keyOutput.AccessKey.AccessKeyId),
		SecretAccessKey: awssdk.ToString(keyOutput.AccessKey.SecretAccessKey),
		UserName:        username,
		CreateDate:      awssdk.ToTime(keyOutput.AccessKey.CreateDate),
	}, nil
}

// buildBucketPolicy builds an IAM policy document granting S3 access to specific buckets.
func (c *Client) buildBucketPolicy(buckets []string) map[string]interface{} {
	bucketARNs := make([]string, 0, len(buckets)*2)
	for _, b := range buckets {
		bucketARNs = append(bucketARNs, fmt.Sprintf("arn:aws:s3:::%s", b))
		bucketARNs = append(bucketARNs, fmt.Sprintf("arn:aws:s3:::%s/*", b))
	}

	return map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{
					"s3:GetObject",
					"s3:PutObject",
					"s3:DeleteObject",
					"s3:ListBucket",
				},
				"Resource": bucketARNs,
			},
		},
	}
}

// DeleteManagedUser removes a user's policies, access keys, and the user itself.
func (c *Client) DeleteManagedUser(ctx context.Context, username string) error {
	// Delete inline policies
	policiesOutput, err := c.IAM.ListUserPolicies(ctx, &iam.ListUserPoliciesInput{
		UserName: awssdk.String(username),
	})
	if err == nil {
		for _, p := range policiesOutput.PolicyNames {
			c.IAM.DeleteUserPolicy(ctx, &iam.DeleteUserPolicyInput{
				UserName:   awssdk.String(username),
				PolicyName: awssdk.String(p),
			})
		}
	}

	// Delete access keys
	keysOutput, err := c.IAM.ListAccessKeys(ctx, &iam.ListAccessKeysInput{
		UserName: awssdk.String(username),
	})
	if err == nil {
		for _, k := range keysOutput.AccessKeyMetadata {
			c.IAM.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
				UserName:    awssdk.String(username),
				AccessKeyId: k.AccessKeyId,
			})
		}
	}

	// Delete the user
	_, err = c.IAM.DeleteUser(ctx, &iam.DeleteUserInput{
		UserName: awssdk.String(username),
	})
	if err != nil {
		return fmt.Errorf("could not delete user %q: %w", username, err)
	}
	return nil
}

// RotateAccessKey creates a new access key for the user.
// Returns the new key. The caller should offer to delete old keys.
func (c *Client) RotateAccessKey(ctx context.Context, username string) (*model.AccessKey, error) {
	keyOutput, err := c.IAM.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{
		UserName: awssdk.String(username),
	})
	if err != nil {
		return nil, fmt.Errorf("could not create new access key for %q: %w", username, err)
	}

	return &model.AccessKey{
		AccessKeyID:     awssdk.ToString(keyOutput.AccessKey.AccessKeyId),
		SecretAccessKey: awssdk.ToString(keyOutput.AccessKey.SecretAccessKey),
		UserName:        username,
		CreateDate:      awssdk.ToTime(keyOutput.AccessKey.CreateDate),
	}, nil
}

// DeleteAccessKey deletes a specific access key.
func (c *Client) DeleteAccessKey(ctx context.Context, username, accessKeyID string) error {
	_, err := c.IAM.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
		UserName:    awssdk.String(username),
		AccessKeyId: awssdk.String(accessKeyID),
	})
	if err != nil {
		return fmt.Errorf("could not delete access key %q: %w", accessKeyID, err)
	}
	return nil
}

// ListAccessKeys returns access key metadata for a user.
func (c *Client) ListAccessKeys(ctx context.Context, username string) ([]model.AccessKey, error) {
	output, err := c.IAM.ListAccessKeys(ctx, &iam.ListAccessKeysInput{
		UserName: awssdk.String(username),
	})
	if err != nil {
		return nil, fmt.Errorf("could not list access keys for %q: %w", username, err)
	}

	keys := make([]model.AccessKey, 0, len(output.AccessKeyMetadata))
	for _, k := range output.AccessKeyMetadata {
		keys = append(keys, model.AccessKey{
			AccessKeyID: awssdk.ToString(k.AccessKeyId),
			UserName:    awssdk.ToString(k.UserName),
			CreateDate:  awssdk.ToTime(k.CreateDate),
		})
	}
	return keys, nil
}
