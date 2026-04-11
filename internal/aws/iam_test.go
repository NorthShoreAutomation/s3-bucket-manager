package aws

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/aws/smithy-go"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/dcorbell/s3m/internal/model"
)

type mockIAM struct {
	IAMAPI
	createUserOutput       *iam.CreateUserOutput
	createUserErr          error
	deleteUserErr          error
	listUsersOutput        *iam.ListUsersOutput
	tagUserErr             error
	getUserOutput          *iam.GetUserOutput
	createAccessKeyOutput  *iam.CreateAccessKeyOutput
	deleteAccessKeyErr     error
	listAccessKeysOutput   *iam.ListAccessKeysOutput
	putUserPolicyErr       error
	putUserPolicyInput     *iam.PutUserPolicyInput
	deleteUserPolicyErr    error
	deleteUserPolicyCalled bool
	listUserPoliciesOutput *iam.ListUserPoliciesOutput
	listUserTagsOutput     *iam.ListUserTagsOutput
	getUserPolicyOutput    *iam.GetUserPolicyOutput
	getUserPolicyErr       error
}

func (m *mockIAM) CreateUser(ctx context.Context, params *iam.CreateUserInput, optFns ...func(*iam.Options)) (*iam.CreateUserOutput, error) {
	return m.createUserOutput, m.createUserErr
}

func (m *mockIAM) DeleteUser(ctx context.Context, params *iam.DeleteUserInput, optFns ...func(*iam.Options)) (*iam.DeleteUserOutput, error) {
	return nil, m.deleteUserErr
}

func (m *mockIAM) ListUsers(ctx context.Context, params *iam.ListUsersInput, optFns ...func(*iam.Options)) (*iam.ListUsersOutput, error) {
	return m.listUsersOutput, nil
}

func (m *mockIAM) TagUser(ctx context.Context, params *iam.TagUserInput, optFns ...func(*iam.Options)) (*iam.TagUserOutput, error) {
	return nil, m.tagUserErr
}

func (m *mockIAM) GetUser(ctx context.Context, params *iam.GetUserInput, optFns ...func(*iam.Options)) (*iam.GetUserOutput, error) {
	return m.getUserOutput, nil
}

func (m *mockIAM) CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	return m.createAccessKeyOutput, nil
}

func (m *mockIAM) DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	return nil, m.deleteAccessKeyErr
}

func (m *mockIAM) ListAccessKeys(ctx context.Context, params *iam.ListAccessKeysInput, optFns ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	return m.listAccessKeysOutput, nil
}

func (m *mockIAM) PutUserPolicy(ctx context.Context, params *iam.PutUserPolicyInput, optFns ...func(*iam.Options)) (*iam.PutUserPolicyOutput, error) {
	m.putUserPolicyInput = params
	return nil, m.putUserPolicyErr
}

func (m *mockIAM) DeleteUserPolicy(ctx context.Context, params *iam.DeleteUserPolicyInput, optFns ...func(*iam.Options)) (*iam.DeleteUserPolicyOutput, error) {
	m.deleteUserPolicyCalled = true
	return nil, m.deleteUserPolicyErr
}

func (m *mockIAM) GetUserPolicy(ctx context.Context, params *iam.GetUserPolicyInput, optFns ...func(*iam.Options)) (*iam.GetUserPolicyOutput, error) {
	return m.getUserPolicyOutput, m.getUserPolicyErr
}

func (m *mockIAM) ListUserPolicies(ctx context.Context, params *iam.ListUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListUserPoliciesOutput, error) {
	return m.listUserPoliciesOutput, nil
}

func (m *mockIAM) ListUserTags(ctx context.Context, params *iam.ListUserTagsInput, optFns ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	// Only return the managed tag for users whose name starts with "s3m-"
	if params.UserName != nil && len(*params.UserName) >= 4 && (*params.UserName)[:4] == "s3m-" {
		return m.listUserTagsOutput, nil
	}
	return &iam.ListUserTagsOutput{}, nil
}

func TestListManagedUsers(t *testing.T) {
	now := time.Now()
	mock := &mockIAM{
		listUsersOutput: &iam.ListUsersOutput{
			Users: []iamtypes.User{
				{UserName: awssdk.String("s3m-alice"), Arn: awssdk.String("arn:aws:iam::123:user/s3m-alice"), CreateDate: &now},
				{UserName: awssdk.String("unmanaged-bob"), Arn: awssdk.String("arn:aws:iam::123:user/unmanaged-bob"), CreateDate: &now},
			},
		},
		listUserTagsOutput: &iam.ListUserTagsOutput{
			Tags: []iamtypes.Tag{
				{Key: awssdk.String("s3m:managed"), Value: awssdk.String("true")},
			},
		},
		listAccessKeysOutput: &iam.ListAccessKeysOutput{
			AccessKeyMetadata: []iamtypes.AccessKeyMetadata{
				{AccessKeyId: awssdk.String("AKIA123")},
			},
		},
	}
	client := &Client{IAM: mock}

	users, err := client.ListManagedUsers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the tagged user should be returned
	if len(users) != 1 {
		t.Fatalf("expected 1 managed user, got %d", len(users))
	}
	if users[0].Name != "s3m-alice" {
		t.Errorf("expected 's3m-alice', got %q", users[0].Name)
	}
	if users[0].KeyCount != 1 {
		t.Errorf("expected 1 key, got %d", users[0].KeyCount)
	}
}

func TestCreateManagedUser(t *testing.T) {
	now := time.Now()
	mock := &mockIAM{
		createUserOutput: &iam.CreateUserOutput{
			User: &iamtypes.User{
				UserName:   awssdk.String("s3m-newuser"),
				Arn:        awssdk.String("arn:aws:iam::123:user/s3m-newuser"),
				CreateDate: &now,
			},
		},
		createAccessKeyOutput: &iam.CreateAccessKeyOutput{
			AccessKey: &iamtypes.AccessKey{
				AccessKeyId:     awssdk.String("AKIA456"),
				SecretAccessKey: awssdk.String("secret123"),
				UserName:        awssdk.String("s3m-newuser"),
				CreateDate:      &now,
			},
		},
	}
	client := &Client{IAM: mock, Account: "123456789012"}

	accesses := []model.BucketAccess{
		{Bucket: "my-bucket", Permission: model.PermReadWrite},
	}
	key, err := client.CreateManagedUser(context.Background(), "s3m-newuser", accesses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.AccessKeyID != "AKIA456" {
		t.Errorf("expected 'AKIA456', got %q", key.AccessKeyID)
	}
	if key.SecretAccessKey != "secret123" {
		t.Errorf("expected 'secret123', got %q", key.SecretAccessKey)
	}
}

func TestActionsForPermission(t *testing.T) {
	tests := []struct {
		level    model.PermissionLevel
		expected []string
	}{
		{model.PermRead, []string{"s3:GetObject", "s3:ListBucket"}},
		{model.PermReadWrite, []string{
			"s3:GetObject", "s3:PutObject", "s3:ListBucket",
			"s3:AbortMultipartUpload", "s3:ListMultipartUploadParts", "s3:ListBucketMultipartUploads",
		}},
		{model.PermReadWriteDelete, []string{
			"s3:GetObject", "s3:PutObject", "s3:ListBucket",
			"s3:AbortMultipartUpload", "s3:ListMultipartUploadParts", "s3:ListBucketMultipartUploads",
			"s3:DeleteObject",
		}},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			actions := actionsForPermission(tt.level)
			if len(actions) != len(tt.expected) {
				t.Fatalf("expected %d actions, got %d: %v", len(tt.expected), len(actions), actions)
			}
			for i, a := range actions {
				if a != tt.expected[i] {
					t.Errorf("action[%d]: expected %q, got %q", i, tt.expected[i], a)
				}
			}
		})
	}
}

func TestPermissionFromActions(t *testing.T) {
	tests := []struct {
		name     string
		actions  []string
		expected model.PermissionLevel
	}{
		{"read only", []string{"s3:GetObject", "s3:ListBucket"}, model.PermRead},
		{"read-write", []string{"s3:GetObject", "s3:PutObject", "s3:ListBucket"}, model.PermReadWrite},
		{"read-write-delete", []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"}, model.PermReadWriteDelete},
		{"just delete implies rwd", []string{"s3:DeleteObject"}, model.PermReadWriteDelete},
		{"just put implies rw", []string{"s3:PutObject"}, model.PermReadWrite},
		{"empty actions", []string{}, model.PermRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := permissionFromActions(tt.actions)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}


func TestGetUserBucketAccess(t *testing.T) {
	// Build a policy with two statements (new format)
	policy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Sid":    "s3m-bucket-a",
				"Effect": "Allow",
				"Action": []string{"s3:GetObject", "s3:ListBucket"},
				"Resource": []string{
					"arn:aws:s3:::bucket-a",
					"arn:aws:s3:::bucket-a/*",
				},
			},
			{
				"Sid":    "s3m-bucket-b",
				"Effect": "Allow",
				"Action": []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"},
				"Resource": []string{
					"arn:aws:s3:::bucket-b",
					"arn:aws:s3:::bucket-b/*",
				},
			},
		},
	}
	policyJSON, _ := json.Marshal(policy)
	encoded := url.QueryEscape(string(policyJSON))

	mock := &mockIAM{
		getUserPolicyOutput: &iam.GetUserPolicyOutput{
			PolicyDocument: awssdk.String(encoded),
		},
	}
	client := &Client{IAM: mock}

	accesses, err := client.GetUserBucketAccess(context.Background(), "s3m-testuser")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accesses) != 2 {
		t.Fatalf("expected 2 accesses, got %d", len(accesses))
	}
	if accesses[0].Bucket != "bucket-a" || accesses[0].Permission != model.PermRead {
		t.Errorf("access[0]: expected bucket-a/read, got %s/%s", accesses[0].Bucket, accesses[0].Permission)
	}
	if accesses[1].Bucket != "bucket-b" || accesses[1].Permission != model.PermReadWriteDelete {
		t.Errorf("access[1]: expected bucket-b/read-write-delete, got %s/%s", accesses[1].Bucket, accesses[1].Permission)
	}
}

func TestGetUserBucketAccessLegacy(t *testing.T) {
	// Legacy format: single statement, no s3m- prefix Sid, multiple bucket ARNs
	policy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"},
				"Resource": []string{
					"arn:aws:s3:::legacy-bucket-1",
					"arn:aws:s3:::legacy-bucket-1/*",
					"arn:aws:s3:::legacy-bucket-2",
					"arn:aws:s3:::legacy-bucket-2/*",
				},
			},
		},
	}
	policyJSON, _ := json.Marshal(policy)
	encoded := url.QueryEscape(string(policyJSON))

	mock := &mockIAM{
		getUserPolicyOutput: &iam.GetUserPolicyOutput{
			PolicyDocument: awssdk.String(encoded),
		},
	}
	client := &Client{IAM: mock}

	accesses, err := client.GetUserBucketAccess(context.Background(), "s3m-legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accesses) != 2 {
		t.Fatalf("expected 2 accesses, got %d", len(accesses))
	}
	for _, a := range accesses {
		if a.Permission != model.PermReadWriteDelete {
			t.Errorf("legacy bucket %q: expected read-write-delete, got %s", a.Bucket, a.Permission)
		}
	}
}

func TestGetUserBucketAccessNoPolicy(t *testing.T) {
	mock := &mockIAM{
		getUserPolicyErr: &smithy.GenericAPIError{Code: "NoSuchEntity", Message: "policy not found"},
	}
	client := &Client{IAM: mock}

	accesses, err := client.GetUserBucketAccess(context.Background(), "s3m-nopolicy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accesses) != 0 {
		t.Fatalf("expected 0 accesses, got %d", len(accesses))
	}
}

func TestSetUserBucketAccess(t *testing.T) {
	mock := &mockIAM{}
	client := &Client{IAM: mock}

	accesses := []model.BucketAccess{
		{Bucket: "bucket-x", Permission: model.PermRead},
		{Bucket: "bucket-y", Permission: model.PermReadWriteDelete},
	}
	err := client.SetUserBucketAccess(context.Background(), "s3m-setuser", accesses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.putUserPolicyInput == nil {
		t.Fatal("PutUserPolicy was not called")
	}

	// Parse the policy document to verify structure
	var doc struct {
		Version   string `json:"Version"`
		Statement []struct {
			Sid      string   `json:"Sid"`
			Effect   string   `json:"Effect"`
			Action   []string `json:"Action"`
			Resource []string `json:"Resource"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(awssdk.ToString(mock.putUserPolicyInput.PolicyDocument)), &doc); err != nil {
		t.Fatalf("could not parse policy document: %v", err)
	}

	if len(doc.Statement) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(doc.Statement))
	}
	if doc.Statement[0].Sid != "s3m-bucket-x" {
		t.Errorf("statement[0].Sid: expected 's3m-bucket-x', got %q", doc.Statement[0].Sid)
	}
	if doc.Statement[0].Effect != "Allow" {
		t.Errorf("statement[0].Effect: expected 'Allow', got %q", doc.Statement[0].Effect)
	}
	// read permission should have 2 actions
	if len(doc.Statement[0].Action) != 2 {
		t.Errorf("statement[0] expected 2 actions (read), got %d", len(doc.Statement[0].Action))
	}
	// read-write-delete should have 7 actions
	if len(doc.Statement[1].Action) != 7 {
		t.Errorf("statement[1] expected 7 actions (read-write-delete), got %d", len(doc.Statement[1].Action))
	}
}

func TestSetUserBucketAccessEmpty(t *testing.T) {
	mock := &mockIAM{}
	client := &Client{IAM: mock}

	err := client.SetUserBucketAccess(context.Background(), "s3m-emptyuser", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.deleteUserPolicyCalled {
		t.Error("expected DeleteUserPolicy to be called")
	}
	if mock.putUserPolicyInput != nil {
		t.Error("PutUserPolicy should not have been called")
	}
}

func TestBuildBucketPolicyWithPermissions(t *testing.T) {
	accesses := []model.BucketAccess{
		{Bucket: "read-bucket", Permission: model.PermRead},
		{Bucket: "rw-bucket", Permission: model.PermReadWrite},
		{Bucket: "rwd-bucket", Permission: model.PermReadWriteDelete},
	}

	policy := buildBucketPolicyWithPermissions(accesses)

	if policy["Version"] != "2012-10-17" {
		t.Errorf("expected Version '2012-10-17', got %v", policy["Version"])
	}

	stmts, ok := policy["Statement"].([]map[string]interface{})
	if !ok {
		t.Fatal("Statement is not []map[string]interface{}")
	}
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(stmts))
	}

	// Verify each statement
	expectations := []struct {
		sid         string
		actionCount int
	}{
		{"s3m-read-bucket", 2},
		{"s3m-rw-bucket", 6},
		{"s3m-rwd-bucket", 7},
	}
	for i, exp := range expectations {
		if stmts[i]["Sid"] != exp.sid {
			t.Errorf("statement[%d].Sid: expected %q, got %v", i, exp.sid, stmts[i]["Sid"])
		}
		actions, ok := stmts[i]["Action"].([]string)
		if !ok {
			t.Fatalf("statement[%d].Action is not []string", i)
		}
		if len(actions) != exp.actionCount {
			t.Errorf("statement[%d] expected %d actions, got %d: %v", i, exp.actionCount, len(actions), actions)
		}
		resources, ok := stmts[i]["Resource"].([]string)
		if !ok {
			t.Fatalf("statement[%d].Resource is not []string", i)
		}
		if len(resources) != 2 {
			t.Errorf("statement[%d] expected 2 resources, got %d", i, len(resources))
		}
	}
}
