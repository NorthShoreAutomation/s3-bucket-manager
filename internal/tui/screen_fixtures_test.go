package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/httpcopy"
	"github.com/dcorbell/s3m/internal/model"
)

// These fixtures render sample data only. They never call live services or a clipboard.
func screenFixture(width, height int) App {
	a := NewApp(nil)
	next, _ := a.Update(tea.WindowSizeMsg{Width: width, Height: height})
	a = next.(App)
	a.buckets.loading = false
	a.users.loading = false
	a.buckets.items = []bucketItem{{name: "example-bucket", region: "us-west-2", created: "2026-01-02", statsKnown: true, accessKnown: true}}
	a.buckets.browsePrefix = "資料/" + strings.Repeat("long-folder/", 3)
	a.buckets.folderDeleteKey = a.buckets.browsePrefix + "old/"
	a.buckets.folderDeleteCnt = 50
	a.buckets.confirmAction = "Review changes to s3://example-bucket/" + a.buckets.browsePrefix
	a.buckets.pendingUser = "sample-user"
	a.buckets.prefixInput.SetValue("new-folder")
	a.buckets.nameInput.SetValue("new-example-bucket")
	a.buckets.deleteInput.SetValue("delete")
	a.buckets.confirmInput.SetValue("example-bucket")
	a.buckets.confirmInput2.SetValue("yes")
	a.buckets.detailCursor = 1
	a.users.detailUser = "sample-user"
	a.users.detailKnown = true
	a.users.pendingBucket = "example-bucket"
	a.users.pendingPermission = model.PermReadWrite
	a.users.pendingOld = model.PermRead
	a.users.pendingAction = "edit"
	a.users.nameInput.SetValue("sample-user")
	a.users.deleteUser = "sample-user"
	a.users.keyListKnown = true
	a.users.keys = []model.AccessKey{{AccessKeyID: "EXAMPLE_KEY_ID", Status: "Inactive", CreateDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}}
	a.users.keyTarget = a.users.keys[0]
	a.users.keyAction = "deactivate"
	a.users.creds = newCredentials("sample-user", "EXAMPLE_KEY_ID", "sample-secret-not-a-credential")
	a.users.creds.savePath = "/example/credentials/sample-user-EXAMPLE_KEY_ID-credentials.json"
	a.users.creds.pathInput.SetValue(a.users.creds.savePath)
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("sample-%02d-資料", i)
		a.users.items = append(a.users.items, userItem{name: name, keyCount: 1, keyCountKnown: true, created: "2026-01-02"})
		a.users.detailAccess = append(a.users.detailAccess, model.BucketAccess{Bucket: name, Permission: model.PermRead})
		a.users.availableBuckets = append(a.users.availableBuckets, bucketItem{name: name, region: "us-west-2"})
		a.buckets.availableUsers = append(a.buckets.availableUsers, userItem{name: name, created: "2026-01-02"})
		a.buckets.bucketUsers = append(a.buckets.bucketUsers, bucketUserItem{username: name, permission: model.PermRead})
		a.buckets.browseItems = append(a.buckets.browseItems, awsClient.BrowseItem{Name: name + ".txt", Key: a.buckets.browsePrefix + name + ".txt", Size: 1024})
	}
	return a
}

func assertFixtureBounds(t *testing.T, view string, width, height int) {
	t.Helper()
	if !utf8.ValidString(view) {
		t.Fatal("render contains invalid UTF-8")
	}
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("height %d exceeds %d", len(lines), height)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("line %d width %d exceeds %d: %q", i, w, width, ansi.Strip(line))
		}
	}
}

func TestEveryUserScreenFitsAndShowsItsAction(t *testing.T) {
	cases := []struct {
		mode   usersMode
		action string
	}{
		{usersList, "enter: access"}, {usersCreate, "New username:"}, {usersCreateBuckets, "enter: select"},
		{usersCreatePerm, "1/2/3: choose"}, {usersCreateReview, "Create user"}, {usersConfirmDelete, "Delete user"},
		{usersShowCreds, "enter: Done"}, {usersDetail, "enter: edit"}, {usersDetailPickBucket, "enter: select"},
		{usersDetailPickPerm, "1/2/3: choose"}, {usersDetailConfirmRemove, "enter: Apply"}, {usersDetailReview, "enter: Apply"},
		{usersKeys, "c: new key"}, {usersKeyReview, "Confirm action"}, {usersKeyDelete, "Confirm action"},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 15}} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%dx%d/mode-%d", size[0], size[1], c.mode), func(t *testing.T) {
				a := screenFixture(size[0], size[1])
				a.screen = screenUsers
				a.users.mode = c.mode
				if c.mode == usersKeyDelete {
					a.users.keyAction = "delete"
					a.users.keyConfirm.SetValue("EXAMPLE_KEY_ID")
				}
				view := a.View()
				assertFixtureBounds(t, view, size[0], size[1])
				if !strings.Contains(ansi.Strip(view), c.action) {
					t.Fatalf("missing action %q:\n%s", c.action, ansi.Strip(view))
				}
				if strings.Contains(view, "sample-secret-not-a-credential") {
					t.Fatal("secret must stay masked in sample captures")
				}
			})
		}
	}
}

func TestEveryBucketScreenFitsAndShowsItsAction(t *testing.T) {
	cases := []struct {
		mode   bucketsMode
		tab    int
		action string
	}{
		{bucketsList, 0, "Enter: Open"}, {bucketsCreate, 0, "Enter: Continue"},
		{bucketsTypeDelete, 0, "Enter: Delete"}, {bucketsConfirmDelete, 0, "Enter: Delete"}, {bucketsConfirmDeleteNonEmpty, 0, "Enter: Delete"},
		{bucketDetail, 0, "Enter: Open"}, {bucketDetail, 1, "Enter: Review edit"}, {bucketDetail, 2, "Tab: Next view"},
		{bucketDetailAddPrefix, 0, "Enter: Create folder"}, {bucketDetailAddFolder, 0, "Enter: Create folder"},
		{bucketDetailConfirm, 0, "Enter: Apply"}, {bucketDetailDeleteFolder, 0, "Enter: Apply"},
		{bucketDetailDeleteSelection, 0, "Enter: Apply"}, {bucketDetailPickUser, 0, "Select"},
		{bucketDetailPickPerm, 0, "Press 1, 2, or 3"}, {bucketDetailConfirmRemoveUser, 1, "[y/N]"},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 15}} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%dx%d/mode-%d/tab-%d", size[0], size[1], c.mode, c.tab), func(t *testing.T) {
				a := screenFixture(size[0], size[1])
				a.buckets.mode = c.mode
				a.buckets.detailTab = c.tab
				view := a.View()
				assertFixtureBounds(t, view, size[0], size[1])
				if !strings.Contains(ansi.Strip(view), c.action) {
					t.Fatalf("missing action %q:\n%s", c.action, ansi.Strip(view))
				}
			})
		}
	}
}

func TestCredentialDialogStatesKeepTheirActionsVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 15}} {
		for _, state := range []string{"save", "unsaved-exit", "copy-failed", "saved", "revealed"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], state), func(t *testing.T) {
				a := screenFixture(size[0], size[1])
				a.screen = screenUsers
				a.users.mode = usersShowCreds
				action := "enter: Done"
				switch state {
				case "save":
					a.users.creds.editingPath = true
					action = "enter: Save"
				case "unsaved-exit":
					a.users.creds.confirmExit = true
					action = "a: I captured it"
				case "copy-failed":
					a.users.creds.saveError = "Copy failed. No clipboard is available."
					a.users.creds.message = "The secret remains available. Press c to retry."
				case "saved":
					a.users.creds.saved = true
				case "revealed":
					a.users.creds.revealed = true
				}
				view := a.View()
				assertFixtureBounds(t, view, size[0], size[1])
				if !strings.Contains(ansi.Strip(view), action) {
					t.Fatalf("missing credential action %q:\n%s", action, ansi.Strip(view))
				}
			})
		}
	}
}

func TestUserAndBucketListsKeepPagedFocusVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 15}} {
		for _, kind := range []string{"users", "user-access", "create-bucket-picker", "add-bucket-picker", "bucket-user-picker", "files", "buckets"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], kind), func(t *testing.T) {
				a := screenFixture(size[0], size[1])
				a.screen = screenUsers
				switch kind {
				case "users":
					a.users.mode = usersList
				case "user-access":
					a.users.mode = usersDetail
				case "create-bucket-picker":
					a.users.mode = usersCreateBuckets
				case "add-bucket-picker":
					a.users.mode = usersDetailPickBucket
				case "bucket-user-picker":
					a.screen = screenBuckets
					a.buckets.mode = bucketDetailPickUser
				case "files":
					a.screen = screenBuckets
					a.buckets.mode = bucketDetail
				case "buckets":
					a.screen = screenBuckets
					a.buckets.items = nil
					for i := 0; i < 50; i++ {
						a.buckets.items = append(a.buckets.items, bucketItem{name: fmt.Sprintf("sample-%02d-資料", i), region: "us-west-2"})
					}
				}
				for i := 0; i < 50; i++ {
					next, _ := a.Update(tea.KeyMsg{Type: tea.KeyPgDown})
					a = next.(App)
				}
				view := a.View()
				assertFixtureBounds(t, view, size[0], size[1])
				if !strings.Contains(ansi.Strip(view), "sample-49-資料") {
					t.Fatalf("paged focused entry is hidden:\n%s", ansi.Strip(view))
				}
			})
		}
	}
}

func TestPickerURLAndShareScreensKeepActionsVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 15}} {
		t.Run(fmt.Sprintf("%dx%d/local-picker", size[0], size[1]), func(t *testing.T) {
			a := screenFixture(size[0], size[1])
			a.buckets.mode = bucketDetail
			a.buckets.showFilePicker = true
			fp := filePickerModel{path: "/example/資料/" + strings.Repeat("long-folder/", 3), width: size[0], height: size[1] - 7}
			for i := 0; i < 50; i++ {
				fp.items = append(fp.items, localFileItem{name: fmt.Sprintf("local-%02d-資料.txt", i), size: 1024})
			}
			fp.allItems = append([]localFileItem(nil), fp.items...)
			for i := 0; i < 50; i++ {
				fp, _, _ = fp.update(tea.KeyMsg{Type: tea.KeyPgDown})
			}
			a.buckets.filePicker = fp
			view := a.View()
			assertFixtureBounds(t, view, size[0], size[1])
			plain := ansi.Strip(view)
			if !strings.Contains(plain, "local-49-資料.txt") || !strings.Contains(plain, "Select/open") {
				t.Fatalf("picker lost focus or action:\n%s", plain)
			}
		})
		for _, phase := range []urlUploadPhase{urlUploadPhaseInput, urlUploadPhaseResolve, urlUploadPhaseReview, urlUploadPhaseProgress} {
			t.Run(fmt.Sprintf("%dx%d/url-%d", size[0], size[1], phase), func(t *testing.T) {
				a := screenFixture(size[0], size[1])
				a.buckets.mode = bucketDetail
				m := newURLUpload(nil, "example-bucket", "us-west-2", a.buckets.browsePrefix)
				m.width = size[0]
				m.phase = phase
				m.resolved = httpcopy.Resolved{Key: "sample.txt", BytesTotal: 1024}
				a.buckets.urlUpload = &m
				view := a.View()
				assertFixtureBounds(t, view, size[0], size[1])
				if !strings.Contains(strings.ToLower(ansi.Strip(view)), "cancel") {
					t.Fatalf("URL screen has no cancel action:\n%s", ansi.Strip(view))
				}
			})
		}
		for _, stage := range []shareStage{shareChoose, shareCustom, shareReview, sharePreparing, shareShorter, shareGenerating, shareResult, shareLink} {
			t.Run(fmt.Sprintf("%dx%d/share-%d", size[0], size[1], stage), func(t *testing.T) {
				a := screenFixture(size[0], size[1])
				a.buckets.mode = bucketDetail
				m := newShare(nil, "example-bucket", a.buckets.browsePrefix+"sample.txt", "us-west-2")
				m.stage = stage
				m.choice = 3
				m.plan = &awsClient.PresignedDownloadPlan{AvailableDuration: 30 * time.Minute, KnownCredentialExpiry: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
				m.result = awsClient.PresignedDownload{URL: strings.Repeat("EXAMPLE_LINK_TEXT_", 50), EffectiveDuration: time.Hour, ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
				a.buckets.share = &m
				view := a.View()
				assertFixtureBounds(t, view, size[0], size[1])
				plain := ansi.Strip(view)
				if !strings.Contains(plain, "Esc:") {
					t.Fatalf("share screen has no exit action:\n%s", plain)
				}
				if stage == shareChoose && !strings.Contains(plain, "> Custom") {
					t.Fatalf("selected duration is hidden:\n%s", plain)
				}
			})
		}
	}
}

type fixtureIAM struct{ awsClient.IAMAPI }

func (*fixtureIAM) ListUsers(context.Context, *iam.ListUsersInput, ...func(*iam.Options)) (*iam.ListUsersOutput, error) {
	return &iam.ListUsersOutput{Users: []iamtypes.User{{UserName: awssdk.String("sample-user")}}}, nil
}
func (*fixtureIAM) ListUserTags(context.Context, *iam.ListUserTagsInput, ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	return &iam.ListUserTagsOutput{Tags: []iamtypes.Tag{{Key: awssdk.String("s3m:managed"), Value: awssdk.String("true")}}}, nil
}
func (*fixtureIAM) ListAccessKeys(context.Context, *iam.ListAccessKeysInput, ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	return &iam.ListAccessKeysOutput{}, nil
}

func TestUserLoadFinishesAfterSwitchingToBuckets(t *testing.T) {
	a := NewApp(&awsClient.Client{IAM: &fixtureIAM{}})
	next, cmd := a.Update(key("u"))
	a = next.(App)
	if cmd == nil || a.users.listRequest == 0 {
		t.Fatal("opening users must retain the new request identity")
	}
	next, _ = a.Update(key("b"))
	a = next.(App)
	next, _ = a.Update(cmd())
	a = next.(App)
	if a.screen != screenBuckets || len(a.users.items) != 1 || a.users.items[0].name != "sample-user" || a.users.loading {
		t.Fatal("inactive user result was lost or reopened its screen")
	}
}
