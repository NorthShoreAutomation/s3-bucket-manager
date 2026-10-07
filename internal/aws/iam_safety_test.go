package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type safetyIAM struct {
	*mockIAM
	created int
	keysErr error
	tagsErr error
	updated *iam.UpdateAccessKeyInput
}

func (m *safetyIAM) CreateAccessKey(ctx context.Context, p *iam.CreateAccessKeyInput, opt ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	m.created++
	return m.mockIAM.CreateAccessKey(ctx, p, opt...)
}
func (m *safetyIAM) ListAccessKeys(ctx context.Context, p *iam.ListAccessKeysInput, opt ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	if m.keysErr != nil {
		return nil, m.keysErr
	}
	return m.mockIAM.ListAccessKeys(ctx, p, opt...)
}
func (m *safetyIAM) ListUserTags(ctx context.Context, p *iam.ListUserTagsInput, opt ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	if m.tagsErr != nil {
		return nil, m.tagsErr
	}
	return m.mockIAM.ListUserTags(ctx, p, opt...)
}
func (m *safetyIAM) UpdateAccessKey(_ context.Context, p *iam.UpdateAccessKeyInput, _ ...func(*iam.Options)) (*iam.UpdateAccessKeyOutput, error) {
	m.updated = p
	return &iam.UpdateAccessKeyOutput{}, nil
}

func TestNewKeyChecksTwoKeyLimit(t *testing.T) {
	m := &safetyIAM{mockIAM: &mockIAM{
		listAccessKeysOutput:  &iam.ListAccessKeysOutput{AccessKeyMetadata: []iamtypes.AccessKeyMetadata{{AccessKeyId: awssdk.String("one")}, {AccessKeyId: awssdk.String("two")}}},
		createAccessKeyOutput: &iam.CreateAccessKeyOutput{AccessKey: &iamtypes.AccessKey{AccessKeyId: awssdk.String("three")}},
	}}
	c := &Client{IAM: m}
	_, err := c.RotateAccessKey(context.Background(), "alice")
	if err == nil || m.created != 0 {
		t.Fatal("must reject creation before calling service at the two-key limit")
	}
}

func TestManagedUsersReportsUnreadableTags(t *testing.T) {
	m := &safetyIAM{mockIAM: &mockIAM{listUsersOutput: &iam.ListUsersOutput{Users: []iamtypes.User{{UserName: awssdk.String("s3m-alice")}}}}, tagsErr: errors.New("denied")}
	_, err := (&Client{IAM: m}).ListManagedUsers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatal("unreadable tags must surface partial metadata")
	}
}

func TestPartialCreateIdentifiesExistingUser(t *testing.T) {
	c := &Client{IAM: &mockIAM{tagUserErr: errors.New("denied")}}
	_, err := c.CreateManagedUser(context.Background(), "alice", nil)
	if err == nil || !strings.Contains(err.Error(), "already created") {
		t.Fatalf("expected deliberate recovery for partial creation, got %v", err)
	}
}

func TestBucketUsersKeepsAccessWhenKeyCountUnavailable(t *testing.T) {
	m := &safetyIAM{mockIAM: &mockIAM{
		listUsersOutput:     &iam.ListUsersOutput{Users: []iamtypes.User{{UserName: awssdk.String("s3m-alice")}}},
		listUserTagsOutput:  &iam.ListUserTagsOutput{Tags: []iamtypes.Tag{{Key: awssdk.String(managedTagKey), Value: awssdk.String(managedTagValue)}}},
		getUserPolicyOutput: &iam.GetUserPolicyOutput{PolicyDocument: awssdk.String(`{"Statement":[{"Sid":"s3m0","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::files","arn:aws:s3:::files/*"]}]}`)},
	}, keysErr: errors.New("key metadata denied")}
	users, err := (&Client{IAM: m}).ListBucketUsers(context.Background(), "files")
	if err == nil || len(users) != 1 || users[0].Username != "s3m-alice" {
		t.Fatalf("known access must remain available with a metadata warning: %v, %v", users, err)
	}
}

func TestAccessKeyStatusCanDeactivateAndReactivate(t *testing.T) {
	m := &safetyIAM{mockIAM: &mockIAM{}}
	c := &Client{IAM: m}
	if err := c.SetAccessKeyActive(context.Background(), "alice", "selected", false); err != nil {
		t.Fatal(err)
	}
	if awssdk.ToString(m.updated.AccessKeyId) != "selected" || m.updated.Status != iamtypes.StatusTypeInactive {
		t.Fatal("must deactivate the selected key only")
	}
	if err := c.SetAccessKeyActive(context.Background(), "alice", "selected", true); err != nil {
		t.Fatal(err)
	}
	if m.updated.Status != iamtypes.StatusTypeActive {
		t.Fatal("retirement must be reversible")
	}
}

func TestAccessKeyListPreservesStatusAndUnknownCount(t *testing.T) {
	m := &safetyIAM{mockIAM: &mockIAM{
		listAccessKeysOutput: &iam.ListAccessKeysOutput{AccessKeyMetadata: []iamtypes.AccessKeyMetadata{{AccessKeyId: awssdk.String("key"), Status: iamtypes.StatusTypeInactive}}},
		listUsersOutput:      &iam.ListUsersOutput{Users: []iamtypes.User{{UserName: awssdk.String("s3m-alice")}}},
		listUserTagsOutput:   &iam.ListUserTagsOutput{Tags: []iamtypes.Tag{{Key: awssdk.String(managedTagKey), Value: awssdk.String(managedTagValue)}}},
	}}
	c := &Client{IAM: m}
	keys, err := c.ListAccessKeys(context.Background(), "s3m-alice")
	if err != nil || len(keys) != 1 || keys[0].Status != "Inactive" {
		t.Fatal("key metadata must include status")
	}
	m.keysErr = errors.New("denied")
	users, err := c.ListManagedUsers(context.Background())
	if err == nil || len(users) != 1 || users[0].KeyCountKnown {
		t.Fatal("unavailable key counts must stay unknown while retaining the user")
	}
}
