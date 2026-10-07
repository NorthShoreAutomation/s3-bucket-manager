package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type shareStage uint8

const (
	shareChoose shareStage = iota
	shareCustom
	shareReview
	sharePreparing
	shareShorter
	shareGenerating
	shareResult
	shareLink
)

var shareRequestSequence atomic.Uint64

type sharePreparedMsg struct {
	request uint64
	plan    *awsClient.PresignedDownloadPlan
	err     error
}

type shareGeneratedMsg struct {
	request uint64
	result  awsClient.PresignedDownload
	err     error
}

type shareClosedMsg struct{}

// shareModel owns the temporary-link dialog and every key typed inside it.
type shareModel struct {
	client      *awsClient.Client
	bucket      string
	key         string
	region      string
	stage       shareStage
	active      bool
	choice      int
	duration    time.Duration
	customInput textinput.Model
	plan        *awsClient.PresignedDownloadPlan
	result      awsClient.PresignedDownload
	copyText    func(string) error
	copyStatus  string
	errText     string
	bodyOffset  int
	linkOffset  int
	request     uint64
	cancel      context.CancelFunc
}

func newShare(client *awsClient.Client, bucket, key, region string) shareModel {
	input := textinput.New()
	input.Placeholder = "30m, 2h, or 3d"
	input.CharLimit = 24
	return shareModel{
		client: client, bucket: bucket, key: key, region: region,
		stage: shareChoose, active: true, duration: time.Hour,
		customInput: input, copyText: clipboard.WriteAll,
	}
}

func (m shareModel) Init() tea.Cmd { return nil }

func (m shareModel) OwnsInput() bool { return m.active }

func (m shareModel) Update(msg tea.Msg) (shareModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	switch typed := msg.(type) {
	case sharePreparedMsg:
		if typed.request != m.request || m.stage != sharePreparing {
			return m, nil
		}
		if m.cancel != nil {
			m.cancel()
		}
		m.cancel = nil
		if typed.err != nil {
			m.errText = typed.err.Error()
			m.stage = shareReview
			return m, nil
		}
		if typed.plan == nil {
			m.errText = "Could not prepare the download link. Try again."
			m.stage = shareReview
			return m, nil
		}
		m.plan = typed.plan
		if typed.plan.NeedsShorterDuration {
			m.stage = shareShorter
			return m, nil
		}
		return m.beginGenerate(false)
	case shareGeneratedMsg:
		if typed.request != m.request || m.stage != shareGenerating {
			return m, nil
		}
		if m.cancel != nil {
			m.cancel()
		}
		m.cancel = nil
		if typed.err != nil {
			if errors.Is(typed.err, awsClient.ErrPresignedDurationNeedsConfirmation) {
				return m.beginPrepare()
			}
			m.errText = typed.err.Error()
			m.stage = shareReview
			return m, nil
		}
		m.result = typed.result
		m.plan = nil
		m.stage = shareResult
		m.copyLink()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(typed)
	default:
		return m, nil
	}
}

func (m shareModel) updateKey(key tea.KeyMsg) (shareModel, tea.Cmd) {
	pressed := key.String()
	if pressed == "ctrl+c" || pressed == "esc" && m.stage != shareLink && m.stage != shareCustom && m.stage != shareReview {
		return m.close()
	}
	switch m.stage {
	case shareChoose:
		switch pressed {
		case "up", "k":
			m.choice = max(0, m.choice-1)
		case "down", "j":
			m.choice = min(3, m.choice+1)
		case "enter", "right":
			switch m.choice {
			case 0:
				m.duration = time.Hour
			case 1:
				m.duration = 24 * time.Hour
			case 2:
				m.duration = awsClient.MaxPresignedDuration
			case 3:
				m.stage = shareCustom
				m.customInput.Focus()
				return m, textinput.Blink
			}
			m.stage = shareReview
			m.errText = ""
		}
	case shareCustom:
		switch pressed {
		case "esc":
			m.customInput.Blur()
			m.stage = shareChoose
			m.errText = ""
		case "enter":
			duration, err := awsClient.ParsePresignedDuration(m.customInput.Value())
			if err != nil {
				m.errText = err.Error()
				return m, nil
			}
			m.customInput.Blur()
			m.duration = duration
			m.stage = shareReview
			m.errText = ""
		default:
			var cmd tea.Cmd
			m.customInput, cmd = m.customInput.Update(key)
			return m, cmd
		}
	case shareReview:
		switch pressed {
		case "esc", "left":
			m.stage = shareChoose
			m.errText = ""
			m.bodyOffset = 0
		case "enter", "g":
			return m.beginPrepare()
		default:
			m.scrollBody(pressed)
		}
	case shareShorter:
		switch pressed {
		case "enter", "y":
			return m.beginGenerate(true)
		case "n", "esc", "left":
			return m.close()
		default:
			m.scrollBody(pressed)
		}
	case shareResult:
		switch pressed {
		case "c":
			m.copyLink()
		case "s", "enter":
			m.stage = shareLink
			m.linkOffset = 0
		case "d":
			m.result = awsClient.PresignedDownload{}
			m.copyStatus = ""
			m.stage = shareChoose
			m.bodyOffset = 0
			m.errText = "A new link does not revoke the previous link."
		case "g":
			m.result = awsClient.PresignedDownload{}
			m.copyStatus = ""
			return m.beginPrepare()
		default:
			m.scrollBody(pressed)
		}
	case shareLink:
		switch pressed {
		case "esc", "left":
			m.stage = shareResult
		case "up", "k":
			m.linkOffset = max(0, m.linkOffset-1)
		case "down", "j":
			m.linkOffset++
		case "pgup":
			m.linkOffset = max(0, m.linkOffset-10)
		case "pgdown":
			m.linkOffset += 10
		case "c":
			m.copyLink()
		}
	}
	return m, nil
}

func (m *shareModel) scrollBody(pressed string) {
	switch pressed {
	case "up", "k":
		m.bodyOffset = max(0, m.bodyOffset-1)
	case "down", "j":
		m.bodyOffset++
	case "pgup":
		m.bodyOffset = max(0, m.bodyOffset-10)
	case "pgdown":
		m.bodyOffset += 10
	}
}

func (m shareModel) beginPrepare() (shareModel, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.request = shareRequestSequence.Add(1)
	m.stage = sharePreparing
	m.plan = nil
	m.errText = ""
	m.bodyOffset = 0
	client, bucket, key, region, duration, request := m.client, m.bucket, m.key, m.region, m.duration, m.request
	return m, func() tea.Msg {
		plan, err := client.PreparePresignedDownload(ctx, bucket, key, region, duration)
		return sharePreparedMsg{request: request, plan: plan, err: err}
	}
}

func (m shareModel) beginGenerate(acceptShorter bool) (shareModel, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.request = shareRequestSequence.Add(1)
	m.stage = shareGenerating
	m.bodyOffset = 0
	client, plan, request := m.client, m.plan, m.request
	return m, func() tea.Msg {
		result, err := client.GeneratePresignedDownload(ctx, plan, acceptShorter)
		return shareGeneratedMsg{request: request, result: result, err: err}
	}
}

func (m *shareModel) copyLink() {
	if m.result.URL == "" {
		return
	}
	if time.Now().After(m.result.ExpiresAt) {
		m.copyStatus = "Link expired. Generate a new link."
		return
	}
	copyText := m.copyText
	if copyText == nil {
		copyText = clipboard.WriteAll
	}
	if err := copyText(m.result.URL); err != nil {
		m.copyStatus = "Copy failed: " + err.Error() + ". Use Show link for manual copy."
		return
	}
	m.copyStatus = "Copied to clipboard."
}

func (m shareModel) close() (shareModel, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.request = shareRequestSequence.Add(1)
	m.active = false
	m.plan = nil
	m.result = awsClient.PresignedDownload{}
	m.copyStatus = ""
	m.errText = ""
	m.bodyOffset = 0
	m.customInput.SetValue("")
	return m, func() tea.Msg { return shareClosedMsg{} }
}

func (m shareModel) View(width, height int) string {
	path := "s3://" + m.bucket + "/" + m.key
	var body, footer string
	switch m.stage {
	case shareChoose:
		choices := []string{"1 hour", "24 hours", "7 days", "Custom"}
		var lines []string
		for i, choice := range choices {
			marker := "  "
			if i == m.choice {
				marker = "> "
			}
			lines = append(lines, marker+choice)
		}
		body = "File: " + path + "\n\nChoose how long the link should work:\n" + strings.Join(lines, "\n")
		footer = "Up/Down: Choose  Enter: Review  Esc: Cancel"
	case shareCustom:
		body = "File: " + path + "\n\nDuration: " + m.customInput.View() + "\nUse a whole number of minutes, hours, or days. Range: 1m to 7d."
		footer = "Enter: Review  Esc: Duration choices"
	case shareReview:
		body = "File: " + path + "\nDuration: " + describeShareDuration(m.duration) + "\n\nGenerate and copy a temporary download link?\nAnyone with the link may download while it works.\nExisting public access stays as it is."
		footer = "Enter: Generate and copy  Esc: Duration choices"
	case sharePreparing:
		body = "File: " + path + "\n\nChecking signing credentials and session lifetime..."
		footer = "Esc: Cancel and close"
	case shareShorter:
		body = "File: " + path + "\nRequested: " + describeShareDuration(m.duration) + "\nAvailable session time: " + describeShareDuration(m.plan.AvailableDuration) + "\nCredential expiry: " + formatShareTime(m.plan.KnownCredentialExpiry) + "\n\nUse available session time or cancel?"
		footer = "Enter: Use available session time  Esc: Cancel"
	case shareGenerating:
		body = "File: " + path + "\n\nGenerating a temporary download link..."
		footer = "Esc: Cancel and close"
	case shareResult:
		body = "File: " + path + "\nLink lifetime: " + describeShareDuration(m.result.EffectiveDuration) + "\nEnds no later than: " + formatShareTime(m.result.ExpiresAt) + "\n\n" + m.copyStatus
		if m.result.SessionExpiryUnknown {
			body += "\nThe session token may expire sooner."
		}
		body += "\nPermissions, policies, or credential changes may end access sooner."
		body += "\nExisting public access can keep the file reachable separately."
		if time.Now().After(m.result.ExpiresAt) {
			body += "\nLink expired. Generate a new link."
		}
		footer = "c: Copy again  s: Show link  d: New duration  g: Generate new  Esc: Close"
	case shareLink:
		lines := strings.Split(wrapText(m.result.URL, max(1, width-2)), "\n")
		visible := max(1, height-7)
		start := min(m.linkOffset, max(0, len(lines)-visible))
		end := min(len(lines), start+visible)
		body = "Full link. Join wrapped lines when copying manually:\n" + strings.Join(lines[start:end], "\n") + "\n" + m.copyStatus
		footer = "Up/Down/PgUp/PgDn: Scroll  c: Copy again  Esc: Back"
	}
	if m.errText != "" {
		body = "Error: " + m.errText + "\n\n" + body
	}
	if m.stage == shareReview || m.stage == shareShorter || m.stage == shareResult {
		lines := strings.Split(wrapText(body, max(1, width)), "\n")
		visible := max(1, height-4)
		start := min(m.bodyOffset, max(0, len(lines)-visible))
		body = strings.Join(lines[start:min(len(lines), start+visible)], "\n")
	}
	if width < 80 {
		switch m.stage {
		case shareReview:
			footer = "Enter: Generate  Up/Down: Scroll  Esc: Back"
		case shareShorter:
			footer = "Enter: Use session time  Up/Down: Scroll  Esc: Cancel"
		case shareResult:
			footer = "c: Copy  s: Show  d: Duration  g: New  Esc: Close"
		case shareLink:
			footer = "Up/Down: Scroll  c: Copy  Esc: Back"
		}
	}
	return renderPanel("Temporary download link", body, footer, width, height)
}

func describeShareDuration(duration time.Duration) string {
	if duration%(24*time.Hour) == 0 {
		return shareUnit(duration/(24*time.Hour), "day")
	}
	if duration%time.Hour == 0 {
		return shareUnit(duration/time.Hour, "hour")
	}
	if duration%time.Minute == 0 {
		return shareUnit(duration/time.Minute, "minute")
	}
	return duration.String()
}

func shareUnit(count time.Duration, unit string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", count, unit)
}

func formatShareTime(value time.Time) string {
	return value.Local().Format("2006-01-02 15:04:05 MST")
}
