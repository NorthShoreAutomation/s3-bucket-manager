package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type screen int

const (
	screenDashboard screen = iota
	screenBuckets
	screenBucketDetail
	screenUsers
	screenUserDetail
	screenCreateBucket
	screenCreateUser
	screenCredentials
)

// App is the root Bubble Tea model.
type App struct {
	client    *awsClient.Client
	screen    screen
	history   []screen
	width     int
	height    int
	err       error
	dashboard dashboardModel
	buckets   bucketsModel
	users     usersModel
	creds     credentialsModel
	showHelp  bool
}

// NewApp creates the root app model.
func NewApp(client *awsClient.Client) App {
	return App{
		client:    client,
		screen:    screenDashboard,
		dashboard: newDashboardModel(client),
		buckets:   newBucketsModel(client),
		users:     newUsersModel(client),
	}
}

func (a App) Init() tea.Cmd {
	return a.dashboard.init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.dashboard.width = msg.Width
		a.dashboard.height = msg.Height
		a.buckets.width = msg.Width
		a.buckets.height = msg.Height
		a.users.width = msg.Width
		a.users.height = msg.Height
		return a, nil

	case tea.KeyMsg:
		// Global keys
		switch msg.String() {
		case "ctrl+c":
			return a, tea.Quit
		case "q":
			// Quit from any screen, unless user is typing in a text input
			if !a.isTextInputActive() {
				return a, tea.Quit
			}
		case "?":
			if !a.isTextInputActive() {
				a.showHelp = !a.showHelp
				return a, nil
			}
		}

		if a.showHelp {
			a.showHelp = false
			return a, nil
		}

	case dashboardLoadedMsg:
		// Cache loaded data into sub-models so they don't re-fetch
		a.buckets.items = msg.buckets
		a.buckets.loading = false
		a.users.items = msg.users
		a.users.loading = false

	case errMsg:
		a.err = msg.err
		return a, nil
	}

	// Route to active screen
	var cmd tea.Cmd
	switch a.screen {
	case screenDashboard:
		a, cmd = a.updateDashboard(msg)
	case screenBuckets, screenBucketDetail, screenCreateBucket:
		a, cmd = a.updateBuckets(msg)
	case screenUsers, screenUserDetail, screenCreateUser, screenCredentials:
		a, cmd = a.updateUsers(msg)
	}

	return a, cmd
}

func (a App) View() string {
	if a.showHelp {
		return a.viewHelp()
	}

	var content string
	switch a.screen {
	case screenDashboard:
		content = a.dashboard.view()
	case screenBuckets, screenBucketDetail, screenCreateBucket:
		content = a.buckets.view()
	case screenUsers, screenUserDetail, screenCreateUser, screenCredentials:
		content = a.users.view()
	}

	if a.err != nil {
		content += "\n" + errorStyle.Render("Error: "+a.err.Error())
	}

	return content
}

func (a App) viewHelp() string {
	s := titleStyle.Render("s3m - Keyboard Shortcuts") + "\n\n"
	s += "  b       Open buckets\n"
	s += "  u       Open users\n"
	s += "  c       Create new item\n"
	s += "  d       Delete selected item\n"
	s += "  enter   Select / drill in\n"
	s += "  esc     Go back\n"
	s += "  ?       Toggle this help\n"
	s += "  q       Quit\n"
	s += "\n" + helpStyle.Render("Press any key to close")
	return s
}

// isTextInputActive returns true when the user is typing in a text field.
func (a App) isTextInputActive() bool {
	return a.buckets.mode == bucketsCreate ||
		a.buckets.mode == bucketsConfirmDeleteNonEmpty ||
		a.buckets.mode == bucketDetailAddPrefix ||
		a.buckets.mode == bucketDetailConfirm ||
		a.users.mode == usersCreate ||
		a.users.mode == usersCreateBuckets
}

func (a App) pushScreen(s screen) App {
	a.history = append(a.history, a.screen)
	a.screen = s
	a.err = nil
	return a
}

func (a App) popScreen() App {
	if len(a.history) > 0 {
		a.screen = a.history[len(a.history)-1]
		a.history = a.history[:len(a.history)-1]
	} else {
		a.screen = screenDashboard
	}
	a.err = nil
	return a
}

// Routing helpers

func (a App) updateDashboard(msg tea.Msg) (App, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "b":
			a = a.pushScreen(screenBuckets)
			// Use cached data if available, otherwise fetch
			if len(a.buckets.items) > 0 {
				return a, nil
			}
			return a, a.buckets.init()
		case "u":
			a = a.pushScreen(screenUsers)
			if len(a.users.items) > 0 {
				return a, nil
			}
			return a, a.users.init()
		}
	}
	var cmd tea.Cmd
	a.dashboard, cmd = a.dashboard.update(msg)
	return a, cmd
}

func (a App) updateBuckets(msg tea.Msg) (App, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Only intercept esc when in the default list mode;
		// sub-modes (create, confirm delete) handle esc themselves.
		if msg.String() == "esc" && a.buckets.mode == bucketsList {
			a = a.popScreen()
			return a, a.dashboard.init()
		}
	}
	var cmd tea.Cmd
	a.buckets, cmd = a.buckets.update(msg)
	return a, cmd
}

func (a App) updateUsers(msg tea.Msg) (App, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" && a.users.mode == usersList {
			a = a.popScreen()
			return a, a.dashboard.init()
		}
	}
	var cmd tea.Cmd
	a.users, cmd = a.users.update(msg)
	return a, cmd
}

// Message types

type errMsg struct{ err error }

type bucketsLoadedMsg struct {
	buckets []bucketItem
}

type bucketNotEmptyMsg struct {
	name   string
	region string
}

type deleteProgressMsg struct {
	deleted int64
}

type usersLoadedMsg struct {
	users []userItem
}

type prefixesLoadedMsg struct {
	bucket   string
	prefixes []prefixItem
}

type credentialsMsg struct {
	accessKeyID string
	secretKey   string
	username    string
}

type operationDoneMsg struct {
	message string
}

// prog holds the running tea.Program so goroutines can send progress messages.
var prog *tea.Program

// Run starts the TUI.
func Run(profile, region string) error {
	ctx := context.Background()
	client, err := awsClient.NewClient(ctx, profile, region)
	if err != nil {
		return err
	}

	app := NewApp(client)
	p := tea.NewProgram(app, tea.WithAltScreen())
	prog = p
	_, err = p.Run()
	return err
}
