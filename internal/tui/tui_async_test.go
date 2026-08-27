package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
	"github.com/dcorbell/s3m/internal/progress"
)

type stubS3ForBucketInit struct {
	awsClient.S3API
	region string
}

type stubS3ForBulkDelete struct {
	awsClient.S3API
	directDeletes []string
	bulkDeletes   []string
	updatedPolicy string
}

func (s *stubS3ForBulkDelete) GetBucketPolicy(_ context.Context, _ *s3.GetBucketPolicyInput, _ ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	return &s3.GetBucketPolicyOutput{Policy: ptr(`{"Version":"2012-10-17","Statement":[{"Sid":"s3m-public-folder","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-a/folder/*"},{"Sid":"unrelated","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-a/other/*"}]}`)}, nil
}

func (s *stubS3ForBulkDelete) PutBucketPolicy(_ context.Context, input *s3.PutBucketPolicyInput, _ ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error) {
	s.updatedPolicy = *input.Policy
	return &s3.PutBucketPolicyOutput{}, nil
}

func (s *stubS3ForBulkDelete) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	s.directDeletes = append(s.directDeletes, *input.Key)
	return &s3.DeleteObjectOutput{}, nil
}

func (s *stubS3ForBulkDelete) ListObjectsV2(_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if *input.Prefix != "folder/" {
		return &s3.ListObjectsV2Output{}, nil
	}
	return &s3.ListObjectsV2Output{Contents: []s3types.Object{
		{Key: ptr("folder/one.txt")},
		{Key: ptr("folder/two.txt")},
	}}, nil
}

func (s *stubS3ForBulkDelete) DeleteObjects(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	for _, object := range input.Delete.Objects {
		s.bulkDeletes = append(s.bulkDeletes, *object.Key)
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func ptr(value string) *string { return &value }

func (s *stubS3ForBucketInit) GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return &s3.GetBucketLocationOutput{
		LocationConstraint: s3types.BucketLocationConstraint(s.region),
	}, nil
}

// HeadBucket is stubbed to return an empty response. The manager-based
// GetBucketRegion helper reads the bucket region from raw HTTP headers via
// middleware that only runs against real SDK clients, so in-process tests
// rely on the GetBucketLocation fallback path in Client.GetBucketRegion.
func (s *stubS3ForBucketInit) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, nil
}

func TestAppUpdateRoutesErrMsgToActiveModel(t *testing.T) {
	app := App{
		screen: screenUsers,
		users: usersModel{
			mode:    usersCreateBuckets,
			loading: true,
		},
	}

	next, _ := app.Update(errMsg{err: errors.New("boom")})
	updated := next.(App)

	if updated.err == nil || updated.err.Error() != "boom" {
		t.Fatalf("expected app error to be recorded, got %#v", updated.err)
	}
	if updated.users.loading {
		t.Fatal("expected users model loading state to be cleared on errMsg")
	}
}

func TestAppDoesNotRecordTransferCancelAsGlobalError(t *testing.T) {
	snap := &atomic.Pointer[progress.Snapshot]{}
	snap.Store(&progress.Snapshot{Done: 1, Total: 10})
	app := App{
		screen: screenBuckets,
		buckets: bucketsModel{
			mode:         bucketDetail,
			transferSnap: snap,
		},
	}

	next, _ := app.Update(errMsg{err: context.Canceled})
	updated := next.(App)

	if updated.err != nil {
		t.Fatalf("expected transfer cancellation not to be recorded as app error, got %v", updated.err)
	}
	if updated.buckets.detailMessage != "Cancelled" {
		t.Fatalf("expected cancellation detail message, got %q", updated.buckets.detailMessage)
	}
}

func TestBrowseKeysIgnoredDuringTransferExceptCancel(t *testing.T) {
	snap := &atomic.Pointer[progress.Snapshot]{}
	snap.Store(&progress.Snapshot{Done: 1, Total: 10})
	cancelled := false
	m := bucketsModel{
		items: []bucketItem{{name: "bucket-a"}},
		mode:  bucketDetail,
		browseItems: []awsClient.BrowseItem{
			{Name: "file.txt", Key: "file.txt"},
		},
		transferSnap: snap,
		transferCancel: func() {
			cancelled = true
		},
	}

	updated, cmd := m.updateBrowse(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd != nil {
		t.Fatal("expected no command for non-cancel key during transfer")
	}
	if updated.showFilePicker {
		t.Fatal("expected upload picker not to open during transfer")
	}

	updated, cmd = updated.updateBrowse(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected cancel key not to start a command")
	}
	if !cancelled {
		t.Fatal("expected esc to call transfer cancel")
	}
}

func TestBrowseSpaceTogglesSelectionAndKeepsItWhileScrolling(t *testing.T) {
	m := bucketsModel{
		mode:   bucketDetail,
		height: 8,
		browseItems: []awsClient.BrowseItem{
			{Name: "one.txt", Key: "one.txt"},
			{Name: "two.txt", Key: "two.txt"},
			{Name: "three.txt", Key: "three.txt"},
		},
	}

	updated, _ := m.updateBrowse(tea.KeyMsg{Type: tea.KeySpace})
	if !updated.browseSelected["one.txt"] {
		t.Fatal("expected space to select the highlighted item")
	}

	updated, _ = updated.updateBrowse(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.updateBrowse(tea.KeyMsg{Type: tea.KeySpace})
	if !updated.browseSelected["one.txt"] || !updated.browseSelected["two.txt"] {
		t.Fatalf("expected selections to survive scrolling, got %#v", updated.browseSelected)
	}

	updated, _ = updated.updateBrowse(tea.KeyMsg{Type: tea.KeyUp})
	updated, _ = updated.updateBrowse(tea.KeyMsg{Type: tea.KeySpace})
	if updated.browseSelected["one.txt"] {
		t.Fatal("expected space to deselect an already selected item")
	}
}

func TestBrowseSelectAllTogglesCurrentListing(t *testing.T) {
	m := bucketsModel{browseItems: []awsClient.BrowseItem{
		{Name: "folder/", Key: "folder/", IsFolder: true},
		{Name: "file.txt", Key: "file.txt"},
	}}

	updated, _ := m.updateBrowse(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(updated.browseSelected) != 2 {
		t.Fatalf("expected all items selected, got %#v", updated.browseSelected)
	}

	updated, _ = updated.updateBrowse(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(updated.browseSelected) != 0 {
		t.Fatalf("expected select-all to clear a fully selected listing, got %#v", updated.browseSelected)
	}

	m.browseSelected = map[string]bool{"file.txt": true}
	updated, _ = m.updateBrowse(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(updated.browseSelected) != 0 {
		t.Fatalf("expected select-all to clear a partial selection, got %#v", updated.browseSelected)
	}
}

func TestBrowseNavigationClearsSelection(t *testing.T) {
	m := bucketsModel{
		items:          []bucketItem{{name: "bucket-a"}},
		browsePrefix:   "parent/",
		browseItems:    []awsClient.BrowseItem{{Name: "child/", Key: "parent/child/", IsFolder: true}},
		browseSelected: map[string]bool{"parent/child/": true},
	}

	updated, _ := m.updateBrowse(tea.KeyMsg{Type: tea.KeyRight})
	if len(updated.browseSelected) != 0 {
		t.Fatalf("expected entering a folder to clear selections, got %#v", updated.browseSelected)
	}
}

func TestBrowseDeleteUsesOneBulkConfirmationForSelection(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket-a"}}
	m.browseItems = []awsClient.BrowseItem{
		{Name: "folder/", Key: "folder/", IsFolder: true},
		{Name: "file.txt", Key: "file.txt"},
	}
	m.browseSelected = map[string]bool{"folder/": true, "file.txt": true}

	updated, _ := m.updateBrowse(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if updated.mode != bucketDetailDeleteSelection {
		t.Fatalf("expected one bulk-delete confirmation mode, got %v", updated.mode)
	}
	if !strings.Contains(updated.confirmAction, "1 file and 1 folder") {
		t.Fatalf("expected confirmation to summarize selection, got %q", updated.confirmAction)
	}
}

func TestBrowseViewMarksSelectedRows(t *testing.T) {
	m := bucketsModel{
		items:          []bucketItem{{name: "bucket-a"}},
		mode:           bucketDetail,
		height:         20,
		browseItems:    []awsClient.BrowseItem{{Name: "file.txt", Key: "file.txt"}},
		browseSelected: map[string]bool{"file.txt": true},
	}

	view := m.viewDetail()
	if !strings.Contains(view, "[x]") {
		t.Fatalf("expected selected-row marker in browse view, got %q", view)
	}
}

func TestBrowseBulkDeleteRunsFilesAndFoldersAfterOneConfirmation(t *testing.T) {
	s3Client := &stubS3ForBulkDelete{}
	m := newBucketsModel(&awsClient.Client{S3: s3Client})
	m.items = []bucketItem{{name: "bucket-a", region: "us-west-2"}}
	m.mode = bucketDetailDeleteSelection
	m.browseItems = []awsClient.BrowseItem{
		{Name: "folder/", Key: "folder/", IsFolder: true},
		{Name: "file.txt", Key: "file.txt"},
	}
	m.browseSelected = map[string]bool{"folder/": true, "file.txt": true}
	m.deleteInput.SetValue("delete")

	updated, cmd := m.updateDeleteSelection(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected confirmed deletion to return one command")
	}
	msg := cmd()
	done, ok := msg.(operationDoneMsg)
	if !ok {
		t.Fatalf("expected successful deletion message, got %#v", msg)
	}
	if done.message != "Deleted 3 objects across 2 selected items" {
		t.Fatalf("expected total deleted-object and selection counts, got %q", done.message)
	}
	if !updated.loading {
		t.Fatal("expected browse model to show loading while deletion runs")
	}
	if len(s3Client.bulkDeletes) != 3 || s3Client.bulkDeletes[0] != "file.txt" {
		t.Fatalf("expected selected file and both folder objects to be batch deleted, got %#v", s3Client.bulkDeletes)
	}
	if strings.Contains(s3Client.updatedPolicy, "s3m-public-folder") || !strings.Contains(s3Client.updatedPolicy, "unrelated") {
		t.Fatalf("expected deleted folder policy removed without disturbing other policy, got %q", s3Client.updatedPolicy)
	}
}

func TestBulkDeleteProgressUpdatesAndClears(t *testing.T) {
	m := bucketsModel{
		items:          []bucketItem{{name: "bucket-a"}},
		mode:           bucketDetail,
		browseItems:    []awsClient.BrowseItem{{Name: "file.txt", Key: "file.txt"}},
		deleteProgress: "Deleting selected items...",
	}

	updated, _ := m.update(selectionDeleteProgressMsg{deleted: 1200})
	if updated.deleteProgress != "Deleting selected items... 1,200 objects removed" {
		t.Fatalf("expected live bulk-delete progress, got %q", updated.deleteProgress)
	}

	updated, _ = updated.update(operationDoneMsg{message: "Deleted 1,200 objects"})
	if updated.deleteProgress != "" {
		t.Fatalf("expected completed operation to clear deletion progress, got %q", updated.deleteProgress)
	}
}

func TestBulkDeleteCanBeCancelledAndRefreshesAfterFailure(t *testing.T) {
	cancelled := false
	m := bucketsModel{
		client:           &awsClient.Client{},
		items:            []bucketItem{{name: "bucket-a"}},
		mode:             bucketDetail,
		browseItems:      []awsClient.BrowseItem{{Name: "file.txt", Key: "file.txt"}},
		browseSelected:   map[string]bool{"file.txt": true},
		bulkDeleting:     true,
		bulkDeleteCancel: func() { cancelled = true },
	}

	updated, cmd := m.updateBrowse(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled || cmd != nil || !updated.bulkDeleting {
		t.Fatalf("expected escape to request cancellation without changing state early, cancelled=%v", cancelled)
	}

	updated, cmd = updated.update(errMsg{err: errors.New("delete failed")})
	if cmd == nil || !updated.loading || updated.bulkDeleting || len(updated.browseSelected) != 0 {
		t.Fatalf("expected failed bulk delete to clear selection and refresh, got %#v", updated)
	}
}

func TestAppDoesNotRecordBulkDeleteCancellationAsGlobalError(t *testing.T) {
	app := App{screen: screenBuckets, buckets: bucketsModel{
		client:       &awsClient.Client{},
		items:        []bucketItem{{name: "bucket-a"}},
		browseItems:  []awsClient.BrowseItem{{Name: "file.txt", Key: "file.txt"}},
		bulkDeleting: true,
	}}

	next, _ := app.Update(errMsg{err: context.Canceled})
	if updated := next.(App); updated.err != nil {
		t.Fatalf("expected bulk-delete cancellation not to become a global error, got %v", updated.err)
	}
}

func TestAppCancelsBulkDeleteBeforeQuitting(t *testing.T) {
	cancelled := false
	app := App{screen: screenBuckets, buckets: bucketsModel{
		bulkDeleting:     true,
		bulkDeleteCancel: func() { cancelled = true },
	}}

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !cancelled || cmd == nil {
		t.Fatalf("expected quit to cancel bulk delete before returning quit command, cancelled=%v", cancelled)
	}
}

func TestRootFilePickerKeysRouteThroughDetail(t *testing.T) {
	m := bucketsModel{
		items:          []bucketItem{{name: "bucket-a"}},
		mode:           bucketDetail,
		showFilePicker: true,
		filePicker: filePickerModel{
			items: []localFileItem{{name: "file.txt"}},
		},
	}

	updated, cmd := m.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected root picker escape not to start a command")
	}
	if updated.showFilePicker {
		t.Fatal("expected escape to close the root file picker")
	}
	if updated.mode != bucketDetail {
		t.Fatalf("expected to remain in bucket detail, got mode %v", updated.mode)
	}
}

func TestRootTransferCancelRoutesThroughDetail(t *testing.T) {
	snap := &atomic.Pointer[progress.Snapshot]{}
	snap.Store(&progress.Snapshot{Done: 1, Total: 10})
	cancelled := false
	m := bucketsModel{
		items:        []bucketItem{{name: "bucket-a"}},
		mode:         bucketDetail,
		transferSnap: snap,
		transferCancel: func() {
			cancelled = true
		},
	}

	updated, cmd := m.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected root transfer escape not to start a command")
	}
	if !cancelled {
		t.Fatal("expected escape to cancel the root transfer")
	}
	if updated.mode != bucketDetail {
		t.Fatalf("expected to remain in bucket detail, got mode %v", updated.mode)
	}
}

func TestUsersIgnoreStaleUserAccessLoadedMsg(t *testing.T) {
	m := usersModel{
		mode:          usersDetail,
		detailUser:    "bob",
		detailLoading: true,
	}

	updated, _ := m.update(userAccessLoadedMsg{
		username: "alice",
		access: []model.BucketAccess{
			{Bucket: "bucket-a", Permission: model.PermRead},
		},
	})

	if !updated.detailLoading {
		t.Fatal("expected stale response to be ignored and loading to remain in progress")
	}
	if len(updated.detailAccess) != 0 {
		t.Fatalf("expected stale response not to overwrite detail access, got %#v", updated.detailAccess)
	}
}

func TestCreateBucketPickerDoesNotUseDetailAccessFilter(t *testing.T) {
	m := usersModel{
		mode:    usersCreateBuckets,
		loading: true,
		detailAccess: []model.BucketAccess{
			{Bucket: "bucket-a", Permission: model.PermRead},
		},
	}

	updated, _ := m.update(createBucketPickerLoadedMsg{
		items: []bucketItem{
			{name: "bucket-a"},
			{name: "bucket-b"},
		},
	})

	if updated.loading {
		t.Fatal("expected create bucket picker load to finish")
	}
	if len(updated.availableBuckets) != 2 {
		t.Fatalf("expected create flow to show all buckets, got %#v", updated.availableBuckets)
	}
}

func TestBucketsIgnoreStaleBucketUsersLoadedMsg(t *testing.T) {
	m := bucketsModel{
		items: []bucketItem{{name: "bucket-b"}},
		mode:  bucketDetail,
	}
	m.bucketUsersLoading = true

	updated, _ := m.update(bucketUsersLoadedMsg{
		bucket: "bucket-a",
		users: []model.UserPermission{
			{Username: "alice", Permission: model.PermRead},
		},
	})

	if !updated.bucketUsersLoading {
		t.Fatal("expected stale bucket response to be ignored")
	}
	if len(updated.bucketUsers) != 0 {
		t.Fatalf("expected stale bucket users not to be applied, got %#v", updated.bucketUsers)
	}
}

func TestBucketDetailShowsLoadErrorInsteadOfEmptyState(t *testing.T) {
	m := bucketsModel{
		items: []bucketItem{{name: "bucket-a", region: "us-west-2", created: "2026-04-12"}},
		mode:  bucketDetail,
	}
	m.bucketUsersError = "failed to fetch access"

	view := m.viewDetail()

	if !strings.Contains(view, "failed to fetch access") {
		t.Fatalf("expected bucket users load error in detail view, got %q", view)
	}
	if strings.Contains(view, "No users assigned.") {
		t.Fatalf("expected error state to replace empty-state copy, got %q", view)
	}
}

func TestBucketsRendersFriendlyIAMAccessDeniedMessage(t *testing.T) {
	// When credentials cannot call iam:ListUsers (common for bucket-scoped keys),
	// the raw AWS error must be translated to a friendly notice so the bucket
	// detail view stays usable.
	m := bucketsModel{
		items: []bucketItem{{name: "bucket-a", region: "us-west-2"}},
		mode:  bucketDetail,
	}
	m.bucketUsersLoading = true

	updated, _ := m.update(bucketUsersLoadedMsg{
		bucket: "bucket-a",
		err:    awsClient.ErrIAMAccessDenied,
	})

	if updated.bucketUsersLoading {
		t.Fatal("expected loading state to clear on error")
	}
	if !strings.Contains(updated.bucketUsersError, "IAM access denied") {
		t.Fatalf("expected friendly IAM-access-denied message, got %q", updated.bucketUsersError)
	}
	if strings.Contains(updated.bucketUsersError, "ListUsers") {
		t.Fatalf("friendly message should hide raw API call name, got %q", updated.bucketUsersError)
	}
}

func TestBucketsRendersRawErrorForUnrelatedFailures(t *testing.T) {
	// Non-IAM errors (throttling, network, etc.) should still bubble up as-is so
	// operators can diagnose real failures — only AccessDenied on iam:ListUsers
	// gets the friendly substitution.
	m := bucketsModel{
		items: []bucketItem{{name: "bucket-a", region: "us-west-2"}},
		mode:  bucketDetail,
	}
	m.bucketUsersLoading = true

	updated, _ := m.update(bucketUsersLoadedMsg{
		bucket: "bucket-a",
		err:    errors.New("throttled: rate exceeded"),
	})

	if !strings.Contains(updated.bucketUsersError, "throttled") {
		t.Fatalf("expected raw error text to be preserved, got %q", updated.bucketUsersError)
	}
}

func TestNewAppForBucketStartsUserAccessLoading(t *testing.T) {
	app := NewAppForBucket(context.Background(), &awsClient.Client{
		Region: "us-west-2",
		S3:     &stubS3ForBucketInit{region: "us-west-2"},
	}, "bucket-a")

	if !app.buckets.bucketUsersLoading {
		t.Fatal("expected direct bucket mode to mark user access as loading")
	}
}

func TestDirectBucketDetailKeepsLoadingStateUntilUsersArrive(t *testing.T) {
	app := NewAppForBucket(context.Background(), &awsClient.Client{
		Region: "us-west-2",
		S3:     &stubS3ForBucketInit{region: "us-west-2"},
	}, "bucket-a")

	updated, _ := app.buckets.update(prefixesLoadedMsg{
		bucket: "bucket-a",
		prefixes: []prefixItem{
			{prefix: "installers/"},
		},
	})
	view := updated.viewDetail()

	if !strings.Contains(view, "Loading user access...") {
		t.Fatalf("expected direct bucket detail to keep user access in loading state, got %q", view)
	}
	if strings.Contains(view, "No users assigned.") {
		t.Fatalf("expected loading state instead of empty-state copy, got %q", view)
	}
}

func TestBucketDetailShowsUnknownCreatedWhenUnavailable(t *testing.T) {
	m := bucketsModel{
		items: []bucketItem{{name: "bucket-a", region: "us-west-2"}},
		mode:  bucketDetail,
	}

	view := m.viewDetail()

	if !strings.Contains(view, "Unknown") {
		t.Fatalf("expected detail view to label missing created date as unknown, got %q", view)
	}
}

func TestAddFolderModeIsTextInputActive(t *testing.T) {
	app := App{
		buckets: bucketsModel{mode: bucketDetailAddFolder},
	}

	if !app.isTextInputActive() {
		t.Fatal("expected folder-name entry to suppress global shortcuts")
	}
}

func TestBrowseAddFolderKeepsCurrentPrefixUntilCreateSucceeds(t *testing.T) {
	m := bucketsModel{
		items:        []bucketItem{{name: "bucket-a", region: "us-west-2"}},
		mode:         bucketDetailAddFolder,
		browsePrefix: "parent/",
		browseItems: []awsClient.BrowseItem{
			{Name: "existing/", Key: "parent/existing/", IsFolder: true},
		},
	}
	m.prefixInput.SetValue("new-folder")

	updated, cmd := m.updateBrowseAddFolder(tea.KeyMsg{Type: tea.KeyEnter})

	if cmd == nil {
		t.Fatal("expected create-folder command")
	}
	if updated.browsePrefix != "parent/" {
		t.Fatalf("expected current prefix to remain until create succeeds, got %q", updated.browsePrefix)
	}
	if len(updated.browseItems) != 1 {
		t.Fatalf("expected current browse items to remain until create succeeds, got %#v", updated.browseItems)
	}
}
