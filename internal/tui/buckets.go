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
	bucketsConfirmDeleteNonEmpty // type bucket name to confirm
)

type bucketsModel struct {
	client         *awsClient.Client
	items          []bucketItem
	cursor         int
	offset         int // first visible row for scrolling
	loading        bool
	width          int
	height         int
	mode           bucketsMode
	nameInput      textinput.Model
	confirmInput   textinput.Model // type bucket name to confirm destructive delete
	message        string
	spinner        spinner.Model
	deleteProgress string // shown during bucket emptying
}

func newBucketsModel(client *awsClient.Client) bucketsModel {
	ti := textinput.New()
	ti.Placeholder = "my-bucket-name"
	ti.CharLimit = 63
	ci := textinput.New()
	ci.Placeholder = "type bucket name to confirm"
	ci.CharLimit = 63
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	return bucketsModel{
		client:       client,
		nameInput:    ti,
		confirmInput: ci,
		loading:      true,
		spinner:      sp,
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
			go func(idx int, name, region string) {
				defer wg.Done()
				stats, _ := m.client.GetBucketStats(ctx, name, region)
				items[idx].objects = stats.ObjectCount
				items[idx].sizeBytes = stats.SizeBytes
			}(i, b.Name, b.Region)
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
		m.deleteProgress = ""
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		return m, nil

	case operationDoneMsg:
		m.message = msg.message
		m.mode = bucketsList
		return m, m.init()

	case bucketNotEmptyMsg:
		m.loading = false
		m.mode = bucketsConfirmDeleteNonEmpty
		m.confirmInput.SetValue("")
		m.confirmInput.Focus()
		return m, textinput.Blink

	case deleteProgressMsg:
		m.deleteProgress = fmt.Sprintf("Emptying bucket... %s objects removed", formatWithCommas(msg.deleted))
		return m, nil

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
		case bucketsConfirmDeleteNonEmpty:
			return m.updateConfirmDeleteNonEmpty(msg)
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
		m.loading = true
		m.mode = bucketsList
		return m, func() tea.Msg {
			ctx := context.Background()
			empty, err := m.client.IsBucketEmpty(ctx, bucket.name, bucket.region)
			if err != nil {
				return errMsg{err: err}
			}
			if !empty {
				// Bucket has objects — ask user to type name to confirm
				return bucketNotEmptyMsg{name: bucket.name, region: bucket.region}
			}
			err = m.client.DeleteBucket(ctx, bucket.name, bucket.region)
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

func (m bucketsModel) updateConfirmDeleteNonEmpty(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.confirmInput.Value())
		bucket := m.items[m.cursor]
		if typed != bucket.name {
			m.message = "Name doesn't match. Delete cancelled."
			m.mode = bucketsList
			return m, nil
		}
		m.loading = true
		m.deleteProgress = "Emptying bucket... 0 objects removed"
		m.mode = bucketsList
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.EmptyBucket(ctx, bucket.name, bucket.region, func(deleted int64) {
				if prog != nil {
					prog.Send(deleteProgressMsg{deleted: deleted})
				}
			})
			if err != nil {
				return errMsg{err: err}
			}
			err = m.client.DeleteBucket(ctx, bucket.name, bucket.region)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Deleted bucket %q and all its objects", bucket.name)}
		}
	case "esc":
		m.mode = bucketsList
		return m, nil
	default:
		var cmd tea.Cmd
		m.confirmInput, cmd = m.confirmInput.Update(msg)
		return m, cmd
	}
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
	case bucketsConfirmDeleteNonEmpty:
		if m.cursor < len(m.items) {
			bucket := m.items[m.cursor]
			s += "\n"
			s += " " + warningStyle.Render("This bucket is not empty.") + "\n"
			s += " " + warningStyle.Render("ALL objects will be permanently deleted.") + "\n\n"
			s += fmt.Sprintf(" Type %s to confirm:\n", warningStyle.Render(bucket.name))
			s += " " + m.confirmInput.View() + "\n\n"
			s += helpStyle.Render(" enter: delete everything  esc: cancel")
		}
		return s
	}

	if m.loading {
		if m.deleteProgress != "" {
			s += fmt.Sprintf(" %s %s\n", m.spinner.View(), m.deleteProgress)
		} else {
			s += fmt.Sprintf(" %s Loading buckets...\n", m.spinner.View())
		}
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
