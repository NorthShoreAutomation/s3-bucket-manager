package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#7C3AED") // purple
	colorSuccess   = lipgloss.Color("#10B981") // green
	colorWarning   = lipgloss.Color("#F59E0B") // yellow
	colorDanger    = lipgloss.Color("#EF4444") // red
	colorMuted     = lipgloss.Color("#6B7280") // gray
	colorHighlight = lipgloss.Color("#3B82F6") // blue

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	breadcrumbStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginBottom(1)

	statusPublic = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true).
			Render("PUBLIC")

	statusPrivate = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true).
			Render("PRIVATE")

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(colorSuccess)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Bold(true)

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				BorderBottom(true).
				BorderStyle(lipgloss.NormalBorder())
)

func accessLabel(public bool) string {
	if public {
		return statusPublic
	}
	return statusPrivate
}
