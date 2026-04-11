package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/aws/smithy-go"

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
func (c *Client) CreateManagedUser(ctx context.Context, username string, accesses []model.BucketAccess) (*model.AccessKey, error) {
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
	policyDoc := buildBucketPolicyWithPermissions(accesses)
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

// actionsForPermission maps a permission level to the corresponding S3 IAM action strings.
func actionsForPermission(level model.PermissionLevel) []string {
	switch level {
	case model.PermRead:
		return []string{"s3:GetObject", "s3:ListBucket"}
	case model.PermReadWrite:
		return []string{
			"s3:GetObject",
			"s3:PutObject",
			"s3:ListBucket",
			"s3:AbortMultipartUpload",
			"s3:ListMultipartUploadParts",
			"s3:ListBucketMultipartUploads",
		}
	case model.PermReadWriteDelete:
		return []string{
			"s3:GetObject",
			"s3:PutObject",
			"s3:ListBucket",
			"s3:AbortMultipartUpload",
			"s3:ListMultipartUploadParts",
			"s3:ListBucketMultipartUploads",
			"s3:DeleteObject",
		}
	default:
		return []string{"s3:GetObject", "s3:ListBucket"}
	}
}

// permissionFromActions determines the permission level from a list of IAM actions.
func permissionFromActions(actions []string) model.PermissionLevel {
	actionSet := make(map[string]bool, len(actions))
	for _, a := range actions {
		actionSet[a] = true
	}
	if actionSet["s3:DeleteObject"] {
		return model.PermReadWriteDelete
	}
	if actionSet["s3:PutObject"] {
		return model.PermReadWrite
	}
	return model.PermRead
}

// buildBucketPolicyWithPermissions builds an IAM policy document with one statement per bucket,
// each scoped to the permission level specified in the access list.
func buildBucketPolicyWithPermissions(accesses []model.BucketAccess) map[string]interface{} {
	statements := make([]map[string]interface{}, 0, len(accesses))
	for i, a := range accesses {
		statements = append(statements, map[string]interface{}{
			"Sid":    fmt.Sprintf("s3m%d", i),
			"Effect": "Allow",
			"Action": actionsForPermission(a.Permission),
			"Resource": []string{
				fmt.Sprintf("arn:aws:s3:::%s", a.Bucket),
				fmt.Sprintf("arn:aws:s3:::%s/*", a.Bucket),
			},
		})
	}
	return map[string]interface{}{
		"Version":   "2012-10-17",
		"Statement": statements,
	}
}

// GetUserBucketAccess retrieves the bucket access list for a user by parsing their
// s3m-bucket-access inline policy.
func (c *Client) GetUserBucketAccess(ctx context.Context, username string) ([]model.BucketAccess, error) {
	output, err := c.IAM.GetUserPolicy(ctx, &iam.GetUserPolicyInput{
		UserName:   awssdk.String(username),
		PolicyName: awssdk.String(policyName),
	})
	if err != nil {
		// NoSuchEntity means no policy attached — return empty slice, not an error
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchEntity" {
			return nil, nil
		}
		return nil, fmt.Errorf("could not get policy for user %q: %w", username, err)
	}

	docStr, err := url.QueryUnescape(awssdk.ToString(output.PolicyDocument))
	if err != nil {
		return nil, fmt.Errorf("could not decode policy document: %w", err)
	}

	var doc struct {
		Statement []struct {
			Sid      string      `json:"Sid"`
			Action   interface{} `json:"Action"`
			Resource interface{} `json:"Resource"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(docStr), &doc); err != nil {
		return nil, fmt.Errorf("could not parse policy document: %w", err)
	}

	var accesses []model.BucketAccess
	for _, stmt := range doc.Statement {
		actions := toStringSlice(stmt.Action)
		resources := toStringSlice(stmt.Resource)
		perm := permissionFromActions(actions)

		// Extract bucket names from Resource ARNs.
		// Each statement covers one bucket (new format) or multiple (legacy).
		// Legacy statements (no s3m Sid prefix) are classified as read-write-delete.
		isLegacy := !strings.HasPrefix(stmt.Sid, "s3m")

		seen := make(map[string]bool)
		for _, r := range resources {
			name := strings.TrimPrefix(r, "arn:aws:s3:::")
			name = strings.TrimSuffix(name, "/*")
			if name != "" && !seen[name] {
				seen[name] = true
				p := perm
				if isLegacy {
					p = model.PermReadWriteDelete
				}
				accesses = append(accesses, model.BucketAccess{
					Bucket:     name,
					Permission: p,
				})
			}
		}
	}
	return accesses, nil
}

// toStringSlice normalises a JSON value that may be a single string or []string.
func toStringSlice(v interface{}) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []interface{}:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// SetUserBucketAccess replaces a user's bucket access policy. If accesses is empty,
// the policy is deleted entirely.
func (c *Client) SetUserBucketAccess(ctx context.Context, username string, accesses []model.BucketAccess) error {
	if len(accesses) == 0 {
		_, err := c.IAM.DeleteUserPolicy(ctx, &iam.DeleteUserPolicyInput{
			UserName:   awssdk.String(username),
			PolicyName: awssdk.String(policyName),
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchEntity" {
				return nil
			}
			return fmt.Errorf("could not delete policy for user %q: %w", username, err)
		}
		return nil
	}

	policyDoc := buildBucketPolicyWithPermissions(accesses)
	policyJSON, err := json.Marshal(policyDoc)
	if err != nil {
		return fmt.Errorf("could not build policy: %w", err)
	}
	_, err = c.IAM.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       awssdk.String(username),
		PolicyName:     awssdk.String(policyName),
		PolicyDocument: awssdk.String(string(policyJSON)),
	})
	if err != nil {
		return fmt.Errorf("could not set policy for user %q: %w", username, err)
	}
	return nil
}

// ListBucketUsers returns all managed users that have access to the given bucket,
// along with their permission level.
func (c *Client) ListBucketUsers(ctx context.Context, bucketName string) ([]model.UserPermission, error) {
	users, err := c.ListManagedUsers(ctx)
	if err != nil {
		return nil, err
	}

	type result struct {
		perm  model.UserPermission
		found bool
	}

	results := make([]result, len(users))
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for i, u := range users {
		wg.Add(1)
		go func(idx int, username string) {
			defer wg.Done()
			accesses, err := c.GetUserBucketAccess(ctx, username)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", username, err))
				mu.Unlock()
				return
			}
			for _, a := range accesses {
				if a.Bucket == bucketName {
					results[idx] = result{
						perm: model.UserPermission{
							Username:   username,
							Permission: a.Permission,
						},
						found: true,
					}
					return
				}
			}
		}(i, u.Name)
	}
	wg.Wait()

	var perms []model.UserPermission
	for _, r := range results {
		if r.found {
			perms = append(perms, r.perm)
		}
	}

	if len(errs) > 0 {
		return perms, fmt.Errorf("failed to fetch access for %d user(s)", len(errs))
	}
	return perms, nil
}

// DeleteManagedUser removes a user's policies, access keys, and the user itself.
func (c *Client) DeleteManagedUser(ctx context.Context, username string) error {
	// Delete inline policies
	policiesOutput, err := c.IAM.ListUserPolicies(ctx, &iam.ListUserPoliciesInput{
		UserName: awssdk.String(username),
	})
	if err == nil {
		for _, p := range policiesOutput.PolicyNames {
			c.IAM.DeleteUserPolicy(ctx, &iam.DeleteUserPolicyInput{ //nolint:errcheck // best-effort cleanup during user deletion
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
			c.IAM.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{ //nolint:errcheck // best-effort cleanup during user deletion
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
