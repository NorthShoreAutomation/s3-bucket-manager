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

type prefixItem struct {
	prefix   string
	isPublic bool
}

type bucketsMode int

const (
	bucketsList                  bucketsMode = iota
	bucketsCreate                            // typing a new bucket name
	bucketsTypeDelete                        // type 'delete' to start deletion
	bucketsConfirmDelete                     // are you sure? [y/N]
	bucketsConfirmDeleteNonEmpty             // type bucket name to confirm emptying
	bucketDetail                             // viewing a single bucket's details
	bucketDetailAddPrefix                    // typing a new prefix name
	bucketDetailConfirm                      // type 'yes' to confirm access change
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
	deleteInput    textinput.Model // type 'delete' to start deletion
	confirmInput   textinput.Model // type bucket name to confirm destructive delete
	message        string
	spinner        spinner.Model
	deleteProgress string // shown during bucket emptying

	// Detail view fields
	detailCursor  int             // cursor position in detail view (0 = bucket row, 1+ = prefixes)
	prefixes      []prefixItem    // prefixes for currently selected bucket
	prefixInput   textinput.Model // for adding new prefixes
	confirmInput2 textinput.Model // for typing 'yes' to confirm access change
	confirmAction string          // description of what will happen
	confirmFunc   func() tea.Msg  // the action to execute on confirmation
	detailMessage string          // status message in detail view
}

func newBucketsModel(client *awsClient.Client) bucketsModel {
	ti := textinput.New()
	ti.Placeholder = "my-bucket-name"
	ti.CharLimit = 63
	di := textinput.New()
	di.Placeholder = "type 'delete' to confirm"
	di.CharLimit = 10
	ci := textinput.New()
	ci.Placeholder = "type bucket name to confirm"
	ci.CharLimit = 63
	pi := textinput.New()
	pi.Placeholder = "prefix-name/"
	pi.CharLimit = 200
	ci2 := textinput.New()
	ci2.Placeholder = "type yes to confirm"
	ci2.CharLimit = 10
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	return bucketsModel{
		client:        client,
		nameInput:     ti,
		deleteInput:   di,
		confirmInput:  ci,
		prefixInput:   pi,
		confirmInput2: ci2,
		loading:       true,
		spinner:       sp,
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
		m.detailMessage = msg.message
		// If we're in detail view, reload prefixes
		if m.mode == bucketDetail || m.mode == bucketDetailConfirm {
			m.mode = bucketDetail
			if m.cursor < len(m.items) {
				return m, m.loadPrefixes()
			}
		}
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

	case prefixesLoadedMsg:
		m.prefixes = msg.prefixes
		m.loading = false
		m.mode = bucketDetail
		m.detailCursor = 0
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
		case bucketsTypeDelete:
			return m.updateTypeDelete(msg)
		case bucketsConfirmDelete:
			return m.updateConfirmDelete(msg)
		case bucketsConfirmDeleteNonEmpty:
			return m.updateConfirmDeleteNonEmpty(msg)
		case bucketDetail:
			return m.updateDetail(msg)
		case bucketDetailAddPrefix:
			return m.updateDetailAddPrefix(msg)
		case bucketDetailConfirm:
			return m.updateDetailConfirm(msg)
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
			m.mode = bucketsTypeDelete
			m.deleteInput.SetValue("")
			m.deleteInput.Focus()
			return m, textinput.Blink
		}
	case "r":
		m.loading = true
		return m, m.init()
	case "enter":
		if len(m.items) > 0 {
			m.mode = bucketDetail
			m.detailCursor = 0
			m.detailMessage = ""
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadPrefixes())
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

func (m bucketsModel) updateTypeDelete(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.deleteInput.Value())
		if typed != "delete" {
			m.message = "You must type 'delete' to confirm. Cancelled."
			m.mode = bucketsList
			return m, nil
		}
		m.mode = bucketsConfirmDelete
		return m, nil
	case "esc":
		m.mode = bucketsList
		return m, nil
	default:
		var cmd tea.Cmd
		m.deleteInput, cmd = m.deleteInput.Update(msg)
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

// --- Detail view updates ---

func (m bucketsModel) updateDetail(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	maxRow := len(m.prefixes) // row 0 = bucket toggle, rows 1..N = prefixes
	switch msg.String() {
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < maxRow {
			m.detailCursor++
		}
	case "enter":
		return m.toggleSelected()
	case "p":
		m.mode = bucketDetailAddPrefix
		m.prefixInput.SetValue("")
		m.prefixInput.Focus()
		return m, textinput.Blink
	case "d":
		// Delete selected prefix (only for prefix rows, not the bucket row)
		if m.detailCursor > 0 && m.detailCursor <= len(m.prefixes) {
			idx := m.detailCursor - 1
			p := m.prefixes[idx]
			if p.isPublic {
				// Must set private first, then remove
				bucket := m.items[m.cursor]
				m.loading = true
				return m, func() tea.Msg {
					ctx := context.Background()
					err := m.client.SetPrefixPrivate(ctx, bucket.name, p.prefix)
					if err != nil {
						return errMsg{err: err}
					}
					return operationDoneMsg{message: fmt.Sprintf("Removed prefix %s", p.prefix)}
				}
			}
			// Private prefix -- just remove from list
			m.prefixes = append(m.prefixes[:idx], m.prefixes[idx+1:]...)
			if m.detailCursor > len(m.prefixes) {
				m.detailCursor = len(m.prefixes)
			}
			m.detailMessage = fmt.Sprintf("Removed prefix %s", p.prefix)
		}
	case "r":
		m.loading = true
		m.detailMessage = ""
		return m, tea.Batch(m.spinner.Tick, m.loadPrefixes())
	case "esc":
		m.mode = bucketsList
		m.detailMessage = ""
		m.prefixes = nil
		// Refresh the bucket list to pick up any access changes
		m.loading = true
		return m, m.init()
	}
	return m, nil
}

func (m bucketsModel) toggleSelected() (bucketsModel, tea.Cmd) {
	bucket := m.items[m.cursor]

	if m.detailCursor == 0 {
		// Toggle whole bucket
		if bucket.isPublic {
			// Making private -- no warning needed
			m.loading = true
			return m, func() tea.Msg {
				ctx := context.Background()
				err := m.client.SetPrefixPrivate(ctx, bucket.name, "")
				if err != nil {
					return errMsg{err: err}
				}
				return operationDoneMsg{message: fmt.Sprintf("Set %s to PRIVATE", bucket.name)}
			}
		}
		// Making public -- requires confirmation
		m.confirmAction = fmt.Sprintf("This will make the ENTIRE bucket %q publicly readable.", bucket.name)
		m.confirmFunc = func() tea.Msg {
			ctx := context.Background()
			err := m.client.SetPrefixPublic(ctx, bucket.name, "")
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Set %s to PUBLIC", bucket.name)}
		}
		m.mode = bucketDetailConfirm
		m.confirmInput2.SetValue("")
		m.confirmInput2.Focus()
		return m, textinput.Blink
	}

	// Toggle a prefix
	idx := m.detailCursor - 1
	if idx < 0 || idx >= len(m.prefixes) {
		return m, nil
	}
	p := m.prefixes[idx]

	if p.isPublic {
		// Making private -- no warning needed
		m.loading = true
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.SetPrefixPrivate(ctx, bucket.name, p.prefix)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Set %s%s to PRIVATE", bucket.name+"/", p.prefix)}
		}
	}

	// Making public -- requires confirmation
	m.confirmAction = fmt.Sprintf("Making %s%s public requires changing the bucket's public access settings.", bucket.name+"/", p.prefix)
	m.confirmFunc = func() tea.Msg {
		ctx := context.Background()
		err := m.client.SetPrefixPublic(ctx, bucket.name, p.prefix)
		if err != nil {
			return errMsg{err: err}
		}
		return operationDoneMsg{message: fmt.Sprintf("Set %s%s to PUBLIC", bucket.name+"/", p.prefix)}
	}
	m.mode = bucketDetailConfirm
	m.confirmInput2.SetValue("")
	m.confirmInput2.Focus()
	return m, textinput.Blink
}

func (m bucketsModel) updateDetailAddPrefix(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.prefixInput.Value())
		if name == "" {
			return m, nil
		}
		// Ensure trailing slash
		if !strings.HasSuffix(name, "/") {
			name += "/"
		}
		// Check for duplicates
		for _, p := range m.prefixes {
			if p.prefix == name {
				m.detailMessage = fmt.Sprintf("Prefix %s already exists", name)
				m.mode = bucketDetail
				return m, nil
			}
		}
		m.prefixes = append(m.prefixes, prefixItem{prefix: name, isPublic: false})
		m.detailMessage = fmt.Sprintf("Added prefix %s (private by default)", name)
		m.mode = bucketDetail
		// Move cursor to the new prefix
		m.detailCursor = len(m.prefixes)
		return m, nil
	case "esc":
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.prefixInput, cmd = m.prefixInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateDetailConfirm(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.confirmInput2.Value())
		if typed != "yes" {
			m.detailMessage = "Cancelled. Type exactly \"yes\" to confirm."
			m.mode = bucketDetail
			return m, nil
		}
		m.loading = true
		m.mode = bucketDetail
		return m, m.confirmFunc
	case "esc":
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.confirmInput2, cmd = m.confirmInput2.Update(msg)
		return m, cmd
	}
}

// loadPrefixes fetches prefix list and access status for the currently selected bucket.
func (m bucketsModel) loadPrefixes() tea.Cmd {
	bucket := m.items[m.cursor]
	return func() tea.Msg {
		ctx := context.Background()
		prefixNames, err := m.client.ListPrefixes(ctx, bucket.name)
		if err != nil {
			return errMsg{err: err}
		}
		accesses, err := m.client.GetPrefixAccessStatus(ctx, bucket.name, prefixNames)
		if err != nil {
			return errMsg{err: err}
		}
		items := make([]prefixItem, len(accesses))
		for i, a := range accesses {
			items[i] = prefixItem{prefix: a.Prefix, isPublic: a.IsPublic}
		}
		return prefixesLoadedMsg{bucket: bucket.name, prefixes: items}
	}
}

// --- Views ---

func (m bucketsModel) view() string {
	switch m.mode {
	case bucketDetail, bucketDetailAddPrefix, bucketDetailConfirm:
		return m.viewDetail()
	default:
		return m.viewList()
	}
}

func (m bucketsModel) viewList() string {
	tableWidth := colName + colRegion + colStatus + colCount + colSize + colCreated + 12 // gaps between cols + left pad
	if m.width > tableWidth {
		tableWidth = m.width
	}

	s := breadcrumbStyle.Render("dashboard > buckets") + "\n"
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
	case bucketsTypeDelete:
		if m.cursor < len(m.items) {
			s += fmt.Sprintf("\n Delete bucket %s\n", warningStyle.Render(m.items[m.cursor].name))
			s += " Type 'delete' to proceed:\n"
			s += " " + m.deleteInput.View() + "\n\n"
			s += helpStyle.Render(" enter: proceed  esc: cancel")
		}
		return s
	case bucketsConfirmDelete:
		if m.cursor < len(m.items) {
			s += fmt.Sprintf("\n "+warningStyle.Render("Are you sure you want to delete %q?")+" [y/N]", m.items[m.cursor].name)
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
		url := ""
		if b.isPublic {
			url = "  " + dimStyle.Render(publicURL(b.name, ""))
		}

		if i == m.cursor {
			icon := accessIconSelected(b.isPublic)
			row := fmt.Sprintf(" %s  %s  %s  %s  %s  %s",
				pad(name, colName), region, icon, count, size, created)
			if b.isPublic {
				row += "  " + publicURL(b.name, "")
			}
			s += rowSelectedStyle.Width(tableWidth).Render(row) + "\n"
		} else {
			icon := accessIcon(b.isPublic)
			row := fmt.Sprintf(" %s  %s  %s  %s  %s  %s%s",
				pad(name, colName), region, icon, count, size, created, url)
			s += rowStyle.Render(row) + "\n"
		}
	}

	if end < len(m.items) {
		s += dimStyle.Render(fmt.Sprintf(" ▼ %d more below", len(m.items)-end)) + "\n"
	}

	s += "\n" + helpStyle.Render(" [enter] Detail  [c] Create  [d] Delete  [r] Refresh  [u] Users  [q] Quit")
	return s
}

func (m bucketsModel) viewDetail() string {
	if m.cursor >= len(m.items) {
		return ""
	}
	bucket := m.items[m.cursor]

	s := breadcrumbStyle.Render(fmt.Sprintf("dashboard > buckets > %s", bucket.name)) + "\n"
	s += screenTitleStyle.Render(bucket.name) + "\n"

	detailWidth := 60
	if m.width > detailWidth {
		detailWidth = m.width
	}
	s += separator(detailWidth) + "\n"

	if m.detailMessage != "" {
		s += " " + successStyle.Render(m.detailMessage) + "\n"
	}

	if m.loading {
		s += fmt.Sprintf("\n %s Loading...\n", m.spinner.View())
		return s
	}

	// Bucket metadata
	labelStyle := lipgloss.NewStyle().Foreground(colorMuted)
	valueStyle := lipgloss.NewStyle().Foreground(colorText)
	s += "\n"
	s += fmt.Sprintf("  %s   %s\n", labelStyle.Render("Region:"), valueStyle.Render(bucket.region))
	s += fmt.Sprintf("  %s  %s\n", labelStyle.Render("Objects:"), valueStyle.Render(formatCount(bucket.objects)))
	s += fmt.Sprintf("  %s     %s\n", labelStyle.Render("Size:"), valueStyle.Render(formatSize(bucket.sizeBytes)))
	s += fmt.Sprintf("  %s  %s\n", labelStyle.Render("Created:"), valueStyle.Render(bucket.created))
	s += "\n"

	// Bucket-level access toggle (row 0)
	bucketAccessLabel := accessIcon(bucket.isPublic) + " " + accessWord(bucket.isPublic)
	if bucket.isPublic {
		bucketAccessLabel += "  " + dimStyle.Render(publicURL(bucket.name, ""))
	}
	if m.detailCursor == 0 {
		row := fmt.Sprintf("  Bucket Access: %s", bucketAccessLabel)
		s += rowSelectedStyle.Width(detailWidth).Render(row) + "\n"
	} else {
		row := fmt.Sprintf("  Bucket Access: %s", bucketAccessLabel)
		s += rowStyle.Render(row) + "\n"
	}

	s += "\n"

	// Prefix list
	if len(m.prefixes) > 0 {
		s += "  " + labelStyle.Render("Prefixes:") + "\n"
		s += "  " + lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", 40)) + "\n"

		for i, p := range m.prefixes {
			icon := accessIcon(p.isPublic)
			label := accessWord(p.isPublic)
			url := ""
			if p.isPublic {
				url = "  " + dimStyle.Render(publicURL(bucket.name, p.prefix))
			}
			row := fmt.Sprintf("    %s  %s %s%s", pad(p.prefix, 30), icon, label, url)
			if m.detailCursor == i+1 {
				s += rowSelectedStyle.Width(detailWidth).Render(row) + "\n"
			} else {
				s += rowStyle.Render(row) + "\n"
			}
		}
	} else {
		s += "  " + dimStyle.Render("No prefixes (folders) found.") + "\n"
	}

	// Sub-mode overlays
	switch m.mode {
	case bucketDetailAddPrefix:
		s += "\n  New prefix name:\n"
		s += "  " + m.prefixInput.View() + "\n\n"
		s += helpStyle.Render("  enter: add  esc: cancel")
		return s
	case bucketDetailConfirm:
		s += "\n"
		s += "  " + warningStyle.Render(m.confirmAction) + "\n"
		s += "  " + warningStyle.Render("Type \"yes\" to confirm:") + "\n"
		s += "  " + m.confirmInput2.View() + "\n\n"
		s += helpStyle.Render("  enter: confirm  esc: cancel")
		return s
	}

	s += "\n" + helpStyle.Render("  [enter] Toggle access  [p] Add prefix  [d] Delete prefix  [r] Refresh  [esc] Back")
	return s
}

// accessWord returns "PUBLIC" or "PRIVATE" as styled text.
func accessWord(public bool) string {
	if public {
		return warningStyle.Render("PUBLIC")
	}
	return dimStyle.Render("PRIVATE")
}
