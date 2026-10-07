package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	bubprogress "github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
	"github.com/dcorbell/s3m/internal/progress"
)

type bucketItem struct {
	name          string
	region        string
	isPublic      bool
	objects       int64
	sizeBytes     int64
	created       string
	statsKnown    bool
	accessKnown   bool
	statsUpdated  time.Time
	managedPublic bool
	policyKnown   bool
}

type prefixItem struct {
	prefix   string
	isPublic bool
}

type bucketsMode int

const (
	bucketsList                   bucketsMode = iota
	bucketsCreate                             // typing a new bucket name
	bucketsTypeDelete                         // type 'delete' to start deletion
	bucketsConfirmDelete                      // are you sure? [y/N]
	bucketsConfirmDeleteNonEmpty              // type bucket name to confirm emptying
	bucketDetail                              // viewing a single bucket's details
	bucketDetailAddPrefix                     // typing a new prefix name
	bucketDetailAddFolder                     // typing a new folder name while browsing inside a prefix
	bucketDetailConfirm                       // type 'yes' to confirm access change
	bucketDetailDeleteFolder                  // type 'delete' to confirm folder deletion
	bucketDetailDeleteSelection               // type 'delete' to confirm selected item deletion
	bucketDetailPickUser                      // selecting a user to add
	bucketDetailPickPerm                      // choosing permission level for new user
	bucketDetailConfirmRemoveUser             // confirm removing user access
)

type bucketUserItem struct {
	username   string
	permission model.PermissionLevel
}

type bucketsModel struct {
	fullBuckets          []bucketItem
	fullBrowse           []awsClient.BrowseItem
	filterInput          textinput.Model
	filterActive         bool
	filterScope          string
	detailTab            int // Files, Access, Details
	err                  error
	errKind              string
	failedTransferReview *transferReview
	partialBucketCreated bool
	mutationCancel       context.CancelFunc
	inspectText          string
	browseRequests       *atomic.Uint64
	prefixRequests       *atomic.Uint64
	listRequests         *atomic.Uint64
	userRequests         *atomic.Uint64
	metadataRequests     *atomic.Uint64
	parentPositions      map[string]browsePosition
	pendingBrowseKey     string
	pendingDelete        []awsClient.BrowseItem
	mutationPending      bool
	cancelling           bool
	quitAfterCancel      bool
	accessOffset         int
	share                *shareModel
	transferReview       *transferReview
	deleteBucket         bucketItem
	userPickerSource     []userItem
	userPickerFilter     userFilter
	userPickerOffset     int
	dialogOffset         int
	client               *awsClient.Client
	items                []bucketItem
	cursor               int
	offset               int // first visible row for scrolling
	loading              bool
	width                int
	height               int
	mode                 bucketsMode
	nameInput            textinput.Model
	deleteInput          textinput.Model // type 'delete' to start deletion
	confirmInput         textinput.Model // type bucket name to confirm destructive delete
	message              string
	spinner              spinner.Model
	deleteProgress       string // shown during bucket emptying

	// Detail view fields
	detailCursor  int             // cursor position in detail view (0 = bucket row, 1+ = users, then prefixes)
	prefixes      []prefixItem    // prefixes for currently selected bucket
	prefixInput   textinput.Model // for adding new prefixes
	confirmInput2 textinput.Model // for typing 'yes' to confirm access change
	confirmAction string          // description of what will happen
	confirmFunc   func(context.Context) tea.Msg
	detailMessage string // status message in detail view

	// Bucket user access
	bucketUsers        []bucketUserItem // users with access to current bucket
	bucketUsersLoading bool             // loading users separately
	bucketUsersError   string
	availableUsers     []userItem // for the user picker (managed users not yet assigned)
	userPickerCursor   int
	pendingUser        string // user selected in picker, awaiting permission

	// File browser fields
	browsePrefix       string                 // current prefix being browsed (empty = root)
	browseItems        []awsClient.BrowseItem // folders + files at current prefix
	browseCursor       int                    // cursor in browse view
	browseOffset       int                    // scroll offset in browse view
	browseSelected     map[string]bool        // selected keys in the current browse listing
	folderDeleteKey    string                 // key of folder being deleted
	folderDeleteCnt    int64                  // object count for folder delete confirm
	folderDeletePublic bool                   // whether the folder being deleted also has public access

	// File picker fields
	filePicker     filePickerModel
	showFilePicker bool

	// URL upload sub-model (non-nil while active)
	urlUpload *urlUploadModel

	// directBucket is true when the app was launched with --bucket, so the
	// bucket list is unreachable and esc from the detail view should quit.
	directBucket bool

	// Transfer progress (upload via p, download via g).
	// transferSnap is non-nil while a transfer is in flight.
	transferSnap     *atomic.Pointer[progress.Snapshot]
	transferBar      bubprogress.Model // bubbles progress bar
	transferLabel    string            // e.g. "Uploading photo.jpg"
	transferLastDone int64
	transferLastTime time.Time
	transferRate     float64
	transferCancel   context.CancelFunc
	bulkDeleteCancel context.CancelFunc
	bulkDeleting     bool
}

func newBucketsModel(client *awsClient.Client) bucketsModel {
	ti := textinput.New()
	ti.Placeholder = "my-bucket-name"
	ti.CharLimit = 63
	di := textinput.New()
	di.Placeholder = "type 'delete' to confirm"
	di.CharLimit = 10
	ci := textinput.New()
	ci.Placeholder = "type bucket name to confirm"
	ci.CharLimit = 63
	pi := textinput.New()
	pi.Placeholder = "prefix-name/"
	pi.CharLimit = 200
	ci2 := textinput.New()
	ci2.Placeholder = "type yes to confirm"
	ci2.CharLimit = 10
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	transferBar := bubprogress.New(
		bubprogress.WithDefaultGradient(),
		bubprogress.WithoutPercentage(),
	)
	return bucketsModel{
		client:           client,
		nameInput:        ti,
		deleteInput:      di,
		confirmInput:     ci,
		prefixInput:      pi,
		confirmInput2:    ci2,
		loading:          true,
		spinner:          sp,
		transferBar:      transferBar,
		filterInput:      textinput.New(),
		browseRequests:   &atomic.Uint64{},
		prefixRequests:   &atomic.Uint64{},
		listRequests:     &atomic.Uint64{},
		userRequests:     &atomic.Uint64{},
		metadataRequests: &atomic.Uint64{},
	}
}

func (m bucketsModel) init() tea.Cmd {
	m.loading = true
	request := nextRequest(m.listRequests)
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx := context.Background()
		buckets, err := m.client.ListBuckets(ctx)
		if err != nil {
			return bucketErrorMsg{err: err, kind: "buckets", request: request}
		}
		items := make([]bucketItem, len(buckets))
		for i, b := range buckets {
			items[i] = bucketItem{
				name:        b.Name,
				region:      b.Region,
				isPublic:    b.IsPublic,
				accessKnown: b.AccessKnown,
				created:     b.CreationDate.Format("2006-01-02"),
			}
		}
		return bucketsLoadedMsg{buckets: items, request: request}
	})
}

// loadBucketStatsCmd returns a command that fetches CloudWatch stats for all
// buckets concurrently. Each bucket's stats arrive as a separate bucketStatsMsg
// so the UI updates progressively.
func (m bucketsModel) loadBucketStatsCmd() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.items))
	for _, b := range m.items {
		name, region := b.name, b.region
		cmds = append(cmds, func() tea.Msg {
			ctx := context.Background()
			stats, err := m.client.GetBucketStats(ctx, name, region)
			return bucketStatsMsg{
				name:      name,
				objects:   stats.ObjectCount,
				sizeBytes: stats.SizeBytes,
				known:     err == nil && stats.Known,
				updated:   stats.UpdatedAt,
			}
		})
	}
	return tea.Batch(cmds...)
}

func (m bucketsModel) currentBucketName() string {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return ""
	}
	return m.items[m.cursor].name
}

func userPermsToItems(users []model.UserPermission) []bucketUserItem {
	items := make([]bucketUserItem, len(users))
	for i, u := range users {
		items[i] = bucketUserItem{username: u.Username, permission: u.Permission}
	}
	return items
}

func (m bucketsModel) update(msg tea.Msg) (bucketsModel, tea.Cmd) {
	previousMode := m.mode
	updated, cmd := m.updateContent(msg)
	if updated.mode != previousMode {
		updated.dialogOffset = 0
	}
	return updated, cmd
}

func (m bucketsModel) updateContent(msg tea.Msg) (bucketsModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && m.inspectText != "" {
		switch key.String() {
		case "esc":
			m.inspectText = ""
			m.dialogOffset = 0
		case "up", "pgup":
			m.dialogOffset = max(0, m.dialogOffset-1)
		case "down", "pgdown":
			m.dialogOffset++
		}
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok && (m.mode != bucketsList && m.mode != bucketDetail && m.mode != bucketDetailPickUser || m.mode == bucketDetail && m.detailTab == 2) {
		if key.String() == "pgdown" {
			m.dialogOffset += max(1, m.height-6)
			return m, nil
		}
		if key.String() == "pgup" {
			m.dialogOffset = max(0, m.dialogOffset-max(1, m.height-6))
			return m, nil
		}
	}
	if result, ok := msg.(transferCheckedMsg); ok {
		return m.updateTransferChecked(result)
	}
	if key, ok := msg.(tea.KeyMsg); ok && m.transferReview != nil {
		return m.updateTransferReview(key)
	}
	if m.share != nil {
		switch msg.(type) {
		case tea.KeyMsg, sharePreparedMsg, shareGeneratedMsg:
			updated, cmd := m.share.Update(msg)
			m.share = &updated
			return m, cmd
		case shareClosedMsg:
			m.share = nil
			return m, nil
		}
	}
	if msg, ok := msg.(tea.KeyMsg); ok {
		if m.filterActive {
			return m.updateFilter(msg)
		}
		if msg.String() == "esc" && m.filterScope != "" && !m.ownsInput() {
			m.clearFilter()
			return m, nil
		}
		if msg.String() == "/" && (m.mode == bucketsList || m.mode == bucketDetail && m.detailTab == 0) && !m.ownsInput() {
			cmd := m.beginFilter()
			return m, cmd
		}
		if m.mutationPending {
			if msg.String() == "esc" || msg.String() == "ctrl+c" {
				if m.mutationCancel != nil {
					m.mutationCancel()
				}
				m.cancelling = true
			}
			return m, nil
		}
	}
	// Delegate all messages to the URL upload sub-model while it is active.
	// urlUploadDoneMsg / urlUploadErrMsg fall through so the cases below can
	// clean up m.urlUpload and refresh the browse listing.
	if m.urlUpload != nil {
		switch msg.(type) {
		case urlUploadDoneMsg, urlUploadErrMsg:
			// handled below - fall through
		case tea.KeyMsg, urlUploadResolvedMsg, urlUploadResolveFailedMsg, urlUploadProgressTickMsg, spinner.TickMsg, bubprogress.FrameMsg:
			updated, cmd := m.urlUpload.Update(msg)
			m.urlUpload = &updated
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case bucketErrorMsg:
		if !m.matchesError(msg) {
			return m, nil
		}
		m.err = msg.err
		m.errKind = msg.kind
		if msg.kind == "prefixes" && m.cursor >= 0 && m.cursor < len(m.items) {
			m.items[m.cursor].policyKnown = false
		}
		if msg.kind == "folder" {
			m.mode = bucketDetailAddFolder
		}
		if msg.kind == "create" {
			var partial *awsClient.PartialBucketCreationError
			m.partialBucketCreated = errors.As(msg.err, &partial)
		}
		if msg.kind == "delete-bucket" {
			m.loading = false
			m.bulkDeleting = false
			m.bulkDeleteCancel = nil
			m.cancelling = false
			m.message = "Bucket deletion stopped. Completed deletions remain. " + m.deleteProgress
			return m, nil
		}
		if msg.kind == "create" {
			m.mode = bucketsCreate
		}
		m.mutationPending = false
		updated, cmd := m.update(errMsg{err: msg.err})
		return updated, cmd
	case errMsg:
		m.mutationCancel = nil
		m.loading = false
		m.mutationPending = false
		m.cancelling = false
		m.bucketUsersLoading = false
		wasTransfer := m.transferSnap != nil
		if wasTransfer {
			if !errors.Is(msg.err, context.Canceled) && m.failedTransferReview != nil {
				m.transferReview = m.failedTransferReview
				m.transferReview.err = msg.err
			}
			m.transferSnap = nil
			m.transferCancel = nil
			m.transferLabel = ""
		}
		if wasTransfer && errors.Is(msg.err, context.Canceled) {
			m.detailMessage = "Cancelled"
			return m, nil
		}
		if m.bulkDeleting {
			m.bulkDeleting = false
			m.bulkDeleteCancel = nil
			m.browseSelected = nil
			m.deleteProgress = ""
			if errors.Is(msg.err, context.Canceled) {
				m.detailMessage = "Bulk delete cancelled"
			}
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
		return m, nil

	case bucketsLoadedMsg:
		if !currentRequest(m.listRequests, msg.request) {
			return m, nil
		}
		focused := m.currentBucketName()
		m.fullBuckets = append([]bucketItem{}, msg.buckets...)
		m.items = msg.buckets
		if m.filterScope == "buckets" {
			m.applyFilter()
		}
		for i, item := range m.items {
			if item.name == focused {
				m.cursor = i
				break
			}
		}
		m.clearError("buckets")
		m.loading = false
		m.message = ""
		m.deleteProgress = ""
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		// Kick off background stats fetch (CloudWatch) - list renders immediately
		return m, m.loadBucketStatsCmd()

	case bucketDeleteCompleteMsg:
		m.loading = true
		m.bulkDeleting = false
		m.bulkDeleteCancel = nil
		m.cancelling = false
		m.clearError("delete-bucket")
		m.message = msg.message
		m.deleteProgress = ""
		return m, m.init()
	case bucketStatsMsg:
		for i := range m.fullBuckets {
			if m.fullBuckets[i].name == msg.name {
				m.fullBuckets[i].objects = msg.objects
				m.fullBuckets[i].sizeBytes = msg.sizeBytes
				m.fullBuckets[i].statsKnown = msg.known
				m.fullBuckets[i].statsUpdated = msg.updated
			}
		}
		for i := range m.items {
			if m.items[i].name == msg.name {
				m.items[i].objects = msg.objects
				m.items[i].sizeBytes = msg.sizeBytes
				m.items[i].statsKnown = msg.known
				m.items[i].statsUpdated = msg.updated
				break
			}
		}
		return m, nil

	case operationDoneMsg:
		m.mutationCancel = nil
		m.err = nil
		m.mutationPending = false
		m.cancelling = false
		m.message = msg.message
		m.detailMessage = msg.message
		m.deleteProgress = ""
		m.bulkDeleting = false
		m.bulkDeleteCancel = nil
		// If we were browsing files, reload the current directory
		if m.mode == bucketDetail || m.mode == bucketDetailConfirm || m.browsePrefix != "" || len(m.browseItems) > 0 {
			m.mode = bucketDetail
			m.loading = true
			if m.detailTab == 1 {
				return m, tea.Batch(m.spinner.Tick, m.loadBrowse(), m.loadPrefixes(), m.loadBucketUsers(), m.loadDirectBucketMetadata())
			}
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
		// If we're in detail view, reload prefixes and bucket users
		if m.mode == bucketDetail || m.mode == bucketDetailConfirm {
			m.mode = bucketDetail
			if m.cursor < len(m.items) {
				return m, tea.Batch(m.loadPrefixes(), m.loadBucketUsers())
			}
		}
		m.mode = bucketsList
		return m, m.init()

	case bucketNotEmptyMsg:
		m.loading = false
		m.mode = bucketsConfirmDeleteNonEmpty
		m.confirmInput.SetValue("")
		m.confirmInput.Focus()
		return m, textinput.Blink

	case deleteProgressMsg:
		m.deleteProgress = fmt.Sprintf("Emptying bucket... %s objects removed", formatWithCommas(msg.deleted))
		return m, nil

	case prefixesLoadedMsg:
		if msg.bucket != m.currentBucketName() || !currentRequest(m.prefixRequests, msg.request) {
			return m, nil
		}
		m.prefixes = msg.prefixes
		m.items[m.cursor].managedPublic = msg.rootPublic
		m.items[m.cursor].policyKnown = msg.known
		m.clearError("prefixes")
		return m, nil

	case browseLoadedMsg:
		if msg.bucket != "" && (msg.bucket != m.currentBucketName() || msg.prefix != m.browsePrefix) || !currentRequest(m.browseRequests, msg.request) {
			return m, nil
		}
		focus := m.pendingBrowseKey
		if focus == "" && m.browseCursor >= 0 && m.browseCursor < len(m.browseItems) {
			focus = m.browseItems[m.browseCursor].Key
		}
		selection := m.browseSelected
		m.fullBrowse = append([]awsClient.BrowseItem{}, msg.items...)
		m.browseItems = msg.items
		if m.filterScope == "files" {
			m.applyFilter()
		}
		m.browseSelected = make(map[string]bool)
		for i, item := range m.browseItems {
			if item.Key == focus {
				m.browseCursor = i
			}
			if selection[item.Key] {
				m.browseSelected[item.Key] = true
			}
		}
		m.loading = false
		m.clearError("browse")
		m.pendingBrowseKey = ""
		m.browseCursor, m.browseOffset = viewportBounds(m.browseCursor, m.browseOffset, len(m.browseItems), m.browseVisibleRows())
		return m, nil

	case browseFolderCreatedMsg:
		m.mutationPending = false
		m.mutationCancel = nil
		m.clearError("folder")
		m.clearFilter()
		m.fullBrowse = nil
		m.message = msg.message
		m.detailMessage = msg.message
		m.browsePrefix = msg.prefix
		m.browseItems = nil
		m.browseCursor = 0
		m.browseOffset = 0
		m.mode = bucketDetail
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())

	case folderCountedMsg:
		m.loading = false
		m.folderDeleteKey = msg.key
		m.folderDeleteCnt = msg.count
		m.folderDeletePublic = msg.isPublic
		m.mode = bucketDetailDeleteFolder
		m.deleteInput.SetValue("")
		m.deleteInput.Focus()
		return m, textinput.Blink

	case bucketUsersLoadedMsg:
		if msg.bucket != m.currentBucketName() || !currentRequest(m.userRequests, msg.request) {
			return m, nil
		}
		m.bucketUsersError = ""
		if msg.err != nil {
			if errors.Is(msg.err, awsClient.ErrIAMAccessDenied) {
				m.bucketUsersError = "IAM access denied - cannot list managed users with these credentials"
			} else {
				m.bucketUsersError = msg.err.Error()
			}
			m.bucketUsers = userPermsToItems(msg.users)
			m.bucketUsersLoading = false
			return m, nil
		}
		m.bucketUsers = userPermsToItems(msg.users)
		m.bucketUsersLoading = false
		return m, nil

	case directBucketMetadataLoadedMsg:
		if m.currentBucketName() != msg.bucket.name || !currentRequest(m.metadataRequests, msg.request) {
			return m, nil
		}
		msg.bucket.policyKnown = m.items[m.cursor].policyKnown
		msg.bucket.managedPublic = m.items[m.cursor].managedPublic
		m.items[m.cursor] = msg.bucket
		return m, nil

	case userPickerLoadedMsg:
		if msg.bucket != m.currentBucketName() || m.mode != bucketDetailPickUser {
			return m, nil
		}
		// Filter out users already assigned to this bucket
		assigned := make(map[string]bool, len(m.bucketUsers))
		for _, u := range m.bucketUsers {
			assigned[u.username] = true
		}
		m.availableUsers = nil
		for _, u := range msg.items {
			if !assigned[u.name] {
				m.availableUsers = append(m.availableUsers, u)
			}
		}
		m.loading = false
		m.userPickerCursor = 0
		m.userPickerSource = append([]userItem{}, m.availableUsers...)
		m.userPickerFilter = userFilter{}
		m.userPickerOffset = 0
		return m, nil

	case bucketAccessUpdatedMsg:
		m.mutationCancel = nil
		if msg.bucket != m.currentBucketName() {
			return m, nil
		}
		m.bucketUsers = userPermsToItems(msg.users)
		m.bucketUsersError = ""
		m.detailMessage = msg.message
		m.loading = false
		m.err = nil
		m.mutationPending = false
		if m.detailCursor > len(m.bucketUsers) {
			m.detailCursor = max(0, len(m.bucketUsers))
		}
		return m, nil

	case folderDeleteProgressMsg:
		m.deleteProgress = fmt.Sprintf("Deleting folder... %s objects removed", formatWithCommas(msg.deleted))
		return m, nil

	case selectionDeleteProgressMsg:
		m.deleteProgress = fmt.Sprintf("Deleting selected items... %s objects removed", formatWithCommas(msg.deleted))
		return m, nil

	case downloadDoneMsg:
		m.failedTransferReview = nil
		m.err = nil
		m.cancelling = false
		m.transferSnap = nil
		m.transferCancel = nil
		m.transferLabel = ""
		m.loading = false
		m.detailMessage = fmt.Sprintf("Downloaded %s to %s", msg.filename, msg.path)
		m.deleteProgress = ""
		return m, nil

	case uploadDoneMsg:
		m.failedTransferReview = nil
		m.err = nil
		m.cancelling = false
		m.transferSnap = nil
		m.transferCancel = nil
		m.transferLabel = ""
		m.loading = false
		m.detailMessage = fmt.Sprintf("Uploaded %s", msg.filename)
		m.deleteProgress = ""
		// Reload the browse view to show the new file
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())

	case urlUploadDoneMsg:
		if m.urlUpload == nil || msg.ID != 0 && msg.ID != m.urlUpload.id {
			return m, nil
		}
		m.urlUpload = nil
		m.detailMessage = fmt.Sprintf("Uploaded %s (%s)", msg.Key, formatSize(msg.Bytes))
		// Reload the browse view to show the new file
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())

	case urlUploadErrMsg:
		if m.urlUpload == nil || msg.ID != 0 && msg.ID != m.urlUpload.id {
			return m, nil
		}
		if msg.Err.Error() != "cancelled" {
			updated := m.urlUpload.failed(msg.Err)
			m.urlUpload = &updated
			return m, nil
		}
		m.urlUpload = nil
		if strings.Contains(msg.Err.Error(), "cancelled") {
			m.detailMessage = "URL upload cancelled"
		} else {
			m.detailMessage = "URL upload failed: " + msg.Err.Error()
		}
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case transferTickMsg:
		if m.transferSnap == nil {
			return m, nil
		}
		snap := m.transferSnap.Load()
		if snap != nil {
			now := time.Now()
			elapsed := now.Sub(m.transferLastTime).Seconds()
			delta := snap.Done - m.transferLastDone
			if elapsed > 0.01 && delta > 0 {
				m.transferRate = float64(delta) / elapsed
			}
			m.transferLastDone = snap.Done
			m.transferLastTime = now
			var barCmd tea.Cmd
			if snap.Total > 0 {
				pct := float64(snap.Done) / float64(snap.Total)
				if pct > 1.0 {
					pct = 1.0
				}
				barCmd = m.transferBar.SetPercent(pct)
			}
			return m, tea.Batch(barCmd, transferTick())
		}
		return m, transferTick()

	case bubprogress.FrameMsg:
		model, cmd := m.transferBar.Update(msg)
		m.transferBar = model.(bubprogress.Model)
		return m, cmd

	case tea.KeyMsg:
		switch m.mode {
		case bucketsList:
			return m.updateList(msg)
		case bucketsCreate:
			return m.updateCreate(msg)
		case bucketsTypeDelete:
			return m.updateTypeDelete(msg)
		case bucketsConfirmDelete:
			return m.updateConfirmDelete(msg)
		case bucketsConfirmDeleteNonEmpty:
			return m.updateConfirmDeleteNonEmpty(msg)
		case bucketDetail:
			return m.updateDetail(msg)
		case bucketDetailAddPrefix:
			return m.updateDetailAddPrefix(msg)
		case bucketDetailAddFolder:
			return m.updateBrowseAddFolder(msg)
		case bucketDetailConfirm:
			return m.updateDetailConfirm(msg)
		case bucketDetailDeleteFolder:
			return m.updateDeleteFolder(msg)
		case bucketDetailDeleteSelection:
			return m.updateDeleteSelection(msg)
		case bucketDetailPickUser:
			return m.updateBucketDetailPickUser(msg)
		case bucketDetailPickPerm:
			return m.updateBucketDetailPickPerm(msg)
		case bucketDetailConfirmRemoveUser:
			return m.updateBucketDetailConfirmRemoveUser(msg)
		}
	}
	return m, nil
}

// visibleRows returns how many bucket rows fit on screen.
// Accounts for breadcrumb, title, header, help line, and padding.
func (m bucketsModel) visibleRows() int {
	overhead := 8
	avail := m.height - overhead
	if avail < 1 {
		avail = 1
	}
	return avail
}

func (m bucketsModel) updateList(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	if m.loading && msg.String() != "r" {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			visible := m.visibleRows()
			if m.cursor >= m.offset+visible {
				m.offset = m.cursor - visible + 1
			}
		}
	case "pgup":
		visible := m.visibleRows()
		m.cursor -= visible
		if m.cursor < 0 {
			m.cursor = 0
		}
		if m.cursor < m.offset {
			m.offset = m.cursor
		}
	case "pgdown":
		visible := m.visibleRows()
		m.cursor += visible
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		if m.cursor >= m.offset+visible {
			m.offset = m.cursor - visible + 1
		}
	case "c":
		m.mode = bucketsCreate
		m.nameInput.SetValue("")
		m.nameInput.Focus()
		return m, textinput.Blink
	case "d":
		if len(m.items) > 0 {
			m.mode = bucketsConfirmDeleteNonEmpty
			m.confirmInput.SetValue("")
			m.confirmInput.Focus()
			m.deleteBucket = m.items[m.cursor]
			return m, textinput.Blink
		}
	case "r":
		m.loading = true
		return m, m.init()
	case "enter", "right", "l":
		if len(m.items) > 0 {
			selectedName := m.items[m.cursor].name
			m.clearFilter()
			for i, item := range m.items {
				if item.name == selectedName {
					m.cursor = i
					break
				}
			}
			m.mode = bucketDetail
			m.detailTab = 0
			m.browsePrefix = ""
			m.browseItems = nil
			m.fullBrowse = nil
			m.browseCursor = 0
			m.browseOffset = 0
			m.detailCursor = 0
			m.detailMessage = ""
			m.loading = true
			m.bucketUsers = nil
			m.bucketUsersLoading = true
			m.bucketUsersError = ""
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse(), m.loadPrefixes(), m.loadBucketUsers(), m.loadDirectBucketMetadata())
		}
	}
	return m, nil
}

func (m bucketsModel) updateCreate(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.partialBucketCreated {
			return m, nil
		}
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			return m, nil
		}
		m.loading = true
		if err := validateBucketName(name); err != nil {
			m.loading = false
			m.err = err
			return m, nil
		}
		m.mutationPending = true
		ctx, cancel := context.WithCancel(context.Background())
		m.mutationCancel = cancel
		return m, func() tea.Msg {
			defer cancel()
			err := m.client.CreateBucket(ctx, name, m.client.Region)
			if err != nil {
				return bucketErrorMsg{err: err, kind: "create"}
			}
			return operationDoneMsg{message: fmt.Sprintf("Created bucket %q", name)}
		}
	case "esc":
		m.mode = bucketsList
		if m.partialBucketCreated {
			m.partialBucketCreated = false
			m.loading = true
			return m, m.init()
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateTypeDelete(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.deleteInput.Value())
		if typed != "delete" {
			m.message = "You must type 'delete' to confirm. Cancelled."
			m.mode = bucketsList
			return m, nil
		}
		m.mode = bucketsConfirmDelete
		return m, nil
	case "esc":
		m.mode = bucketsList
		return m, nil
	default:
		var cmd tea.Cmd
		m.deleteInput, cmd = m.deleteInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateConfirmDelete(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		bucket := m.items[m.cursor]
		m.loading = true
		m.mode = bucketsList
		return m, func() tea.Msg {
			ctx := context.Background()
			empty, err := m.client.IsBucketEmpty(ctx, bucket.name, bucket.region)
			if err != nil {
				return bucketErrorMsg{err: err}
			}
			if !empty {
				return bucketNotEmptyMsg{name: bucket.name, region: bucket.region}
			}
			err = m.client.DeleteBucket(ctx, bucket.name, bucket.region)
			if err != nil {
				return bucketErrorMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Deleted bucket %q", bucket.name)}
		}
	default:
		m.mode = bucketsList
	}
	return m, nil
}

func (m bucketsModel) updateConfirmDeleteNonEmpty(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		bucket := m.deleteBucket
		if bucket.name == "" {
			bucket = m.items[m.cursor]
		}
		if strings.TrimSpace(m.confirmInput.Value()) != bucket.name {
			m.err = fmt.Errorf("type the exact bucket name to confirm")
			return m, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.loading = true
		m.bulkDeleting = true
		m.bulkDeleteCancel = cancel
		m.mode = bucketsList
		m.deleteProgress = "Deleting bucket contents... 0 objects removed"
		return m, func() tea.Msg {
			defer cancel()
			err := m.client.EmptyBucket(ctx, bucket.name, bucket.region, func(deleted int64) {
				if prog != nil {
					prog.Send(deleteProgressMsg{deleted: deleted})
				}
			})
			if err != nil {
				return bucketErrorMsg{err: err, kind: "delete-bucket"}
			}
			if err := m.client.DeleteBucket(ctx, bucket.name, bucket.region); err != nil {
				return bucketErrorMsg{err: err, kind: "delete-bucket"}
			}
			return bucketDeleteCompleteMsg{message: fmt.Sprintf("Deleted bucket %s and its contents", bucket.name)}
		}
	case "esc":
		m.mode = bucketsList
		return m, nil
	default:
		var cmd tea.Cmd
		m.confirmInput, cmd = m.confirmInput.Update(msg)
		return m, cmd
	}
}

// --- Detail view updates ---

// cursorSection returns which section the detail cursor is currently in.
func (m bucketsModel) cursorSection() string {
	if m.detailCursor == 0 {
		return "bucket"
	}
	if m.detailCursor <= len(m.bucketUsers) {
		return "users"
	}
	return "prefixes"
}

// userIndex returns the index into bucketUsers for the current cursor position.
func (m bucketsModel) userIndex() int {
	return m.detailCursor - 1
}

// prefixIndex returns the index into prefixes for the current cursor position.
func (m bucketsModel) prefixIndex() int {
	return m.detailCursor - 1 - len(m.bucketUsers)
}

func (m bucketsModel) updateDetail(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	if m.showFilePicker || m.transferSnap != nil || m.bulkDeleting {
		return m.updateBrowse(msg)
	}
	if msg.String() == "tab" {
		m.detailTab = (m.detailTab + 1) % 3
		return m, nil
	}
	if msg.String() == "shift+tab" {
		m.detailTab = (m.detailTab + 2) % 3
		return m, nil
	}
	if m.detailTab == 0 {
		return m.updateBrowse(msg)
	}
	if msg.String() == "esc" || msg.String() == "left" {
		return m.leaveBucket()
	}
	if msg.String() == "r" {
		m.bucketUsersLoading = true
		return m, tea.Batch(m.loadPrefixes(), m.loadBucketUsers(), m.loadDirectBucketMetadata())
	}
	if m.detailTab == 2 {
		return m, nil
	}
	maxRow := len(m.bucketUsers) + len(m.prefixes)
	switch msg.String() {
	case "up", "k":
		m.detailCursor = max(0, m.detailCursor-1)
	case "down", "j":
		m.detailCursor = min(maxRow, m.detailCursor+1)
	case "pgup":
		m.detailCursor = max(0, m.detailCursor-m.browseVisibleRows())
	case "pgdown":
		m.detailCursor = min(maxRow, m.detailCursor+m.browseVisibleRows())
	case "e", "enter":
		if m.cursorSection() == "users" {
			idx := m.userIndex()
			if idx >= 0 && idx < len(m.bucketUsers) {
				m.pendingUser = m.bucketUsers[idx].username
				m.mode = bucketDetailPickPerm
			}
		} else {
			return m.toggleSelected()
		}
	case "a":
		if m.bucketUsersLoading {
			return m, nil
		}
		m.mode = bucketDetailPickUser
		m.loading = true
		m.userPickerCursor = 0
		bucket := m.currentBucketName()
		return m, func() tea.Msg {
			users, err := m.client.ListManagedUsers(context.Background())
			if err != nil {
				return bucketErrorMsg{err: err, bucket: bucket}
			}
			items := make([]userItem, len(users))
			for i, u := range users {
				items[i] = userItem{name: u.Name, keyCount: u.KeyCount, created: u.CreateDate.Format("2006-01-02")}
			}
			return userPickerLoadedMsg{bucket: bucket, items: items}
		}
	case "d":
		if m.cursorSection() == "users" && m.userIndex() >= 0 && m.userIndex() < len(m.bucketUsers) {
			m.mode = bucketDetailConfirmRemoveUser
		}
	}
	_, m.accessOffset = viewportBounds(m.detailCursor, m.accessOffset, maxRow+1, m.browseVisibleRows())
	return m, nil
}

func (m bucketsModel) leaveBucket() (bucketsModel, tea.Cmd) {
	nextRequest(m.metadataRequests)
	nextRequest(m.browseRequests)
	nextRequest(m.prefixRequests)
	nextRequest(m.userRequests)
	m.clearFilter()
	m.mode = bucketsList
	m.browsePrefix = ""
	m.browseItems = nil
	m.fullBrowse = nil
	m.browseSelected = nil
	m.detailMessage = ""
	if m.directBucket {
		return m, tea.Quit
	}
	return m, nil
}

func (m bucketsModel) updateBucketDetailPickUser(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	if m.userPickerSource == nil {
		m.userPickerSource = append([]userItem{}, m.availableUsers...)
	}
	if msg.String() == "esc" && !m.userPickerFilter.active && m.userPickerFilter.query != "" {
		m.userPickerFilter.active = true
	}
	if m.userPickerFilter.key(msg) {
		m.availableUsers = nil
		for _, item := range m.userPickerSource {
			if m.userPickerFilter.matches(item.name) {
				m.availableUsers = append(m.availableUsers, item)
			}
		}
		m.userPickerCursor = 0
		m.userPickerOffset = 0
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if m.userPickerCursor > 0 {
			m.userPickerCursor--
		}
	case "down", "j":
		if m.userPickerCursor < len(m.availableUsers)-1 {
			m.userPickerCursor++
		}
	case "pgup":
		m.userPickerCursor = max(0, m.userPickerCursor-m.browseVisibleRows())
	case "pgdown":
		m.userPickerCursor = min(max(0, len(m.availableUsers)-1), m.userPickerCursor+m.browseVisibleRows())
	case "enter":
		if len(m.availableUsers) > 0 && m.userPickerCursor < len(m.availableUsers) {
			m.pendingUser = m.availableUsers[m.userPickerCursor].name
			m.mode = bucketDetailPickPerm
		}
	case "esc":
		m.mode = bucketDetail
		m.availableUsers = nil
		m.userPickerCursor = 0
	}
	m.userPickerCursor, m.userPickerOffset = viewportBounds(m.userPickerCursor, m.userPickerOffset, len(m.availableUsers), m.browseVisibleRows())
	return m, nil
}

func (m bucketsModel) updateBucketDetailPickPerm(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	if msg.String() == "esc" {
		m.mode = bucketDetail
		m.pendingUser = ""
		return m, nil
	}
	perms := map[string]model.PermissionLevel{"1": model.PermRead, "2": model.PermReadWrite, "3": model.PermReadWriteDelete}
	perm, ok := perms[msg.String()]
	if !ok {
		return m, nil
	}
	bucket := m.items[m.cursor]
	username := m.pendingUser
	old := "None"
	for _, u := range m.bucketUsers {
		if u.username == username {
			old = string(u.permission)
		}
	}
	m.confirmAction = fmt.Sprintf("Apply access for %s on s3://%s? Current: %s. Requested: %s.", username, bucket.name, old, perm)
	m.mode = bucketDetailConfirm
	m.confirmInput2.SetValue("")
	m.confirmInput2.Focus()
	m.confirmFunc = func(ctx context.Context) tea.Msg {
		access, err := m.client.GetUserBucketAccess(ctx, username)
		if err != nil {
			return bucketErrorMsg{err: err, bucket: bucket.name}
		}
		found := false
		for i := range access {
			if access[i].Bucket == bucket.name {
				access[i].Permission = perm
				found = true
			}
		}
		if !found {
			access = append(access, model.BucketAccess{Bucket: bucket.name, Permission: perm})
		}
		if err := m.client.SetUserBucketAccess(ctx, username, access); err != nil {
			return bucketErrorMsg{err: err, bucket: bucket.name}
		}
		users, err := m.client.ListBucketUsers(ctx, bucket.name)
		if err != nil {
			return bucketErrorMsg{err: fmt.Errorf("Access saved; refresh failed: %w", err), bucket: bucket.name}
		}
		return bucketAccessUpdatedMsg{bucket: bucket.name, users: users, message: fmt.Sprintf("Applied %s access for %s", perm, username)}
	}
	return m, textinput.Blink
}

func (m bucketsModel) updateBucketDetailConfirmRemoveUser(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		idx := m.userIndex()
		if idx >= 0 && idx < len(m.bucketUsers) {
			u := m.bucketUsers[idx]
			bucket := m.items[m.cursor]
			username := u.username
			m.loading = true
			m.mutationPending = true
			m.mode = bucketDetail

			ctx, cancel := context.WithCancel(context.Background())
			m.mutationCancel = cancel
			return m, func() tea.Msg {
				defer cancel()
				// Get user's full access, remove entry for this bucket
				access, err := m.client.GetUserBucketAccess(ctx, username)
				if err != nil {
					return bucketErrorMsg{err: err}
				}
				updated := make([]model.BucketAccess, 0, len(access))
				for _, a := range access {
					if a.Bucket != bucket.name {
						updated = append(updated, a)
					}
				}
				err = m.client.SetUserBucketAccess(ctx, username, updated)
				if err != nil {
					return bucketErrorMsg{err: err}
				}
				users := make([]model.UserPermission, 0, len(m.bucketUsers)-1)
				for _, item := range m.bucketUsers {
					if item.username == username {
						continue
					}
					users = append(users, model.UserPermission{Username: item.username, Permission: item.permission})
				}
				return bucketAccessUpdatedMsg{
					bucket:  bucket.name,
					message: fmt.Sprintf("Removed %s access to %s", username, bucket.name),
					users:   users,
				}
			}
		}
		m.mode = bucketDetail
	default:
		m.mode = bucketDetail
	}
	return m, nil
}

func (m bucketsModel) toggleSelected() (bucketsModel, tea.Cmd) {
	bucket := m.items[m.cursor]
	if !bucket.policyKnown {
		m.err = fmt.Errorf("public policy is unknown; refresh Access before editing")
		return m, nil
	}
	prefix := ""
	current := bucket.managedPublic
	if m.detailCursor > len(m.bucketUsers) {
		idx := m.prefixIndex()
		if idx < 0 || idx >= len(m.prefixes) {
			return m, nil
		}
		prefix = m.prefixes[idx].prefix
		current = m.prefixes[idx].isPublic
	}
	action := "Add managed public read grant"
	if current {
		action = "Remove managed public read grant"
	}
	m.confirmAction = fmt.Sprintf("%s for s3://%s/%s? This changes the bucket policy and public settings. Other grants may still apply.", action, bucket.name, prefix)
	m.confirmFunc = func(ctx context.Context) tea.Msg {
		var err error
		if current {
			err = m.client.SetPrefixPrivate(ctx, bucket.name, prefix, bucket.region)
		} else {
			err = m.client.SetPrefixPublic(ctx, bucket.name, prefix, bucket.region)
		}
		if err != nil {
			return bucketErrorMsg{err: err, bucket: bucket.name}
		}
		return operationDoneMsg{message: "Applied public access change. Refresh Access to inspect current settings."}
	}
	m.mode = bucketDetailConfirm
	m.confirmInput2.SetValue("")
	m.confirmInput2.Focus()
	return m, textinput.Blink
}

func (m bucketsModel) updateDetailAddPrefix(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.prefixInput.Value())
		if name == "" {
			return m, nil
		}
		// Ensure trailing slash
		if !strings.HasSuffix(name, "/") {
			name += "/"
		}
		// Check for duplicates
		for _, p := range m.prefixes {
			if p.prefix == name {
				m.detailMessage = fmt.Sprintf("Prefix %s already exists", name)
				m.mode = bucketDetail
				return m, nil
			}
		}
		bucket := m.items[m.cursor]
		m.loading = true
		m.mode = bucketDetail
		return m, func() tea.Msg {
			ctx := context.Background()
			err := m.client.CreatePrefix(ctx, bucket.name, name, bucket.region)
			if err != nil {
				return bucketErrorMsg{err: err}
			}
			return operationDoneMsg{message: fmt.Sprintf("Added prefix %s (private by default)", name)}
		}
	case "esc":
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.prefixInput, cmd = m.prefixInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateBrowseAddFolder(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		raw := strings.TrimSpace(m.prefixInput.Value())
		raw = strings.Trim(raw, "/")
		if raw == "" {
			return m, nil
		}
		newKey := m.browsePrefix + raw + "/"
		// Reject duplicates within the current view
		entries := m.fullBrowse
		if entries == nil {
			entries = m.browseItems
		}
		for _, item := range entries {
			if item.Key == newKey {
				m.detailMessage = fmt.Sprintf("Folder %q already exists here", raw)
				m.mode = bucketDetail
				return m, nil
			}
			if !item.IsFolder && item.Name == raw {
				m.detailMessage = fmt.Sprintf("A file named %q already exists here", raw)
				m.mode = bucketDetail
				return m, nil
			}
		}
		bucket := m.items[m.cursor]
		m.loading = true
		m.mutationPending = true
		m.mode = bucketDetail
		ctx, cancel := context.WithCancel(context.Background())
		m.mutationCancel = cancel
		return m, func() tea.Msg {
			defer cancel()
			err := m.client.CreatePrefix(ctx, bucket.name, newKey, bucket.region)
			if err != nil {
				return bucketErrorMsg{err: err, kind: "folder", bucket: bucket.name}
			}
			return browseFolderCreatedMsg{
				prefix:  newKey,
				message: fmt.Sprintf("Created folder %s", newKey),
			}
		}
	case "esc":
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.prefixInput, cmd = m.prefixInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateDetailConfirm(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.confirmInput2.Value())
		if typed != "yes" {
			m.detailMessage = "Cancelled. Type exactly \"yes\" to confirm."
			m.mode = bucketDetail
			return m, nil
		}
		m.loading = true
		m.mutationPending = true
		m.mode = bucketDetail
		ctx, cancel := context.WithCancel(context.Background())
		m.mutationCancel = cancel
		action := m.confirmFunc
		return m, func() tea.Msg { defer cancel(); return action(ctx) }
	case "esc":
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.confirmInput2, cmd = m.confirmInput2.Update(msg)
		return m, cmd
	}
}

// loadPrefixes fetches prefix list and access status for the currently selected bucket.
func (m bucketsModel) loadPrefixes() tea.Cmd {
	bucket := m.items[m.cursor]
	request := nextRequest(m.prefixRequests)
	return func() tea.Msg {
		ctx := context.Background()
		prefixNames, err := m.client.ListPrefixes(ctx, bucket.name, bucket.region)
		if err != nil {
			return bucketErrorMsg{err: err, kind: "prefixes", bucket: bucket.name, request: request}
		}
		accesses, err := m.client.GetPrefixAccessStatus(ctx, bucket.name, bucket.region, append([]string{""}, prefixNames...))
		if err != nil {
			return bucketErrorMsg{err: err, kind: "prefixes", bucket: bucket.name, request: request}
		}
		items := make([]prefixItem, 0, len(prefixNames))
		rootPublic := false
		for _, a := range accesses {
			if a.Prefix == "" {
				rootPublic = a.IsPublic
				continue
			}
			items = append(items, prefixItem{prefix: a.Prefix, isPublic: a.IsPublic})
		}
		return prefixesLoadedMsg{bucket: bucket.name, prefixes: items, request: request, rootPublic: rootPublic, known: true}
	}
}

func (m bucketsModel) loadDirectBucketMetadata() tea.Cmd {
	bucket := m.items[m.cursor]
	request := nextRequest(m.metadataRequests)
	return func() tea.Msg {
		ctx := context.Background()
		item := bucket

		if m.directBucket && item.created == "" {
			if buckets, err := m.client.ListBuckets(ctx); err == nil {
				for _, b := range buckets {
					if b.Name != bucket.name {
						continue
					}
					item.region = b.Region
					item.created = b.CreationDate.Format("2006-01-02")
					break
				}
			}
		}

		stats, statsErr := m.client.GetBucketStats(ctx, bucket.name, item.region)
		item.objects = stats.ObjectCount
		item.sizeBytes = stats.SizeBytes
		item.statsKnown = statsErr == nil && stats.Known
		item.statsUpdated = stats.UpdatedAt

		item.isPublic, item.accessKnown, _ = m.client.PublicAccessStatus(ctx, bucket.name, item.region)

		return directBucketMetadataLoadedMsg{bucket: item, request: request}
	}
}

// loadBucketUsers fetches the list of users with access to the currently selected bucket.
func (m bucketsModel) loadBucketUsers() tea.Cmd {
	bucket := m.items[m.cursor]
	request := nextRequest(m.userRequests)
	return func() tea.Msg {
		ctx := context.Background()
		users, err := m.client.ListBucketUsers(ctx, bucket.name)
		if err != nil {
			return bucketUsersLoadedMsg{bucket: bucket.name, err: err, request: request}
		}
		return bucketUsersLoadedMsg{bucket: bucket.name, users: users, request: request}
	}
}

// transferTickMsg fires every 250ms while a transfer is in flight and
// drives the progress bar / rate display.
type transferTickMsg struct{}

func transferTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return transferTickMsg{}
	})
}

// renderTransferProgress returns the multi-line progress block shown
// during an in-flight upload or download. Returns "" when no transfer
// is active.
func (m bucketsModel) renderTransferProgress() string {
	if m.transferSnap == nil {
		return ""
	}
	snap := m.transferSnap.Load()
	if snap == nil {
		return ""
	}

	label := m.transferLabel
	bar := m.transferBar.View()

	done := formatSize(snap.Done)
	rate := progress.FormatRate(m.transferRate)

	var status string
	if snap.Total > 0 {
		total := formatSize(snap.Total)
		pct := float64(snap.Done) / float64(snap.Total) * 100
		eta := ""
		if rateVal := progress.ParseRateBytesPerSec(rate); rateVal > 0 {
			remaining := float64(snap.Total-snap.Done) / rateVal
			eta = " - ETA " + progress.FormatDuration(time.Duration(remaining*float64(time.Second)))
		}
		status = fmt.Sprintf("%s / %s (%.0f%%) - %s%s", done, total, pct, rate, eta)
	} else {
		status = fmt.Sprintf("%s - %s", done, rate)
	}

	return fmt.Sprintf("  %s\n  %s\n  %s\n", label, bar, dimStyle.Render(status))
}

// --- Views ---

func (m bucketsModel) view() (content string) {
	defer func() { content = fitTerminal(content, m.width, m.height) }()
	switch m.mode {
	case bucketDetail, bucketDetailAddPrefix, bucketDetailAddFolder, bucketDetailConfirm, bucketDetailDeleteFolder, bucketDetailDeleteSelection:
		return m.viewDetail()
	case bucketDetailPickUser:
		return m.viewPickUser()
	case bucketDetailPickPerm:
		return m.viewPickPerm()
	case bucketDetailConfirmRemoveUser:
		return m.viewConfirmRemoveUser()
	default:
		return m.viewList()
	}
}

func (m bucketsModel) viewPickUser() string {
	body := m.userPickerFilter.view(len(m.availableUsers), len(m.userPickerSource))
	if m.loading {
		body += "Loading managed users..."
	} else if len(m.availableUsers) == 0 {
		body += "No matching users. Clear the filter or refresh Managed users."
	}
	rows := m.browseVisibleRows()
	cursor, offset := viewportBounds(m.userPickerCursor, m.userPickerOffset, len(m.availableUsers), rows)
	for i := offset; i < min(len(m.availableUsers), offset+rows); i++ {
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		body += marker + m.availableUsers[i].name + "\n"
	}
	return renderPanel("Assign user to "+m.currentBucketName(), body, "/: Filter  Enter: Select  Esc: Cancel", m.width, m.height)
}

func (m bucketsModel) viewPickPerm() string {
	bucket := m.items[m.cursor]
	s := breadcrumbStyle.Render(fmt.Sprintf("dashboard > buckets > %s > Add user", bucket.name)) + "\n"
	s += screenTitleStyle.Render(fmt.Sprintf("Permission for %q:", m.pendingUser)) + "\n\n"

	s += "  [1] read\n"
	s += "  [2] read-write\n"
	s += "  [3] read-write-delete\n\n"

	s += helpStyle.Render("  Press 1, 2, or 3  [esc] Cancel")
	return s
}

func (m bucketsModel) viewConfirmRemoveUser() string {
	bucket := m.items[m.cursor]
	s := breadcrumbStyle.Render(fmt.Sprintf("dashboard > buckets > %s", bucket.name)) + "\n"
	idx := m.userIndex()
	if idx >= 0 && idx < len(m.bucketUsers) {
		s += warningStyle.Render(fmt.Sprintf("Remove %q access to this bucket? [y/N]", m.bucketUsers[idx].username))
	}
	return s
}

// --- File Browser ---

func (m bucketsModel) loadBrowse() tea.Cmd {
	bucket := m.items[m.cursor]
	prefix := m.browsePrefix
	request := nextRequest(m.browseRequests)
	return func() tea.Msg {
		ctx := context.Background()
		items, err := m.client.ListContents(ctx, bucket.name, prefix, bucket.region)
		if err != nil {
			return bucketErrorMsg{err: err, kind: "browse", bucket: bucket.name, prefix: prefix, request: request}
		}
		return browseLoadedMsg{items: items, bucket: bucket.name, prefix: prefix, request: request}
	}
}

func (m bucketsModel) browseVisibleRows() int {
	overhead := 9
	if m.statusText() != "" {
		overhead++
	}
	avail := m.height - overhead
	if avail < 1 {
		avail = 1
	}
	return avail
}

func (m bucketsModel) updateBrowse(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	// When the file picker is active, delegate all keys to it
	if m.showFilePicker {
		ownedInput := m.filePicker.ownsInput()
		fp, cmd, selected := m.filePicker.update(msg)
		m.filePicker = fp
		m.filePicker.width = m.width
		m.filePicker.height = m.height
		if msg.String() == "esc" && !ownedInput {
			m.showFilePicker = false
			return m, nil
		}
		if selected != "" {
			m.showFilePicker = false
			return m.prepareLocalUpload(selected)
		}
		return m, cmd
	}

	if m.transferSnap != nil && msg.String() != "esc" && msg.String() != "ctrl+c" {
		return m, nil
	}
	if m.bulkDeleting {
		if msg.String() == "esc" && m.bulkDeleteCancel != nil {
			m.bulkDeleteCancel()
		}
		return m, nil
	}

	switch msg.String() {
	case "i":
		if m.browseCursor >= 0 && m.browseCursor < len(m.browseItems) {
			m.inspectText = "Full object path\ns3://" + m.currentBucketName() + "/" + m.browseItems[m.browseCursor].Key
			m.dialogOffset = 0
		}
	case "up", "k":
		if m.browseCursor > 0 {
			m.browseCursor--
			if m.browseCursor < m.browseOffset {
				m.browseOffset = m.browseCursor
			}
		}
	case "down", "j":
		if m.browseCursor < len(m.browseItems)-1 {
			m.browseCursor++
			visible := m.browseVisibleRows()
			if m.browseCursor >= m.browseOffset+visible {
				m.browseOffset = m.browseCursor - visible + 1
			}
		}
	case "pgup":
		visible := m.browseVisibleRows()
		m.browseCursor -= visible
		if m.browseCursor < 0 {
			m.browseCursor = 0
		}
		if m.browseCursor < m.browseOffset {
			m.browseOffset = m.browseCursor
		}
	case "pgdown":
		visible := m.browseVisibleRows()
		m.browseCursor += visible
		if m.browseCursor >= len(m.browseItems) {
			m.browseCursor = max(0, len(m.browseItems)-1)
		}
		if m.browseCursor >= m.browseOffset+visible {
			m.browseOffset = m.browseCursor - visible + 1
		}
	case "enter", "right", "l":
		if m.browseCursor >= 0 && m.browseCursor < len(m.browseItems) && m.browseItems[m.browseCursor].IsFolder {
			if m.parentPositions == nil {
				m.parentPositions = make(map[string]browsePosition)
			}
			m.parentPositions[m.browsePrefix] = browsePosition{key: m.browseItems[m.browseCursor].Key, filter: m.filterInput.Value(), cursor: m.browseCursor, offset: m.browseOffset}
			prefix := m.browseItems[m.browseCursor].Key
			m.clearFilter()
			m.browsePrefix = prefix
			m.browseSelected = nil
			m.browseItems = nil
			m.fullBrowse = nil
			m.browseCursor = 0
			m.browseOffset = 0
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
		}
	case "left", "h", "esc":
		if m.transferSnap != nil && m.transferCancel != nil {
			m.transferCancel()
			m.cancelling = true
			return m, nil
		}
		if m.browsePrefix == "" {
			return m.leaveBucket()
		}
		m.clearFilter()
		m.browseSelected = nil
		trimmed := strings.TrimSuffix(m.browsePrefix, "/")
		parent := ""
		if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
			parent = trimmed[:slash+1]
		}
		m.browsePrefix = parent
		m.browseItems = nil
		m.fullBrowse = nil
		m.loading = true
		position := m.parentPositions[parent]
		m.pendingBrowseKey = position.key
		m.browseCursor = position.cursor
		m.browseOffset = position.offset
		if position.filter != "" {
			m.filterScope = "files"
			m.filterInput.SetValue(position.filter)
		}
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
	case " ":
		if m.browseCursor < len(m.browseItems) {
			if m.browseSelected == nil {
				m.browseSelected = make(map[string]bool)
			}
			key := m.browseItems[m.browseCursor].Key
			if m.browseSelected[key] {
				delete(m.browseSelected, key)
			} else {
				m.browseSelected[key] = true
			}
		}
	case "a":
		if len(m.browseSelected) > 0 {
			m.browseSelected = nil
		} else {
			m.browseSelected = make(map[string]bool, len(m.browseItems))
			for _, item := range m.browseItems {
				m.browseSelected[item.Key] = true
			}
		}
	case "c":
		return m.updateBrowse(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	case "d":
		if m.browseCursor < len(m.browseItems) {
			if len(m.browseSelected) > 0 {
				files, folders := m.selectedBrowseCounts()
				m.confirmAction = fmt.Sprintf("Delete %s?", describeBrowseSelection(files, folders))
				m.pendingDelete = nil
				for _, item := range m.browseItems {
					if m.browseSelected[item.Key] {
						m.pendingDelete = append(m.pendingDelete, item)
					}
				}
				m.mode = bucketDetailDeleteSelection
				m.deleteInput.SetValue("")
				m.deleteInput.Focus()
				return m, textinput.Blink
			}
			item := m.browseItems[m.browseCursor]
			bucket := m.items[m.cursor]
			if item.IsFolder {
				// Folder delete - count objects first (with spinner)
				m.loading = true
				m.deleteProgress = "Counting objects..."
				return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
					ctx := context.Background()
					count, err := m.client.CountObjects(ctx, bucket.name, item.Key, bucket.region)
					if err != nil {
						return bucketErrorMsg{err: err}
					}
					accesses, err := m.client.GetPrefixAccessStatus(ctx, bucket.name, bucket.region, []string{item.Key})
					if err != nil {
						return bucketErrorMsg{err: err}
					}
					isPublic := len(accesses) > 0 && accesses[0].IsPublic
					return folderCountedMsg{name: item.Name, key: item.Key, count: count, isPublic: isPublic}
				})
			}
			// File delete
			m.confirmAction = fmt.Sprintf("Delete file %q?", item.Name)
			m.confirmFunc = func(ctx context.Context) tea.Msg {
				err := m.client.DeleteObject(ctx, bucket.name, item.Key, bucket.region)
				if err != nil {
					return bucketErrorMsg{err: err}
				}
				return operationDoneMsg{message: fmt.Sprintf("Deleted %s", item.Name)}
			}
			m.mode = bucketDetailConfirm
			m.confirmInput2.SetValue("")
			m.confirmInput2.Focus()
			return m, textinput.Blink
		}
	case "g":
		if m.browseCursor >= 0 && m.browseCursor < len(m.browseItems) && !m.browseItems[m.browseCursor].IsFolder {
			return m.prepareDownload(m.browseItems[m.browseCursor])
		}
	case "s":
		if m.browseCursor >= 0 && m.browseCursor < len(m.browseItems) && !m.browseItems[m.browseCursor].IsFolder {
			bucket := m.items[m.cursor]
			share := newShare(m.client, bucket.name, m.browseItems[m.browseCursor].Key, bucket.region)
			m.share = &share
			return m, m.share.Init()
		}
	case "n":
		// Create a new folder at the current browse prefix
		m.mode = bucketDetailAddFolder
		m.prefixInput.SetValue("")
		m.prefixInput.Focus()
		return m, textinput.Blink
	case "p":
		// Open local file picker for upload
		fp := newFilePicker()
		fp.width = m.width
		fp.height = m.height
		fp = fp.loadDir()
		m.filePicker = fp
		m.showFilePicker = true
		return m, nil
	case "U":
		// Open URL upload modal
		bucket := m.items[m.cursor]
		um := newURLUpload(m.client, bucket.name, bucket.region, m.browsePrefix)
		um.width = m.width
		m.urlUpload = &um
		return m, m.urlUpload.Init()
	case "r":
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.loadBrowse())
	}
	return m, nil
}

func (m bucketsModel) selectedBrowseCounts() (files, folders int) {
	for _, item := range m.browseItems {
		if !m.browseSelected[item.Key] {
			continue
		}
		if item.IsFolder {
			folders++
		} else {
			files++
		}
	}
	return files, folders
}

func describeBrowseSelection(files, folders int) string {
	parts := make([]string, 0, 2)
	if files > 0 {
		parts = append(parts, fmt.Sprintf("%d file%s", files, pluralSuffix(files)))
	}
	if folders > 0 {
		parts = append(parts, fmt.Sprintf("%d folder%s", folders, pluralSuffix(folders)))
	}
	return strings.Join(parts, " and ")
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (m bucketsModel) updateDeleteSelection(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if strings.TrimSpace(m.deleteInput.Value()) != "delete" {
			m.detailMessage = "You must type 'delete' to confirm. Cancelled."
			m.mode = bucketDetail
			return m, nil
		}

		if m.cursor < 0 || m.cursor >= len(m.items) {
			m.mode = bucketDetail
			m.detailMessage = "Delete cancelled: bucket is no longer available"
			return m, nil
		}
		bucket := m.items[m.cursor]
		selected := append([]awsClient.BrowseItem{}, m.pendingDelete...)
		if m.pendingDelete == nil {
			for _, item := range m.browseItems {
				if m.browseSelected[item.Key] {
					selected = append(selected, item)
				}
			}
		}
		if len(selected) == 0 {
			m.mode = bucketDetail
			m.browseSelected = nil
			m.detailMessage = "Delete cancelled: selection is no longer available"
			return m, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.loading = true
		m.mode = bucketDetail
		m.deleteProgress = "Deleting selected items..."
		m.bulkDeleting = true
		m.bulkDeleteCancel = cancel
		return m, func() tea.Msg {
			var deleted int64
			fileKeys := make([]string, 0, len(selected))
			folderKeys := make([]string, 0, len(selected))
			for _, item := range selected {
				if item.IsFolder {
					folderKeys = append(folderKeys, item.Key)
				} else {
					fileKeys = append(fileKeys, item.Key)
				}
			}
			fileDeleted, err := m.client.DeleteObjectKeys(ctx, bucket.name, fileKeys, bucket.region, func(count int64) {
				if prog != nil {
					prog.Send(selectionDeleteProgressMsg{deleted: count})
				}
			})
			if err != nil {
				return bucketErrorMsg{err: err}
			}
			deleted = fileDeleted
			deletedFolders := make([]string, 0, len(folderKeys))
			for _, folderKey := range folderKeys {
				before := deleted
				if err := m.client.DeletePrefix(ctx, bucket.name, folderKey, bucket.region, func(folderDeleted int64) {
					deleted = before + folderDeleted
					if prog != nil {
						prog.Send(selectionDeleteProgressMsg{deleted: deleted})
					}
				}); err != nil {
					if len(deletedFolders) > 0 {
						cleanupCtx := context.WithoutCancel(ctx)
						if cleanupErr := m.client.SetPrefixesPrivate(cleanupCtx, bucket.name, deletedFolders, bucket.region); cleanupErr != nil {
							return bucketErrorMsg{err: fmt.Errorf("%v; public-access cleanup also failed: %w", err, cleanupErr)}
						}
					}
					return bucketErrorMsg{err: err}
				}
				deletedFolders = append(deletedFolders, folderKey)
			}
			if len(deletedFolders) > 0 {
				if err := m.client.SetPrefixesPrivate(ctx, bucket.name, deletedFolders, bucket.region); err != nil {
					return bucketErrorMsg{err: err}
				}
			}
			return operationDoneMsg{message: fmt.Sprintf(
				"Deleted %s objects across %s selected items",
				formatWithCommas(deleted), formatWithCommas(int64(len(selected))),
			)}
		}
	case "esc":
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.deleteInput, cmd = m.deleteInput.Update(msg)
		return m, cmd
	}
}

func (m bucketsModel) updateDeleteFolder(msg tea.KeyMsg) (bucketsModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		typed := strings.TrimSpace(m.deleteInput.Value())
		if typed != "delete" {
			m.detailMessage = "You must type 'delete' to confirm. Cancelled."
			m.deleteProgress = ""
			m.mode = bucketDetail
			return m, nil
		}
		bucket := m.items[m.cursor]
		m.loading = true
		m.deleteProgress = "Deleting folder... 0 objects removed"
		m.mode = bucketDetail
		folderKey := m.folderDeleteKey
		ctx, cancel := context.WithCancel(context.Background())
		m.bulkDeleting = true
		m.bulkDeleteCancel = cancel
		return m, func() tea.Msg {
			defer cancel()
			err := m.client.DeletePrefix(ctx, bucket.name, folderKey, bucket.region, func(deleted int64) {
				if prog != nil {
					prog.Send(folderDeleteProgressMsg{deleted: deleted})
				}
			})
			if err != nil {
				return bucketErrorMsg{err: err}
			}
			if m.folderDeletePublic {
				err = m.client.SetPrefixPrivate(ctx, bucket.name, folderKey, bucket.region)
				if err != nil {
					return bucketErrorMsg{err: err}
				}
			}
			return operationDoneMsg{message: fmt.Sprintf("Deleted folder %s and all its contents", folderKey)}
		}
	case "esc":
		m.deleteProgress = ""
		m.folderDeletePublic = false
		m.mode = bucketDetail
		return m, nil
	default:
		var cmd tea.Cmd
		m.deleteInput, cmd = m.deleteInput.Update(msg)
		return m, cmd
	}
}
