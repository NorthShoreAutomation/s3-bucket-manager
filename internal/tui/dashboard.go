package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type dashboardModel struct {
	client      *awsClient.Client
	bucketCount int
	userCount   int
	loading     bool
	width       int
	height      int
}

type dashboardLoadedMsg struct {
	bucketCount int
	userCount   int
}

func newDashboardModel(client *awsClient.Client) dashboardModel {
	return dashboardModel{
		client:  client,
		loading: true,
	}
}

func (d dashboardModel) init() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		buckets, _ := d.client.ListBuckets(ctx)
		users, _ := d.client.ListManagedUsers(ctx)
		return dashboardLoadedMsg{
			bucketCount: len(buckets),
			userCount:   len(users),
		}
	}
}

func (d dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case dashboardLoadedMsg:
		d.bucketCount = msg.bucketCount
		d.userCount = msg.userCount
		d.loading = false
	}
	return d, nil
}

func (d dashboardModel) view() string {
	s := titleStyle.Render("s3m - S3 Bucket Manager") + "\n\n"

	profileText := d.client.Profile
	if profileText == "" {
		profileText = "default"
	}
	s += fmt.Sprintf("  Profile: %s\n", profileText)
	s += fmt.Sprintf("  Region:  %s\n", d.client.Region)
	s += fmt.Sprintf("  Account: %s\n", d.client.Account)
	s += "\n"

	if d.loading {
		s += "  Loading...\n"
	} else {
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 3).
			MarginRight(2)

		bucketBox := boxStyle.Render(fmt.Sprintf("Buckets\n  %d", d.bucketCount))
		userBox := boxStyle.Render(fmt.Sprintf("Users\n  %d", d.userCount))

		s += lipgloss.JoinHorizontal(lipgloss.Top, bucketBox, userBox) + "\n"
	}

	s += "\n"
	s += helpStyle.Render("  [b] Buckets  [u] Users  [a] Access  [?] Help  [q] Quit")

	return s
}
