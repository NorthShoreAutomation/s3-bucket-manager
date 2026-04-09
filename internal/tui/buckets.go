package tui

import (
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
	client  *awsClient.Client
	items   []bucketItem
	cursor  int
	loading bool
	width   int
	height  int
	mode    bucketsMode
}

func newBucketsModel(client *awsClient.Client) bucketsModel {
	return bucketsModel{client: client}
}

func (m bucketsModel) init() tea.Cmd {
	return func() tea.Msg {
		return bucketsLoadedMsg{}
	}
}

func (m bucketsModel) update(msg tea.Msg) (bucketsModel, tea.Cmd) {
	return m, nil
}

func (m bucketsModel) view() string {
	return titleStyle.Render("Buckets") + "\n\nLoading..."
}
