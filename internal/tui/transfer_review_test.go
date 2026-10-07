package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

func TestDownloadReviewDoesNotWriteOrReplaceBeforeConsent(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(dest, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket"}}
	m.mode = bucketDetail
	m, _ = m.prepareDownload(awsClient.BrowseItem{Name: "report.txt", Key: "report.txt"})
	m.transferReview.path.SetValue(dest)
	m, cmd := m.updateTransferReview(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.transferReview.exists || m.transferSnap != nil {
		t.Fatal("existing path did not stop for consent")
	}
	m, cmd = m.updateTransferReview(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.transferSnap != nil {
		t.Fatal("Enter approved an overwrite")
	}
	m, _ = m.updateTransferReview(tea.KeyMsg{Type: tea.KeyEsc})
	if m.transferReview != nil {
		t.Fatal("cancel did not close preview")
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "keep" {
		t.Fatal("review changed original")
	}
}

func TestUploadCheckFailurePreservesSourceAndCannotStart(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket"}}
	m.mode = bucketDetail
	m, _ = m.prepareLocalUpload("/local/report.txt")
	m, _ = m.updateTransferChecked(transferCheckedMsg{id: m.transferReview.id, err: os.ErrPermission})
	m, cmd := m.updateTransferReview(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.transferSnap != nil || m.transferReview.source != "/local/report.txt" {
		t.Fatal("unknown destination allowed mutation or lost source")
	}
}
