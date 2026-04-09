package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type bucketItem struct {
	name     string
	region   string
	isPublic bool
	objects  int64
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
	loading   bool
	width     int
	height    int
	mode      bucketsMode
	nameInput textinput.Model
	message   string
}

func newBucketsModel(client *awsClient.Client) bucketsModel {
	ti := textinput.New()
	ti.Placeholder = "my-bucket-name"
	ti.CharLimit = 63
	return bucketsModel{
		client:    client,
		nameInput: ti,
		loading:   true,
	}
}

func (m bucketsModel) init() tea.Cmd {
	m.loading = true
	return func() tea.Msg {
		ctx := context.Background()
		buckets, err := m.client.ListBuckets(ctx)
		if err != nil {
			return errMsg{err: err}
		}
		items := make([]bucketItem, len(buckets))
		for i, b := range buckets {
			count, _ := m.client.GetBucketObjectCount(ctx, b.Name)
			items[i] = bucketItem{
				name:     b.Name,
				region:   b.Region,
				isPublic: b.IsPublic,
				objects:  count,
			}
		}
		return bucketsLoadedMsg{buckets: items}
	}
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

func (m bucketsModel) updateList(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
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
	s := breadcrumbStyle.Render("Dashboard > Buckets") + "\n"
	s += titleStyle.Render("Buckets") + "\n"

	if m.message != "" {
		s += successStyle.Render(m.message) + "\n\n"
	}

	switch m.mode {
	case bucketsCreate:
		s += "New bucket name:\n"
		s += m.nameInput.View() + "\n\n"
		s += helpStyle.Render("enter: create  esc: cancel")
		return s
	case bucketsConfirmDelete:
		if m.cursor < len(m.items) {
			s += warningStyle.Render(fmt.Sprintf("Delete bucket %q? [y/N]", m.items[m.cursor].name))
		}
		return s
	}

	if m.loading {
		s += "Loading buckets...\n"
		return s
	}

	if len(m.items) == 0 {
		s += "No buckets found.\n\n"
		s += helpStyle.Render("[c] Create  [esc] Back")
		return s
	}

	// Table header
	s += fmt.Sprintf("  %-30s %-15s %-10s %s\n",
		tableHeaderStyle.Render("NAME"),
		tableHeaderStyle.Render("REGION"),
		tableHeaderStyle.Render("ACCESS"),
		tableHeaderStyle.Render("OBJECTS"))

	for i, b := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		name := b.name
		if i == m.cursor {
			name = selectedStyle.Render(name)
		}
		s += fmt.Sprintf("%s%-30s %-15s %-10s %d\n",
			cursor, name, b.region, accessLabel(b.isPublic), b.objects)
	}

	s += "\n" + helpStyle.Render("[c] Create  [d] Delete  [esc] Back")
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
