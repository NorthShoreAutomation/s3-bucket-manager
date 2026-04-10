package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// --- Color Palette (Tokyo Night-inspired, boosted for readability) ---
var (
	colorPageTitle  = lipgloss.Color("#e0e6f5") // near-white titles
	colorPrimary    = lipgloss.Color("#7aa2f7") // bright blue
	colorText       = lipgloss.Color("#c8d0e8") // primary text — high contrast
	colorMuted      = lipgloss.Color("#8690b2") // labels, help bar — readable but not loud
	colorDim        = lipgloss.Color("#636d8c") // breadcrumb, zero counts — still visible
	colorBorder     = lipgloss.Color("#4a5478") // separators, borders
	colorHeaderBg   = lipgloss.Color("#232738") // header row shelf
	colorSelectBg   = lipgloss.Color("#7aa2f7") // selected row bg
	colorSelectFg   = lipgloss.Color("#1a1b26") // selected row text
	colorSuccess    = lipgloss.Color("#73daca") // green for success messages
	colorDanger     = lipgloss.Color("#f7768e") // red for errors
	colorWarningTxt = lipgloss.Color("#e0af68") // warning text / public indicator
)

// --- Shared Styles ---
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPageTitle)

	screenTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPageTitle).
				PaddingLeft(1)

	breadcrumbStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			PaddingLeft(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(colorSuccess)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarningTxt).
			Bold(true)

	// Table
	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPageTitle).
				Background(colorHeaderBg)

	rowStyle = lipgloss.NewStyle().
			Foreground(colorText)

	rowSelectedStyle = lipgloss.NewStyle().
				Background(colorSelectBg).
				Foreground(colorSelectFg).
				Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Legacy alias — used by users.go and access.go until they're updated
	selectedStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	// Fixed column widths
	colName    = 38
	colRegion  = 13
	colStatus  = 4
	colCount   = 8
	colSize    = 9
	colCreated = 12
)

// --- Status Indicators ---

func accessIcon(public bool) string {
	if public {
		return lipgloss.NewStyle().Foreground(colorWarningTxt).Render("\U0001F310") // globe
	}
	return dimStyle.Render("\U0001F512") // lock
}

func accessIconSelected(public bool) string {
	if public {
		return lipgloss.NewStyle().Foreground(colorWarningTxt).Render("\U0001F310")
	}
	return lipgloss.NewStyle().Foreground(colorSelectFg).Render("\U0001F512")
}

// --- Helpers ---

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-1] + "…"
	}
	return s
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

func formatCount(n int64) string {
	if n == 0 {
		return "—"
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return formatWithCommas(n)
}

func formatWithCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func separator(width int) string {
	line := strings.Repeat("─", width)
	return lipgloss.NewStyle().Foreground(colorBorder).Render(line)
}

func formatSize(bytes int64) string {
	if bytes == 0 {
		return "—"
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case bytes >= tb:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(tb))
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func publicURL(bucket, prefix string) string {
	if prefix == "" {
		return fmt.Sprintf("https://%s.s3.amazonaws.com/", bucket)
	}
	return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, prefix)
}
