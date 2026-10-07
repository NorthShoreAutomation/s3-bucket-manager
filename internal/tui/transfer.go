package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/progress"
)

var transferIDs atomic.Uint64

type transferReview struct {
	id                       uint64
	upload                   bool
	source, key, name        string
	path                     textinput.Model
	editing, checked, exists bool
	condition                *string
	err                      error
}

type transferCheckedMsg struct {
	id        uint64
	condition *string
	err       error
}

func (m bucketsModel) prepareLocalUpload(source string) (bucketsModel, tea.Cmd) {
	input := textinput.New()
	input.SetValue(m.browsePrefix + filepath.Base(source))
	m.transferReview = &transferReview{id: transferIDs.Add(1), upload: true, source: source, key: input.Value(), name: filepath.Base(source), path: input}
	return m, m.checkUploadDestination()
}

func (m bucketsModel) checkUploadDestination() tea.Cmd {
	review := *m.transferReview
	bucket := m.items[m.cursor]
	return func() tea.Msg {
		condition, err := m.client.ObjectWriteCondition(context.Background(), bucket.name, review.key, bucket.region)
		return transferCheckedMsg{id: review.id, condition: condition, err: err}
	}
}

func (m bucketsModel) prepareDownload(item awsClient.BrowseItem) (bucketsModel, tea.Cmd) {
	destination, err := filepath.Abs(filepath.Base(item.Key))
	if err != nil {
		m.err = err
		return m, nil
	}
	input := textinput.New()
	input.SetValue(destination)
	input.Focus()
	m.transferReview = &transferReview{id: transferIDs.Add(1), key: item.Key, name: item.Name, path: input, editing: true}
	return m, textinput.Blink
}

func (m bucketsModel) updateTransferChecked(msg transferCheckedMsg) (bucketsModel, tea.Cmd) {
	if m.transferReview == nil || msg.id != m.transferReview.id {
		return m, nil
	}
	m.transferReview.condition = msg.condition
	m.transferReview.checked = msg.err == nil
	m.transferReview.exists = msg.condition != nil
	m.transferReview.err = msg.err
	return m, nil
}

func (m bucketsModel) updateTransferReview(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	r := m.transferReview
	if msg.String() == "esc" {
		m.transferReview = nil
		return m, nil
	}
	if r.editing {
		if msg.String() == "enter" {
			if r.path.Value() == "" {
				r.err = fmt.Errorf("enter a destination")
				return m, nil
			}
			r.editing = false
			r.path.Blur()
			r.err = nil
			if r.upload {
				r.key = r.path.Value()
				r.checked = false
				r.id = transferIDs.Add(1)
				return m, m.checkUploadDestination()
			}
			info, err := os.Lstat(r.path.Value())
			r.exists = err == nil
			if err != nil && !os.IsNotExist(err) {
				r.err = err
				return m, nil
			}
			if info != nil && !info.Mode().IsRegular() {
				r.err = fmt.Errorf("destination must be a regular file")
				return m, nil
			}
			r.checked = true
			return m, nil
		}
		var cmd tea.Cmd
		r.path, cmd = r.path.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "r":
		r.editing = true
		r.path.Focus()
		return m, textinput.Blink
	case "enter", "o":
		if !r.checked || r.err != nil || r.exists && msg.String() != "o" {
			return m, nil
		}
		return m.startReviewedTransfer()
	}
	return m, nil
}

func (m bucketsModel) startReviewedTransfer() (bucketsModel, tea.Cmd) {
	r := *m.transferReview
	bucket := m.items[m.cursor]
	snap := &atomic.Pointer[progress.Snapshot]{}
	snap.Store(&progress.Snapshot{Total: -1})
	ctx, cancel := context.WithCancel(context.Background())
	m.transferReview = nil
	m.failedTransferReview = &r
	m.transferSnap = snap
	m.transferCancel = cancel
	m.transferLastTime = time.Now()
	m.transferLastDone = 0
	m.transferRate = 0
	m.err = nil
	m.cancelling = false
	m.transferLabel = "Downloading " + r.name
	if r.upload {
		m.transferLabel = "Uploading " + r.name
	}
	return m, tea.Batch(transferTick(), func() tea.Msg {
		defer cancel()
		if r.upload {
			f, err := os.Open(r.source)
			if err != nil {
				return bucketErrorMsg{err: err, bucket: bucket.name}
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return bucketErrorMsg{err: err, bucket: bucket.name}
			}
			snap.Store(&progress.Snapshot{Total: info.Size()})
			reader := progress.NewReader(f, func(done int64) { snap.Store(&progress.Snapshot{Done: done, Total: info.Size()}) })
			if err := m.client.UploadStreamConditional(ctx, bucket.name, r.key, bucket.region, reader, awsClient.AutoPartSize(info.Size()), 0, r.condition); err != nil {
				return bucketErrorMsg{err: err, bucket: bucket.name}
			}
			return uploadDoneMsg{filename: r.key}
		}
		size, err := m.client.GetObjectSize(ctx, bucket.name, r.key, bucket.region)
		if err != nil {
			size = -1
		}
		snap.Store(&progress.Snapshot{Total: size})
		_, err = awsClient.WriteDownloadSafely(ctx, r.path.Value(), r.exists, func(w io.WriterAt) (int64, error) {
			writer := progress.NewWriterAt(w, func(done int64) { snap.Store(&progress.Snapshot{Done: done, Total: size}) })
			return m.client.DownloadFile(ctx, bucket.name, r.key, bucket.region, writer, 0, 0)
		})
		if err != nil {
			return bucketErrorMsg{err: err, bucket: bucket.name}
		}
		return downloadDoneMsg{filename: r.name, path: r.path.Value()}
	})
}

func (m bucketsModel) viewTransferReview() string {
	r := m.transferReview
	title := "Download file"
	destination := r.path.View()
	footer := "Enter: Review destination  Esc: Cancel"
	if r.upload {
		title = "Upload file"
	}
	body := "Destination: " + destination + "\n"
	if r.upload {
		body += "Source: " + r.source + "\nBucket: " + m.currentBucketName() + "\n"
	} else {
		body += "Source: s3://" + m.currentBucketName() + "/" + r.key + "\n"
	}
	if r.err != nil {
		body += "Error: " + r.err.Error() + "\nUse r to edit and retry."
	} else if !r.editing && !r.checked {
		body += "Checking destination..."
	}
	if !r.editing && r.checked {
		footer = "Enter: Transfer  r: Rename  Esc: Cancel"
		if r.exists {
			body += "Destination exists. Overwrite replaces its current content."
			footer = "o: Overwrite  r: Rename  Esc: Cancel"
		}
	}
	return renderPanel(title, body, footer, m.width, m.height)
}
