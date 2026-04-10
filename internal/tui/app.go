package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

type screen int

const (
	screenBuckets screen = iota
	screenBucketDetail
	screenUsers
	screenUserDetail
	screenCreateBucket
	screenCreateUser
	screenCredentials
)

// App is the root Bubble Tea model.
type App struct {
	client   *awsClient.Client
	screen   screen
	width    int
	height   int
	err      error
	buckets  bucketsModel
	users    usersModel
	showHelp bool
}

// NewApp creates the root app model.
func NewApp(client *awsClient.Client) App {
	return App{
		client:  client,
		screen:  screenBuckets,
		buckets: newBucketsModel(client),
		users:   newUsersModel(client),
	}
}

func (a App) Init() tea.Cmd {
	return a.buckets.init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
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

	case errMsg:
		a.err = msg.err
		return a, nil
	}

	// Route to active screen
	var cmd tea.Cmd
	switch a.screen {
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
		a.buckets.mode == bucketsTypeDelete ||
		a.buckets.mode == bucketsConfirmDeleteNonEmpty ||
		a.buckets.mode == bucketDetailAddPrefix ||
		a.buckets.mode == bucketDetailConfirm ||
		a.buckets.mode == bucketDetailDeleteFolder ||
		a.users.mode == usersCreate ||
		a.users.mode == usersCreateBuckets
}

// Routing helpers

func (a App) updateBuckets(msg tea.Msg) (App, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if !a.isTextInputActive() {
			switch msg.String() {
			case "u":
				// Shortcut to users screen from bucket list
				a.screen = screenUsers
				if len(a.users.items) > 0 {
					return a, nil
				}
				return a, a.users.init()
			case "esc":
				if a.buckets.mode == bucketsList {
					// Bucket list is home — esc quits
					return a, tea.Quit
				}
			}
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
			// Go back to bucket list
			a.screen = screenBuckets
			return a, nil
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

type browseLoadedMsg struct {
	items []awsClient.BrowseItem
}

type folderCountedMsg struct {
	name  string
	key   string
	count int64
}

type folderDeleteProgressMsg struct {
	deleted int64
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
