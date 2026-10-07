package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	awsdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type shareTestSigner struct{ calls int }

func (s *shareTestSigner) PresignGetObject(_ context.Context, _ *s3.GetObjectInput, opts ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	s.calls++
	var options s3.PresignOptions
	for _, opt := range opts {
		opt(&options)
	}
	return &v4.PresignedHTTPRequest{URL: fmt.Sprintf("https://example.test/file?X-Amz-Date=%s&X-Amz-Expires=%d&X-Amz-Signature=private", time.Now().UTC().Format("20060102T150405Z"), int64(options.Expires/time.Second))}, nil
}

func newTestShare(expires time.Time) (shareModel, *shareTestSigner) {
	signer := &shareTestSigner{}
	client := &awsClient.Client{
		PresignS3: signer,
		Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
			return awsdk.Credentials{AccessKeyID: "TEST", SecretAccessKey: "secret", CanExpire: !expires.IsZero(), Expires: expires}, nil
		}),
	}
	return newShare(client, "photos", "trip/private photo.jpg", "us-west-2"), signer
}

func shareKey(value string) tea.KeyMsg {
	switch value {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
	}
}

func TestShareReviewsFocusedFileBeforeGenerating(t *testing.T) {
	m, signer := newTestShare(time.Time{})
	if !m.OwnsInput() || !strings.Contains(m.View(80, 24), "1 hour") {
		t.Fatal("share dialog did not start with one-hour choice and input ownership")
	}
	m, cmd := m.Update(shareKey("enter"))
	if cmd != nil || signer.calls != 0 || !strings.Contains(m.View(80, 24), "s3://photos/trip/private photo.jpg") {
		t.Fatal("share must show full file path before signing")
	}
	m, cmd = m.Update(shareKey("enter"))
	if cmd == nil || signer.calls != 0 {
		t.Fatal("review confirmation did not start preparation")
	}
	prepared, ok := cmd().(sharePreparedMsg)
	if !ok || prepared.err != nil {
		t.Fatalf("unexpected prepare result: %+v", prepared)
	}
	m, cmd = m.Update(prepared)
	if cmd == nil || signer.calls != 0 {
		t.Fatal("prepared duration did not start generation")
	}
	generated, ok := cmd().(shareGeneratedMsg)
	if !ok || generated.err != nil || signer.calls != 1 {
		t.Fatalf("unexpected generation result: %+v", generated)
	}
	m.copyText = func(string) error { return nil }
	m, _ = m.Update(generated)
	if !strings.Contains(m.View(80, 24), "Copied") || !strings.Contains(m.View(80, 24), "Ends no later than") {
		t.Fatalf("missing copy and expiry status: %s", m.View(80, 24))
	}
}

func TestShareCustomDurationAndInputPriority(t *testing.T) {
	m, _ := newTestShare(time.Time{})
	for range 3 {
		m, _ = m.Update(shareKey("down"))
	}
	m, _ = m.Update(shareKey("enter"))
	m, _ = m.Update(shareKey("q"))
	if m.customInput.Value() != "q" || !m.OwnsInput() {
		t.Fatal("global shortcut stole input from custom duration")
	}
	m.customInput.SetValue("")
	for _, key := range []string{"3", "0", "m"} {
		m, _ = m.Update(shareKey(key))
	}
	m, _ = m.Update(shareKey("enter"))
	if m.duration != 30*time.Minute || !strings.Contains(m.View(80, 24), "30 minutes") {
		t.Fatalf("custom duration not reviewed: %s", m.View(80, 24))
	}
	m, _ = m.Update(shareKey("esc"))
	if !m.OwnsInput() {
		t.Fatal("escape from review should return to duration choices")
	}
}

func TestShareRequiresConsentWhenSessionExpiresSooner(t *testing.T) {
	m, signer := newTestShare(time.Now().Add(30 * time.Minute))
	m.copyText = func(string) error { return nil }
	m, _ = m.Update(shareKey("enter"))
	m, cmd := m.Update(shareKey("enter"))
	m, cmd = m.Update(cmd().(sharePreparedMsg))
	if cmd != nil || signer.calls != 0 || !strings.Contains(m.View(80, 24), "Use available session time") {
		t.Fatal("session limit did not wait for explicit decision")
	}
	m, cmd = m.Update(shareKey("enter"))
	if cmd == nil {
		t.Fatal("consent did not start generation")
	}
	m, _ = m.Update(cmd().(shareGeneratedMsg))
	if signer.calls != 1 || m.result.EffectiveDuration >= time.Hour {
		t.Fatal("link was not shortened after consent")
	}
}

func TestShareCopyFailureKeepsSameLinkAndRetry(t *testing.T) {
	m, signer := newTestShare(time.Time{})
	copies := 0
	m.copyText = func(raw string) error {
		copies++
		if copies == 1 {
			return errors.New("no clipboard")
		}
		if !strings.Contains(raw, "X-Amz-Signature") {
			t.Fatal("copy omitted link")
		}
		return nil
	}
	m, _ = m.Update(shareKey("enter"))
	m, cmd := m.Update(shareKey("enter"))
	m, cmd = m.Update(cmd().(sharePreparedMsg))
	m, _ = m.Update(cmd().(shareGeneratedMsg))
	firstURL := m.result.URL
	if !strings.Contains(m.View(80, 24), "Copy failed") {
		t.Fatal("copy failure was not shown")
	}
	m, _ = m.Update(shareKey("c"))
	if copies != 2 || signer.calls != 1 || m.result.URL != firstURL || !strings.Contains(m.View(80, 24), "Copied") {
		t.Fatal("copy retry regenerated or lost link")
	}
	m, _ = m.Update(shareKey("s"))
	if !strings.Contains(strings.ReplaceAll(m.View(80, 24), "\n", ""), "X-Amz-Signature") {
		t.Fatalf("full link not available for manual copy: %q", m.View(80, 24))
	}
	m, _ = m.Update(shareKey("esc"))
	if m.result.URL != firstURL {
		t.Fatal("closing link display should keep result")
	}
}

func TestShareCloseClearsLinkAndIgnoresStaleResult(t *testing.T) {
	m, _ := newTestShare(time.Time{})
	m, _ = m.Update(shareKey("enter"))
	m, cmd := m.Update(shareKey("enter"))
	prepared := cmd().(sharePreparedMsg)
	m, closeCmd := m.Update(shareKey("esc"))
	if _, ok := closeCmd().(shareClosedMsg); !ok || m.OwnsInput() || m.plan != nil || m.result.URL != "" {
		t.Fatal("closing share dialog did not clear pending data")
	}
	m, cmd = m.Update(prepared)
	if cmd != nil || m.plan != nil || m.OwnsInput() {
		t.Fatal("stale result reopened closed dialog")
	}
}

func TestShareNewDialogIgnoresPriorDialogResult(t *testing.T) {
	old, _ := newTestShare(time.Time{})
	old, _ = old.Update(shareKey("enter"))
	_, oldCmd := old.Update(shareKey("enter"))
	oldResult := oldCmd().(sharePreparedMsg)
	newDialog, _ := newTestShare(time.Time{})
	newDialog, _ = newDialog.Update(shareKey("enter"))
	newDialog, newCmd := newDialog.Update(shareKey("enter"))
	newDialog, cmd := newDialog.Update(oldResult)
	if cmd != nil || newDialog.stage != sharePreparing {
		t.Fatal("prior dialog result changed the new share flow")
	}
	newDialog, cmd = newDialog.Update(newCmd().(sharePreparedMsg))
	if cmd == nil || newDialog.stage != shareGenerating {
		t.Fatal("current dialog result was not applied")
	}
}

func TestShareLongLinkCanBeScrolledAtCompactSize(t *testing.T) {
	m, _ := newTestShare(time.Time{})
	m.stage = shareResult
	m.result = awsClient.PresignedDownload{
		URL:       "https://example.test/file?token=" + strings.Repeat("a", 600) + "END",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	m, _ = m.Update(shareKey("s"))
	first := m.View(60, 15)
	if !strings.Contains(first, "Esc") {
		t.Fatal("compact link footer hid close control")
	}
	if strings.Contains(first, "END") {
		t.Fatal("long link unexpectedly fits the compact viewport")
	}
	for range 20 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if !strings.Contains(m.View(60, 15), "END") {
		t.Fatal("scrolling did not expose the end of the link")
	}
}

func TestShareLongPathReviewCanBeScrolled(t *testing.T) {
	m, _ := newTestShare(time.Time{})
	m.key = strings.Repeat("folder/", 200) + "last-file.txt"
	m, _ = m.Update(shareKey("enter"))
	first := m.View(60, 15)
	if strings.Contains(first, "last-file.txt") {
		t.Fatal("fixture should require scrolling to reach path end")
	}
	for range 20 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if !strings.Contains(m.View(60, 15), "last-file.txt") {
		t.Fatal("review did not expose the full file path after scrolling")
	}
}

func TestShareExpiredResultRequiresRegeneration(t *testing.T) {
	m, signer := newTestShare(time.Time{})
	copies := 0
	m.copyText = func(string) error { copies++; return nil }
	m.stage = shareResult
	m.result = awsClient.PresignedDownload{URL: "https://example.test/expired", ExpiresAt: time.Now().Add(-time.Minute)}
	m, _ = m.Update(shareKey("c"))
	if copies != 0 || !strings.Contains(m.View(80, 24), "Link expired") {
		t.Fatal("expired result was copied or lacked a warning")
	}
	m, cmd := m.Update(shareKey("g"))
	if cmd == nil || signer.calls != 0 || m.result.URL != "" {
		t.Fatal("regeneration did not start a new signing request")
	}
}
