package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// --- Color Palette (Tokyo Night-inspired, dark terminal safe) ---
var (
	colorPageTitle  = lipgloss.Color("#c0caf5") // bright, near-white
	colorPrimary    = lipgloss.Color("#7aa2f7") // bright blue
	colorText       = lipgloss.Color("#a9b1d6") // primary text
	colorDim        = lipgloss.Color("#565f89") // dimmed (breadcrumb, private, zero)
	colorBorder     = lipgloss.Color("#3b4261") // separators, borders
	colorHeaderBg   = lipgloss.Color("#1f2335") // header row shelf
	colorSelectBg   = lipgloss.Color("#7aa2f7") // selected row bg
	colorSelectFg   = lipgloss.Color("#1a1b26") // selected row text
	colorSuccess    = lipgloss.Color("#73daca") // green for success messages
	colorDanger     = lipgloss.Color("#f7768e") // red for errors
	colorWarningTxt = lipgloss.Color("#e0af68") // warning text
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
			Foreground(colorDim)

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
		return dimStyle.Render("—")
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

// Keep old function name working for other screens during transition
func accessLabel(public bool) string {
	return accessIcon(public)
}
