package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func viewportBounds(cursor, offset, count, rows int) (int, int) {
	rows = max(1, rows)
	cursor = max(0, min(cursor, count-1))
	offset = max(0, min(offset, max(0, count-rows)))
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+rows {
		offset = cursor - rows + 1
	}
	return cursor, offset
}

func fitTerminal(content string, width, height int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = truncate(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

// Wrap paragraphs using terminal columns, including wide glyphs and ANSI text.
func wrapText(content string, width int) string {
	return ansi.Hardwrap(content, max(1, width), true)
}

func renderPanel(title, body, footer string, width, height int) string {
	return renderScrollablePanel(title, body, footer, width, height, 0)
}

func renderScrollablePanel(title, body, footer string, width, height, offset int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	footer = truncate(footer, width)
	head := truncate(title, width) + "\n" + separator(width)
	lines := strings.Split(strings.TrimRight(wrapText(body, width), "\n"), "\n")
	available := max(1, height-4)
	offset = max(0, min(offset, max(0, len(lines)-available)))
	lines = lines[offset:]
	if len(lines) > available {
		lines = lines[:available]
	}
	return fitTerminal(head+"\n"+strings.Join(lines, "\n")+"\n"+helpStyle.Render(footer), width, height)
}
