package tui

import (
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
	usersConfirmDelete
	usersCredentials
)

type usersModel struct {
	client  *awsClient.Client
	items   []userItem
	cursor  int
	loading bool
	width   int
	height  int
	mode    usersMode
}

func newUsersModel(client *awsClient.Client) usersModel {
	return usersModel{client: client}
}

func (m usersModel) init() tea.Cmd {
	return func() tea.Msg {
		return usersLoadedMsg{}
	}
}

func (m usersModel) update(msg tea.Msg) (usersModel, tea.Cmd) {
	return m, nil
}

func (m usersModel) view() string {
	return titleStyle.Render("Users") + "\n\nLoading..."
}
