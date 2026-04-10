package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
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
	usersConfirmDelete
	usersShowCreds
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

	case tea.KeyMsg:
		switch m.mode {
		case usersList:
			return m.updateList(msg)
		case usersCreate:
			return m.updateCreateName(msg)
		case usersCreateBuckets:
			return m.updateCreateBuckets(msg)
		case usersConfirmDelete:
			return m.updateConfirmDelete(msg)
		case usersShowCreds:
			return m.updateShowCreds(msg)
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
		username := strings.TrimSpace(m.nameInput.Value())
		buckets := strings.Split(bucketsStr, ",")
		for i := range buckets {
			buckets[i] = strings.TrimSpace(buckets[i])
		}
		m.loading = true
		m.mode = usersList
		return m, func() tea.Msg {
			ctx := context.Background()
			key, err := m.client.CreateManagedUser(ctx, username, buckets)
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
		return m, nil
	default:
		var cmd tea.Cmd
		m.bucketsInput, cmd = m.bucketsInput.Update(msg)
		return m, cmd
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
		m.creds.saved = true
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
		s += helpStyle.Render("enter: create  esc: cancel")
		return s
	case usersConfirmDelete:
		if m.cursor < len(m.items) {
			s += warningStyle.Render(fmt.Sprintf("Delete user %q and all their access keys? [y/N]", m.items[m.cursor].name))
		}
		return s
	case usersShowCreds:
		return m.viewCredentials()
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

	s += "\n" + helpStyle.Render("[c] Create  [d] Delete  [r] Rotate key  [esc] Back")
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
		s += successStyle.Render("  Credentials saved!") + "\n\n"
	}

	s += helpStyle.Render("[s] Save to file  [esc] Done")
	return s
}
