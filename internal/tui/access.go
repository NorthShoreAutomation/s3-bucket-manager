package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type prefixItem struct {
	prefix   string
	isPublic bool
}

type accessMode int

const (
	accessBucketList accessMode = iota
	accessPrefixList
	accessConfirmToggle
)

type accessModel struct {
	client       *awsClient.Client
	bucketNames  []string
	prefixes     []prefixItem
	bucket       string
	cursor       int
	loading      bool
	width        int
	height       int
	mode         accessMode
	message      string
	togglePublic bool
}

func newAccessModel(client *awsClient.Client) accessModel {
	return accessModel{client: client, loading: true}
}

func (m accessModel) init() tea.Cmd {
	m.loading = true
	m.mode = accessBucketList
	return func() tea.Msg {
		ctx := context.Background()
		buckets, err := m.client.ListBuckets(ctx)
		if err != nil {
			return errMsg{err: err}
		}
		names := make([]string, len(buckets))
		for i, b := range buckets {
			names[i] = b.Name
		}
		return accessBucketsMsg{names: names}
	}
}

type accessBucketsMsg struct {
	names []string
}

func (m accessModel) update(msg tea.Msg) (accessModel, tea.Cmd) {
	switch msg := msg.(type) {
	case accessBucketsMsg:
		m.bucketNames = msg.names
		m.loading = false
		m.mode = accessBucketList
		m.cursor = 0
		return m, nil

	case prefixesLoadedMsg:
		m.bucket = msg.bucket
		items := make([]prefixItem, len(msg.prefixes))
		copy(items, msg.prefixes)
		m.prefixes = items
		m.loading = false
		m.mode = accessPrefixList
		m.cursor = 0
		return m, nil

	case operationDoneMsg:
		m.message = msg.message
		// Reload prefixes
		return m, m.loadPrefixes(m.bucket)

	case tea.KeyMsg:
		switch m.mode {
		case accessBucketList:
			return m.updateBucketList(msg)
		case accessPrefixList:
			return m.updatePrefixList(msg)
		case accessConfirmToggle:
			return m.updateConfirmToggle(msg)
		}
	}
	return m, nil
}

func (m accessModel) updateBucketList(msg tea.KeyMsg) (accessModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.bucketNames)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.bucketNames) > 0 {
			bucket := m.bucketNames[m.cursor]
			m.loading = true
			return m, m.loadPrefixes(bucket)
		}
	}
	return m, nil
}

func (m accessModel) loadPrefixes(bucket string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		prefixNames, err := m.client.ListPrefixes(ctx, bucket)
		if err != nil {
			return errMsg{err: err}
		}
		accesses, err := m.client.GetPrefixAccessStatus(ctx, bucket, prefixNames)
		if err != nil {
			return errMsg{err: err}
		}
		items := make([]prefixItem, len(accesses))
		for i, a := range accesses {
			items[i] = prefixItem{prefix: a.Prefix, isPublic: a.IsPublic}
		}
		return prefixesLoadedMsg{bucket: bucket, prefixes: items}
	}
}

func (m accessModel) updatePrefixList(msg tea.KeyMsg) (accessModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.prefixes)-1 {
			m.cursor++
		}
	case "enter", " ":
		if len(m.prefixes) > 0 {
			p := m.prefixes[m.cursor]
			m.togglePublic = !p.isPublic
			m.mode = accessConfirmToggle
		}
	case "esc":
		m.mode = accessBucketList
		m.cursor = 0
		m.message = ""
	}
	return m, nil
}

func (m accessModel) updateConfirmToggle(msg tea.KeyMsg) (accessModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		p := m.prefixes[m.cursor]
		m.loading = true
		m.mode = accessPrefixList
		if m.togglePublic {
			return m, func() tea.Msg {
				ctx := context.Background()
				err := m.client.SetPrefixPublic(ctx, m.bucket, p.prefix)
				if err != nil {
					return errMsg{err: err}
				}
				return operationDoneMsg{message: fmt.Sprintf("Set %s%s to PUBLIC", m.bucket+"/", p.prefix)}
			}
		}
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.SetPrefixPrivate(ctx, m.bucket, p.prefix)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Set %s%s to PRIVATE", m.bucket+"/", p.prefix)}
		}
	default:
		m.mode = accessPrefixList
	}
	return m, nil
}

func (m accessModel) view() string {
	switch m.mode {
	case accessBucketList:
		return m.viewBucketList()
	case accessPrefixList:
		return m.viewPrefixList()
	case accessConfirmToggle:
		return m.viewConfirmToggle()
	}
	return ""
}

func (m accessModel) viewBucketList() string {
	s := breadcrumbStyle.Render("Dashboard > Access") + "\n"
	s += titleStyle.Render("Access Control - Select Bucket") + "\n"

	if m.loading {
		s += "Loading buckets...\n"
		return s
	}

	if len(m.bucketNames) == 0 {
		s += "No buckets found.\n"
		return s
	}

	for i, name := range m.bucketNames {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		display := name
		if i == m.cursor {
			display = selectedStyle.Render(name)
		}
		s += fmt.Sprintf("%s%s\n", cursor, display)
	}

	s += "\n" + helpStyle.Render("[enter] Select  [esc] Back")
	return s
}

func (m accessModel) viewPrefixList() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("Dashboard > Access > %s", m.bucket)) + "\n"
	s += titleStyle.Render(fmt.Sprintf("Access Control - %s", m.bucket)) + "\n"

	if m.message != "" {
		s += successStyle.Render(m.message) + "\n\n"
	}

	if m.loading {
		s += "Loading prefixes...\n"
		return s
	}

	if len(m.prefixes) == 0 {
		s += "No prefixes (folders) found in this bucket.\n"
		s += helpStyle.Render("[esc] Back")
		return s
	}

	s += fmt.Sprintf("  %-40s %s\n",
		tableHeaderStyle.Render("PREFIX"),
		tableHeaderStyle.Render("ACCESS"))

	for i, p := range m.prefixes {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		name := p.prefix
		if i == m.cursor {
			name = selectedStyle.Render(p.prefix)
		}
		s += fmt.Sprintf("%s%-40s %s\n", cursor, name, accessLabel(p.isPublic))
	}

	s += "\n" + helpStyle.Render("[enter/space] Toggle access  [esc] Back")
	return s
}

func (m accessModel) viewConfirmToggle() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("Dashboard > Access > %s", m.bucket)) + "\n"
	p := m.prefixes[m.cursor]

	if m.togglePublic {
		s += warningStyle.Render(fmt.Sprintf("Make %s%s PUBLIC? Anyone on the internet can read it. [y/N]", m.bucket+"/", p.prefix))
	} else {
		s += fmt.Sprintf("Make %s%s PRIVATE? [y/N]", m.bucket+"/", p.prefix)
	}
	return s
}
