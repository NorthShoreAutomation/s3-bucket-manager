package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- Color Palette (Tokyo Night-inspired, boosted for readability) ---
var (
	colorPageTitle  = lipgloss.AdaptiveColor{Light: "#172240", Dark: "#e0e6f5"} // near-white titles
	colorPrimary    = lipgloss.AdaptiveColor{Light: "#2359a8", Dark: "#7aa2f7"} // bright blue
	colorText       = lipgloss.AdaptiveColor{Light: "#24334b", Dark: "#c8d0e8"} // primary text - high contrast
	colorMuted      = lipgloss.AdaptiveColor{Light: "#4c5870", Dark: "#a0aac4"} // labels, help bar - readable but not loud
	colorDim        = lipgloss.AdaptiveColor{Light: "#59647a", Dark: "#a0aac4"} // breadcrumb, zero counts - still visible
	colorBorder     = lipgloss.AdaptiveColor{Light: "#78849b", Dark: "#4a5478"} // separators, borders
	colorHeaderBg   = lipgloss.AdaptiveColor{Light: "#e5eaf3", Dark: "#232738"} // header row shelf
	colorSelectBg   = lipgloss.AdaptiveColor{Light: "#2359a8", Dark: "#7aa2f7"} // selected row bg
	colorSelectFg   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#1a1b26"} // selected row text
	colorSuccess    = lipgloss.AdaptiveColor{Light: "#08754f", Dark: "#73daca"} // green for success messages
	colorDanger     = lipgloss.AdaptiveColor{Light: "#b12140", Dark: "#f7768e"} // red for errors
	colorWarningTxt = lipgloss.AdaptiveColor{Light: "#8a5900", Dark: "#e0af68"} // warning text / public indicator
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
)

// --- Helpers ---

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	return ansi.Truncate(s, maxLen, "…")
}

func pad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = truncate(s, width)
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

func padRight(s string, width int) string {
	if ansi.StringWidth(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-ansi.StringWidth(s)) + s
}

func formatCount(n int64) string {
	if n == 0 {
		return "0"
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
	line := strings.Repeat("─", max(0, width))
	return lipgloss.NewStyle().Foreground(colorBorder).Render(line)
}

func formatSize(bytes int64) string {
	if bytes == 0 {
		return "0 B"
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
