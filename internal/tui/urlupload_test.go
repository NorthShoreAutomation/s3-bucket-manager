package tui

// TODO: add an integration-style model test once bubbletea testing utilities
// mature enough to drive sub-models without a running tea.Program.
//
// The interaction we'd want to test:
//   1. Construct urlUploadModel via newURLUpload.
//   2. Set urlInput.SetValue("https://example.com/file.zip").
//   3. Send tea.KeyMsg{Type: tea.KeyEnter}.
//   4. Assert m.phase == urlUploadPhaseProgress.
//
// Testing that path requires mocking *aws.Client or injecting an
// httpcopy.Uploader — a small refactor for a follow-up.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestURLUploadInputViewRendersCleanly ensures the input-phase view renders
// without panicking and includes expected content.
func TestURLUploadInputViewRendersCleanly(t *testing.T) {
	m := newURLUpload(nil, "my-bucket", "us-east-1", "uploads/")
	v := m.viewInput()
	if !strings.Contains(v, "enter") {
		t.Fatalf("expected help text in input view, got:\n%s", v)
	}
	if !strings.Contains(v, "cancel") {
		t.Fatalf("expected cancel hint in input view, got:\n%s", v)
	}
	if !strings.Contains(v, "my-bucket") {
		t.Fatalf("expected bucket name in breadcrumb, got:\n%s", v)
	}
}

// TestURLUploadEscInInputPhaseEmitsCancelled ensures the Esc handler emits
// urlUploadErrMsg with "cancelled" in the error text.
func TestURLUploadEscInInputPhaseEmitsCancelled(t *testing.T) {
	m := newURLUpload(nil, "my-bucket", "us-east-1", "")

	_, cmd := m.updateInput(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a command from Esc keystroke")
	}
	result := cmd()
	errResult, ok := result.(urlUploadErrMsg)
	if !ok {
		t.Fatalf("expected urlUploadErrMsg, got %T", result)
	}
	if !strings.Contains(errResult.Err.Error(), "cancelled") {
		t.Fatalf("expected 'cancelled' in error, got: %v", errResult.Err)
	}
}

// TestURLUploadEnterWithEmptyURLIsNoop ensures pressing Enter with no URL
// does not advance to the progress phase.
func TestURLUploadEnterWithEmptyURLIsNoop(t *testing.T) {
	m := newURLUpload(nil, "my-bucket", "us-east-1", "")

	updated, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.phase != urlUploadPhaseInput {
		t.Fatalf("expected phase to remain input, got %d", updated.phase)
	}
}

// TestURLUploadTabSwitchesActiveInput verifies Tab cycles between the two inputs.
func TestURLUploadTabSwitchesActiveInput(t *testing.T) {
	m := newURLUpload(nil, "my-bucket", "us-east-1", "")
	if m.activeInput != 0 {
		t.Fatal("expected URL input (0) to be active initially")
	}

	updated, _ := m.updateInput(tea.KeyMsg{Type: tea.KeyTab})
	if updated.activeInput != 1 {
		t.Fatalf("expected key input (1) active after tab, got %d", updated.activeInput)
	}

	updated2, _ := updated.updateInput(tea.KeyMsg{Type: tea.KeyTab})
	if updated2.activeInput != 0 {
		t.Fatalf("expected URL input (0) active after second tab, got %d", updated2.activeInput)
	}
}

// TestFormatRate covers the formatRate helper.
func TestFormatRate(t *testing.T) {
	cases := []struct {
		bps  float64
		want string
	}{
		{0, "—/s"},
		{-1, "—/s"},
		{512, "512 B/s"},
		{1.5 * (1 << 10), "1.5 KB/s"},
		{3.2 * (1 << 20), "3.2 MB/s"},
		{1.1 * (1 << 30), "1.1 GB/s"},
	}
	for _, tc := range cases {
		got := formatRate(tc.bps)
		if got != tc.want {
			t.Errorf("formatRate(%v) = %q, want %q", tc.bps, got, tc.want)
		}
	}
}

// TestFormatDuration covers the formatDuration helper.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{62 * time.Second, "1m02s"},
		{time.Hour + 23*time.Minute, "1h23m"},
	}
	for _, tc := range cases {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
