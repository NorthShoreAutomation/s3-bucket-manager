package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestBucketFilterUpdatesWhileTypingAndProtectsShortcuts(t *testing.T) {
	a := NewApp(nil)
	a.buckets.loading = false
	a.buckets.items = []bucketItem{{name: "archive"}, {name: "Quarterly"}, {name: "uploads"}}
	for _, s := range []string{"/", "q", "u"} {
		next, _ := a.Update(key(s))
		a = next.(App)
	}
	if a.screen != screenBuckets || len(a.buckets.items) != 1 || a.buckets.currentBucketName() != "Quarterly" {
		t.Fatalf("live filter did not retain matching bucket: %#v", a.buckets.items)
	}
	if !strings.Contains(a.View(), "1 of 3") {
		t.Fatal("missing filtered count")
	}
	next, _ := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = next.(App)
	if len(a.buckets.items) != 3 {
		t.Fatal("escape did not restore buckets")
	}
}

func TestFileFilterSearchesWholeListingAndActionsUseFullKeys(t *testing.T) {
	m := newBucketsModel(nil)
	m.loading = false
	m.mode = bucketDetail
	m.items = []bucketItem{{name: "bucket"}}
	for i := 0; i < 1500; i++ {
		m.browseItems = append(m.browseItems, awsClient.BrowseItem{Name: fmt.Sprintf("file-%04d", i), Key: fmt.Sprintf("dir/file-%04d", i)})
	}
	m, _ = m.update(key("/"))
	for _, r := range "1499" {
		m, _ = m.update(key(string(r)))
	}
	if len(m.browseItems) != 1 || m.browseItems[0].Key != "dir/file-1499" {
		t.Fatal("filter did not search full listing")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.updateBrowse(key("a"))
	if len(m.browseSelected) != 1 || !m.browseSelected["dir/file-1499"] {
		t.Fatal("selection did not use displayed full key")
	}
	m, _ = m.update(key("/"))
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.browseItems) != 1500 || len(m.browseSelected) != 0 {
		t.Fatal("clear did not restore list and reset selection")
	}
}

func TestObsoleteBrowseAndPrefixResultsCannotReplaceCurrentResource(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "new"}}
	m.mode = bucketDetail
	m.browsePrefix = "current/"
	m.loading = true
	m, _ = m.update(browseLoadedMsg{bucket: "old", prefix: "other/", items: []awsClient.BrowseItem{{Name: "wrong"}}})
	if len(m.browseItems) != 0 || !m.loading {
		t.Fatal("obsolete browse response changed state")
	}
	m, _ = m.update(prefixesLoadedMsg{bucket: "old", prefixes: []prefixItem{{prefix: "wrong/"}}})
	if len(m.prefixes) != 0 || !m.loading {
		t.Fatal("obsolete prefix response changed state")
	}
}

func TestInactiveBucketOwnerReceivesResult(t *testing.T) {
	a := NewApp(nil)
	a.screen = screenUsers
	next, _ := a.Update(bucketsLoadedMsg{buckets: []bucketItem{{name: "loaded"}}})
	a = next.(App)
	if len(a.buckets.items) != 1 || a.screen != screenUsers {
		t.Fatal("inactive bucket result was lost or stole screen")
	}
}

func TestTerminalTextHelpersPreserveUnicode(t *testing.T) {
	for _, s := range []string{"日本語資料", "👩‍💻 report", "e\u0301clair"} {
		for w := 0; w < 8; w++ {
			if got := truncate(s, w); !utf8.ValidString(got) || ansi.StringWidth(got) > w {
				t.Fatalf("invalid truncation %q at %d", got, w)
			}
			if got := pad(s, w); !utf8.ValidString(got) || ansi.StringWidth(got) != w {
				t.Fatalf("invalid padding %q at %d", got, w)
			}
		}
	}
}

func TestBucketFilesAndDialogsFitSupportedTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 15}} {
		m := newBucketsModel(nil)
		m.width = size[0]
		m.height = size[1]
		m.loading = false
		m.mode = bucketDetail
		m.items = []bucketItem{{name: strings.Repeat("long", 15)}}
		for i := 0; i < 100; i++ {
			m.browseItems = append(m.browseItems, awsClient.BrowseItem{Name: strings.Repeat("資料", 30), Key: fmt.Sprint(i)})
		}
		for _, mode := range []bucketsMode{bucketDetail, bucketDetailAddFolder, bucketDetailConfirm, bucketDetailDeleteSelection} {
			m.mode = mode
			m.confirmAction = strings.Repeat("long path ", 30)
			v := m.view()
			width := 0
			for _, line := range strings.Split(v, "\n") {
				width = max(width, ansi.StringWidth(line))
			}
			if width > size[0] || len(strings.Split(v, "\n")) > size[1] {
				t.Fatalf("mode %d overflow at %v: %dx%d", mode, size, ansi.StringWidth(v), len(strings.Split(v, "\n")))
			}
		}
	}
}

func TestFileBrowserSeparatesLocationListingAndFocus(t *testing.T) {
	for _, size := range [][2]int{{60, 12}, {60, 15}, {80, 24}, {120, 40}} {
		for _, status := range []string{"", "Upload complete."} {
			a := screenFixture(size[0], size[1])
			a.buckets.mode = bucketDetail
			a.buckets.browseCursor = 49
			a.buckets.detailMessage = status
			view := a.View()
			assertFixtureBounds(t, view, size[0], size[1])
			lines := strings.Split(ansi.Strip(view), "\n")
			location, header, focused, detail := -1, -1, -1, -1
			for i, line := range lines {
				switch {
				case strings.Contains(line, "s3://example-bucket/"):
					location = i
				case strings.Contains(line, "NAME") && strings.Contains(line, "SIZE"):
					header = i
				case strings.HasPrefix(line, "> [ ] sample-49"):
					focused = i
				case strings.HasPrefix(line, "Focus: sample-49"):
					detail = i
				}
			}
			if location < 0 || header <= location || focused <= header || detail <= focused {
				t.Fatalf("missing location, listing, or focused-file context at %v:\n%s", size, ansi.Strip(view))
			}
			if !strings.Contains(strings.Join(lines[location+1:header], "\n"), strings.Repeat("─", size[0])) ||
				!strings.Contains(strings.Join(lines[focused+1:detail], "\n"), strings.Repeat("─", size[0])) {
				t.Fatalf("file list needs visible boundaries at %v:\n%s", size, ansi.Strip(view))
			}
			for _, label := range []string{"Checked: 0", "Enter: Open", "m: Actions"} {
				if !strings.Contains(ansi.Strip(view), label) {
					t.Fatalf("missing %q at %v:\n%s", label, size, ansi.Strip(view))
				}
			}
		}
	}
}

func TestEmptyPageDownAndCreateFailureKeepUsefulState(t *testing.T) {
	m := newBucketsModel(nil)
	m.loading = false
	m, _ = m.updateList(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor < 0 {
		t.Fatal("empty cursor went negative")
	}
	m.mode = bucketsCreate
	m.nameInput.SetValue("valid-name")
	m, _ = m.update(bucketErrorMsg{err: errors.New("denied"), kind: "create"})
	if m.mode != bucketsCreate || m.nameInput.Value() != "valid-name" || m.loading {
		t.Fatal("failed create lost input")
	}
}

func TestBucketUserPickerFiltersAndDoesNotTreatLettersAsCommands(t *testing.T) {
	m := newBucketsModel(nil)
	m.loading = false
	m.items = []bucketItem{{name: "bucket"}}
	m.mode = bucketDetailPickUser
	m.availableUsers = []userItem{{name: "alice"}, {name: "quarterly"}, {name: "bob"}}
	m, _ = m.update(key("/"))
	m, _ = m.update(key("q"))
	if strings.Contains(m.viewPickUser(), "alice") || !strings.Contains(m.viewPickUser(), "quarterly") {
		t.Fatal("picker did not apply live filter")
	}
}

func TestAccessChangesAlwaysRequireReview(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket", isPublic: true, accessKnown: true, policyKnown: true, managedPublic: true}}
	m.mode = bucketDetail
	m.detailTab = 1
	m.loading = false
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != bucketDetailConfirm || cmd == nil || m.loading {
		t.Fatal("access change bypassed review")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != bucketDetail || m.mutationPending {
		t.Fatal("cancel made a mutation")
	}
}

func TestOldBrowseFailureDoesNotClearNewerRequest(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket"}}
	m.browsePrefix = "folder/"
	m.loading = true
	m.browseRequests.Store(2)
	m, _ = m.update(bucketErrorMsg{err: errors.New("old failure"), kind: "browse", bucket: "bucket", prefix: "folder/", request: 1})
	if m.err != nil || !m.loading {
		t.Fatal("old failure replaced newer operation")
	}
}

func TestNewFolderChecksFilteredOutEntries(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket"}}
	m.mode = bucketDetailAddFolder
	m.loading = false
	m.fullBrowse = []awsClient.BrowseItem{{Name: "existing/", Key: "existing/", IsFolder: true}}
	m.prefixInput.SetValue("existing")
	m, cmd := m.updateBrowseAddFolder(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.loading {
		t.Fatal("folder hidden by filter bypassed duplicate check")
	}
}

func TestFolderFailurePreservesInputAndMatchingError(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket"}}
	m.mode = bucketDetail
	m.prefixInput.SetValue("draft")
	m.mutationPending = true
	m, _ = m.update(bucketErrorMsg{kind: "folder", err: errors.New("permission denied")})
	if m.mode != bucketDetailAddFolder || m.prefixInput.Value() != "draft" || m.mutationPending {
		t.Fatal("folder failure lost retry input")
	}
	m, _ = m.update(browseLoadedMsg{bucket: "bucket"})
	if m.err == nil {
		t.Fatal("unrelated listing success cleared a failed mutation")
	}
}

func TestLeavingBucketInvalidatesDelayedBrowseResults(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket"}}
	m.mode = bucketDetail
	m.browseRequests.Store(4)
	m, _ = m.leaveBucket()
	m.loading = true
	m, _ = m.update(browseLoadedMsg{bucket: "bucket", request: 4, items: []awsClient.BrowseItem{{Name: "old"}}})
	if len(m.browseItems) != 0 || !m.loading {
		t.Fatal("abandoned bucket read changed the home screen")
	}
}

func TestInspectShowsFullObjectPathAndReturnsToSelection(t *testing.T) {
	m := newBucketsModel(nil)
	m.loading = false
	m.mode = bucketDetail
	m.width = 80
	m.height = 24
	m.items = []bucketItem{{name: "bucket"}}
	m.browseItems = []awsClient.BrowseItem{{Name: "report.txt", Key: "archive/report.txt"}}
	m, _ = m.update(key("i"))
	if !strings.Contains(m.view(), "Full object path") || !strings.Contains(m.view(), "s3://bucket/archive/report.txt") {
		t.Fatal("full object path is unavailable")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != bucketDetail || m.browseCursor != 0 || strings.Contains(m.view(), "Full object path") {
		t.Fatal("inspection did not return to focused file")
	}
}

func TestMetadataArrivalPreservesConfirmedPolicy(t *testing.T) {
	m := newBucketsModel(nil)
	m.items = []bucketItem{{name: "bucket", policyKnown: true, managedPublic: true}}
	m, _ = m.update(directBucketMetadataLoadedMsg{bucket: bucketItem{name: "bucket", region: "us-west-2", accessKnown: true}})
	if !m.items[0].policyKnown || !m.items[0].managedPublic {
		t.Fatal("metadata replaced confirmed policy")
	}
}
