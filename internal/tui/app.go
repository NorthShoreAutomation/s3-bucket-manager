package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
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
	client     *awsClient.Client
	screen     screen
	width      int
	height     int
	err        error
	buckets    bucketsModel
	users      usersModel
	showHelp   bool
	quitPrompt bool
	helpOffset int
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
	if a.buckets.directBucket {
		return tea.Batch(
			a.buckets.spinner.Tick,
			a.buckets.loadDirectBucketMetadata(),
			a.buckets.loadBrowse(),
			a.buckets.loadPrefixes(),
			a.buckets.loadBucketUsers(),
		)
	}
	return a.buckets.init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		a.width = size.Width
		a.height = size.Height
		a.buckets.width = size.Width
		// Preserve browser context and actions at the 12-line minimum window size.
		a.buckets.height = max(11, size.Height-3)
		a.users.width = size.Width
		a.users.height = max(1, size.Height-3)
		a.buckets.filePicker.width = size.Width
		a.buckets.filePicker.height = max(1, size.Height-7)
		return a, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		if a.quitPrompt {
			a.quitPrompt = false
			if k.String() == "y" {
				a.buckets.quitAfterCancel = true
				cmd := a.cancelOperation()
				return a, cmd
			}
			return a, nil
		}
		if a.showHelp {
			switch k.String() {
			case "down", "j":
				a.helpOffset++
			case "up", "k":
				a.helpOffset = max(0, a.helpOffset-1)
			case "pgdown":
				a.helpOffset += max(1, a.height-7)
			case "pgup":
				a.helpOffset = max(0, a.helpOffset-max(1, a.height-7))
			default:
				a.showHelp = false
			}
			return a, nil
		}
		if k.String() == "ctrl+c" && a.operationActive() {
			cmd := a.cancelOperation()
			return a, cmd
		}
		if k.String() == "q" && a.operationActive() {
			a.quitPrompt = true
			return a, nil
		}
		if !a.isTextInputActive() {
			switch k.String() {
			case "q", "ctrl+c":
				return a, tea.Quit
			case "?", "m":
				a.showHelp = true
				a.helpOffset = 0
				return a, nil
			case "u":
				a.screen = screenUsers
				if len(a.users.items) == 0 {
					cmd := a.users.init()
					return a, cmd
				}
				return a, nil
			case "b":
				a.screen = screenBuckets
				return a, nil
			}
		}
	}
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case bucketDeleteCompleteMsg, bucketsLoadedMsg, bucketStatsMsg, bucketNotEmptyMsg, deleteProgressMsg, prefixesLoadedMsg, browseLoadedMsg, browseFolderCreatedMsg, folderCountedMsg, folderDeleteProgressMsg, selectionDeleteProgressMsg, downloadDoneMsg, uploadDoneMsg, bucketUsersLoadedMsg, userPickerLoadedMsg, bucketAccessUpdatedMsg, directBucketMetadataLoadedMsg, bucketErrorMsg, operationDoneMsg, urlUploadDoneMsg, urlUploadErrMsg, urlUploadResolvedMsg, urlUploadResolveFailedMsg, urlUploadProgressTickMsg, sharePreparedMsg, shareGeneratedMsg, shareClosedMsg, transferTickMsg, transferCheckedMsg:
		a.buckets, cmd = a.buckets.update(msg)
		if result, ok := msg.(bucketAccessUpdatedMsg); ok && result.bucket == a.buckets.currentBucketName() && a.users.detailUser != "" {
			var refresh tea.Cmd
			a.users, refresh = a.users.loadAccess()
			cmd = tea.Batch(cmd, refresh)
		}
	case usersResultMsg, usersLoadedMsg, credentialsMsg, userAccessLoadedMsg, createBucketPickerLoadedMsg, detailBucketPickerLoadedMsg, accessUpdatedMsg:
		a.users, cmd = a.users.update(msg)
		if result, ok := msg.(usersResultMsg); ok && result.kind == "permission" && result.err == nil && a.buckets.mode == bucketDetail && len(a.buckets.items) > 0 {
			a.buckets.bucketUsersLoading = true
			cmd = tea.Batch(cmd, a.buckets.loadBucketUsers())
		}
	case errMsg:
		err := msg.err
		if !errors.Is(err, context.Canceled) {
			a.err = err
		}
		if a.screen == screenUsers {
			a.users, cmd = a.users.update(msg)
		} else {
			a.buckets, cmd = a.buckets.update(msg)
		}
	default:
		if a.screen == screenUsers || a.screen == screenUserDetail || a.screen == screenCreateUser || a.screen == screenCredentials {
			if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" && a.users.mode == usersList && !a.users.ownsInput() && a.users.filter.query == "" {
				a.screen = screenBuckets
				return a, nil
			}
			a.users, cmd = a.users.update(msg)
		} else {
			a.buckets, cmd = a.buckets.update(msg)
		}
	}
	if a.buckets.quitAfterCancel && !a.operationActive() {
		if a.users.mode == usersShowCreds && a.users.creds.secretKey != "" {
			a.buckets.quitAfterCancel = false
			a.users.creds.message = "Save the new credentials before quitting."
			return a, cmd
		}
		return a, tea.Quit
	}
	return a, cmd
}

func (a App) operationActive() bool {
	return a.users.mutating || a.buckets.mutationPending || a.buckets.transferSnap != nil || a.buckets.bulkDeleting || a.buckets.urlUpload != nil && a.buckets.urlUpload.Active()
}

func (a *App) cancelOperation() tea.Cmd {
	if a.users.mutating {
		updated, cmd := a.users.update(tea.KeyMsg{Type: tea.KeyCtrlC})
		a.users = updated
		return cmd
	}
	a.buckets.cancelling = true
	if a.buckets.mutationCancel != nil {
		a.buckets.mutationCancel()
	}
	if a.buckets.transferCancel != nil {
		a.buckets.transferCancel()
	}
	if a.buckets.bulkDeleteCancel != nil {
		a.buckets.bulkDeleteCancel()
	}
	if a.buckets.urlUpload != nil {
		updated, cmd := a.buckets.urlUpload.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		a.buckets.urlUpload = &updated
		return cmd
	}
	return nil
}

func (a App) View() string {
	if a.width > 0 && (a.width < 60 || a.height < 12) {
		return fitTerminal("Resize to at least 60 columns and 12 lines. Your place is preserved.", a.width, a.height)
	}
	if a.quitPrompt {
		return renderPanel("Cancel task and quit?", "Completed changes will remain. Stay to continue monitoring.", "y: Cancel and quit after completion  Any other key: Stay", a.width, a.height)
	}
	if a.showHelp {
		return a.viewHelp()
	}
	var content string
	if a.screen == screenUsers || a.screen == screenUserDetail || a.screen == screenCreateUser || a.screen == screenCredentials {
		content = a.users.view()
	} else {
		content = a.buckets.view()
	}
	account, profile, region := "Unknown", "default", "Unknown"
	if a.client != nil {
		if a.client.Account != "" {
			account = a.client.Account
		}
		if a.client.Profile != "" {
			profile = a.client.Profile
		}
		if a.client.Region != "" {
			region = a.client.Region
		}
	}
	header := fmt.Sprintf("s3m  Account: %s  Profile: %s  Region: %s", account, profile, region)
	if a.err != nil {
		header += "\n" + errorStyle.Render("Error: "+a.err.Error())
	}
	return fitTerminal(header+"\n"+content, a.width, a.height)
}

func (a App) viewHelp() string {
	text := "b: Buckets   u: Managed users   q: Quit\n/: Filter names as you type\nEnter in filter: Keep results   Esc: Clear filter\nUp/Down: Move   PgUp/PgDn: Page   r: Refresh\nEnter/Right: Open   Left/Esc: Back\n"
	if a.screen == screenUsers {
		text += "c: Create user   d: Review deletion\ne: Review permission   a: Assign bucket\nK: Manage access keys\nKeys: c Create, x Deactivate, a Activate, d Delete inactive\nCredentials: v Reveal, c Copy, s Save, Enter Done\n"
	} else if a.buckets.mode == bucketsList {
		text += "c: Create bucket   d: Review bucket deletion\n"
	} else {
		text += "Tab: Files / Access / Details\nSpace: Select   a: Select displayed results or clear\nn: New folder   d: Review deletion   i: Full path\np: Upload local file   U: Upload from URL\ng: Download to a chosen path\ns: Create a temporary download link\nAccess: Enter Review edit, a Add user, d Remove\nEsc / Ctrl+C during a task: Cancel and wait\n"
	}
	lines := strings.Split(text, "\n")
	a.helpOffset = min(a.helpOffset, max(0, len(lines)-max(1, a.height-5)))
	return renderPanel("Help", strings.Join(lines[a.helpOffset:], "\n"), "Up/Down: Scroll   Esc: Close", a.width, a.height)
}

func (a App) isTextInputActive() bool {
	if a.screen == screenUsers || a.screen == screenUserDetail || a.screen == screenCreateUser || a.screen == screenCredentials {
		return a.users.ownsInput()
	}
	return a.buckets.ownsInput()
}

// Message types

type errMsg struct{ err error }

type bucketsLoadedMsg struct {
	buckets []bucketItem
	request uint64
}

type bucketStatsMsg struct {
	name      string
	objects   int64
	sizeBytes int64
	known     bool
	updated   time.Time
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
	bucket     string
	prefixes   []prefixItem
	request    uint64
	rootPublic bool
	known      bool
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
	items   []awsClient.BrowseItem
	bucket  string
	prefix  string
	request uint64
}

type browseFolderCreatedMsg struct {
	prefix  string
	message string
}

type folderCountedMsg struct {
	name     string
	key      string
	count    int64
	isPublic bool
}

type folderDeleteProgressMsg struct {
	deleted int64
}

type selectionDeleteProgressMsg struct {
	deleted int64
}

type downloadDoneMsg struct {
	filename string
	path     string
}

type uploadDoneMsg struct {
	filename string
}

type userAccessLoadedMsg struct {
	username string
	access   []model.BucketAccess
}

type createBucketPickerLoadedMsg struct {
	items []bucketItem
}

type detailBucketPickerLoadedMsg struct {
	username string
	items    []bucketItem
}

type accessUpdatedMsg struct {
	username string
	access   []model.BucketAccess
	message  string
}

type bucketUsersLoadedMsg struct {
	bucket  string
	users   []model.UserPermission
	err     error
	request uint64
}

type userPickerLoadedMsg struct {
	bucket string
	items  []userItem
}

type bucketAccessUpdatedMsg struct {
	bucket  string
	message string
	users   []model.UserPermission
}

type directBucketMetadataLoadedMsg struct {
	bucket  bucketItem
	request uint64
}

// prog holds the running tea.Program so goroutines can send progress messages.
var prog *tea.Program

// Run starts the TUI.
func Run(profile, region, bucket string) error {
	ctx := context.Background()
	client, err := awsClient.NewClient(ctx, profile, region)
	if err != nil {
		return err
	}

	var app App
	if bucket != "" {
		app = NewAppForBucket(ctx, client, bucket)
	} else {
		app = NewApp(client)
	}
	p := tea.NewProgram(app, tea.WithAltScreen())
	prog = p
	_, err = p.Run()
	return err
}

// NewAppForBucket creates an App pre-seeded with a single bucket and opens
// directly in bucket detail mode. Used when credentials cannot list buckets.
func NewAppForBucket(ctx context.Context, client *awsClient.Client, bucket string) App {
	// Resolve bucket region via HeadBucket so users never need to pass --region
	// manually. Falls back to the client's configured region only if the
	// HeadBucket probe fails (unreachable endpoint, bucket missing, etc.).
	bucketRegion, err := client.GetBucketRegion(ctx, bucket)
	if err != nil || bucketRegion == "" {
		bucketRegion = client.Region
	}
	b := newBucketsModel(client)
	b.items = []bucketItem{{name: bucket, region: bucketRegion}}
	b.cursor = 0
	b.mode = bucketDetail
	b.loading = true
	b.bucketUsersLoading = true
	b.directBucket = true
	return App{
		client:  client,
		screen:  screenBucketDetail,
		buckets: b,
		users:   newUsersModel(client),
	}
}
