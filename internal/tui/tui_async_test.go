package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
)

type stubS3ForBucketInit struct {
	awsClient.S3API
	region string
}

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
