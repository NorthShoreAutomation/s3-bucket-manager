package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type browsePosition struct {
	key, filter    string
	cursor, offset int
}

func (m bucketsModel) ownsInput() bool {
	return m.inspectText != "" || m.transferReview != nil || m.share != nil || m.filterActive || m.showFilePicker || m.urlUpload != nil || m.mode != bucketsList && m.mode != bucketDetail || m.mutationPending || m.transferSnap != nil || m.bulkDeleting
}

func (m *bucketsModel) beginFilter() tea.Cmd {
	if m.filterScope == "" {
		switch m.mode {
		case bucketsList:
			m.filterScope = "buckets"
		case bucketDetailPickUser:
			m.filterScope = "users"
		default:
			m.filterScope = "files"
		}
	}
	if m.filterScope == "buckets" && m.fullBuckets == nil {
		m.fullBuckets = append([]bucketItem{}, m.items...)
	}
	if m.filterScope == "files" && m.fullBrowse == nil {
		m.fullBrowse = append(m.fullBrowse, m.browseItems...)
	}
	m.filterActive = true
	m.filterInput.Width = max(8, m.width-30)
	m.filterInput.Focus()
	return textinput.Blink
}

func (m *bucketsModel) applyFilter() {
	query := strings.ToLower(m.filterInput.Value())
	switch m.filterScope {
	case "buckets":
		m.items = nil
		for _, item := range m.fullBuckets {
			if strings.Contains(strings.ToLower(item.name), query) {
				m.items = append(m.items, item)
			}
		}
		m.cursor, m.offset = viewportBounds(m.cursor, 0, len(m.items), m.visibleRows())
	case "files":
		m.browseItems = nil
		for _, item := range m.fullBrowse {
			if strings.Contains(strings.ToLower(item.Name), query) {
				m.browseItems = append(m.browseItems, item)
			}
		}
		m.browseCursor, m.browseOffset = viewportBounds(m.browseCursor, 0, len(m.browseItems), m.browseVisibleRows())
		m.browseSelected = nil
	}
}

func (m bucketsModel) updateFilter(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterInput.SetValue("")
		m.applyFilter()
		m.filterActive = false
		m.filterInput.Blur()
		m.filterScope = ""
		return m, nil
	case "enter":
		m.filterActive = false
		m.filterInput.Blur()
		return m, nil
	case "up", "down", "pgup", "pgdown":
		if m.filterScope == "buckets" {
			return m.updateList(msg)
		}
		return m.updateBrowse(msg)
	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.applyFilter()
		return m, cmd
	}
}

func (m *bucketsModel) clearFilter() {
	if m.filterScope != "" {
		m.filterInput.SetValue("")
		m.applyFilter()
	}
	m.filterScope = ""
	m.filterActive = false
	m.filterInput.Blur()
}
