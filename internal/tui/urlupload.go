package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	bubprogress "github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/httpcopy"
	"github.com/dcorbell/s3m/internal/progress"
)

// urlUploadPhase tracks which visual phase the URL upload modal is in.
type urlUploadPhase int

const (
	urlUploadPhaseInput    urlUploadPhase = iota // URL + key text inputs
	urlUploadPhaseProgress                       // spinner / progress bar
	urlUploadPhaseResolve
	urlUploadPhaseReview
)

// urlUploadDoneMsg is emitted when the upload completes successfully.
type urlUploadDoneMsg struct {
	ID    uint64
	Key   string
	Bytes int64
}

// urlUploadErrMsg is emitted on failure (including cancellation).
// The Err.Error() string contains "cancelled" when the user pressed Ctrl-C or Esc.
type urlUploadErrMsg struct {
	ID  uint64
	Err error
}

type urlUploadResolvedMsg struct {
	ID        uint64
	resolved  httpcopy.Resolved
	condition *string
}
type urlUploadResolveFailedMsg struct {
	ID  uint64
	err error
}

var urlUploadIDs atomic.Uint64

// urlUploadProgressTickMsg is emitted by the 4Hz ticker to pull the latest
// progress snapshot into the Bubble Tea update loop.
type urlUploadProgressTickMsg struct{}

// progressSnapshot holds the latest values written by the httpcopy goroutine
// and read by the TUI tick.  We store it behind an atomic.Pointer so no mutex
// is needed across the goroutine boundary.
type progressSnapshot struct {
	done  int64
	total int64  // -1 when unknown
	phase string // "resolving" | "uploading" | "done"
	key   string // derived S3 key (set once filename is known)
}

// sharedSnap is allocated on the heap so the goroutine and the value-copied
// model can both reference the same atomic slot.
type sharedSnap struct {
	ptr atomic.Pointer[progressSnapshot]
}

// urlUploadModel is a self-contained Bubble Tea sub-model for URL upload.
// The parent (bucketsModel) delegates Update and View to it while non-nil,
// and listens for urlUploadDoneMsg / urlUploadErrMsg to return to browsing.
type urlUploadModel struct {
	// configuration
	id         uint64
	resolved   httpcopy.Resolved
	condition  *string
	inputError string
	cancelling bool
	aws        *awsClient.Client
	bucket     string
	region     string
	prefix     string // current S3 prefix (used to build the default key suffix)

	// UI state
	phase       urlUploadPhase
	urlInput    textinput.Model
	keyInput    textinput.Model
	activeInput int // 0 = URL, 1 = key

	// Progress phase
	spinner spinner.Model
	bar     bubprogress.Model
	cancel  context.CancelFunc
	snap    *sharedSnap // shared with the goroutine

	// Rate tracking (updated on each tick)
	lastDone    int64
	lastTime    time.Time
	currentRate float64

	// Cached last-seen snapshot for View()
	lastSnap *progressSnapshot

	width int
}

// newURLUpload returns a configured urlUploadModel ready for Init / Update / View.
func newURLUpload(client *awsClient.Client, bucket, region, currentPrefix string) urlUploadModel {
	urlIn := textinput.New()
	urlIn.Placeholder = "Paste HTTPS or WeTransfer URL…"
	urlIn.CharLimit = 2048
	urlIn.Focus()

	keyIn := textinput.New()
	keyIn.Placeholder = "Key (blank = current prefix + filename)"
	keyIn.CharLimit = 1024

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	bar := bubprogress.New(
		bubprogress.WithDefaultGradient(),
		bubprogress.WithoutPercentage(), // we render our own status line
	)
	bar.Width = 60

	return urlUploadModel{
		id:          urlUploadIDs.Add(1),
		aws:         client,
		bucket:      bucket,
		region:      region,
		prefix:      currentPrefix,
		phase:       urlUploadPhaseInput,
		urlInput:    urlIn,
		keyInput:    keyIn,
		activeInput: 0,
		spinner:     sp,
		bar:         bar,
	}
}

// Active reports remote work that must acknowledge cancellation before dismissal.
func (m urlUploadModel) Active() bool {
	return m.phase == urlUploadPhaseResolve || m.phase == urlUploadPhaseProgress
}

// Init starts the cursor blink for the URL input.
func (m urlUploadModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update routes messages to the active phase handler.
func (m urlUploadModel) Update(msg tea.Msg) (urlUploadModel, tea.Cmd) {
	switch event := msg.(type) {
	case urlUploadResolvedMsg:
		if event.ID != m.id {
			return m, nil
		}
		if m.cancelling {
			return m, func() tea.Msg { return urlUploadErrMsg{ID: m.id, Err: fmt.Errorf("cancelled")} }
		}
		m.resolved = event.resolved
		m.condition = event.condition
		m.phase = urlUploadPhaseReview
		m.cancel = nil
		return m, nil
	case urlUploadResolveFailedMsg:
		if event.ID != m.id {
			return m, nil
		}
		if m.cancelling {
			return m, func() tea.Msg { return urlUploadErrMsg{ID: m.id, Err: fmt.Errorf("cancelled")} }
		}
		m.phase = urlUploadPhaseInput
		m.inputError = event.err.Error()
		m.cancel = nil
		m.cancelling = false
		return m, textinput.Blink
	}
	switch m.phase {
	case urlUploadPhaseInput:
		return m.updateInput(msg)
	case urlUploadPhaseProgress:
		return m.updateProgress(msg)
	case urlUploadPhaseResolve:
		if key, ok := msg.(tea.KeyMsg); ok && (key.String() == "esc" || key.String() == "ctrl+c") {
			if m.cancel != nil {
				m.cancel()
			}
			m.cancelling = true
			return m, nil
		}
		if _, ok := msg.(spinner.TickMsg); ok {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case urlUploadPhaseReview:
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc", "ctrl+c":
				return m, func() tea.Msg { return urlUploadErrMsg{ID: m.id, Err: fmt.Errorf("cancelled")} }
			case "r":
				m.phase = urlUploadPhaseInput
				m.activeInput = 1
				m.keyInput.SetValue(m.resolved.Key)
				m.urlInput.Blur()
				m.keyInput.Focus()
				return m, textinput.Blink
			case "enter":
				if m.condition == nil {
					return m.startUpload()
				}
			case "o":
				if m.condition != nil {
					return m.startUpload()
				}
			}
		}
	}
	return m, nil
}

func (m urlUploadModel) updateInput(msg tea.Msg) (urlUploadModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, func() tea.Msg {
				return urlUploadErrMsg{ID: m.id, Err: fmt.Errorf("cancelled")}
			}

		case "tab":
			m.activeInput = 1 - m.activeInput
			if m.activeInput == 0 {
				m.urlInput.Focus()
				m.keyInput.Blur()
			} else {
				m.urlInput.Blur()
				m.keyInput.Focus()
			}
			return m, textinput.Blink

		case "enter":
			rawURL := strings.TrimSpace(m.urlInput.Value())
			if rawURL == "" {
				return m, nil // no URL yet - ignore
			}
			key := strings.TrimSpace(m.keyInput.Value())
			if key == "" {
				// Pass the current prefix so httpcopy appends the derived filename.
				key = m.prefix
			}

			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			m.phase = urlUploadPhaseResolve
			m.inputError = ""
			m.cancelling = false
			uploader, bucket, region, id := m.aws, m.bucket, m.region, m.id
			return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
				defer cancel()
				resolved, err := httpcopy.Resolve(ctx, httpcopy.Options{URL: rawURL, Key: key})
				if err != nil {
					return urlUploadResolveFailedMsg{ID: id, err: err}
				}
				condition, err := uploader.ObjectWriteCondition(ctx, bucket, resolved.Key, region)
				if err != nil {
					return urlUploadResolveFailedMsg{ID: id, err: err}
				}
				return urlUploadResolvedMsg{ID: id, resolved: resolved, condition: condition}
			})
		}
	}

	// Forward key/mouse events to the focused text input.
	var cmd tea.Cmd
	if m.activeInput == 0 {
		m.urlInput, cmd = m.urlInput.Update(msg)
	} else {
		m.keyInput, cmd = m.keyInput.Update(msg)
	}
	return m, cmd
}

// failed preserves editable inputs after a rejected or failed upload.
func (m urlUploadModel) failed(err error) urlUploadModel {
	m.phase = urlUploadPhaseInput
	m.inputError = err.Error()
	m.cancel = nil
	m.cancelling = false
	m.snap = nil
	m.lastSnap = nil
	if m.activeInput == 0 {
		m.urlInput.Focus()
		m.keyInput.Blur()
	} else {
		m.urlInput.Blur()
		m.keyInput.Focus()
	}
	return m
}

func (m urlUploadModel) startUpload() (urlUploadModel, tea.Cmd) {
	shared := &sharedSnap{}
	shared.ptr.Store(&progressSnapshot{phase: "uploading", total: m.resolved.BytesTotal, key: m.resolved.Key})
	m.snap = shared
	m.lastSnap = shared.ptr.Load()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.phase = urlUploadPhaseProgress
	m.lastTime = time.Now()
	m.cancelling = false
	uploader, bucket, region, id, resolved, condition := m.aws, m.bucket, m.region, m.id, m.resolved, m.condition
	run := func() tea.Msg {
		defer cancel()
		key, err := httpcopy.Run(ctx, uploader, httpcopy.Options{URL: resolved.URL, Bucket: bucket, Key: resolved.Key, Region: region, Conditional: true, Condition: condition, Progress: func(p httpcopy.Progress) {
			shared.ptr.Store(&progressSnapshot{done: p.BytesDone, total: p.BytesTotal, phase: p.Phase, key: p.Filename})
		}})
		if err != nil {
			if errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "cleanup failed") {
				err = fmt.Errorf("cancelled")
			}
			return urlUploadErrMsg{ID: id, Err: err}
		}
		snap := shared.ptr.Load()
		var bytes int64
		if snap != nil {
			bytes = snap.done
		}
		return urlUploadDoneMsg{ID: id, Key: key, Bytes: bytes}
	}
	return m, tea.Batch(m.spinner.Tick, run, tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return urlUploadProgressTickMsg{} }))
}

func (m urlUploadModel) updateProgress(msg tea.Msg) (urlUploadModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.cancelling = true
			if m.cancel != nil {
				m.cancel()
			}
			// The run goroutine will return urlUploadErrMsg with a context error.
			// We wrap it so the parent can detect "cancelled" in the message.
			return m, nil
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case bubprogress.FrameMsg:
		model, cmd := m.bar.Update(msg)
		m.bar = model.(bubprogress.Model)
		return m, cmd

	case urlUploadProgressTickMsg:
		if m.snap == nil {
			return m, nil
		}
		snap := m.snap.ptr.Load()
		if snap != nil {
			now := time.Now()
			elapsed := now.Sub(m.lastTime).Seconds()
			delta := snap.done - m.lastDone
			if elapsed > 0.01 && delta > 0 {
				m.currentRate = float64(delta) / elapsed
			}
			m.lastDone = snap.done
			m.lastTime = now
			m.lastSnap = snap

			// Advance the progress bar when total is known.
			var barCmd tea.Cmd
			if snap.total > 0 {
				pct := float64(snap.done) / float64(snap.total)
				if pct > 1.0 {
					pct = 1.0
				}
				barCmd = m.bar.SetPercent(pct)
			}

			nextTick := tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
				return urlUploadProgressTickMsg{}
			})
			return m, tea.Batch(barCmd, nextTick)
		}

		nextTick := tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
			return urlUploadProgressTickMsg{}
		})
		return m, nextTick
	}

	return m, nil
}

// View renders the URL upload modal.
func (m urlUploadModel) View() string {
	switch m.phase {
	case urlUploadPhaseInput:
		return m.viewInput()
	case urlUploadPhaseProgress:
		return m.viewProgress()
	case urlUploadPhaseResolve:
		label := "Resolving source and checking destination..."
		if m.cancelling {
			label = "Cancelling..."
		}
		return screenTitleStyle.Render("Upload from URL") + "\n" + m.spinner.View() + " " + label + "\n" + helpStyle.Render("[esc] Cancel")
	case urlUploadPhaseReview:
		s := screenTitleStyle.Render("Review upload") + "\n" + truncate("Destination: s3://"+m.bucket+"/"+m.resolved.Key, m.viewWidth()) + "\n"
		if m.resolved.BytesTotal >= 0 {
			s += "Size: " + formatSize(m.resolved.BytesTotal) + "\n"
		}
		if m.condition != nil {
			s += "An object already exists at this destination.\n" + helpStyle.Render("[o] Overwrite  [r] Rename  [esc] Cancel")
		} else {
			s += helpStyle.Render("[enter] Upload  [r] Rename  [esc] Cancel")
		}
		return s
	}
	return ""
}

func (m urlUploadModel) viewInput() string {
	m.urlInput.Width = max(8, m.viewWidth()-4)
	m.keyInput.Width = max(8, m.viewWidth()-4)
	s := screenTitleStyle.Render("Upload from URL") + "\n"
	s += truncate("Destination: s3://"+m.bucket+"/"+m.prefix, m.viewWidth()) + "\n"
	s += "URL:\n" + m.urlInput.View() + "\n"
	s += "Destination filename or path (optional):\n" + m.keyInput.View() + "\n"
	if m.inputError != "" {
		s += errorStyle.Render(truncate(m.inputError, m.viewWidth())) + "\n"
	}
	s += helpStyle.Render("enter: review destination  tab: next field  esc: cancel")
	return s
}

func (m urlUploadModel) viewProgress() string {
	m.bar.Width = max(8, m.viewWidth()-4)
	snap := m.lastSnap
	phase := "resolving"
	key := ""
	if snap != nil {
		phase = snap.phase
		key = snap.key
	}

	dest := fmt.Sprintf("s3://%s/", m.bucket)
	if key != "" {
		dest = fmt.Sprintf("s3://%s/%s", m.bucket, key)
	}

	s := breadcrumbStyle.Render(truncate(dest, m.viewWidth())) + "\n"
	title := "Uploading..."
	if m.cancelling {
		title = "Cancelling..."
	}
	s += screenTitleStyle.Render(title) + "\n"
	s += separator(m.viewWidth()) + "\n\n"

	if phase == "resolving" {
		s += "  " + m.spinner.View() + " " + dimStyle.Render("Resolving URL…") + "\n\n"
	} else {
		s += "  " + m.bar.View() + "\n\n"
		if snap != nil {
			s += "  " + m.statusLine(snap) + "\n\n"
		}
	}

	s += helpStyle.Render("  [esc / ctrl+c] cancel")
	return s
}

// statusLine renders a human-readable progress description.
// Format: "<done> / <total> (<pct>%) - <rate> - ETA <eta>"
// When total is unknown: "<done> - <rate>"
func (m urlUploadModel) statusLine(snap *progressSnapshot) string {
	done := formatSize(snap.done)
	rate := progress.FormatRate(m.currentRate)

	if snap.total <= 0 {
		return dimStyle.Render(fmt.Sprintf("%s - %s", done, rate))
	}

	pct := float64(snap.done) / float64(snap.total) * 100
	total := formatSize(snap.total)

	eta := ""
	rateVal := progress.ParseRateBytesPerSec(rate)
	if rateVal > 0 {
		remaining := float64(snap.total-snap.done) / rateVal
		eta = " - ETA " + progress.FormatDuration(time.Duration(remaining*float64(time.Second)))
	}

	return dimStyle.Render(fmt.Sprintf("%s / %s (%.0f%%) - %s%s", done, total, pct, rate, eta))
}

func (m urlUploadModel) viewWidth() int {
	if m.width > 10 {
		return m.width - 4
	}
	return 60
}
