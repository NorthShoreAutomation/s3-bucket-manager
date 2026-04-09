package tui

import (
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
)

type accessModel struct {
	client   *awsClient.Client
	buckets  []string
	prefixes []prefixItem
	bucket   string
	cursor   int
	loading  bool
	width    int
	height   int
	mode     accessMode
}

func newAccessModel(client *awsClient.Client) accessModel {
	return accessModel{client: client, loading: true}
}

func (m accessModel) init() tea.Cmd {
	return func() tea.Msg {
		return bucketsLoadedMsg{}
	}
}

func (m accessModel) update(msg tea.Msg) (accessModel, tea.Cmd) {
	return m, nil
}

func (m accessModel) view() string {
	return titleStyle.Render("Access Control") + "\n\nLoading..."
}
