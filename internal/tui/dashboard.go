package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
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
	spinner     spinner.Model
}

type dashboardLoadedMsg struct {
	buckets []bucketItem
	users   []userItem
}

func newDashboardModel(client *awsClient.Client) dashboardModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	return dashboardModel{
		client:  client,
		loading: true,
		spinner: sp,
	}
}

func (d dashboardModel) init() tea.Cmd {
	return tea.Batch(d.spinner.Tick, func() tea.Msg {
		ctx := context.Background()
		buckets, _ := d.client.ListBuckets(ctx)
		var items []bucketItem
		for _, b := range buckets {
			count, _ := d.client.GetBucketObjectCount(ctx, b.Name)
			items = append(items, bucketItem{
				name:     b.Name,
				region:   b.Region,
				isPublic: b.IsPublic,
				objects:  count,
			})
		}
		users, _ := d.client.ListManagedUsers(ctx)
		var userItems []userItem
		for _, u := range users {
			userItems = append(userItems, userItem{
				name:     u.Name,
				keyCount: u.KeyCount,
				created:  u.CreateDate.Format("2006-01-02"),
			})
		}
		return dashboardLoadedMsg{
			buckets: items,
			users:   userItems,
		}
	})
}

func (d dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case dashboardLoadedMsg:
		d.bucketCount = len(msg.buckets)
		d.userCount = len(msg.users)
		d.loading = false
		return d, nil
	case spinner.TickMsg:
		if d.loading {
			var cmd tea.Cmd
			d.spinner, cmd = d.spinner.Update(msg)
			return d, cmd
		}
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
		s += fmt.Sprintf("  %s Loading buckets and users...\n", d.spinner.View())
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
