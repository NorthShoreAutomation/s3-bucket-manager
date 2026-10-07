package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pickerKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func TestFilePickerLiveFilterAndEscape(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "BETA.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fp := filePickerModel{path: dir, height: 12}.loadDir()
	fp, _, _ = fp.update(pickerKey("/"))
	fp, _, _ = fp.update(pickerKey("beta"))
	if len(fp.items) != 1 || fp.items[0].name != "BETA.txt" {
		t.Fatalf("filter did not narrow: %v", fp.items)
	}
	fp, _, _ = fp.update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(fp.items) != 2 || fp.filterActive {
		t.Fatal("escape did not restore directory")
	}
	fp, _, _ = fp.update(pickerKey("."))
	if len(fp.items) != 3 {
		t.Fatal("hidden-file toggle failed")
	}
}
func TestFilePickerFailedLoadClearsSelectableItems(t *testing.T) {
	fp := filePickerModel{path: filepath.Join(t.TempDir(), "missing"), items: []localFileItem{{name: "stale"}}}.loadDir()
	if fp.err == nil || len(fp.items) != 0 {
		t.Fatal("failed load retained stale entries")
	}
	_, _, selected := fp.update(tea.KeyMsg{Type: tea.KeyEnter})
	if selected != "" {
		t.Fatal("selected stale item")
	}
}
func TestFilePickerPathEntry(t *testing.T) {
	dir := t.TempDir()
	fp := newFilePicker()
	fp, _, _ = fp.update(pickerKey("p"))
	fp.pathInput.SetValue(dir)
	fp, _, _ = fp.update(tea.KeyMsg{Type: tea.KeyEnter})
	if fp.path != dir || fp.pathActive {
		t.Fatalf("path input did not navigate: %s", fp.path)
	}
}
