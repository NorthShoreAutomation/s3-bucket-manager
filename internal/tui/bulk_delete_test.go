package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

func TestBulkDeletePreservesCompletedCountAfterStop(t *testing.T) {
	for _, kind := range []string{"folder", "selection"} {
		for _, failure := range []string{"cancel", "service"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				app := NewApp(&awsClient.Client{})
				app.width, app.height = 100, 28
				app.buckets.width, app.buckets.height = 100, 25
				app.buckets.items = []bucketItem{{name: "bucket-a", region: "us-west-2"}}
				app.buckets.mode = bucketDetail
				app.buckets.bulkDeleting = true
				app.buckets.bulkDeleteCancel = func() {}
				app.buckets.loading = true
				step := func(msg tea.Msg) tea.Cmd {
					next, cmd := app.Update(msg)
					app = next.(App)
					return cmd
				}
				if kind == "folder" {
					step(folderDeleteProgressMsg{deleted: 1200})
				} else {
					step(selectionDeleteProgressMsg{deleted: 1200})
				}
				if !strings.Contains(app.View(), "1,200 objects removed") {
					t.Fatal("progress count must be visible before failure")
				}
				failureErr := errors.New("service rejected deletion")
				if failure == "cancel" {
					failureErr = fmt.Errorf("could not list objects: %w", context.Canceled)
				}
				// Do not execute the returned refresh command or any cloud operation.
				if step(bucketErrorMsg{err: failureErr, bucket: "bucket-a"}) == nil {
					t.Fatal("failure must queue browser refresh")
				}
				if app.buckets.bulkDeleting || !app.buckets.loading || app.buckets.deleteProgress != "" {
					t.Fatal("expected stopped delete with cleared progress and pending refresh")
				}
				if !strings.Contains(app.View(), "1,200 objects removed") {
					t.Fatal("completed-deletion count must survive in the rendered failure state")
				}
				step(browseLoadedMsg{bucket: "bucket-a", request: app.buckets.browseRequests.Load()})
				if !strings.Contains(app.View(), "1,200 objects removed") {
					t.Fatal("completed-deletion count must remain visible after refresh")
				}
				if app.buckets.loading || app.buckets.err != nil {
					t.Fatal("expected successful browse refresh to clear busy state and error")
				}
			})
		}
	}
}
