package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/httpcopy"
)

// urlUploadPhase tracks which visual phase the URL upload modal is in.
type urlUploadPhase int

const (
	urlUploadPhaseInput    urlUploadPhase = iota // URL + key text inputs
	urlUploadPhaseProgress                       // spinner / progress bar
)

// urlUploadDoneMsg is emitted when the upload completes successfully.
type urlUploadDoneMsg struct {
	Key   string
	Bytes int64
}

// urlUploadErrMsg is emitted on failure (including cancellation).
// The Err.Error() string contains "cancelled" when the user pressed Ctrl-C or Esc.
type urlUploadErrMsg struct {
	Err error
}

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
	aws    *awsClient.Client
	bucket string
	region string
	prefix string // current S3 prefix (used to build the default key suffix)

	// UI state
	phase       urlUploadPhase
	urlInput    textinput.Model
	keyInput    textinput.Model
	activeInput int // 0 = URL, 1 = key

	// Progress phase
	spinner spinner.Model
	bar     progress.Model
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

var urlUploadLabelStyle = lipgloss.NewStyle().Foreground(colorMuted)

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

	bar := progress.New(
		progress.WithDefaultGradient(),
		progress.WithoutPercentage(), // we render our own status line
	)
	bar.Width = 60

	return urlUploadModel{
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

// Init starts the cursor blink for the URL input.
func (m urlUploadModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update routes messages to the active phase handler.
func (m urlUploadModel) Update(msg tea.Msg) (urlUploadModel, tea.Cmd) {
	switch m.phase {
	case urlUploadPhaseInput:
		return m.updateInput(msg)
	case urlUploadPhaseProgress:
		return m.updateProgress(msg)
	}
	return m, nil
}

func (m urlUploadModel) updateInput(msg tea.Msg) (urlUploadModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg {
				return urlUploadErrMsg{Err: fmt.Errorf("cancelled")}
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
				return m, nil // no URL yet — ignore
			}
			key := strings.TrimSpace(m.keyInput.Value())
			if key == "" {
				// Pass the current prefix so httpcopy appends the derived filename.
				key = m.prefix
			}

			// Allocate shared snapshot slot on the heap so the goroutine and
			// the (value-copied) model can both reference the same atomic.
			shared := &sharedSnap{}
			initialSnap := &progressSnapshot{phase: "resolving", total: -1}
			shared.ptr.Store(initialSnap)
			m.snap = shared

			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			m.phase = urlUploadPhaseProgress
			m.lastTime = time.Now()

			// Capture by value so the goroutine closure is self-contained.
			uploader := m.aws
			bucket := m.bucket
			region := m.region

			runCmd := func() tea.Msg {
				finalKey, err := httpcopy.Run(ctx, uploader, httpcopy.Options{
					URL:    rawURL,
					Bucket: bucket,
					Key:    key,
					Region: region,
					Progress: func(p httpcopy.Progress) {
						shared.ptr.Store(&progressSnapshot{
							done:  p.BytesDone,
							total: p.BytesTotal,
							phase: p.Phase,
							key:   p.Filename,
						})
					},
				})
				cancel()
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return urlUploadErrMsg{Err: fmt.Errorf("cancelled")}
					}
					return urlUploadErrMsg{Err: err}
				}
				snap := shared.ptr.Load()
				bytes := int64(0)
				if snap != nil {
					bytes = snap.done
				}
				return urlUploadDoneMsg{Key: finalKey, Bytes: bytes}
			}

			tickCmd := tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
				return urlUploadProgressTickMsg{}
			})

			return m, tea.Batch(m.spinner.Tick, runCmd, tickCmd)
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

func (m urlUploadModel) updateProgress(msg tea.Msg) (urlUploadModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
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

	case progress.FrameMsg:
		model, cmd := m.bar.Update(msg)
		m.bar = model.(progress.Model)
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
	}
	return ""
}

func (m urlUploadModel) viewInput() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("s3://%s/%s", m.bucket, m.prefix)) + "\n"
	s += screenTitleStyle.Render("Upload from URL") + "\n"
	s += separator(m.viewWidth()) + "\n\n"

	s += "  " + urlUploadLabelStyle.Render("URL") + "\n"
	s += "  " + m.urlInput.View() + "\n\n"

	s += "  " + urlUploadLabelStyle.Render("S3 Key") + " " + dimStyle.Render("(optional)") + "\n"
	s += "  " + m.keyInput.View() + "\n\n"

	s += helpStyle.Render("  [enter] upload  [tab] switch field  [esc] cancel")
	return s
}

func (m urlUploadModel) viewProgress() string {
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

	s := breadcrumbStyle.Render(dest) + "\n"
	s += screenTitleStyle.Render("Uploading…") + "\n"
	s += separator(m.viewWidth()) + "\n\n"

	if phase == "resolving" {
		s += "  " + m.spinner.View() + " " + dimStyle.Render("Resolving URL…") + "\n\n"
	} else {
		s += "  " + m.bar.View() + "\n\n"
		if snap != nil {
			s += "  " + m.statusLine(snap) + "\n\n"
		}
	}

	s += helpStyle.Render("  [ctrl+c] cancel")
	return s
}

// statusLine renders a human-readable progress description.
// Format: "<done> / <total> (<pct>%) — <rate> — ETA <eta>"
// When total is unknown: "<done> — <rate>"
func (m urlUploadModel) statusLine(snap *progressSnapshot) string {
	done := formatSize(snap.done)
	rate := formatRate(m.currentRate)

	if snap.total <= 0 {
		return dimStyle.Render(fmt.Sprintf("%s — %s", done, rate))
	}

	pct := float64(snap.done) / float64(snap.total) * 100
	total := formatSize(snap.total)

	eta := ""
	rateVal := parseRateBytesPerSec(rate)
	if rateVal > 0 {
		remaining := float64(snap.total-snap.done) / rateVal
		eta = " — ETA " + formatDuration(time.Duration(remaining*float64(time.Second)))
	}

	return dimStyle.Render(fmt.Sprintf("%s / %s (%.0f%%) — %s%s", done, total, pct, rate, eta))
}

func (m urlUploadModel) viewWidth() int {
	if m.width > 10 {
		return m.width - 4
	}
	return 60
}

// formatRate formats bytes/s into a human-readable rate string like "12.3 MB/s".
func formatRate(bps float64) string {
	if bps <= 0 {
		return "—/s"
	}
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case bps >= gb:
		return fmt.Sprintf("%.1f GB/s", bps/gb)
	case bps >= mb:
		return fmt.Sprintf("%.1f MB/s", bps/mb)
	case bps >= kb:
		return fmt.Sprintf("%.1f KB/s", bps/kb)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

// parseRateBytesPerSec is a quick reverse-parse of formatRate output so the
// ETA calculation can use the displayed rate rather than carrying a raw float.
// Returns 0 if the string cannot be parsed or is "—/s".
func parseRateBytesPerSec(r string) float64 {
	if r == "—/s" || r == "" {
		return 0
	}
	var val float64
	var unit string
	if _, err := fmt.Sscanf(r, "%f %s", &val, &unit); err != nil {
		return 0
	}
	switch {
	case strings.HasPrefix(unit, "GB"):
		return val * (1 << 30)
	case strings.HasPrefix(unit, "MB"):
		return val * (1 << 20)
	case strings.HasPrefix(unit, "KB"):
		return val * (1 << 10)
	default:
		return val
	}
}

// formatDuration renders a duration as "1h23m", "4m12s", or "45s".
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	min := d / time.Minute
	d -= min * time.Minute
	sec := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, min)
	}
	if min > 0 {
		return fmt.Sprintf("%dm%02ds", min, sec)
	}
	return fmt.Sprintf("%ds", sec)
}
