package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
)

type userItem struct {
	name     string
	keyCount int
	created  string
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
)

type usersModel struct {
	client       *awsClient.Client
	items        []userItem
	cursor       int
	loading      bool
	width        int
	height       int
	mode         usersMode
	nameInput    textinput.Model
	bucketsInput textinput.Model
	message      string
	creds        credentialsModel

	// Detail view
	detailUser    string               // username being viewed
	detailAccess  []model.BucketAccess // loaded bucket access
	detailCursor  int                  // cursor in access list
	detailLoading bool
	detailMessage string

	// Bucket picker (for adding access)
	availableBuckets []bucketItem // buckets not already assigned
	pickerCursor     int          // cursor in picker list
	pendingBucket    string       // selected bucket, awaiting permission choice
}

func newUsersModel(client *awsClient.Client) usersModel {
	ni := textinput.New()
	ni.Placeholder = "username"
	ni.CharLimit = 64

	bi := textinput.New()
	bi.Placeholder = "bucket1,bucket2"
	bi.CharLimit = 256

	return usersModel{
		client:       client,
		nameInput:    ni,
		bucketsInput: bi,
		loading:      true,
	}
}

func (m usersModel) init() tea.Cmd {
	m.loading = true
	return func() tea.Msg {
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
		return usersLoadedMsg{users: items}
	}
}

func (m usersModel) update(msg tea.Msg) (usersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case usersLoadedMsg:
		m.items = msg.users
		m.loading = false
		m.message = ""
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		return m, nil

	case credentialsMsg:
		m.creds = credentialsModel{
			accessKeyID: msg.accessKeyID,
			secretKey:   msg.secretKey,
			username:    msg.username,
		}
		m.mode = usersShowCreds
		return m, nil

	case operationDoneMsg:
		m.message = msg.message
		m.mode = usersList
		return m, m.init()

	case userAccessLoadedMsg:
		m.detailAccess = msg.access
		m.detailLoading = false
		if m.detailCursor >= len(m.detailAccess) {
			m.detailCursor = max(0, len(m.detailAccess)-1)
		}
		return m, nil

	case bucketPickerLoadedMsg:
		// Filter out buckets already assigned to this user
		assigned := make(map[string]bool, len(m.detailAccess))
		for _, a := range m.detailAccess {
			assigned[a.Bucket] = true
		}
		m.availableBuckets = nil
		for _, b := range msg.items {
			if !assigned[b.name] {
				m.availableBuckets = append(m.availableBuckets, b)
			}
		}
		m.detailLoading = false
		m.pickerCursor = 0
		return m, nil

	case accessUpdatedMsg:
		m.detailAccess = msg.access
		m.detailMessage = msg.message
		m.detailLoading = false
		if m.detailCursor >= len(m.detailAccess) {
			m.detailCursor = max(0, len(m.detailAccess)-1)
		}
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case usersList:
			return m.updateList(msg)
		case usersCreate:
			return m.updateCreateName(msg)
		case usersCreateBuckets:
			return m.updateCreateBuckets(msg)
		case usersCreatePerm:
			return m.updateCreatePerm(msg)
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
		case usersDetailConfirmRemove:
			return m.updateDetailConfirmRemove(msg)
		}
	}
	return m, nil
}

func (m usersModel) updateList(msg tea.KeyMsg) (usersModel, tea.Cmd) {
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
		m.mode = usersCreate
		m.nameInput.SetValue("")
		m.nameInput.Focus()
		return m, textinput.Blink
	case "d":
		if len(m.items) > 0 {
			m.mode = usersConfirmDelete
		}
	case "enter":
		if len(m.items) > 0 {
			m.detailUser = m.items[m.cursor].name
			m.detailLoading = true
			m.detailMessage = ""
			m.detailCursor = 0
			m.mode = usersDetail
			return m, func() tea.Msg {
				ctx := context.Background()
				access, err := m.client.GetUserBucketAccess(ctx, m.detailUser)
				if err != nil {
					return errMsg{err: err}
				}
				return userAccessLoadedMsg{username: m.detailUser, access: access}
			}
		}
	case "r":
		if len(m.items) > 0 {
			username := m.items[m.cursor].name
			m.loading = true
			return m, func() tea.Msg {
				ctx := context.Background()
				key, err := m.client.RotateAccessKey(ctx, username)
				if err != nil {
					return errMsg{err: err}
				}
				return credentialsMsg{
					accessKeyID: key.AccessKeyID,
					secretKey:   key.SecretAccessKey,
					username:    username,
				}
			}
		}
	}
	return m, nil
}

func (m usersModel) updateCreateName(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			return m, nil
		}
		m.mode = usersCreateBuckets
		m.bucketsInput.SetValue("")
		m.bucketsInput.Focus()
		return m, textinput.Blink
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
	switch msg.String() {
	case "enter":
		bucketsStr := strings.TrimSpace(m.bucketsInput.Value())
		if bucketsStr == "" {
			return m, nil
		}
		m.mode = usersCreatePerm
		return m, nil
	case "esc":
		m.mode = usersList
		return m, nil
	default:
		var cmd tea.Cmd
		m.bucketsInput, cmd = m.bucketsInput.Update(msg)
		return m, cmd
	}
}

func (m usersModel) updateCreatePerm(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	var perm model.PermissionLevel
	switch msg.String() {
	case "1":
		perm = model.PermRead
	case "2":
		perm = model.PermReadWrite
	case "3":
		perm = model.PermReadWriteDelete
	case "esc":
		m.mode = usersList
		return m, nil
	default:
		return m, nil
	}

	username := strings.TrimSpace(m.nameInput.Value())
	bucketsStr := strings.TrimSpace(m.bucketsInput.Value())
	buckets := strings.Split(bucketsStr, ",")
	accesses := make([]model.BucketAccess, 0, len(buckets))
	for _, b := range buckets {
		accesses = append(accesses, model.BucketAccess{
			Bucket:     strings.TrimSpace(b),
			Permission: perm,
		})
	}
	m.loading = true
	m.mode = usersList
	return m, func() tea.Msg {
		ctx := context.Background()
		key, err := m.client.CreateManagedUser(ctx, username, accesses)
		if err != nil {
			return errMsg{err: err}
		}
		return credentialsMsg{
			accessKeyID: key.AccessKeyID,
			secretKey:   key.SecretAccessKey,
			username:    username,
		}
	}
}

func (m usersModel) updateConfirmDelete(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		username := m.items[m.cursor].name
		m.loading = true
		m.mode = usersList
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.DeleteManagedUser(ctx, username)
			if err != nil {
				return errMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Deleted user %q", username)}
		}
	default:
		m.mode = usersList
	}
	return m, nil
}

func (m usersModel) updateShowCreds(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "s":
		path, err := saveCredentialsToFile(m.creds.username, m.creds.accessKeyID, m.creds.secretKey)
		if err != nil {
			m.creds.saved = false
			m.creds.savePath = ""
			m.creds.saveError = err.Error()
			return m, nil
		}
		m.creds.saved = true
		m.creds.savePath = path
		m.creds.saveError = ""
		return m, nil
	case "esc", "enter":
		m.mode = usersList
		return m, m.init()
	}
	return m, nil
}

func (m usersModel) view() string {
	s := breadcrumbStyle.Render("Dashboard > Users") + "\n"
	s += titleStyle.Render("Users") + "\n"

	if m.message != "" {
		s += successStyle.Render(m.message) + "\n\n"
	}

	switch m.mode {
	case usersCreate:
		s += "New username:\n"
		s += m.nameInput.View() + "\n\n"
		s += helpStyle.Render("enter: next  esc: cancel")
		return s
	case usersCreateBuckets:
		s += fmt.Sprintf("Buckets for %q (comma-separated):\n", m.nameInput.Value())
		s += m.bucketsInput.View() + "\n\n"
		s += helpStyle.Render("enter: next  esc: cancel")
		return s
	case usersCreatePerm:
		s += fmt.Sprintf("Permission level for %q buckets?\n\n", m.nameInput.Value())
		s += "  [1] read\n"
		s += "  [2] read-write\n"
		s += "  [3] read-write-delete\n\n"
		s += helpStyle.Render("Press 1, 2, or 3  [esc] Cancel")
		return s
	case usersConfirmDelete:
		if m.cursor < len(m.items) {
			s += warningStyle.Render(fmt.Sprintf("Delete user %q and all their access keys? [y/N]", m.items[m.cursor].name))
		}
		return s
	case usersShowCreds:
		return m.viewCredentials()
	case usersDetail:
		return m.viewDetail()
	case usersDetailPickBucket:
		return m.viewPickBucket()
	case usersDetailPickPerm:
		return m.viewPickPerm()
	case usersDetailConfirmRemove:
		return m.viewConfirmRemove()
	}

	if m.loading {
		s += "Loading users...\n"
		return s
	}

	if len(m.items) == 0 {
		s += "No s3m-managed users found.\n\n"
		s += helpStyle.Render("[c] Create  [esc] Back")
		return s
	}

	s += fmt.Sprintf("  %-30s %-10s %s\n",
		tableHeaderStyle.Render("USERNAME"),
		tableHeaderStyle.Render("KEYS"),
		tableHeaderStyle.Render("CREATED"))

	for i, u := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		name := u.name
		if i == m.cursor {
			name = selectedStyle.Render(name)
		}
		s += fmt.Sprintf("%s%-30s %-10d %s\n", cursor, name, u.keyCount, u.created)
	}

	s += "\n" + helpStyle.Render("[enter] View  [c] Create  [d] Delete  [r] Rotate key  [esc] Back")
	return s
}

func (m usersModel) viewCredentials() string {
	s := breadcrumbStyle.Render("Dashboard > Users > Credentials") + "\n"
	s += titleStyle.Render("New Credentials") + "\n\n"

	s += fmt.Sprintf("  Username:       %s\n", m.creds.username)
	s += fmt.Sprintf("  Access Key ID:  %s\n", m.creds.accessKeyID)
	s += fmt.Sprintf("  Secret Key:     %s\n", m.creds.secretKey)
	s += "\n"
	s += warningStyle.Render("  WARNING: This is the only time the secret key will be shown.") + "\n\n"

	if m.creds.saved {
		s += successStyle.Render(fmt.Sprintf("  Credentials saved to %s", m.creds.savePath)) + "\n\n"
	}
	if m.creds.saveError != "" {
		s += errorStyle.Render("  "+m.creds.saveError) + "\n\n"
	}

	s += helpStyle.Render("[s] Save to file  [esc] Done")
	return s
}

// nextPermission cycles: read → read-write → read-write-delete → read.
func nextPermission(p model.PermissionLevel) model.PermissionLevel {
	switch p {
	case model.PermRead:
		return model.PermReadWrite
	case model.PermReadWrite:
		return model.PermReadWriteDelete
	default:
		return model.PermRead
	}
}

func (m usersModel) updateDetail(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < len(m.detailAccess)-1 {
			m.detailCursor++
		}
	case "enter":
		if len(m.detailAccess) > 0 && m.detailCursor < len(m.detailAccess) {
			m.detailAccess[m.detailCursor].Permission = nextPermission(m.detailAccess[m.detailCursor].Permission)
			m.detailLoading = true
			m.detailMessage = ""
			accessCopy := make([]model.BucketAccess, len(m.detailAccess))
			copy(accessCopy, m.detailAccess)
			username := m.detailUser
			changedBucket := m.detailAccess[m.detailCursor].Bucket
			changedPerm := m.detailAccess[m.detailCursor].Permission
			return m, func() tea.Msg {
				ctx := context.Background()
				err := m.client.SetUserBucketAccess(ctx, username, accessCopy)
				if err != nil {
					return errMsg{err: err}
				}
				return accessUpdatedMsg{
					access:  accessCopy,
					message: fmt.Sprintf("Updated %s to %s", changedBucket, changedPerm),
				}
			}
		}
	case "a":
		m.mode = usersDetailPickBucket
		m.pickerCursor = 0
		m.detailLoading = true
		return m, func() tea.Msg {
			ctx := context.Background()
			buckets, err := m.client.ListBuckets(ctx)
			if err != nil {
				return errMsg{err: err}
			}
			items := make([]bucketItem, len(buckets))
			for i, b := range buckets {
				items[i] = bucketItem{
					name:   b.Name,
					region: b.Region,
				}
			}
			return bucketPickerLoadedMsg{items: items}
		}
	case "d":
		if len(m.detailAccess) > 0 && m.detailCursor < len(m.detailAccess) {
			m.mode = usersDetailConfirmRemove
		}
	case "r":
		username := m.detailUser
		m.detailLoading = true
		return m, func() tea.Msg {
			ctx := context.Background()
			key, err := m.client.RotateAccessKey(ctx, username)
			if err != nil {
				return errMsg{err: err}
			}
			return credentialsMsg{
				accessKeyID: key.AccessKeyID,
				secretKey:   key.SecretAccessKey,
				username:    username,
			}
		}
	case "esc":
		m.mode = usersList
		m.detailUser = ""
		m.detailAccess = nil
		m.detailCursor = 0
		m.detailMessage = ""
		m.detailLoading = false
		return m, nil
	}
	return m, nil
}

func (m usersModel) updateDetailPickBucket(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "down", "j":
		if m.pickerCursor < len(m.availableBuckets)-1 {
			m.pickerCursor++
		}
	case "enter":
		if len(m.availableBuckets) > 0 && m.pickerCursor < len(m.availableBuckets) {
			m.pendingBucket = m.availableBuckets[m.pickerCursor].name
			m.mode = usersDetailPickPerm
		}
	case "esc":
		m.mode = usersDetail
		m.availableBuckets = nil
		m.pickerCursor = 0
	}
	return m, nil
}

func (m usersModel) updateDetailPickPerm(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	var perm model.PermissionLevel
	switch msg.String() {
	case "1":
		perm = model.PermRead
	case "2":
		perm = model.PermReadWrite
	case "3":
		perm = model.PermReadWriteDelete
	case "esc":
		m.mode = usersDetail
		m.pendingBucket = ""
		return m, nil
	default:
		return m, nil
	}

	// Add the new access
	newAccess := model.BucketAccess{Bucket: m.pendingBucket, Permission: perm}
	m.detailAccess = append(m.detailAccess, newAccess)
	m.detailLoading = true
	m.mode = usersDetail
	m.pendingBucket = ""

	accessCopy := make([]model.BucketAccess, len(m.detailAccess))
	copy(accessCopy, m.detailAccess)
	username := m.detailUser
	bucket := newAccess.Bucket
	permission := newAccess.Permission

	return m, func() tea.Msg {
		ctx := context.Background()
		err := m.client.SetUserBucketAccess(ctx, username, accessCopy)
		if err != nil {
			return errMsg{err: err}
		}
		return accessUpdatedMsg{
			access:  accessCopy,
			message: fmt.Sprintf("Added %s with %s access", bucket, permission),
		}
	}
}

func (m usersModel) updateDetailConfirmRemove(msg tea.KeyMsg) (usersModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.detailCursor < len(m.detailAccess) {
			removedBucket := m.detailAccess[m.detailCursor].Bucket
			// Remove the entry at detailCursor
			m.detailAccess = append(m.detailAccess[:m.detailCursor], m.detailAccess[m.detailCursor+1:]...)
			if m.detailCursor >= len(m.detailAccess) {
				m.detailCursor = max(0, len(m.detailAccess)-1)
			}
			m.detailLoading = true
			m.mode = usersDetail

			accessCopy := make([]model.BucketAccess, len(m.detailAccess))
			copy(accessCopy, m.detailAccess)
			username := m.detailUser

			return m, func() tea.Msg {
				ctx := context.Background()
				err := m.client.SetUserBucketAccess(ctx, username, accessCopy)
				if err != nil {
					return errMsg{err: err}
				}
				return accessUpdatedMsg{
					access:  accessCopy,
					message: fmt.Sprintf("Removed access to %s", removedBucket),
				}
			}
		}
	default:
		m.mode = usersDetail
	}
	return m, nil
}

func (m usersModel) viewDetail() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("Dashboard > Users > %s", m.detailUser)) + "\n"
	s += titleStyle.Render(m.detailUser) + "\n"
	s += separator(40) + "\n"

	// Show user metadata
	for _, u := range m.items {
		if u.name == m.detailUser {
			s += fmt.Sprintf("  Created:  %s\n", u.created)
			s += fmt.Sprintf("  Keys:     %d\n", u.keyCount)
			break
		}
	}
	s += "\n"

	if m.detailMessage != "" {
		s += successStyle.Render("  "+m.detailMessage) + "\n\n"
	}

	if m.detailLoading {
		s += "  Loading...\n"
		return s
	}

	if len(m.detailAccess) == 0 {
		s += "  No bucket access. Press [a] to add.\n\n"
		s += helpStyle.Render(" [a] Add bucket  [r] Rotate key  [esc] Back")
		return s
	}

	s += fmt.Sprintf("  %-30s %s\n",
		tableHeaderStyle.Render("BUCKET"),
		tableHeaderStyle.Render("PERMISSION"))

	for i, a := range m.detailAccess {
		cursor := "  "
		if i == m.detailCursor {
			cursor = "> "
		}
		bucketName := pad(a.Bucket, 30)
		permStr := string(a.Permission)
		if i == m.detailCursor {
			bucketName = rowSelectedStyle.Render(pad(a.Bucket, 30))
			permStr = rowSelectedStyle.Render(string(a.Permission))
		}
		s += fmt.Sprintf("%s%s %s\n", cursor, bucketName, permStr)
	}

	s += "\n" + helpStyle.Render(" [a] Add bucket  [d] Remove  [enter] Cycle permission  [r] Rotate key  [esc] Back")
	return s
}

func (m usersModel) viewPickBucket() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("Dashboard > Users > %s > Add bucket", m.detailUser)) + "\n"
	s += titleStyle.Render("Select a bucket:") + "\n\n"

	if m.detailLoading {
		s += "  Loading buckets...\n"
		return s
	}

	if len(m.availableBuckets) == 0 {
		s += "  All buckets are already assigned.\n\n"
		s += helpStyle.Render(" [esc] Back")
		return s
	}

	for i, b := range m.availableBuckets {
		cursor := "  "
		if i == m.pickerCursor {
			cursor = "> "
		}
		name := pad(b.name, 30)
		region := b.region
		if i == m.pickerCursor {
			name = rowSelectedStyle.Render(pad(b.name, 30))
			region = rowSelectedStyle.Render(b.region)
		}
		s += fmt.Sprintf("%s%s %s\n", cursor, name, region)
	}

	s += "\n" + helpStyle.Render(" [enter] Select  [esc] Cancel")
	return s
}

func (m usersModel) viewPickPerm() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("Dashboard > Users > %s > Add bucket", m.detailUser)) + "\n"
	s += titleStyle.Render(fmt.Sprintf("Permission for %q:", m.pendingBucket)) + "\n\n"

	s += "  [1] read\n"
	s += "  [2] read-write\n"
	s += "  [3] read-write-delete\n\n"

	s += helpStyle.Render(" Press 1, 2, or 3  [esc] Cancel")
	return s
}

func (m usersModel) viewConfirmRemove() string {
	s := breadcrumbStyle.Render(fmt.Sprintf("Dashboard > Users > %s", m.detailUser)) + "\n"
	if m.detailCursor < len(m.detailAccess) {
		s += warningStyle.Render(fmt.Sprintf("Remove access to %q? [y/N]", m.detailAccess[m.detailCursor].Bucket))
	}
	return s
}

func saveCredentialsToFile(username, keyID, secret string) (string, error) {
	filename := fmt.Sprintf("%s-credentials.json", username)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve home directory: %w", err)
	}
	path := filepath.Join(home, filename)

	data := map[string]string{
		"username":          username,
		"access_key_id":     keyID,
		"secret_access_key": secret,
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("could not encode credentials: %w", err)
	}

	if err := os.WriteFile(path, jsonData, 0600); err != nil {
		return "", fmt.Errorf("could not save credentials: %w", err)
	}
	return path, nil
}
