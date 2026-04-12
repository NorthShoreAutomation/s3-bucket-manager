package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
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
	bucketsList                   bucketsMode = iota
	bucketsCreate                             // typing a new bucket name
	bucketsTypeDelete                         // type 'delete' to start deletion
	bucketsConfirmDelete                      // are you sure? [y/N]
	bucketsConfirmDeleteNonEmpty              // type bucket name to confirm emptying
	bucketDetail                              // viewing a single bucket's details
	bucketDetailAddPrefix                     // typing a new prefix name
	bucketDetailConfirm                       // type 'yes' to confirm access change
	bucketDetailDeleteFolder                  // type 'delete' to confirm folder deletion
	bucketDetailPickUser                      // selecting a user to add
	bucketDetailPickPerm                      // choosing permission level for new user
	bucketDetailConfirmRemoveUser             // confirm removing user access
)

type bucketUserItem struct {
	username   string
	permission model.PermissionLevel
}

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
	detailCursor  int             // cursor position in detail view (0 = bucket row, 1+ = users, then prefixes)
	prefixes      []prefixItem    // prefixes for currently selected bucket
	prefixInput   textinput.Model // for adding new prefixes
	confirmInput2 textinput.Model // for typing 'yes' to confirm access change
	confirmAction string          // description of what will happen
	confirmFunc   func() tea.Msg  // the action to execute on confirmation
	detailMessage string          // status message in detail view

	// Bucket user access
	bucketUsers        []bucketUserItem // users with access to current bucket
	bucketUsersLoading bool             // loading users separately
	bucketUsersError   string
	availableUsers     []userItem // for the user picker (managed users not yet assigned)
	userPickerCursor   int
	pendingUser        string // user selected in picker, awaiting permission

	// File browser fields
	browsePrefix       string                 // current prefix being browsed (empty = root)
	browseItems        []awsClient.BrowseItem // folders + files at current prefix
	browseCursor       int                    // cursor in browse view
	browseOffset       int                    // scroll offset in browse view
	folderDeleteKey    string                 // key of folder being deleted
	folderDeleteCnt    int64                  // object count for folder delete confirm
	folderDeletePublic bool                   // whether the folder being deleted also has public access

	// File picker fields
	filePicker     filePickerModel
	showFilePicker bool
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
		for i, b := range buckets {
			items[i] = bucketItem{
				name:     b.Name,
				region:   b.Region,
				isPublic: b.IsPublic,
				created:  b.CreationDate.Format("2006-01-02"),
			}
		}
		return bucketsLoadedMsg{buckets: items}
	})
}

// loadBucketStatsCmd returns a command that fetches CloudWatch stats for all
// buckets concurrently. Each bucket's stats arrive as a separate bucketStatsMsg
// so the UI updates progressively.
func (m bucketsModel) loadBucketStatsCmd() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.items))
	for _, b := range m.items {
		name, region := b.name, b.region
		cmds = append(cmds, func() tea.Msg {
			ctx := context.Background()
			stats, _ := m.client.GetBucketStats(ctx, name, region)
			return bucketStatsMsg{
				name:      name,
				objects:   stats.ObjectCount,
				sizeBytes: stats.SizeBytes,
			}
		})
	}
	return tea.Batch(cmds...)
}

func (m bucketsModel) currentBucketName() string {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return ""
	}
	return m.items[m.cursor].name
}

func userPermsToItems(users []model.UserPermission) []bucketUserItem {
	items := make([]bucketUserItem, len(users))
	for i, u := range users {
		items[i] = bucketUserItem{username: u.Username, permission: u.Permission}
	}
	return items
}

func (m bucketsModel) update(msg tea.Msg) (bucketsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case errMsg:
		m.loading = false
		m.bucketUsersLoading = false
		return m, nil

	case bucketsLoadedMsg:
		m.items = msg.buckets
		m.loading = false
		m.message = ""
		m.deleteProgress = ""
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		// Kick off background stats fetch (CloudWatch) — list renders immediately
		return m, m.loadBucketStatsCmd()

	case bucketStatsMsg:
		for i := range m.items {
			if m.items[i].name == msg.name {
				m.items[i].objects = msg.objects
				m.items[i].sizeBytes = msg.sizeBytes
				break
			}
		}
		return m, nil

	case operationDoneMsg:
		m.message = msg.message
		m.detailMessage = msg.message
		// If we were browsing files, reload the current directory
		if m.browsePrefix != "" || len(m.browseItems) > 0 {
			m.mode = bucketDetail
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
		// If we're in detail view, reload prefixes and bucket users
		if m.mode == bucketDetail || m.mode == bucketDetailConfirm {
			m.mode = bucketDetail
			if m.cursor < len(m.items) {
				return m, tea.Batch(m.loadPrefixes(), m.loadBucketUsers())
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
		m.mode = bucketDetail
		m.detailCursor = 0
		// If no prefixes, auto-load root contents to show files
		if len(m.prefixes) == 0 {
			m.browsePrefix = ""
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
		m.loading = false
		return m, nil

	case browseLoadedMsg:
		m.browseItems = msg.items
		m.loading = false
		m.browseCursor = 0
		m.browseOffset = 0
		return m, nil

	case folderCountedMsg:
		m.loading = false
		m.folderDeleteKey = msg.key
		m.folderDeleteCnt = msg.count
		m.folderDeletePublic = msg.isPublic
		m.mode = bucketDetailDeleteFolder
		m.deleteInput.SetValue("")
		m.deleteInput.Focus()
		return m, textinput.Blink

	case bucketUsersLoadedMsg:
		if msg.bucket != m.currentBucketName() {
			return m, nil
		}
		m.bucketUsersError = ""
		if msg.err != nil {
			m.bucketUsersError = msg.err.Error()
			m.bucketUsers = nil
			m.bucketUsersLoading = false
			return m, nil
		}
		m.bucketUsers = userPermsToItems(msg.users)
		m.bucketUsersLoading = false
		return m, nil

	case userPickerLoadedMsg:
		if msg.bucket != m.currentBucketName() || m.mode != bucketDetailPickUser {
			return m, nil
		}
		// Filter out users already assigned to this bucket
		assigned := make(map[string]bool, len(m.bucketUsers))
		for _, u := range m.bucketUsers {
			assigned[u.username] = true
		}
		m.availableUsers = nil
		for _, u := range msg.items {
			if !assigned[u.name] {
				m.availableUsers = append(m.availableUsers, u)
			}
		}
		m.loading = false
		m.userPickerCursor = 0
		return m, nil

	case bucketAccessUpdatedMsg:
		if msg.bucket != m.currentBucketName() {
			return m, nil
		}
		m.bucketUsers = userPermsToItems(msg.users)
		m.bucketUsersError = ""
		m.detailMessage = msg.message
		m.loading = false
		if m.detailCursor > len(m.bucketUsers) {
			m.detailCursor = max(0, len(m.bucketUsers))
		}
		return m, nil

	case folderDeleteProgressMsg:
		m.deleteProgress = fmt.Sprintf("Deleting folder... %s objects removed", formatWithCommas(msg.deleted))
		return m, nil

	case downloadDoneMsg:
		m.loading = false
		m.detailMessage = fmt.Sprintf("Downloaded %s to %s", msg.filename, msg.path)
		m.deleteProgress = ""
		return m, nil

	case uploadDoneMsg:
		m.loading = false
		m.detailMessage = fmt.Sprintf("Uploaded %s", msg.filename)
		m.deleteProgress = ""
		// Reload the browse view to show the new file
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())

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
		case bucketDetailDeleteFolder:
			return m.updateDeleteFolder(msg)
		case bucketDetailPickUser:
			return m.updateBucketDetailPickUser(msg)
		case bucketDetailPickPerm:
			return m.updateBucketDetailPickPerm(msg)
		case bucketDetailConfirmRemoveUser:
			return m.updateBucketDetailConfirmRemoveUser(msg)
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
	case "enter", "right", "l":
		if len(m.items) > 0 {
			m.mode = bucketDetail
			m.detailCursor = 0
			m.detailMessage = ""
			m.loading = true
			m.bucketUsers = nil
			m.bucketUsersLoading = true
			m.bucketUsersError = ""
			return m, tea.Batch(m.spinner.Tick, m.loadPrefixes(), m.loadBucketUsers())
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

// cursorSection returns which section the detail cursor is currently in.
func (m bucketsModel) cursorSection() string {
	if m.detailCursor == 0 {
		return "bucket"
	}
	if m.detailCursor <= len(m.bucketUsers) {
		return "users"
	}
	return "prefixes"
}

// userIndex returns the index into bucketUsers for the current cursor position.
func (m bucketsModel) userIndex() int {
	return m.detailCursor - 1
}

// prefixIndex returns the index into prefixes for the current cursor position.
func (m bucketsModel) prefixIndex() int {
	return m.detailCursor - 1 - len(m.bucketUsers)
}

func (m bucketsModel) updateDetail(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	// When browsing inside a prefix, delegate to browse handler
	if m.browsePrefix != "" || len(m.browseItems) > 0 {
		return m.updateBrowse(msg)
	}

	maxRow := len(m.bucketUsers) + len(m.prefixes) // row 0 = bucket toggle
	switch msg.String() {
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < maxRow {
			m.detailCursor++
		}
	case "right", "l":
		// Drill into selected prefix (only for prefix rows)
		section := m.cursorSection()
		if section == "prefixes" {
			idx := m.prefixIndex()
			if idx >= 0 && idx < len(m.prefixes) {
				m.browsePrefix = m.prefixes[idx].prefix
				m.browseCursor = 0
				m.browseOffset = 0
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
			}
		}
	case "enter":
		section := m.cursorSection()
		switch section {
		case "bucket":
			return m.toggleSelected()
		case "users":
			// Cycle permission for the selected user
			idx := m.userIndex()
			if idx >= 0 && idx < len(m.bucketUsers) {
				u := m.bucketUsers[idx]
				newPerm := nextPermission(u.permission)
				m.loading = true
				m.detailMessage = ""
				bucket := m.items[m.cursor]
				username := u.username
				updatedUsers := make([]model.UserPermission, 0, len(m.bucketUsers))
				for i, item := range m.bucketUsers {
					perm := item.permission
					if i == idx {
						perm = newPerm
					}
					updatedUsers = append(updatedUsers, model.UserPermission{
						Username:   item.username,
						Permission: perm,
					})
				}
				return m, func() tea.Msg {
					ctx := context.Background()
					// Get user's full access, update this bucket's permission
					access, err := m.client.GetUserBucketAccess(ctx, username)
					if err != nil {
						return errMsg{err: err}
					}
					found := false
					for i, a := range access {
						if a.Bucket == bucket.name {
							access[i].Permission = newPerm
							found = true
							break
						}
					}
					if !found {
						access = append(access, model.BucketAccess{Bucket: bucket.name, Permission: newPerm})
					}
					err = m.client.SetUserBucketAccess(ctx, username, access)
					if err != nil {
						return errMsg{err: err}
					}
					return bucketAccessUpdatedMsg{
						bucket:  bucket.name,
						message: fmt.Sprintf("Updated %s to %s", username, newPerm),
						users:   updatedUsers,
					}
				}
			}
		case "prefixes":
			return m.toggleSelected()
		}
	case "a":
		// Add user — available from bucket toggle or user section
		section := m.cursorSection()
		if (section == "bucket" || section == "users") && !m.bucketUsersLoading {
			m.mode = bucketDetailPickUser
			m.userPickerCursor = 0
			m.loading = true
			return m, func() tea.Msg {
				ctx := context.Background()
				users, err := m.client.ListManagedUsers(ctx)
				if err != nil {
					return errMsg{err: err}
				}
				items := make([]userItem, len(users))
				for i, u := range users {
					items[i] = userItem{
						name:     u.Name,
						keyCount: u.KeyCount,
						created:  u.CreateDate.Format("2006-01-02"),
					}
				}
				return userPickerLoadedMsg{bucket: m.currentBucketName(), items: items}
			}
		}
	case "d":
		section := m.cursorSection()
		switch section {
		case "users":
			// Remove user access
			idx := m.userIndex()
			if idx >= 0 && idx < len(m.bucketUsers) {
				m.mode = bucketDetailConfirmRemoveUser
			}
		case "prefixes":
			// Delete selected prefix
			idx := m.prefixIndex()
			if idx >= 0 && idx < len(m.prefixes) {
				p := m.prefixes[idx]
				bucket := m.items[m.cursor]
				m.loading = true
				m.deleteProgress = "Counting objects..."
				return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
					ctx := context.Background()
					count, err := m.client.CountObjects(ctx, bucket.name, p.prefix, bucket.region)
					if err != nil {
						return errMsg{err: err}
					}
					return folderCountedMsg{name: p.prefix, key: p.prefix, count: count, isPublic: p.isPublic}
				})
			}
		}
	case "c":
		m.mode = bucketDetailAddPrefix
		m.prefixInput.SetValue("")
		m.prefixInput.Focus()
		return m, textinput.Blink
	case "r":
		m.loading = true
		m.detailMessage = ""
		m.bucketUsersLoading = true
		m.bucketUsersError = ""
		return m, tea.Batch(m.spinner.Tick, m.loadPrefixes(), m.loadBucketUsers())
	case "left", "h", "esc":
		m.mode = bucketsList
		m.detailMessage = ""
		m.prefixes = nil
		m.bucketUsers = nil
		m.bucketUsersError = ""
		m.loading = true
		return m, m.init()
	}
	return m, nil
}

func (m bucketsModel) updateBucketDetailPickUser(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.userPickerCursor > 0 {
			m.userPickerCursor--
		}
	case "down", "j":
		if m.userPickerCursor < len(m.availableUsers)-1 {
			m.userPickerCursor++
		}
	case "enter":
		if len(m.availableUsers) > 0 && m.userPickerCursor < len(m.availableUsers) {
			m.pendingUser = m.availableUsers[m.userPickerCursor].name
			m.mode = bucketDetailPickPerm
		}
	case "esc":
		m.mode = bucketDetail
		m.availableUsers = nil
		m.userPickerCursor = 0
	}
	return m, nil
}

func (m bucketsModel) updateBucketDetailPickPerm(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	var perm model.PermissionLevel
	switch msg.String() {
	case "1":
		perm = model.PermRead
	case "2":
		perm = model.PermReadWrite
	case "3":
		perm = model.PermReadWriteDelete
	case "esc":
		m.mode = bucketDetail
		m.pendingUser = ""
		return m, nil
	default:
		return m, nil
	}

	bucket := m.items[m.cursor]
	username := m.pendingUser
	m.loading = true
	m.mode = bucketDetail
	m.pendingUser = ""

	return m, func() tea.Msg {
		ctx := context.Background()
		// Get user's current access and upsert this bucket entry.
		access, err := m.client.GetUserBucketAccess(ctx, username)
		if err != nil {
			return errMsg{err: err}
		}
		found := false
		for i, a := range access {
			if a.Bucket == bucket.name {
				access[i].Permission = perm
				found = true
				break
			}
		}
		if !found {
			access = append(access, model.BucketAccess{Bucket: bucket.name, Permission: perm})
		}
		err = m.client.SetUserBucketAccess(ctx, username, access)
		if err != nil {
			return errMsg{err: err}
		}
		users := make([]model.UserPermission, 0, len(m.bucketUsers)+1)
		replaced := false
		for _, item := range m.bucketUsers {
			if item.username == username {
				users = append(users, model.UserPermission{Username: username, Permission: perm})
				replaced = true
				continue
			}
			users = append(users, model.UserPermission{Username: item.username, Permission: item.permission})
		}
		if !replaced {
			users = append(users, model.UserPermission{Username: username, Permission: perm})
		}
		return bucketAccessUpdatedMsg{
			bucket:  bucket.name,
			message: fmt.Sprintf("Added %s with %s access", username, perm),
			users:   users,
		}
	}
}

func (m bucketsModel) updateBucketDetailConfirmRemoveUser(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		idx := m.userIndex()
		if idx >= 0 && idx < len(m.bucketUsers) {
			u := m.bucketUsers[idx]
			bucket := m.items[m.cursor]
			username := u.username
			m.loading = true
			m.mode = bucketDetail

			return m, func() tea.Msg {
				ctx := context.Background()
				// Get user's full access, remove entry for this bucket
				access, err := m.client.GetUserBucketAccess(ctx, username)
				if err != nil {
					return errMsg{err: err}
				}
				updated := make([]model.BucketAccess, 0, len(access))
				for _, a := range access {
					if a.Bucket != bucket.name {
						updated = append(updated, a)
					}
				}
				err = m.client.SetUserBucketAccess(ctx, username, updated)
				if err != nil {
					return errMsg{err: err}
				}
				users := make([]model.UserPermission, 0, len(m.bucketUsers)-1)
				for _, item := range m.bucketUsers {
					if item.username == username {
						continue
					}
					users = append(users, model.UserPermission{Username: item.username, Permission: item.permission})
				}
				return bucketAccessUpdatedMsg{
					bucket:  bucket.name,
					message: fmt.Sprintf("Removed %s access to %s", username, bucket.name),
					users:   users,
				}
			}
		}
		m.mode = bucketDetail
	default:
		m.mode = bucketDetail
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
				err := m.client.SetPrefixPrivate(ctx, bucket.name, "", bucket.region)
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
			err := m.client.SetPrefixPublic(ctx, bucket.name, "", bucket.region)
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

	// Toggle a prefix (offset by user rows)
	idx := m.prefixIndex()
	if idx < 0 || idx >= len(m.prefixes) {
		return m, nil
	}
	p := m.prefixes[idx]

	if p.isPublic {
		// Making private -- no warning needed
		m.loading = true
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.SetPrefixPrivate(ctx, bucket.name, p.prefix, bucket.region)
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
		err := m.client.SetPrefixPublic(ctx, bucket.name, p.prefix, bucket.region)
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
		bucket := m.items[m.cursor]
		m.loading = true
		m.mode = bucketDetail
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.CreatePrefix(ctx, bucket.name, name, bucket.region)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Added prefix %s (private by default)", name)}
		}
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
		prefixNames, err := m.client.ListPrefixes(ctx, bucket.name, bucket.region)
		if err != nil {
			return errMsg{err: err}
		}
		accesses, err := m.client.GetPrefixAccessStatus(ctx, bucket.name, bucket.region, prefixNames)
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

// loadBucketUsers fetches the list of users with access to the currently selected bucket.
func (m bucketsModel) loadBucketUsers() tea.Cmd {
	bucket := m.items[m.cursor]
	return func() tea.Msg {
		ctx := context.Background()
		users, err := m.client.ListBucketUsers(ctx, bucket.name)
		if err != nil {
			return bucketUsersLoadedMsg{bucket: bucket.name, err: err}
		}
		return bucketUsersLoadedMsg{bucket: bucket.name, users: users}
	}
}

// --- Views ---

func (m bucketsModel) view() string {
	switch m.mode {
	case bucketDetail, bucketDetailAddPrefix, bucketDetailConfirm, bucketDetailDeleteFolder:
		return m.viewDetail()
	case bucketDetailPickUser:
		return m.viewPickUser()
	case bucketDetailPickPerm:
		return m.viewPickPerm()
	case bucketDetailConfirmRemoveUser:
		return m.viewConfirmRemoveUser()
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

	s += "\n" + helpStyle.Render(" [enter/→] Detail  [c] Create  [d] Delete  [r] Refresh  [u] Users  [q] Quit")
	return s
}

func (m bucketsModel) viewDetail() string {
	if m.cursor >= len(m.items) {
		return ""
	}
	bucket := m.items[m.cursor]

	// Breadcrumb includes browse path when drilling into contents
	crumb := fmt.Sprintf("dashboard > buckets > %s", bucket.name)
	if m.browsePrefix != "" {
		crumb += " > /" + m.browsePrefix
	}
	s := breadcrumbStyle.Render(crumb) + "\n"
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
		if m.deleteProgress != "" {
			s += fmt.Sprintf("\n %s %s\n", m.spinner.View(), m.deleteProgress)
		} else {
			s += fmt.Sprintf("\n %s Loading...\n", m.spinner.View())
		}
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

	// USER ACCESS section
	if m.bucketUsersLoading {
		s += "\n  " + dimStyle.Render("Loading user access...") + "\n"
	} else {
		s += "\n"
		s += fmt.Sprintf("  %s\n", tableHeaderStyle.Render(fmt.Sprintf("  %-30s %s", fmt.Sprintf("USER ACCESS (%d)", len(m.bucketUsers)), "PERMISSION")))
		if m.bucketUsersError != "" {
			s += "  " + errorStyle.Render(m.bucketUsersError) + "\n"
		} else if len(m.bucketUsers) == 0 {
			s += "  " + dimStyle.Render("No users assigned.") + "\n"
		} else {
			for i, u := range m.bucketUsers {
				cursor := "  "
				if m.detailCursor == i+1 {
					cursor = "> "
				}
				uname := pad(u.username, 30)
				permStr := string(u.permission)
				if m.detailCursor == i+1 {
					uname = rowSelectedStyle.Render(pad(u.username, 30))
					permStr = rowSelectedStyle.Render(string(u.permission))
				}
				s += fmt.Sprintf("%s  %s  %s\n", cursor, uname, permStr)
			}
		}
	}

	s += "\n"

	// When file picker is active, render it instead of the browse/prefix view
	if m.showFilePicker {
		s += m.filePicker.view(detailWidth)
		return s
	}

	// Show browse items if we've drilled into a prefix, otherwise show prefix list
	if len(m.browseItems) > 0 || m.browsePrefix != "" {
		// Browsing contents
		if bucket.isPublic {
			s += " " + dimStyle.Render(publicURL(bucket.name, m.browsePrefix)) + "\n"
		}

		if len(m.browseItems) == 0 {
			s += "\n " + dimStyle.Render("Empty — no files or folders here.") + "\n"
		} else {
			header := fmt.Sprintf(" %s  %s  %s",
				pad("NAME", 40), padRight("SIZE", 10), pad("MODIFIED", 20))
			s += tableHeaderStyle.Width(detailWidth).Render(header) + "\n"

			visible := m.browseVisibleRows()
			end := m.browseOffset + visible
			if end > len(m.browseItems) {
				end = len(m.browseItems)
			}
			if m.browseOffset > 0 {
				s += dimStyle.Render(fmt.Sprintf(" ▲ %d more above", m.browseOffset)) + "\n"
			}
			for i := m.browseOffset; i < end; i++ {
				item := m.browseItems[i]
				var icon, name, sz, mod string
				if item.IsFolder {
					icon = "\U0001F4C1 "
					name = item.Name
				} else {
					icon = "   "
					name = item.Name
					sz = formatSize(item.Size)
					if !item.LastModified.IsZero() {
						mod = item.LastModified.Format("2006-01-02 15:04")
					}
				}
				display := icon + pad(name, 37)
				row := fmt.Sprintf(" %s  %s  %s", display, padRight(sz, 10), pad(mod, 20))
				if i == m.browseCursor {
					s += rowSelectedStyle.Width(detailWidth).Render(row) + "\n"
				} else {
					s += rowStyle.Render(row) + "\n"
				}
			}
			if end < len(m.browseItems) {
				s += dimStyle.Render(fmt.Sprintf(" ▼ %d more below", len(m.browseItems)-end)) + "\n"
			}

			// Show selected item URL for public buckets
			if bucket.isPublic && m.browseCursor < len(m.browseItems) {
				s += "\n " + lipgloss.NewStyle().Foreground(colorPrimary).Render(
					publicURL(bucket.name, m.browseItems[m.browseCursor].Key))
			}
		}

		// Confirm overlay (for file delete while browsing)
		if m.mode == bucketDetailConfirm {
			s += "\n"
			s += "  " + warningStyle.Render(m.confirmAction) + "\n"
			s += "  " + warningStyle.Render("Type \"yes\" to confirm:") + "\n"
			s += "  " + m.confirmInput2.View() + "\n\n"
			s += helpStyle.Render("  enter: confirm  esc: cancel")
			return s
		}

		// Folder delete confirm overlay
		if m.mode == bucketDetailDeleteFolder {
			s += "\n"
			s += "  " + warningStyle.Render(fmt.Sprintf("This will delete %s files in %s", formatWithCommas(m.folderDeleteCnt), m.folderDeleteKey)) + "\n"
			s += "  " + warningStyle.Render("Type 'delete' to continue:") + "\n"
			s += "  " + m.deleteInput.View() + "\n\n"
			s += helpStyle.Render("  enter: delete  esc: cancel")
			return s
		}

		s += "\n" + helpStyle.Render("  [→] Open folder  [←] Back  [p] Upload  [g] Download  [d] Delete  [r] Refresh  [esc] Prefix list")
	} else {
		// Prefix list
		if len(m.prefixes) > 0 {
			s += "  " + labelStyle.Render("Prefixes:") + "  " + dimStyle.Render("[→ to browse]") + "\n"
			s += "  " + lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", 40)) + "\n"

			for i, p := range m.prefixes {
				icon := accessIcon(p.isPublic)
				label := accessWord(p.isPublic)
				url := ""
				if p.isPublic {
					url = "  " + dimStyle.Render(publicURL(bucket.name, p.prefix))
				}
				row := fmt.Sprintf("    %s  %s %s%s", pad(p.prefix, 30), icon, label, url)
				if m.detailCursor == len(m.bucketUsers)+i+1 {
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

		// Context-sensitive help bar
		switch m.cursorSection() {
		case "bucket":
			s += "\n" + helpStyle.Render("  [enter] Toggle public/private  [a] Add user  [r] Refresh  [esc] Back")
		case "users":
			s += "\n" + helpStyle.Render("  [enter] Cycle permission  [a] Add user  [d] Remove  [r] Refresh  [esc] Back")
		case "prefixes":
			s += "\n" + helpStyle.Render("  [enter] Toggle access  [→] Browse  [c] Add prefix  [d] Delete prefix  [r] Refresh  [←] Back")
		}
	}
	return s
}

func (m bucketsModel) viewPickUser() string {
	bucket := m.items[m.cursor]
	s := breadcrumbStyle.Render(fmt.Sprintf("dashboard > buckets > %s > Add user", bucket.name)) + "\n"
	s += screenTitleStyle.Render("Select a user:") + "\n\n"

	if m.loading {
		s += fmt.Sprintf(" %s Loading users...\n", m.spinner.View())
		return s
	}

	if len(m.availableUsers) == 0 {
		s += "  All managed users are already assigned.\n\n"
		s += helpStyle.Render("  [esc] Back")
		return s
	}

	for i, u := range m.availableUsers {
		cursor := "  "
		if i == m.userPickerCursor {
			cursor = "> "
		}
		name := pad(u.name, 30)
		created := u.created
		if i == m.userPickerCursor {
			name = rowSelectedStyle.Render(pad(u.name, 30))
			created = rowSelectedStyle.Render(u.created)
		}
		s += fmt.Sprintf("%s%s %s\n", cursor, name, created)
	}

	s += "\n" + helpStyle.Render("  [enter] Select  [esc] Cancel")
	return s
}

func (m bucketsModel) viewPickPerm() string {
	bucket := m.items[m.cursor]
	s := breadcrumbStyle.Render(fmt.Sprintf("dashboard > buckets > %s > Add user", bucket.name)) + "\n"
	s += screenTitleStyle.Render(fmt.Sprintf("Permission for %q:", m.pendingUser)) + "\n\n"

	s += "  [1] read\n"
	s += "  [2] read-write\n"
	s += "  [3] read-write-delete\n\n"

	s += helpStyle.Render("  Press 1, 2, or 3  [esc] Cancel")
	return s
}

func (m bucketsModel) viewConfirmRemoveUser() string {
	bucket := m.items[m.cursor]
	s := breadcrumbStyle.Render(fmt.Sprintf("dashboard > buckets > %s", bucket.name)) + "\n"
	idx := m.userIndex()
	if idx >= 0 && idx < len(m.bucketUsers) {
		s += warningStyle.Render(fmt.Sprintf("Remove %q access to this bucket? [y/N]", m.bucketUsers[idx].username))
	}
	return s
}

// accessWord returns "PUBLIC" or "PRIVATE" as styled text.
func accessWord(public bool) string {
	if public {
		return warningStyle.Render("PUBLIC")
	}
	return dimStyle.Render("PRIVATE")
}

// --- File Browser ---

func (m bucketsModel) loadBrowse() tea.Cmd {
	bucket := m.items[m.cursor]
	prefix := m.browsePrefix
	return func() tea.Msg {
		ctx := context.Background()
		items, err := m.client.ListContents(ctx, bucket.name, prefix, bucket.region)
		if err != nil {
			return errMsg{err: err}
		}
		return browseLoadedMsg{items: items}
	}
}

func (m bucketsModel) browseVisibleRows() int {
	overhead := 6
	avail := m.height - overhead
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (m bucketsModel) updateBrowse(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	// When the file picker is active, delegate all keys to it
	if m.showFilePicker {
		fp, cmd, selected := m.filePicker.update(msg)
		m.filePicker = fp
		m.filePicker.width = m.width
		m.filePicker.height = m.height
		if msg.String() == "esc" {
			m.showFilePicker = false
			return m, nil
		}
		if selected != "" {
			// File selected — start upload
			m.showFilePicker = false
			m.loading = true
			bucket := m.items[m.cursor]
			prefix := m.browsePrefix
			filename := filepath.Base(selected)
			m.deleteProgress = fmt.Sprintf("Uploading %s...", filename)
			return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
				ctx := context.Background()
				f, err := os.Open(selected)
				if err != nil {
					return errMsg{err: fmt.Errorf("could not open %s: %w", selected, err)}
				}
				defer f.Close()
				key := prefix + filename
				err = m.client.UploadObject(ctx, bucket.name, key, bucket.region, f)
				if err != nil {
					return errMsg{err: err}
				}
				return uploadDoneMsg{filename: filename}
			})
		}
		return m, cmd
	}

	switch msg.String() {
	case "up", "k":
		if m.browseCursor > 0 {
			m.browseCursor--
			if m.browseCursor < m.browseOffset {
				m.browseOffset = m.browseCursor
			}
		}
	case "down", "j":
		if m.browseCursor < len(m.browseItems)-1 {
			m.browseCursor++
			visible := m.browseVisibleRows()
			if m.browseCursor >= m.browseOffset+visible {
				m.browseOffset = m.browseCursor - visible + 1
			}
		}
	case "right", "l":
		// Drill into folder
		if m.browseCursor < len(m.browseItems) && m.browseItems[m.browseCursor].IsFolder {
			m.browsePrefix = m.browseItems[m.browseCursor].Key
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
	case "left", "h":
		// Go up one level
		trimmed := strings.TrimSuffix(m.browsePrefix, "/")
		lastSlash := strings.LastIndex(trimmed, "/")
		if lastSlash >= 0 {
			m.browsePrefix = trimmed[:lastSlash+1]
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
		// At a top-level prefix — go back to prefix list
		m.browsePrefix = ""
		m.browseItems = nil
		return m, nil
	case "esc":
		// Esc always goes back to prefix list
		m.browsePrefix = ""
		m.browseItems = nil
		return m, nil
	case "enter":
		// Enter toggles access on the current item if it's a folder
		if m.browseCursor < len(m.browseItems) && m.browseItems[m.browseCursor].IsFolder {
			return m.toggleBrowseFolder()
		}
	case "d":
		if m.browseCursor < len(m.browseItems) {
			item := m.browseItems[m.browseCursor]
			bucket := m.items[m.cursor]
			if item.IsFolder {
				// Folder delete — count objects first (with spinner)
				m.loading = true
				m.deleteProgress = "Counting objects..."
				return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
					ctx := context.Background()
					count, err := m.client.CountObjects(ctx, bucket.name, item.Key, bucket.region)
					if err != nil {
						return errMsg{err: err}
					}
					accesses, err := m.client.GetPrefixAccessStatus(ctx, bucket.name, bucket.region, []string{item.Key})
					if err != nil {
						return errMsg{err: err}
					}
					isPublic := len(accesses) > 0 && accesses[0].IsPublic
					return folderCountedMsg{name: item.Name, key: item.Key, count: count, isPublic: isPublic}
				})
			}
			// File delete
			m.confirmAction = fmt.Sprintf("Delete file %q?", item.Name)
			m.confirmFunc = func() tea.Msg {
				ctx := context.Background()
				err := m.client.DeleteObject(ctx, bucket.name, item.Key, bucket.region)
				if err != nil {
					return errMsg{err: err}
				}
				return operationDoneMsg{message: fmt.Sprintf("Deleted %s", item.Name)}
			}
			m.mode = bucketDetailConfirm
			m.confirmInput2.SetValue("")
			m.confirmInput2.Focus()
			return m, textinput.Blink
		}
	case "g":
		// Download selected file to current working directory
		if m.browseCursor < len(m.browseItems) && !m.browseItems[m.browseCursor].IsFolder {
			item := m.browseItems[m.browseCursor]
			bucket := m.items[m.cursor]
			m.loading = true
			m.deleteProgress = fmt.Sprintf("Downloading %s...", item.Name)
			return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
				ctx := context.Background()
				cwd, err := os.Getwd()
				if err != nil {
					return errMsg{err: fmt.Errorf("could not get working directory: %w", err)}
				}
				body, err := m.client.DownloadObject(ctx, bucket.name, item.Key, bucket.region)
				if err != nil {
					return errMsg{err: fmt.Errorf("could not download %s: %w", item.Name, err)}
				}
				defer body.Close()
				outPath := filepath.Join(cwd, item.Name)
				f, err := os.Create(outPath)
				if err != nil {
					return errMsg{err: fmt.Errorf("could not create file %s: %w", outPath, err)}
				}
				defer f.Close()
				if _, err := io.Copy(f, body); err != nil {
					return errMsg{err: fmt.Errorf("could not write file %s: %w", outPath, err)}
				}
				return downloadDoneMsg{filename: item.Name, path: outPath}
			})
		}
	case "p":
		// Open local file picker for upload
		fp := newFilePicker()
		fp.width = m.width
		fp.height = m.height
		fp = fp.loadDir()
		m.filePicker = fp
		m.showFilePicker = true
		return m, nil
	case "r":
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
	}
	return m, nil
}

func (m bucketsModel) toggleBrowseFolder() (bucketsModel, tea.Cmd) {
	if m.browseCursor >= len(m.browseItems) {
		return m, nil
	}
	item := m.browseItems[m.browseCursor]
	if !item.IsFolder {
		return m, nil
	}
	bucket := m.items[m.cursor]

	// Use the folder's key as the prefix to toggle
	prefix := item.Key

	// Check current access status
	accesses, _ := m.client.GetPrefixAccessStatus(context.Background(), bucket.name, bucket.region, []string{prefix})
	isPublic := len(accesses) > 0 && accesses[0].IsPublic

	if isPublic {
		m.loading = true
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.SetPrefixPrivate(ctx, bucket.name, prefix, bucket.region)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Set %s to PRIVATE", prefix)}
		}
	}

	// Making public requires confirmation
	m.confirmAction = fmt.Sprintf("Making %s%s public requires changing the bucket's public access settings.", bucket.name+"/", prefix)
	m.confirmFunc = func() tea.Msg {
		ctx := context.Background()
		err := m.client.SetPrefixPublic(ctx, bucket.name, prefix, bucket.region)
		if err != nil {
			return errMsg{err: err}
		}
		return operationDoneMsg{message: fmt.Sprintf("Set %s to PUBLIC", prefix)}
	}
	m.mode = bucketDetailConfirm
	m.confirmInput2.SetValue("")
	m.confirmInput2.Focus()
	return m, textinput.Blink
}

func (m bucketsModel) updateDeleteFolder(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.deleteInput.Value())
		if typed != "delete" {
			m.detailMessage = "You must type 'delete' to confirm. Cancelled."
			m.deleteProgress = ""
			m.mode = bucketDetail
			return m, nil
		}
		bucket := m.items[m.cursor]
		m.loading = true
		m.deleteProgress = "Deleting folder... 0 objects removed"
		m.mode = bucketDetail
		folderKey := m.folderDeleteKey
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.DeletePrefix(ctx, bucket.name, folderKey, bucket.region, func(deleted int64) {
				if prog != nil {
					prog.Send(folderDeleteProgressMsg{deleted: deleted})
				}
			})
			if err != nil {
				return errMsg{err: err}
			}
			if m.folderDeletePublic {
				err = m.client.SetPrefixPrivate(ctx, bucket.name, folderKey, bucket.region)
				if err != nil {
					return errMsg{err: err}
				}
			}
			return operationDoneMsg{message: fmt.Sprintf("Deleted folder %s and all its contents", folderKey)}
		}
	case "esc":
		m.deleteProgress = ""
		m.folderDeletePublic = false
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.deleteInput, cmd = m.deleteInput.Update(msg)
		return m, cmd
	}
}
