package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
)

type userServiceMock struct {
	awsClient.IAMAPI
	created    int
	policies   int
	policyErr  error
	policyFunc func(context.Context) error
	keys       []iamtypes.AccessKeyMetadata
	deleted    int
}

func (m *userServiceMock) ListUsers(context.Context, *iam.ListUsersInput, ...func(*iam.Options)) (*iam.ListUsersOutput, error) {
	return &iam.ListUsersOutput{}, nil
}
func (m *userServiceMock) CreateAccessKey(context.Context, *iam.CreateAccessKeyInput, ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	m.created++
	return nil, errors.New("unexpected key creation")
}
func (m *userServiceMock) PutUserPolicy(ctx context.Context, _ *iam.PutUserPolicyInput, _ ...func(*iam.Options)) (*iam.PutUserPolicyOutput, error) {
	m.policies++
	if m.policyFunc != nil {
		return nil, m.policyFunc(ctx)
	}
	return nil, m.policyErr
}
func (m *userServiceMock) ListAccessKeys(context.Context, *iam.ListAccessKeysInput, ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	return &iam.ListAccessKeysOutput{AccessKeyMetadata: m.keys}, nil
}
func (m *userServiceMock) DeleteAccessKey(context.Context, *iam.DeleteAccessKeyInput, ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	m.deleted++
	return &iam.DeleteAccessKeyOutput{}, nil
}

func userKey(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestUserRefreshDoesNotCreateKey(t *testing.T) {
	service := &userServiceMock{}
	m := newUsersModel(&awsClient.Client{IAM: service})
	m.loading = false
	m.items = []userItem{{name: "alice"}}
	updated, cmd := m.update(userKey("r"))
	if cmd == nil || !updated.loading {
		t.Fatal("refresh must load users")
	}
	if strings.Contains(updated.view(), "Rotate") {
		t.Fatal("refresh must not advertise key creation")
	}
	updated, _ = updated.update(cmd())
	if updated.loading || service.created != 0 {
		t.Fatal("refresh created a credential or failed to complete")
	}
}

func TestUserSlashFilterKeepsShortcutCharactersAndClears(t *testing.T) {
	m := newUsersModel(nil)
	m.loading = false
	m.items = []userItem{{name: "s3m-alice"}, {name: "UPPER-qr"}, {name: "other"}}
	m, _ = m.update(userKey("/"))
	m, _ = m.update(userKey("q"))
	m, _ = m.update(userKey("r"))
	if !m.ownsInput() || len(m.userIndices()) != 1 || m.items[m.userIndices()[0]].name != "UPPER-qr" {
		t.Fatal("filter must apply each character without taking shortcuts")
	}
	m, _ = m.update(userKey("esc"))
	if m.filter.active || m.filter.query != "" || len(m.userIndices()) != 3 {
		t.Fatal("Escape must clear and restore the list")
	}
}

func TestUserFilterMatchesBucketPickerNames(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersCreateBuckets
	m.loading = false
	m.availableBuckets = []bucketItem{{name: "one"}, {name: "MEDIA-assets"}}
	m, _ = m.update(userKey("/"))
	m, _ = m.update(userKey("media"))
	m, _ = m.update(userKey("enter"))
	m, _ = m.update(userKey("enter"))
	if m.mode != usersCreatePerm || m.pendingBucket != "MEDIA-assets" {
		t.Fatal("filtered picker must select the original matching bucket")
	}
}

func TestPermissionFailureKeepsConfirmedStateAndRetry(t *testing.T) {
	service := &userServiceMock{policyErr: errors.New("denied")}
	m := newUsersModel(&awsClient.Client{IAM: service})
	m.mode = usersDetail
	m.detailUser = "alice"
	m.detailAccess = []model.BucketAccess{{Bucket: "files", Permission: model.PermRead}}
	m, _ = m.update(userKey("enter"))
	m, _ = m.update(userKey("3"))
	if service.policies != 0 || m.detailAccess[0].Permission != model.PermRead {
		t.Fatal("permission choice mutated service or confirmed state")
	}
	m, cmd := m.update(userKey("enter"))
	m, _ = m.update(cmd())
	if service.policies != 1 || m.detailAccess[0].Permission != model.PermRead || !m.detailError || m.mutating {
		t.Fatal("failed apply must retain old access and allow retry")
	}
	if _, cmd = m.update(userKey("enter")); cmd == nil {
		t.Fatal("Apply must allow a deliberate retry")
	}
}

func TestUserListScrollsToLastEntry(t *testing.T) {
	m := newUsersModel(nil)
	m.loading = false
	m.height = 15
	m.width = 60
	for i := 0; i < 50; i++ {
		m.items = append(m.items, userItem{name: fmt.Sprintf("user-%02d", i)})
	}
	m.cursor = 49
	view := m.view()
	if !strings.Contains(view, "> user-49") || strings.Contains(view, "user-00") {
		t.Fatal("viewport must show the focused final entry")
	}
	if len(strings.Split(view, "\n")) > 15 {
		t.Fatal("user list exceeds available height")
	}
}

func TestCredentialCopyFailureKeepsSecretForRetry(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersShowCreds
	m.creds = newCredentials("alice", "key", "secret")
	calls := 0
	m.creds.copyText = func(string) error {
		calls++
		if calls == 1 {
			return errors.New("no clipboard")
		}
		return nil
	}
	m, _ = m.update(userKey("c"))
	if m.creds.secretKey != "secret" || m.creds.saveError == "" {
		t.Fatal("failed copy lost access to secret")
	}
	m, _ = m.update(userKey("c"))
	if calls != 2 || m.creds.saveError != "" || m.creds.secretKey != "secret" {
		t.Fatal("retry must reuse the same secret")
	}
}

func TestCredentialDoneClearsSecretAfterAcknowledgement(t *testing.T) {
	m := newUsersModel(&awsClient.Client{IAM: &userServiceMock{}})
	m.mode = usersShowCreds
	m.creds = newCredentials("alice", "key", "secret")
	m, _ = m.update(userKey("enter"))
	if !m.creds.confirmExit {
		t.Fatal("unsaved exit must require acknowledgement")
	}
	m, _ = m.update(userKey("a"))
	if m.mode != usersList || m.creds.secretKey != "" || m.creds.accessKeyID != "" {
		t.Fatal("leaving credentials must clear display data")
	}
}

func TestKeyCreationDisabledWhenMetadataUnknown(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersKeys
	m.detailUser = "alice"
	m.keyLoading = false
	m, _ = m.update(userKey("c"))
	if m.mode != usersKeys {
		t.Fatal("unknown key count must not present creation as available")
	}
}

func TestUserKeyReviewKeepsActionsVisibleAndScrolls(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersKeyReview
	m.detailUser = "alice"
	m.keyAction = "create"
	m.width = 60
	m.height = 12
	view := m.view()
	if !strings.Contains(view, "Confirm action") || len(strings.Split(view, "\n")) > 12 {
		t.Fatal("compact key review must retain its action footer")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !strings.Contains(m.view(), "Verify applications") {
		t.Fatal("long key review must allow scrolling to recovery guidance")
	}
}

func TestUserResultIgnoresOlderRequestAndOtherUsername(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersDetail
	m.detailUser = "alice"
	m.detailRequest = 2
	m.detailLoading = true
	m.detailAccess = []model.BucketAccess{{Bucket: "confirmed", Permission: model.PermRead}}
	for _, result := range []usersResultMsg{
		{id: 1, kind: "access", username: "alice", access: []model.BucketAccess{{Bucket: "stale"}}},
		{id: 2, kind: "access", username: "bob", access: []model.BucketAccess{{Bucket: "wrong"}}},
	} {
		m, _ = m.update(result)
	}
	if !m.detailLoading || len(m.detailAccess) != 1 || m.detailAccess[0].Bucket != "confirmed" {
		t.Fatal("stale results must not clear a newer load or replace its data")
	}
}

func TestKeyDeletionRequiresInactiveKeyAndTypedID(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersKeys
	m.detailUser = "alice"
	m.keyListKnown = true
	m.keys = []model.AccessKey{{AccessKeyID: "active", Status: "Active"}}
	m, cmd := m.update(userKey("d"))
	if cmd != nil || m.mode != usersKeys {
		t.Fatal("active key deletion must be rejected")
	}
	m.keys[0].Status = "Inactive"
	m, _ = m.update(userKey("d"))
	if m.mode != usersKeyDelete {
		t.Fatal("inactive key requires a separate confirmation")
	}
	m, cmd = m.update(userKey("enter"))
	if cmd != nil || m.mutating {
		t.Fatal("empty typed confirmation must not delete")
	}
	m.keyConfirm.SetValue("wrong")
	m, cmd = m.update(userKey("enter"))
	if cmd != nil {
		t.Fatal("wrong key ID must not delete")
	}
	m.keyConfirm.SetValue("active")
	_, cmd = m.update(userKey("enter"))
	if cmd == nil {
		t.Fatal("matching key ID must allow explicit deletion")
	}
}

func TestUnavailableUserPolicyCannotBeOverwritten(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersDetail
	m.detailUser = "alice"
	m.detailRequest = 1
	m, _ = m.update(usersResultMsg{id: 1, kind: "access", username: "alice", err: errors.New("denied")})
	m, cmd := m.update(userKey("a"))
	if cmd != nil || m.mode != usersDetail {
		t.Fatal("unknown existing permissions must not be replaced through Add")
	}
	if strings.Contains(m.view(), "No matching bucket access") {
		t.Fatal("unavailable permissions must not be reported as no access")
	}
}

func TestUserMutationCancellationWaitsForResult(t *testing.T) {
	service := &userServiceMock{policyFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	m := newUsersModel(&awsClient.Client{IAM: service})
	m.mode = usersDetailReview
	m.detailUser = "alice"
	m.detailAccess = []model.BucketAccess{{Bucket: "files", Permission: model.PermRead}}
	m.pendingAccess = []model.BucketAccess{{Bucket: "files", Permission: model.PermReadWrite}}
	m, cmd := m.update(userKey("enter"))
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	m, _ = m.update(userKey("esc"))
	if !m.mutating || m.mode != usersDetailReview || !strings.Contains(m.view(), "Cancelling") {
		t.Fatal("cancel must wait for the worker without leaving its review")
	}
	select {
	case msg := <-result:
		m, _ = m.update(msg)
	case <-time.After(time.Second):
		t.Fatal("worker context was not cancelled")
	}
	if m.mutating || m.detailAccess[0].Permission != model.PermRead {
		t.Fatal("cancelled apply must keep the confirmed permission")
	}
}

func TestUserCancellationRetainsSecretProducedBeforeCompletion(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersKeyReview
	m.mutating = true
	m.mutationRequest = 1
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m, _ = m.update(usersResultMsg{id: 1, kind: "new-key", username: "alice", key: &model.AccessKey{AccessKeyID: "example", SecretAccessKey: "preserve-this-secret"}, err: context.Canceled})
	if m.mode != usersShowCreds || m.creds.secretKey != "preserve-this-secret" {
		t.Fatal("cancellation must not discard a key that was created")
	}
}

func TestKeyDeletionRechecksInactiveStatusBeforeDelete(t *testing.T) {
	service := &userServiceMock{keys: []iamtypes.AccessKeyMetadata{{AccessKeyId: awssdk.String("selected"), Status: iamtypes.StatusTypeActive}}}
	m := newUsersModel(&awsClient.Client{IAM: service})
	m.mode = usersKeyDelete
	m.detailUser = "alice"
	m.keyAction = "delete"
	m.keyTarget = model.AccessKey{AccessKeyID: "selected", Status: "Inactive"}
	m.keyConfirm.SetValue("selected")
	m, cmd := m.update(userKey("enter"))
	m, _ = m.update(cmd())
	if service.deleted != 0 || !m.detailError {
		t.Fatal("a key reactivated after review must not be deleted")
	}
}

func TestPermissionEnterDoesNotMutateConfirmedAccess(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersDetail
	m.detailUser = "alice"
	m.detailAccess = []model.BucketAccess{{Bucket: "files", Permission: model.PermRead}}
	updated, cmd := m.update(userKey("enter"))
	if cmd != nil {
		t.Fatal("opening permission choices must not submit a service mutation")
	}
	if updated.detailAccess[0].Permission != model.PermRead {
		t.Fatal("confirmed permission changed before apply")
	}
}

func TestCreateUserPermissionRequiresReview(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersCreatePerm
	m.pendingBucket = "files"
	m.nameInput.SetValue("alice")
	updated, cmd := m.update(userKey("2"))
	if cmd != nil {
		t.Fatal("choosing permission must review before creating user")
	}
	if !strings.Contains(updated.view(), "Review") {
		t.Fatal("expected explicit review screen")
	}
}

func TestCredentialsMaskedUntilReveal(t *testing.T) {
	m := newUsersModel(nil)
	m.mode = usersShowCreds
	m.creds = credentialsModel{username: "alice", accessKeyID: "AKIA123", secretKey: "never-show-by-default"}
	if strings.Contains(m.viewCredentials(), m.creds.secretKey) {
		t.Fatal("secret shown without reveal")
	}
	updated, _ := m.update(userKey("esc"))
	if updated.mode != usersShowCreds {
		t.Fatal("unsaved secret must require acknowledgement before leaving")
	}
}

func TestCredentialSaveNeverOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := saveCredentialsToFile("alice", "AKIA123", "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = saveCredentialsToFile("alice", "AKIA123", "second"); err == nil {
		t.Fatal("expected existing destination to be preserved")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "first") || strings.Contains(string(data), "second") {
		t.Fatal("existing credential file changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %o", info.Mode().Perm())
	}
}
