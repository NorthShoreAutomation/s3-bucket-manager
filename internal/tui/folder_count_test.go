package tui

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

var errFolderReviewStop = errors.New("scratch probe stops before any deletion")

type folderCountReviewS3 struct {
	awsClient.S3API
	t            *testing.T
	probe        bool
	readErr      error
	countBucket  string
	deleteBucket string
	deletePrefix string
}

func (s *folderCountReviewS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if s.probe {
		s.deleteBucket = awssdk.ToString(in.Bucket)
		s.deletePrefix = awssdk.ToString(in.Prefix)
		return nil, errFolderReviewStop
	}
	if s.readErr != nil {
		return nil, s.readErr
	}
	s.countBucket = awssdk.ToString(in.Bucket)
	return &s3.ListObjectsV2Output{KeyCount: awssdk.Int32(3)}, nil
}

func (s *folderCountReviewS3) GetBucketPolicy(_ context.Context, _ *s3.GetBucketPolicyInput, _ ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	return &s3.GetBucketPolicyOutput{Policy: awssdk.String(`{"Version":"2012-10-17","Statement":[]}`)}, nil
}

func (s *folderCountReviewS3) DeleteObjects(_ context.Context, _ *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	s.t.Fatal("destructive method must never run in this probe")
	return nil, errFolderReviewStop
}

func TestStaleFolderCountCannotOpenDeleteInAnotherBucket(t *testing.T) {
	stub := &folderCountReviewS3{t: t}
	app := NewApp(&awsClient.Client{S3: stub})
	app.buckets.items = []bucketItem{{name: "bucket-a", region: "us-west-2"}, {name: "bucket-b", region: "us-west-2"}}
	app.buckets.fullBuckets = append([]bucketItem(nil), app.buckets.items...)
	app.buckets.mode = bucketDetail
	app.buckets.loading = false
	app.buckets.width = 100
	app.buckets.height = 25
	app.buckets.browseItems = []awsClient.BrowseItem{{Name: "folder/", Key: "folder/", IsFolder: true}}
	step := func(msg tea.Msg) tea.Cmd {
		next, cmd := app.Update(msg)
		app = next.(App)
		return cmd
	}
	runes := func(value string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)} }

	countBatch := step(runes("d"))
	if countBatch == nil || !app.buckets.loading || app.buckets.mode != bucketDetail {
		t.Fatal("d must start count while staying in bucket detail")
	}
	batch, ok := countBatch().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("expected spinner and count commands, got %#v", batch)
	}
	// Execute only mocked reads and hold the result to model delayed delivery.
	lateCount, ok := batch[1]().(folderCountedMsg)
	if !ok || stub.countBucket != "bucket-a" || lateCount.count != 3 {
		t.Fatalf("expected actual count command result from A, got %#v, bucket=%q", lateCount, stub.countBucket)
	}
	t.Log("1. d in bucket-a/folder/ starts count; hold actual folderCountedMsg(count=3)")

	step(tea.KeyMsg{Type: tea.KeyEsc})
	if app.buckets.mode != bucketsList || !app.buckets.loading {
		t.Fatal("Escape must return to list while loading remains true")
	}
	step(tea.KeyMsg{Type: tea.KeyDown})
	if app.buckets.cursor != 0 {
		t.Fatal("direct navigation should be blocked while loading")
	}
	t.Log("2. Escape returns to list with loading=true; Down is blocked")

	if step(runes("r")) == nil {
		t.Fatal("refresh must be allowed while loading")
	}
	step(bucketsLoadedMsg{buckets: append([]bucketItem(nil), app.buckets.items...), request: app.buckets.listRequests.Load()})
	if app.buckets.loading {
		t.Fatal("matching list refresh result must clear loading")
	}
	t.Log("3. r plus current bucketsLoadedMsg clears loading before old count arrives")

	step(tea.KeyMsg{Type: tea.KeyDown})
	step(tea.KeyMsg{Type: tea.KeyEnter})
	if app.buckets.currentBucketName() != "bucket-b" || app.buckets.mode != bucketDetail {
		t.Fatal("Down then Enter must open B")
	}
	step(browseLoadedMsg{bucket: "bucket-b", request: app.buckets.browseRequests.Load(), items: []awsClient.BrowseItem{{Name: "other.txt", Key: "other.txt"}}})
	t.Log("4. Down then Enter opens bucket-b; current browseLoadedMsg finishes B listing")

	step(lateCount)
	if app.buckets.mode != bucketDetail || app.buckets.currentBucketName() != "bucket-b" || app.buckets.folderDeleteKey != "" {
		t.Fatalf("obsolete folder count changed current bucket state: mode=%v bucket=%s target=%s", app.buckets.mode, app.buckets.currentBucketName(), app.buckets.folderDeleteKey)
	}
}

func TestFolderCountResultsAndFailuresIgnoreNewerBrowse(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			stub := &folderCountReviewS3{t: t}
			m := newBucketsModel(&awsClient.Client{S3: stub})
			m.loading = false
			m.mode = bucketDetail
			m.items = []bucketItem{{name: "bucket-a", region: "us-west-2"}}
			m.browseItems = []awsClient.BrowseItem{{Name: "folder/", Key: "folder/", IsFolder: true}}
			m, cmd := m.updateBrowse(key("d"))
			batch := cmd().(tea.BatchMsg)
			if failure {
				stub.readErr = errFolderReviewStop
			}
			result := batch[1]()
			nextRequest(m.browseRequests)
			m.browsePrefix = "other/"
			m.loading = true
			m, _ = m.update(result)
			if !m.loading || m.mode != bucketDetail || m.err != nil || m.folderDeleteKey != "" {
				t.Fatalf("obsolete folder count result changed newer browse: mode=%v loading=%v err=%v target=%s", m.mode, m.loading, m.err, m.folderDeleteKey)
			}
		})
	}
}

func TestFolderCountKeepsCurrentDeletionWorking(t *testing.T) {
	stub := &folderCountReviewS3{t: t}
	m := newBucketsModel(&awsClient.Client{S3: stub})
	m.loading = false
	m.mode = bucketDetail
	m.items = []bucketItem{{name: "bucket-a", region: "us-west-2"}}
	m.browseItems = []awsClient.BrowseItem{{Name: "folder/", Key: "folder/", IsFolder: true}}
	m, cmd := m.updateBrowse(key("d"))
	batch := cmd().(tea.BatchMsg)
	m, _ = m.update(batch[1]())
	if m.mode != bucketDetailDeleteFolder || m.folderDeleteKey != "folder/" || m.folderDeleteCnt != 3 {
		t.Fatal("current count should open the correct delete review")
	}
	m.deleteInput.SetValue("delete")
	m, cmd = m.updateDeleteFolder(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.bulkDeleting {
		t.Fatal("confirmed current folder should queue deletion")
	}
	stub.probe = true
	result, ok := cmd().(bucketErrorMsg)
	if !ok || !errors.Is(result.err, errFolderReviewStop) || stub.deleteBucket != "bucket-a" || stub.deletePrefix != "folder/" {
		t.Fatalf("wrong captured deletion target: bucket=%s prefix=%s result=%#v", stub.deleteBucket, stub.deletePrefix, result)
	}
}
