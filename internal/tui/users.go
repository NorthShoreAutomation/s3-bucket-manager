package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
)

type userItem struct {
	name          string
	keyCount      int
	created       string
	keyCountKnown bool
}

type usersMode int

const (
	usersList usersMode = iota
	usersCreate
	usersCreateBuckets
	usersCreatePerm
	usersConfirmDelete
	usersShowCreds
	usersDetail
	usersDetailPickBucket
	usersDetailPickPerm
	usersDetailConfirmRemove
	usersCreateReview
	usersDetailReview
	usersKeys
	usersKeyReview
	usersKeyDelete
)

var nextUsersRequest atomic.Uint64

// usersResultMsg belongs to the users model even when another screen is visible.
// The request and username stop old reads from replacing a newer resource.
type usersResultMsg struct {
	id       uint64
	kind     string
	username string
	items    []userItem
	buckets  []bucketItem
	access   []model.BucketAccess
	keys     []model.AccessKey
	key      *model.AccessKey
	message  string
	err      error
}

type userFilter struct {
	query  string
	active bool
}

func (f *userFilter) key(msg tea.KeyMsg) bool {
	if !f.active {
		if msg.String() == "/" {
			f.active = true
			return true
		}
		return false
	}
	switch msg.String() {
	case "esc":
		f.query = ""
		f.active = false
	case "enter":
		f.active = false
	case "backspace", "ctrl+h":
		r := []rune(f.query)
		if len(r) > 0 {
			f.query = string(r[:len(r)-1])
		}
	case "up", "down", "pgup", "pgdown":
		return false
	default:
		switch msg.Type {
		case tea.KeyRunes:
			f.query += string(msg.Runes)
		case tea.KeySpace:
			f.query += " "
		}
	}
	return true
}
func (f userFilter) matches(name string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(f.query))
}
func (f userFilter) view(count, total int) string {
	if f.query != "" || f.active {
		return fmt.Sprintf("Filter: %s  (%d of %d)  esc: clear\n", f.query, count, total)
	}
	return fmt.Sprintf("%d entries  /: filter\n", total)
}

type usersModel struct {
	client                                                                 *awsClient.Client
	items                                                                  []userItem
	cursor, offset                                                         int
	loading                                                                bool
	width, height                                                          int
	mode                                                                   usersMode
	nameInput                                                              textinput.Model
	message                                                                string
	messageError                                                           bool
	creds                                                                  credentialsModel
	detailUser                                                             string
	detailAccess                                                           []model.BucketAccess
	detailCursor, detailOffset                                             int
	detailLoading                                                          bool
	detailKnown                                                            bool
	detailMessage                                                          string
	detailError                                                            bool
	availableBuckets                                                       []bucketItem
	pickerCursor, pickerOffset                                             int
	pendingBucket                                                          string
	pendingPermission                                                      model.PermissionLevel
	pendingAccess                                                          []model.BucketAccess
	pendingAction                                                          string
	pendingOld                                                             model.PermissionLevel
	filter, pickerFilter, detailFilter                                     userFilter
	listRequest, detailRequest, pickerRequest, mutationRequest, keyRequest uint64
	mutating                                                               bool
	mutationCancel                                                         context.CancelFunc
	cancelRequested                                                        bool
	partialCreate                                                          bool
	deleteUser                                                             string
	keys                                                                   []model.AccessKey
	keyCursor, keyOffset                                                   int
	keyLoading                                                             bool
	keyListKnown                                                           bool
	keyTarget                                                              model.AccessKey
	keyAction                                                              string
	keyConfirm                                                             textinput.Model
	currentSessionKeyID                                                    string
	dialogOffset                                                           int
}

func newUsersModel(client *awsClient.Client) usersModel {
	ni := textinput.New()
	ni.Placeholder = "username"
	ni.CharLimit = 64
	confirmation := textinput.New()
	confirmation.CharLimit = 128
	m := usersModel{client: client, nameInput: ni, keyConfirm: confirmation, loading: true}
	if client != nil {
		m.currentSessionKeyID = client.CurrentAccessKeyID
	}
	return m
}
func (m *usersModel) resetDetailState() {
	m.detailUser = ""
	m.detailAccess = nil
	m.detailCursor = 0
	m.detailOffset = 0
	m.detailLoading = false
	m.detailKnown = false
	m.detailMessage = ""
	m.detailError = false
	m.pendingBucket = ""
	m.pendingAccess = nil
	m.detailFilter = userFilter{}
}
func (m usersModel) ownsInput() bool {
	return m.mutating || m.filter.active || m.pickerFilter.active || m.detailFilter.active || (m.mode != usersList && m.mode != usersDetail)
}
func (m *usersModel) init() tea.Cmd {
	m.loading = true
	m.listRequest = nextUsersRequest.Add(1)
	id, client := m.listRequest, m.client
	return func() tea.Msg {
		users, err := client.ListManagedUsers(context.Background())
		items := make([]userItem, len(users))
		for i, u := range users {
			items[i] = userItem{name: u.Name, keyCount: u.KeyCount, keyCountKnown: u.KeyCountKnown, created: u.CreateDate.Format("2006-01-02")}
		}
		return usersResultMsg{id: id, kind: "list", items: items, err: err}
	}
}
func (m usersModel) loadUsers() (usersModel, tea.Cmd) { cmd := m.init(); return m, cmd }
func (m usersModel) loadAccess() (usersModel, tea.Cmd) {
	m.detailLoading = true
	m.detailRequest = nextUsersRequest.Add(1)
	id, username, client := m.detailRequest, m.detailUser, m.client
	return m, func() tea.Msg {
		access, err := client.GetUserBucketAccess(context.Background(), username)
		return usersResultMsg{id: id, kind: "access", username: username, access: access, err: err}
	}
}
func (m usersModel) loadKeys() (usersModel, tea.Cmd) {
	m.keyLoading = true
	m.keyRequest = nextUsersRequest.Add(1)
	id, username, client := m.keyRequest, m.detailUser, m.client
	return m, func() tea.Msg {
		keys, err := client.ListAccessKeys(context.Background(), username)
		return usersResultMsg{id: id, kind: "keys", username: username, keys: keys, err: err}
	}
}
func (m usersModel) loadBucketPicker() (usersModel, tea.Cmd) {
	m.pickerRequest = nextUsersRequest.Add(1)
	m.loading = true
	m.pickerCursor = 0
	m.pickerOffset = 0
	m.pickerFilter = userFilter{}
	id, username, client := m.pickerRequest, m.detailUser, m.client
	return m, func() tea.Msg {
		buckets, err := client.ListBuckets(context.Background())
		items := make([]bucketItem, len(buckets))
		for i, b := range buckets {
			items[i] = bucketItem{name: b.Name, region: b.Region}
		}
		return usersResultMsg{id: id, kind: "buckets", username: username, buckets: items, err: err}
	}
}

func (m *usersModel) beginMutation() context.Context {
	m.mutating = true
	m.cancelRequested = false
	m.mutationRequest = nextUsersRequest.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	m.mutationCancel = cancel
	return ctx
}

func (m *usersModel) finishMutation() {
	if m.mutationCancel != nil {
		m.mutationCancel()
		m.mutationCancel = nil
	}
	m.mutating = false
	m.cancelRequested = false
}

func (m usersModel) mutationStatus() string {
	if m.cancelRequested {
		return "Cancelling. Waiting for the service result."
	}
	return "Applying. Esc / Ctrl+C: Cancel and wait."
}
func (m usersModel) update(msg tea.Msg) (usersModel, tea.Cmd) {
	previousMode := m.mode
	updated, cmd := m.updateContent(msg)
	if updated.mode != previousMode {
		updated.dialogOffset = 0
	}
	return updated, cmd
}

func (m usersModel) updateContent(msg tea.Msg) (usersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case usersResultMsg:
		return m.applyResult(msg)
	case errMsg:
		m.loading = false
		m.detailLoading = false
		m.keyLoading = false
		m.finishMutation()
		m.message = msg.err.Error()
		m.messageError = true
	case usersLoadedMsg:
		m.replaceUsers(msg.users)
		m.loading = false
	case credentialsMsg:
		m.creds = newCredentials(msg.username, msg.accessKeyID, msg.secretKey)
		m.mode = usersShowCreds
		m.loading = false
		m.finishMutation()
	case userAccessLoadedMsg:
		if msg.username == m.detailUser && m.mode != usersList && m.mode != usersCreateBuckets {
			m.detailAccess = append([]model.BucketAccess(nil), msg.access...)
			m.detailLoading = false
			m.detailKnown = true
			m.clampDetail()
		}
	case accessUpdatedMsg:
		if msg.username == m.detailUser && m.mode != usersList {
			m.detailAccess = append([]model.BucketAccess(nil), msg.access...)
			m.detailKnown = true
			m.detailLoading = false
			m.finishMutation()
			m.detailMessage = msg.message
			m.detailError = false
			m.clampDetail()
		}
	case createBucketPickerLoadedMsg:
		if m.mode == usersCreateBuckets {
			m.availableBuckets = append([]bucketItem(nil), msg.items...)
			m.loading = false
			m.pickerCursor = 0
		}
	case detailBucketPickerLoadedMsg:
		if msg.username == m.detailUser && m.mode == usersDetailPickBucket {
			m.setAvailableBuckets(msg.items)
			m.loading = false
			m.detailLoading = false
			m.pickerCursor = 0
		}
	case operationDoneMsg:
		m.message = msg.message
		m.mode = usersList
		return m.loadUsers()
	case tea.KeyMsg:
		if m.mutating {
			if msg.String() == "esc" || msg.String() == "ctrl+c" {
				m.cancelRequested = true
				if m.mutationCancel != nil {
					m.mutationCancel()
				}
			}
			return m, nil
		}
		if m.mode != usersList && m.mode != usersDetail && m.mode != usersKeys && m.mode != usersCreateBuckets && m.mode != usersDetailPickBucket && !m.creds.editingPath {
			if msg.String() == "pgdown" {
				m.dialogOffset += max(1, m.height-3)
				return m, nil
			}
			if msg.String() == "pgup" {
				m.dialogOffset = max(0, m.dialogOffset-max(1, m.height-3))
				return m, nil
			}
		}
		switch m.mode {
		case usersList:
			return m.updateList(msg)
		case usersCreate:
			return m.updateCreateName(msg)
		case usersCreateBuckets:
			return m.updateCreateBuckets(msg)
		case usersCreatePerm:
			return m.updateCreatePerm(msg)
		case usersCreateReview:
			return m.updateCreateReview(msg)
		case usersConfirmDelete:
			return m.updateConfirmDelete(msg)
		case usersShowCreds:
			return m.updateShowCreds(msg)
		case usersDetail:
			return m.updateDetail(msg)
		case usersDetailPickBucket:
			return m.updateDetailPickBucket(msg)
		case usersDetailPickPerm:
			return m.updateDetailPickPerm(msg)
		case usersDetailReview, usersDetailConfirmRemove:
			return m.updateDetailConfirmRemove(msg)
		case usersKeys, usersKeyReview, usersKeyDelete:
			return m.updateKeys(msg)
		}
	}
	return m, nil
}
func (m usersModel) applyResult(r usersResultMsg) (usersModel, tea.Cmd) {
	wasCancelled := m.cancelRequested
	if wasCancelled && errors.Is(r.err, context.Canceled) {
		var partial *awsClient.PartialUserCreationError
		if !errors.As(r.err, &partial) {
			r.err = fmt.Errorf("Cancellation requested. The service may have completed the request. Refresh before retrying: %w", r.err)
		}
	}
	switch r.kind {
	case "list":
		if r.id != m.listRequest {
			return m, nil
		}
		m.loading = false
		m.replaceUsers(r.items)
		if r.err != nil {
			m.message = "Partial user list: " + r.err.Error()
			if len(r.items) == 0 {
				m.message = "Could not load users: " + r.err.Error()
			}
			m.messageError = true
		} else {
			m.messageError = false
			if m.message == "" || strings.HasPrefix(m.message, "Partial user list:") || strings.HasPrefix(m.message, "Could not load users:") {
				m.message = ""
			}
		}
	case "access":
		if r.id != m.detailRequest || r.username != m.detailUser {
			return m, nil
		}
		m.detailLoading = false
		if r.err != nil {
			m.detailMessage = r.err.Error()
			m.detailError = true
		} else {
			m.detailAccess = append([]model.BucketAccess(nil), r.access...)
			m.detailKnown = true
			m.detailError = false
			m.detailMessage = ""
			m.clampDetail()
		}
	case "buckets":
		if r.id != m.pickerRequest || (m.mode != usersCreateBuckets && m.mode != usersDetailPickBucket) || r.username != m.detailUser {
			return m, nil
		}
		m.loading = false
		if r.err != nil {
			m.message = r.err.Error()
			m.messageError = true
			m.availableBuckets = nil
		} else {
			m.setAvailableBuckets(r.buckets)
			m.message = ""
			m.messageError = false
		}
	case "permission":
		if r.id != m.mutationRequest || r.username != m.detailUser {
			return m, nil
		}
		m.finishMutation()
		m.detailLoading = false
		if r.err != nil {
			m.detailMessage = r.err.Error()
			m.detailError = true
			return m, nil
		}
		m.detailAccess = append([]model.BucketAccess(nil), r.access...)
		m.detailKnown = true
		m.detailMessage = r.message
		m.detailError = false
		m.pendingAccess = nil
		m.mode = usersDetail
		m.clampDetail()
		return m.loadAccess()
	case "create", "new-key":
		if r.id != m.mutationRequest {
			return m, nil
		}
		m.finishMutation()
		m.loading = false
		m.keyLoading = false
		if r.err != nil && r.key == nil {
			m.message = r.err.Error()
			m.messageError = true
			if r.kind == "new-key" {
				m.detailMessage = r.err.Error()
				m.detailError = true
			}
			var partial *awsClient.PartialUserCreationError
			m.partialCreate = errors.As(r.err, &partial)
			return m, nil
		}
		if r.key != nil {
			m.creds = newCredentials(r.username, r.key.AccessKeyID, r.key.SecretAccessKey)
			m.creds.returnKeys = r.kind == "new-key"
			m.mode = usersShowCreds
			m.message = ""
			m.messageError = false
			if wasCancelled {
				m.creds.message = "Key creation completed before cancellation. Save this secret."
			}
			if r.err != nil {
				m.creds.saveError = r.err.Error()
			}
		}
	case "keys":
		if r.id != m.keyRequest || r.username != m.detailUser {
			return m, nil
		}
		m.keyLoading = false
		if r.err != nil {
			m.detailMessage = r.err.Error()
			m.detailError = true
			m.keys = nil
			m.keyListKnown = false
		} else {
			m.keys = append([]model.AccessKey(nil), r.keys...)
			m.keyListKnown = true
			m.detailMessage = ""
			m.detailError = false
			m.keyCursor = max(0, min(m.keyCursor, len(m.keys)-1))
		}
	case "key-change":
		if r.id != m.mutationRequest || r.username != m.detailUser {
			return m, nil
		}
		m.finishMutation()
		if r.err != nil {
			m.detailMessage = r.err.Error()
			m.detailError = true
			return m, nil
		}
		m.mode = usersKeys
		m.detailMessage = r.message
		m.detailError = false
		return m.loadKeys()
	case "delete":
		if r.id != m.mutationRequest {
			return m, nil
		}
		m.finishMutation()
		if r.err != nil {
			m.message = r.err.Error()
			m.messageError = true
			return m, nil
		}
		m.message = r.message
		m.messageError = false
		m.mode = usersList
		return m.loadUsers()
	}
	return m, nil
}
func (m *usersModel) replaceUsers(items []userItem) {
	focused := ""
	indices := m.userIndices()
	if m.cursor >= 0 && m.cursor < len(indices) {
		focused = m.items[indices[m.cursor]].name
	}
	m.items = append([]userItem(nil), items...)
	indices = m.userIndices()
	m.cursor = max(0, min(m.cursor, len(indices)-1))
	for i, index := range indices {
		if m.items[index].name == focused {
			m.cursor = i
			break
		}
	}
}
func (m *usersModel) setAvailableBuckets(items []bucketItem) {
	m.availableBuckets = nil
	assigned := map[string]bool{}
	if m.mode == usersDetailPickBucket {
		for _, a := range m.detailAccess {
			assigned[a.Bucket] = true
		}
	}
	for _, b := range items {
		if !assigned[b.name] {
			m.availableBuckets = append(m.availableBuckets, b)
		}
	}
}
func (m usersModel) userIndices() []int {
	var out []int
	for i, u := range m.items {
		if m.filter.matches(u.name) {
			out = append(out, i)
		}
	}
	return out
}
func (m usersModel) bucketIndices() []int {
	var out []int
	for i, b := range m.availableBuckets {
		if m.pickerFilter.matches(b.name) {
			out = append(out, i)
		}
	}
	return out
}
func (m usersModel) accessIndices() []int {
	var out []int
	for i, a := range m.detailAccess {
		if m.detailFilter.matches(a.Bucket) {
			out = append(out, i)
		}
	}
	return out
}
func (m *usersModel) clampDetail() {
	m.detailCursor = max(0, min(m.detailCursor, len(m.accessIndices())-1))
}
func userMove(cursor *int, key string, count, rows int) {
	switch key {
	case "up", "k":
		*cursor--
	case "down", "j":
		*cursor++
	case "pgup":
		*cursor -= rows
	case "pgdown":
		*cursor += rows
	case "home":
		*cursor = 0
	case "end":
		*cursor = count - 1
	}
	*cursor = max(0, min(*cursor, count-1))
}
func (m usersModel) rows() int {
	if m.height <= 0 {
		return 10
	}
	return max(1, m.height-10)
}
func (m usersModel) updateList(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if m.filter.key(msg) {
		m.cursor = 0
		m.offset = 0
		return m, nil
	}
	indices := m.userIndices()
	userMove(&m.cursor, msg.String(), len(indices), m.rows())
	switch msg.String() {
	case "esc":
		if m.filter.query != "" {
			m.filter.query = ""
			m.cursor = 0
			m.offset = 0
		}
	case "c":
		m.resetDetailState()
		m.mode = usersCreate
		m.nameInput.SetValue("")
		m.nameInput.Focus()
		m.partialCreate = false
		m.message = ""
		return m, textinput.Blink
	case "r":
		return m.loadUsers()
	case "d":
		if len(indices) > 0 {
			m.deleteUser = m.items[indices[m.cursor]].name
			m.mode = usersConfirmDelete
		}
	case "enter", "right":
		if len(indices) > 0 {
			m.detailUser = m.items[indices[m.cursor]].name
			m.detailCursor = 0
			m.detailOffset = 0
			m.detailFilter = userFilter{}
			m.mode = usersDetail
			m.detailAccess = nil
			m.detailKnown = false
			return m.loadAccess()
		}
	case "K":
		if len(indices) > 0 {
			m.detailUser = m.items[indices[m.cursor]].name
			m.mode = usersKeys
			return m.loadKeys()
		}
	}
	return m, nil
}
func (m usersModel) updateCreateName(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.message = "Enter a username."
			m.messageError = true
			return m, nil
		}
		m.mode = usersCreateBuckets
		return m.loadBucketPicker()
	case "esc":
		m.mode = usersList
		return m, nil
	default:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
}
func (m usersModel) updateCreateBuckets(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if m.pickerFilter.key(msg) {
		m.pickerCursor = 0
		m.pickerOffset = 0
		return m, nil
	}
	indices := m.bucketIndices()
	userMove(&m.pickerCursor, msg.String(), len(indices), m.rows())
	switch msg.String() {
	case "enter":
		if !m.loading && len(indices) > 0 {
			m.pendingBucket = m.availableBuckets[indices[m.pickerCursor]].name
			m.mode = usersCreatePerm
		}
	case "esc":
		m.mode = usersCreate
		m.nameInput.Focus()
		return m, textinput.Blink
	case "r":
		return m.loadBucketPicker()
	}
	return m, nil
}
func permissionKey(key string) (model.PermissionLevel, bool) {
	switch key {
	case "1":
		return model.PermRead, true
	case "2":
		return model.PermReadWrite, true
	case "3":
		return model.PermReadWriteDelete, true
	}
	return "", false
}
func (m usersModel) updateCreatePerm(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if msg.String() == "esc" {
		m.mode = usersCreateBuckets
		return m, nil
	}
	if p, ok := permissionKey(msg.String()); ok {
		m.pendingPermission = p
		m.mode = usersCreateReview
	}
	return m, nil
}
func (m usersModel) updateCreateReview(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = usersCreatePerm
	case "enter":
		if m.partialCreate {
			return m, nil
		}
		ctx := m.beginMutation()
		id, client, username, bucket, permission := m.mutationRequest, m.client, strings.TrimSpace(m.nameInput.Value()), m.pendingBucket, m.pendingPermission
		return m, func() tea.Msg {
			key, err := client.CreateManagedUser(ctx, username, []model.BucketAccess{{Bucket: bucket, Permission: permission}})
			return usersResultMsg{id: id, kind: "create", username: username, key: key, err: err}
		}
	}
	return m, nil
}
func (m usersModel) updateConfirmDelete(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if msg.String() != "y" && msg.String() != "Y" {
		m.mode = usersList
		return m, nil
	}
	ctx := m.beginMutation()
	id, client, username := m.mutationRequest, m.client, m.deleteUser
	return m, func() tea.Msg {
		err := client.DeleteManagedUser(ctx, username)
		return usersResultMsg{id: id, kind: "delete", username: username, message: fmt.Sprintf("Deleted user %q", username), err: err}
	}
}
func (m usersModel) updateDetail(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if m.detailFilter.key(msg) {
		m.detailCursor = 0
		m.detailOffset = 0
		return m, nil
	}
	indices := m.accessIndices()
	userMove(&m.detailCursor, msg.String(), len(indices), m.rows())
	switch msg.String() {
	case "enter", "e":
		if !m.detailLoading && len(indices) > 0 {
			a := m.detailAccess[indices[m.detailCursor]]
			m.pendingBucket = a.Bucket
			m.pendingOld = a.Permission
			m.pendingPermission = a.Permission
			m.pendingAction = "edit"
			m.mode = usersDetailPickPerm
		}
	case "a":
		if !m.detailLoading && (m.detailKnown || m.detailAccess != nil) {
			m.mode = usersDetailPickBucket
			m.pendingAction = "add"
			return m.loadBucketPicker()
		}
	case "d":
		if !m.detailLoading && len(indices) > 0 {
			a := m.detailAccess[indices[m.detailCursor]]
			m.pendingBucket = a.Bucket
			m.pendingOld = a.Permission
			m.pendingAction = "remove"
			m.pendingAccess = copyAccessWithout(m.detailAccess, a.Bucket)
			m.mode = usersDetailConfirmRemove
		}
	case "r":
		return m.loadAccess()
	case "K":
		m.mode = usersKeys
		return m.loadKeys()
	case "esc", "left":
		m.mode = usersList
		m.resetDetailState()
	}
	return m, nil
}
func copyAccessWithout(access []model.BucketAccess, bucket string) []model.BucketAccess {
	var out []model.BucketAccess
	for _, a := range access {
		if a.Bucket != bucket {
			out = append(out, a)
		}
	}
	return out
}
func (m usersModel) updateDetailPickBucket(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if m.pickerFilter.key(msg) {
		m.pickerCursor = 0
		m.pickerOffset = 0
		return m, nil
	}
	indices := m.bucketIndices()
	userMove(&m.pickerCursor, msg.String(), len(indices), m.rows())
	switch msg.String() {
	case "enter":
		if !m.loading && len(indices) > 0 {
			m.pendingBucket = m.availableBuckets[indices[m.pickerCursor]].name
			m.pendingOld = ""
			m.pendingAction = "add"
			m.mode = usersDetailPickPerm
		}
	case "esc":
		m.mode = usersDetail
	case "r":
		return m.loadBucketPicker()
	}
	return m, nil
}
func (m usersModel) updateDetailPickPerm(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if msg.String() == "esc" {
		if m.pendingAction == "add" {
			m.mode = usersDetailPickBucket
		} else {
			m.mode = usersDetail
		}
		return m, nil
	}
	if p, ok := permissionKey(msg.String()); ok {
		m.pendingPermission = p
		m.pendingAccess = copyAccessWithout(m.detailAccess, m.pendingBucket)
		m.pendingAccess = append(m.pendingAccess, model.BucketAccess{Bucket: m.pendingBucket, Permission: p})
		m.mode = usersDetailReview
	}
	return m, nil
}
func (m usersModel) updateDetailConfirmRemove(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "n":
		m.pendingAccess = nil
		m.mode = usersDetail
		return m, nil
	case "enter":
		ctx := m.beginMutation()
		id, client, username, access := m.mutationRequest, m.client, m.detailUser, append([]model.BucketAccess(nil), m.pendingAccess...)
		return m, func() tea.Msg {
			err := client.SetUserBucketAccess(ctx, username, access)
			return usersResultMsg{id: id, kind: "permission", username: username, access: access, message: "Permissions updated.", err: err}
		}
	}
	return m, nil
}

func (m usersModel) updateKeys(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if m.mode == usersKeys {
		userMove(&m.keyCursor, msg.String(), len(m.keys), m.rows())
		switch msg.String() {
		case "esc", "left":
			m.mode = usersDetail
			return m, nil
		case "r":
			return m.loadKeys()
		case "c":
			if m.keyLoading {
				return m, nil
			}
			if !m.keyListKnown {
				m.detailMessage = "Key count is unknown. Refresh the key list before creating a key."
				m.detailError = true
				return m, nil
			}
			if len(m.keys) >= 2 {
				m.detailMessage = "Two keys already exist. Deactivate and delete an unused key before creating another."
				m.detailError = true
				return m, nil
			}
			m.keyAction = "create"
			m.mode = usersKeyReview
		case "d", "x", "a":
			if m.keyLoading || len(m.keys) == 0 {
				return m, nil
			}
			m.keyTarget = m.keys[m.keyCursor]
			if msg.String() == "d" {
				if m.keyTarget.Status != "Inactive" {
					m.detailMessage = "Deactivate this key and verify its replacement before deleting it."
					m.detailError = true
					return m, nil
				}
				m.keyAction = "delete"
				m.keyConfirm.SetValue("")
				m.keyConfirm.Focus()
				m.mode = usersKeyDelete
				return m, textinput.Blink
			}
			if msg.String() == "x" {
				if m.keyTarget.Status != "Active" {
					return m, nil
				}
				m.keyAction = "deactivate"
			} else {
				if m.keyTarget.Status != "Inactive" {
					return m, nil
				}
				m.keyAction = "activate"
			}
			m.mode = usersKeyReview
		}
		return m, nil
	}
	if msg.String() == "esc" {
		m.mode = usersKeys
		return m, nil
	}
	if m.mode == usersKeyDelete {
		if msg.String() != "enter" {
			var cmd tea.Cmd
			m.keyConfirm, cmd = m.keyConfirm.Update(msg)
			return m, cmd
		}
		if m.keyConfirm.Value() != m.keyTarget.AccessKeyID {
			m.detailMessage = "Type the complete key ID to confirm deletion."
			m.detailError = true
			return m, nil
		}
	}
	if msg.String() != "enter" {
		return m, nil
	}
	ctx := m.beginMutation()
	id, client, username, action, keyID := m.mutationRequest, m.client, m.detailUser, m.keyAction, m.keyTarget.AccessKeyID
	return m, func() tea.Msg {
		if action == "create" {
			key, err := client.RotateAccessKey(ctx, username)
			return usersResultMsg{id: id, kind: "new-key", username: username, key: key, err: err}
		}
		var err error
		if action == "delete" {
			keys, listErr := client.ListAccessKeys(ctx, username)
			if listErr != nil {
				err = listErr
			} else {
				inactive := false
				for _, key := range keys {
					if key.AccessKeyID == keyID && key.Status == "Inactive" {
						inactive = true
					}
				}
				if !inactive {
					err = errors.New("This key is no longer inactive. Refresh and review before deleting.")
				} else {
					err = client.DeleteAccessKey(ctx, username, keyID)
				}
			}
		} else {
			err = client.SetAccessKeyActive(ctx, username, keyID, action == "activate")
		}
		return usersResultMsg{id: id, kind: "key-change", username: username, message: fmt.Sprintf("Key %s: %s complete.", keyID, action), err: err}
	}
}
func (m usersModel) updateShowCreds(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	if m.creds.editingPath {
		switch msg.String() {
		case "esc":
			m.creds.editingPath = false
			return m, nil
		case "enter":
			path := strings.TrimSpace(m.creds.pathInput.Value())
			if path == "" {
				m.creds.saveError = "Enter a destination path."
				return m, nil
			}
			if err := saveCredentialsAt(path, m.creds.username, m.creds.accessKeyID, m.creds.secretKey); err != nil {
				m.creds.saveError = err.Error()
				return m, nil
			}
			m.creds.saved = true
			m.creds.savePath = path
			m.creds.saveError = ""
			m.creds.editingPath = false
			m.creds.message = "Credentials saved."
			return m, nil
		default:
			var cmd tea.Cmd
			m.creds.pathInput, cmd = m.creds.pathInput.Update(msg)
			return m, cmd
		}
	}
	if m.creds.confirmExit {
		switch msg.String() {
		case "esc", "n":
			m.creds.confirmExit = false
		case "a":
			m.creds.captured = true
			return m.closeCredentials()
		}
		return m, nil
	}
	switch msg.String() {
	case "v":
		m.creds.revealed = !m.creds.revealed
	case "c":
		writer := m.creds.copyText
		if writer == nil {
			writer = newCredentials("", "", "").copyText
		}
		data := fmt.Sprintf("AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s", m.creds.accessKeyID, m.creds.secretKey)
		if err := writer(data); err != nil {
			m.creds.saveError = "Copy failed: " + err.Error()
			m.creds.message = "The secret remains available. Press c to retry."
		} else {
			m.creds.saveError = ""
			m.creds.message = "Credentials copied. Save them before leaving."
		}
	case "s":
		if m.creds.pathInput.Value() == "" {
			m.creds = newCredentials(m.creds.username, m.creds.accessKeyID, m.creds.secretKey)
		}
		m.creds.editingPath = true
		m.creds.pathInput.Focus()
		return m, textinput.Blink
	case "a":
		m.creds.captured = true
	case "esc", "enter", "q", "ctrl+c":
		if m.creds.saved || m.creds.captured {
			return m.closeCredentials()
		}
		m.creds.confirmExit = true
	}
	return m, nil
}
func (m usersModel) closeCredentials() (usersModel, tea.Cmd) {
	returnKeys := m.creds.returnKeys
	m.creds = credentialsModel{}
	if returnKeys {
		m.mode = usersKeys
		return m.loadKeys()
	}
	m.mode = usersList
	m.resetDetailState()
	return m.loadUsers()
}
func permissionLabel(p model.PermissionLevel) string {
	switch p {
	case "":
		return "None"
	case model.PermReadWrite:
		return "Read and upload"
	case model.PermReadWriteDelete:
		return "Read, upload, and delete"
	default:
		return "Read"
	}
}
func permissionChoices() string {
	return "[1] Read: list and download files\n[2] Read and upload: also create or replace files\n[3] Read, upload, and delete: also remove files\n"
}
func userBounds(cursor, offset, count, rows int) (int, int) {
	if count == 0 {
		return 0, 0
	}
	cursor = max(0, min(cursor, count-1))
	offset = max(0, min(offset, max(0, count-rows)))
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+rows {
		offset = cursor - rows + 1
	}
	return offset, min(count, offset+rows)
}
func (m usersModel) userHeading(location, title string) string {
	return breadcrumbStyle.Render(location) + "\n" + titleStyle.Render(title) + "\n"
}
func (m usersModel) notice(text string, isError bool) string {
	if text == "" {
		return ""
	}
	if isError {
		return errorStyle.Render(text) + "\n"
	}
	return successStyle.Render(text) + "\n"
}
func (m usersModel) view() string {
	content := m.viewContent()
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	lines := strings.Split(strings.TrimRight(wrapText(content, width), "\n"), "\n")
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	footer := lines[len(lines)-1]
	body := lines[:len(lines)-1]
	available := max(1, height-1)
	offset := max(0, min(m.dialogOffset, max(0, len(body)-available)))
	if m.mode == usersList || m.mode == usersDetail || m.mode == usersKeys || m.mode == usersCreateBuckets || m.mode == usersDetailPickBucket {
		offset = 0
	}
	return strings.Join(body[offset:min(len(body), offset+available)], "\n") + "\n" + footer
}

func (m usersModel) viewContent() string {
	if m.mode == usersShowCreds {
		return m.viewCredentials()
	}
	s := m.userHeading("Users", "Managed users") + m.notice(m.message, m.messageError)
	if m.mutating {
		s += warningStyle.Render(m.mutationStatus()) + "\n"
	}
	switch m.mode {
	case usersCreate:
		return s + "Name > Bucket > Permission > Review\n\nNew username:\n" + m.nameInput.View() + "\n\n" + helpStyle.Render("enter: next  esc: cancel")
	case usersCreateBuckets:
		return m.viewBucketPicker(true)
	case usersCreatePerm:
		return m.userHeading("Users > New user > Permission", "Choose permission") + "Bucket: " + m.pendingBucket + "\n\n" + permissionChoices() + "\n" + helpStyle.Render("1/2/3: choose  esc: back")
	case usersCreateReview:
		s = m.userHeading("Users > New user > Review", "Review new user") + m.notice(m.message, m.messageError)
		s += fmt.Sprintf("Username: %s\nBucket: %s\nAccess: %s\n", m.nameInput.Value(), m.pendingBucket, permissionLabel(m.pendingPermission))
		if m.partialCreate {
			return s + "\nCreation stopped after creating the user.\nReview the existing user in IAM.\nDo not repeat Create.\n" + helpStyle.Render("esc: back")
		}
		if m.mutating {
			return s + "\n" + m.mutationStatus()
		}
		return s + "\n" + helpStyle.Render("enter: Create user  esc: back")
	case usersConfirmDelete:
		return s + warningStyle.Render(fmt.Sprintf("Delete user %q and all their access keys?", m.deleteUser)) + "\n\n" + helpStyle.Render("y: Delete user  any other key: cancel")
	case usersDetail:
		return m.viewDetail()
	case usersDetailPickBucket:
		return m.viewPickBucket()
	case usersDetailPickPerm:
		return m.viewPickPerm()
	case usersDetailReview, usersDetailConfirmRemove:
		return m.viewConfirmRemove()
	case usersKeys, usersKeyReview, usersKeyDelete:
		return m.viewKeys()
	}
	if m.loading {
		return s + "Loading users...\n" + helpStyle.Render("r: refresh")
	}
	indices := m.userIndices()
	s += m.filter.view(len(indices), len(m.items))
	if len(indices) == 0 {
		if len(m.items) == 0 {
			if m.messageError {
				s += "User list is unavailable. Press r to retry.\n"
			} else {
				s += "No managed users found.\n"
			}
		} else {
			s += "No users match this filter. Press / then Escape to clear.\n"
		}
	} else {
		width := 30
		if m.width > 0 {
			width = max(10, m.width-27)
		}
		s += fmt.Sprintf("  %s  KEYS  CREATED\n", pad("USERNAME", width))
		start, end := userBounds(m.cursor, m.offset, len(indices), m.rows())
		for row := start; row < end; row++ {
			u := m.items[indices[row]]
			prefix := "  "
			name := truncate(u.name, width)
			if row == m.cursor {
				prefix = "> "
				name = rowSelectedStyle.Render(pad(name, width))
			}
			count := "Unknown"
			if u.keyCountKnown {
				count = fmt.Sprint(u.keyCount)
			}
			s += fmt.Sprintf("%s%s  %-7s %s\n", prefix, pad(name, width), count, u.created)
		}
		s += fmt.Sprintf("Showing %d-%d of %d\n", start+1, end, len(indices))
	}
	return s + "\n" + helpStyle.Render("enter: access K: keys c: new d: delete r: refresh /: filter")
}
func (m usersModel) viewDetail() string {
	s := m.userHeading("Users > "+m.detailUser, "Bucket access: "+m.detailUser) + m.notice(m.detailMessage, m.detailError)
	if m.detailLoading {
		return s + "Loading access...\n" + helpStyle.Render("r: retry  esc: back")
	}
	if !m.detailKnown && m.detailAccess == nil {
		return s + "Current bucket access is unavailable.\n" + helpStyle.Render("r: retry  K: keys  esc: back")
	}
	indices := m.accessIndices()
	s += m.detailFilter.view(len(indices), len(m.detailAccess))
	if len(indices) == 0 {
		s += "No matching bucket access. Press a to add access.\n"
	} else {
		start, end := userBounds(m.detailCursor, m.detailOffset, len(indices), m.rows())
		for row := start; row < end; row++ {
			a := m.detailAccess[indices[row]]
			prefix := "  "
			if row == m.detailCursor {
				prefix = "> "
			}
			s += prefix + truncate(a.Bucket, max(15, m.width-29)) + "  " + permissionLabel(a.Permission) + "\n"
		}
		s += fmt.Sprintf("Showing %d-%d of %d\n", start+1, end, len(indices))
	}
	return s + "\n" + helpStyle.Render("enter: edit  a: add  d: remove  K: keys  r: refresh  /: filter  esc: back")
}
func (m usersModel) viewBucketPicker(create bool) string {
	title := "Add bucket access"
	if create {
		title = "New user: choose bucket"
	}
	s := m.userHeading("Users > Bucket", title) + m.notice(m.message, m.messageError)
	if m.loading || m.detailLoading {
		return s + "Loading buckets...\n" + helpStyle.Render("r: retry  esc: back")
	}
	indices := m.bucketIndices()
	s += m.pickerFilter.view(len(indices), len(m.availableBuckets))
	if len(indices) == 0 {
		s += "No matching buckets. Clear the filter or create a bucket.\n"
	} else {
		start, end := userBounds(m.pickerCursor, m.pickerOffset, len(indices), m.rows())
		for row := start; row < end; row++ {
			b := m.availableBuckets[indices[row]]
			prefix := "  "
			if row == m.pickerCursor {
				prefix = "> "
			}
			s += prefix + truncate(b.name, max(15, m.width-20)) + "  " + b.region + "\n"
		}
		s += fmt.Sprintf("Showing %d-%d of %d\n", start+1, end, len(indices))
	}
	return s + "\n" + helpStyle.Render("enter: select  /: filter  r: refresh  esc: back")
}
func (m usersModel) viewPickBucket() string { return m.viewBucketPicker(false) }
func (m usersModel) viewPickPerm() string {
	return m.userHeading("Users > "+m.detailUser+" > Permission", "Choose permission") + "Bucket: " + m.pendingBucket + "\nCurrent: " + permissionLabel(m.pendingOld) + "\n\n" + permissionChoices() + "\n" + helpStyle.Render("1/2/3: choose  esc: back")
}
func (m usersModel) viewConfirmRemove() string {
	s := m.userHeading("Users > "+m.detailUser+" > Review", "Review permission change") + m.notice(m.detailMessage, m.detailError)
	s += fmt.Sprintf("User: %s\nBucket: %s\nCurrent: %s\n", m.detailUser, m.pendingBucket, permissionLabel(m.pendingOld))
	if m.pendingAction == "remove" {
		s += "Requested: Remove bucket access\n"
	} else {
		s += "Requested: " + permissionLabel(m.pendingPermission) + "\n"
	}
	if m.mutating {
		return s + "\n" + m.mutationStatus()
	}
	return s + "\n" + helpStyle.Render("enter: Apply  esc: cancel")
}
func (m usersModel) viewKeys() string {
	s := m.userHeading("Users > "+m.detailUser+" > Keys", "Access keys: "+m.detailUser) + m.notice(m.detailMessage, m.detailError)
	if m.mode != usersKeys {
		if m.keyAction == "create" {
			s += "Create new key. Existing keys remain active.\n\n1. Save or copy the new secret.\n2. Update dependent applications.\n3. Select the old key and explicitly deactivate it.\n4. Verify applications before deleting the old key.\n"
		} else {
			s += fmt.Sprintf("Selected key: %s\nCurrent status: %s\nAction: %s\n", m.keyTarget.AccessKeyID, m.keyTarget.Status, m.keyAction)
			if m.currentSessionKeyID != "" && m.keyTarget.AccessKeyID == m.currentSessionKeyID {
				s += warningStyle.Render("This key is used by the current session. Access may stop.") + "\n"
			}
			if m.keyAction == "deactivate" {
				s += "Confirm dependent applications use a replacement.\nYou can reactivate this key if needed.\n"
			}
			if m.keyAction == "delete" {
				s += "Verify the replacement works before deleting this key.\nDeletion cannot be reversed. Type the complete key ID:\n" + m.keyConfirm.View() + "\n"
			}
		}
		if m.mutating {
			return s + "\n" + m.mutationStatus()
		}
		return s + "\n" + helpStyle.Render("enter: Confirm action  esc: cancel")
	}
	if m.keyLoading {
		return s + "Loading keys...\n" + helpStyle.Render("r: retry  esc: back")
	}
	if len(m.keys) == 0 {
		s += "No keys found.\n"
	} else {
		start, end := userBounds(m.keyCursor, m.keyOffset, len(m.keys), m.rows())
		for row := start; row < end; row++ {
			key := m.keys[row]
			prefix := "  "
			if row == m.keyCursor {
				prefix = "> "
			}
			status := key.Status
			if status == "" {
				status = "Unknown"
			}
			s += fmt.Sprintf("%s%s  %s  %s\n", prefix, key.AccessKeyID, status, key.CreateDate.Format("2006-01-02"))
		}
	}
	return s + "\n" + helpStyle.Render("c: new key  x: deactivate  a: reactivate  d: delete inactive  r: refresh  esc: back")
}
func (m usersModel) viewCredentials() string {
	s := m.userHeading("Users > Credentials", "New credentials")
	s += fmt.Sprintf("Username: %s\nAccess key ID: %s\n", m.creds.username, m.creds.accessKeyID)
	secret := "********************************"
	if m.creds.revealed {
		secret = m.creds.secretKey
	}
	s += "Secret: " + secret + "\n"
	s += warningStyle.Render("Save this secret now. It cannot be retrieved later.") + "\n"
	if m.creds.returnKeys {
		s += "Existing keys remain active. Update applications first.\n"
	}
	s += m.notice(m.creds.saveError, true) + m.notice(m.creds.message, false)
	if m.creds.saved {
		s += "Saved: " + m.creds.savePath + "\n"
	}
	if m.creds.confirmExit {
		return s + "\nLeave without saving?\nConfirm you captured the secret before leaving.\n\n" + helpStyle.Render("a: I captured it, Done  esc: stay")
	}
	if m.creds.editingPath {
		return s + "\nSave to a new file (existing files stay unchanged):\n" + m.creds.pathInput.View() + "\n\n" + helpStyle.Render("enter: Save  esc: cancel")
	}
	if !m.creds.saved {
		s += "Save destination: " + m.creds.savePath + "\n"
	}
	return s + "\n" + helpStyle.Render("v: reveal  c: copy  s: save  a: captured  enter: Done")
}
