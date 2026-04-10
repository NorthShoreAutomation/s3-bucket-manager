package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type bucketItem struct {
	name      string
	region    string
	isPublic  bool
	objects   int64
	sizeBytes int64
	created   string
}

type bucketsMode int

const (
	bucketsList bucketsMode = iota
	bucketsCreate
	bucketsConfirmDelete
)

type bucketsModel struct {
	client    *awsClient.Client
	items     []bucketItem
	cursor    int
	offset    int // first visible row for scrolling
	loading   bool
	width     int
	height    int
	mode      bucketsMode
	nameInput textinput.Model
	message   string
	spinner   spinner.Model
}

func newBucketsModel(client *awsClient.Client) bucketsModel {
	ti := textinput.New()
	ti.Placeholder = "my-bucket-name"
	ti.CharLimit = 63
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	return bucketsModel{
		client:    client,
		nameInput: ti,
		loading:   true,
		spinner:   sp,
	}
}

func (m bucketsModel) init() tea.Cmd {
	m.loading = true
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx := context.Background()
		buckets, err := m.client.ListBuckets(ctx)
		if err != nil {
			return errMsg{err: err}
		}
		items := make([]bucketItem, len(buckets))
		var wg sync.WaitGroup
		for i, b := range buckets {
			items[i] = bucketItem{
				name:     b.Name,
				region:   b.Region,
				isPublic: b.IsPublic,
				created:  b.CreationDate.Format("2006-01-02"),
			}
			wg.Add(1)
			go func(idx int, name string) {
				defer wg.Done()
				stats, _ := m.client.GetBucketStats(ctx, name)
				items[idx].objects = stats.ObjectCount
				items[idx].sizeBytes = stats.SizeBytes
			}(i, b.Name)
		}
		wg.Wait()
		return bucketsLoadedMsg{buckets: items}
	})
}

func (m bucketsModel) update(msg tea.Msg) (bucketsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case bucketsLoadedMsg:
		m.items = msg.buckets
		m.loading = false
		m.message = ""
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		return m, nil

	case operationDoneMsg:
		m.message = msg.message
		m.mode = bucketsList
		return m, m.init()

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case tea.KeyMsg:
		switch m.mode {
		case bucketsList:
			return m.updateList(msg)
		case bucketsCreate:
			return m.updateCreate(msg)
		case bucketsConfirmDelete:
			return m.updateConfirmDelete(msg)
		}
	}
	return m, nil
}

// visibleRows returns how many bucket rows fit on screen.
// Accounts for breadcrumb, title, header, help line, and padding.
func (m bucketsModel) visibleRows() int {
	overhead := 6 // breadcrumb + title + message + header + blank + help
	avail := m.height - overhead
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (m bucketsModel) updateList(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			visible := m.visibleRows()
			if m.cursor >= m.offset+visible {
				m.offset = m.cursor - visible + 1
			}
		}
	case "c":
		m.mode = bucketsCreate
		m.nameInput.SetValue("")
		m.nameInput.Focus()
		return m, textinput.Blink
	case "d":
		if len(m.items) > 0 {
			m.mode = bucketsConfirmDelete
		}
	case "r":
		m.loading = true
		return m, m.init()
	}
	return m, nil
}

func (m bucketsModel) updateCreate(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			return m, nil
		}
		m.loading = true
		m.mode = bucketsList
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.CreateBucket(ctx, name, m.client.Region)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Created bucket %q", name)}
		}
	case "esc":
		m.mode = bucketsList
		return m, nil
	default:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateConfirmDelete(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		bucket := m.items[m.cursor]
		if bucket.objects > 0 {
			m.message = fmt.Sprintf("Bucket %q has %d objects. Empty it first.", bucket.name, bucket.objects)
			m.mode = bucketsList
			return m, nil
		}
		m.loading = true
		m.mode = bucketsList
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.DeleteBucket(ctx, bucket.name)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Deleted bucket %q", bucket.name)}
		}
	default:
		m.mode = bucketsList
	}
	return m, nil
}

func (m bucketsModel) view() string {
	tableWidth := colName + colRegion + colStatus + colCount + colSize + colCreated + 12 // gaps between cols + left pad
	if m.width > tableWidth {
		tableWidth = m.width
	}

	s := breadcrumbStyle.Render("dashboard › buckets") + "\n"
	s += screenTitleStyle.Render(fmt.Sprintf("Buckets (%d)", len(m.items))) + "\n"
	s += separator(tableWidth) + "\n"

	if m.message != "" {
		s += " " + successStyle.Render(m.message) + "\n"
	}

	switch m.mode {
	case bucketsCreate:
		s += " New bucket name:\n"
		s += " " + m.nameInput.View() + "\n\n"
		s += helpStyle.Render(" enter: create  esc: cancel")
		return s
	case bucketsConfirmDelete:
		if m.cursor < len(m.items) {
			s += "\n " + warningStyle.Render(fmt.Sprintf("Delete bucket %q? [y/N]", m.items[m.cursor].name))
		}
		return s
	}

	if m.loading {
		s += fmt.Sprintf(" %s Loading buckets...\n", m.spinner.View())
		return s
	}

	if len(m.items) == 0 {
		s += " No buckets found.\n\n"
		s += helpStyle.Render(" [c] Create  [esc] Back")
		return s
	}

	// Table header row
	header := fmt.Sprintf(" %s  %s  %s  %s  %s  %s",
		pad("NAME", colName),
		pad("REGION", colRegion),
		pad("", colStatus),
		padRight("OBJECTS", colCount),
		padRight("SIZE", colSize),
		pad("CREATED", colCreated))
	s += tableHeaderStyle.Width(tableWidth).Render(header) + "\n"

	visible := m.visibleRows()
	end := m.offset + visible
	if end > len(m.items) {
		end = len(m.items)
	}

	if m.offset > 0 {
		s += dimStyle.Render(fmt.Sprintf(" ▲ %d more above", m.offset)) + "\n"
	}

	for i := m.offset; i < end; i++ {
		b := m.items[i]
		name := truncate(b.name, colName)
		region := pad(b.region, colRegion)
		count := padRight(formatCount(b.objects), colCount)
		size := padRight(formatSize(b.sizeBytes), colSize)
		created := pad(b.created, colCreated)

		if i == m.cursor {
			icon := accessIconSelected(b.isPublic)
			row := fmt.Sprintf(" %s  %s  %s  %s  %s  %s",
				pad(name, colName), region, icon, count, size, created)
			s += rowSelectedStyle.Width(tableWidth).Render(row) + "\n"
		} else {
			icon := accessIcon(b.isPublic)
			row := fmt.Sprintf(" %s  %s  %s  %s  %s  %s",
				pad(name, colName), region, icon, count, size, created)
			s += rowStyle.Render(row) + "\n"
		}
	}

	if end < len(m.items) {
		s += dimStyle.Render(fmt.Sprintf(" ▼ %d more below", len(m.items)-end)) + "\n"
	}

	s += "\n" + helpStyle.Render(" [c] Create  [d] Delete  [r] Refresh  [esc] Back")
	return s
}
