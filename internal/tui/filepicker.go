package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type localFileItem struct {
	name    string
	isDir   bool
	size    int64
	modTime string // formatted "2006-01-02 15:04"
}

type filePickerModel struct {
	path   string          // current directory path
	items  []localFileItem // directory contents
	cursor int
	offset int
	width  int
	height int
	err    error
}

func newFilePicker() filePickerModel {
	cwd, _ := os.Getwd()
	return filePickerModel{path: cwd}
}

// loadDir reads the directory at fp.path and populates fp.items.
func (fp filePickerModel) loadDir() filePickerModel {
	entries, err := os.ReadDir(fp.path)
	if err != nil {
		fp.err = err
		return fp
	}

	fp.items = nil
	// Directories first, then files, each sorted alphabetically
	var dirs, files []localFileItem
	for _, e := range entries {
		// Skip hidden files
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		item := localFileItem{
			name:  e.Name(),
			isDir: e.IsDir(),
			size:  info.Size(),
		}
		if !info.ModTime().IsZero() {
			item.modTime = info.ModTime().Format("2006-01-02 15:04")
		}
		if e.IsDir() {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	fp.items = append(dirs, files...)
	fp.cursor = 0
	fp.offset = 0
	fp.err = nil
	return fp
}

func (fp filePickerModel) visibleRows() int {
	overhead := 6
	avail := fp.height - overhead
	if avail < 3 {
		avail = 3
	}
	return avail
}

// update handles keys for the file picker. Returns the updated model,
// a tea.Cmd, and a selectedFile path (non-empty when user picks a file).
func (fp filePickerModel) update(msg tea.KeyMsg) (filePickerModel, tea.Cmd, string) {
	switch msg.String() {
	case "up", "k":
		if fp.cursor > 0 {
			fp.cursor--
			if fp.cursor < fp.offset {
				fp.offset = fp.cursor
			}
		}
	case "down", "j":
		if fp.cursor < len(fp.items)-1 {
			fp.cursor++
			visible := fp.visibleRows()
			if fp.cursor >= fp.offset+visible {
				fp.offset = fp.cursor - visible + 1
			}
		}
	case "right", "l":
		// Enter directory
		if fp.cursor < len(fp.items) && fp.items[fp.cursor].isDir {
			fp.path = filepath.Join(fp.path, fp.items[fp.cursor].name)
			fp = fp.loadDir()
		}
	case "left", "h":
		// Go up one directory
		parent := filepath.Dir(fp.path)
		if parent != fp.path {
			fp.path = parent
			fp = fp.loadDir()
		}
	case "enter":
		// Select file for upload (only files, not dirs)
		if fp.cursor < len(fp.items) && !fp.items[fp.cursor].isDir {
			selected := filepath.Join(fp.path, fp.items[fp.cursor].name)
			return fp, nil, selected
		}
		// If it's a directory, enter it
		if fp.cursor < len(fp.items) && fp.items[fp.cursor].isDir {
			fp.path = filepath.Join(fp.path, fp.items[fp.cursor].name)
			fp = fp.loadDir()
		}
	case "esc":
		// Signal cancel — caller checks mode
		return fp, nil, ""
	}
	return fp, nil, ""
}

// view renders the file picker, matching the S3 browser's visual style.
func (fp filePickerModel) view(detailWidth int) string {
	s := breadcrumbStyle.Render("local > "+fp.path) + "\n"
	s += screenTitleStyle.Render("Select file to upload") + "\n"
	s += separator(detailWidth) + "\n"

	if fp.err != nil {
		s += "\n " + errorStyle.Render("Error: "+fp.err.Error()) + "\n"
		s += "\n" + helpStyle.Render("  [\u2190] Back  [esc] Cancel")
		return s
	}

	if len(fp.items) == 0 {
		s += "\n " + dimStyle.Render("Empty directory.") + "\n"
		s += "\n" + helpStyle.Render("  [\u2190] Back  [esc] Cancel")
		return s
	}

	// Table header — same layout as S3 browser
	header := fmt.Sprintf(" %s  %s  %s",
		pad("NAME", 40), padRight("SIZE", 10), pad("MODIFIED", 20))
	s += tableHeaderStyle.Width(detailWidth).Render(header) + "\n"

	visible := fp.visibleRows()
	end := fp.offset + visible
	if end > len(fp.items) {
		end = len(fp.items)
	}
	if fp.offset > 0 {
		s += dimStyle.Render(fmt.Sprintf(" \u25b2 %d more above", fp.offset)) + "\n"
	}
	for i := fp.offset; i < end; i++ {
		item := fp.items[i]
		var icon, sz string
		if item.isDir {
			icon = "\U0001F4C1 "
		} else {
			icon = "   "
			sz = formatSize(item.size)
		}
		display := icon + pad(item.name, 37)
		row := fmt.Sprintf(" %s  %s  %s", display, padRight(sz, 10), pad(item.modTime, 20))
		if i == fp.cursor {
			s += rowSelectedStyle.Width(detailWidth).Render(row) + "\n"
		} else {
			s += rowStyle.Render(row) + "\n"
		}
	}
	if end < len(fp.items) {
		s += dimStyle.Render(fmt.Sprintf(" \u25bc %d more below", len(fp.items)-end)) + "\n"
	}

	s += "\n" + helpStyle.Render("  [enter] Select file  [\u2192] Open folder  [\u2190] Back  [esc] Cancel")
	return s
}
