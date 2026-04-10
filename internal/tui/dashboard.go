package tui

import (
	"context"
	"fmt"
	"sync"

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
				stats, _ := d.client.GetBucketStats(ctx, name, region)
				items[idx].objects = stats.ObjectCount
				items[idx].sizeBytes = stats.SizeBytes
			}(i, b.Name, b.Region)
		}
		wg.Wait()
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
	profileText := d.client.Profile
	if profileText == "" {
		profileText = "default"
	}

	s := "\n"
	s += screenTitleStyle.Render("s3m — S3 Bucket Manager") + "\n\n"

	// Account info — compact single-line style
	labelStyle := lipgloss.NewStyle().Foreground(colorMuted)
	valueStyle := lipgloss.NewStyle().Foreground(colorText).Bold(true)
	s += fmt.Sprintf("  %s %s   %s %s   %s %s\n",
		labelStyle.Render("Profile:"), valueStyle.Render(profileText),
		labelStyle.Render("Region:"), valueStyle.Render(d.client.Region),
		labelStyle.Render("Account:"), valueStyle.Render(d.client.Account))

	if d.loading {
		s += fmt.Sprintf("\n  %s Loading buckets and users...\n", d.spinner.View())
	} else {
		countStyle := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
		s += "\n"
		s += fmt.Sprintf("  %s %s    %s %s\n",
			countStyle.Render(fmt.Sprintf("%d", d.bucketCount)),
			labelStyle.Render("buckets"),
			countStyle.Render(fmt.Sprintf("%d", d.userCount)),
			labelStyle.Render("managed users"))
	}

	s += "\n" + helpStyle.Render("  [b] Buckets  [u] Users  [?] Help  [q] Quit") + "\n"

	return s
}
