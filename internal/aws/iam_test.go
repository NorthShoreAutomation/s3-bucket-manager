package aws

import (
	"context"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type mockIAM struct {
	IAMAPI
	createUserOutput      *iam.CreateUserOutput
	createUserErr         error
	deleteUserErr         error
	listUsersOutput       *iam.ListUsersOutput
	tagUserErr            error
	getUserOutput         *iam.GetUserOutput
	createAccessKeyOutput *iam.CreateAccessKeyOutput
	deleteAccessKeyErr    error
	listAccessKeysOutput  *iam.ListAccessKeysOutput
	putUserPolicyErr      error
	deleteUserPolicyErr   error
	listUserPoliciesOutput *iam.ListUserPoliciesOutput
	listUserTagsOutput    *iam.ListUserTagsOutput
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
	return nil, m.putUserPolicyErr
}

func (m *mockIAM) DeleteUserPolicy(ctx context.Context, params *iam.DeleteUserPolicyInput, optFns ...func(*iam.Options)) (*iam.DeleteUserPolicyOutput, error) {
	return nil, m.deleteUserPolicyErr
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

	key, err := client.CreateManagedUser(context.Background(), "s3m-newuser", []string{"my-bucket"})
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
