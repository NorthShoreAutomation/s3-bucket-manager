package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type localFileItem struct {
	name    string
	isDir   bool
	size    int64
	modTime string
}

type filePickerModel struct {
	path                                 string
	items, allItems                      []localFileItem
	cursor, offset, width, height        int
	err                                  error
	filter                               string
	filterActive, pathActive, showHidden bool
	pathInput                            textinput.Model
}

func newFilePicker() filePickerModel       { cwd, _ := os.Getwd(); return filePickerModel{path: cwd} }
func (fp filePickerModel) ownsInput() bool { return fp.filterActive || fp.pathActive }
func (fp filePickerModel) loadDir() filePickerModel {
	fp.items = nil
	fp.allItems = nil
	fp.cursor = 0
	fp.offset = 0
	entries, err := os.ReadDir(fp.path)
	if err != nil {
		fp.err = err
		return fp
	}
	for _, e := range entries {
		if !fp.showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		item := localFileItem{name: e.Name(), isDir: e.IsDir(), size: info.Size()}
		if !info.ModTime().IsZero() {
			item.modTime = info.ModTime().Format("2006-01-02 15:04")
		}
		fp.allItems = append(fp.allItems, item)
	}
	sort.Slice(fp.allItems, func(i, j int) bool {
		a, b := fp.allItems[i], fp.allItems[j]
		if a.isDir != b.isDir {
			return a.isDir
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	fp.err = nil
	return fp.applyFilter()
}
func (fp filePickerModel) applyFilter() filePickerModel {
	fp.items = nil
	for _, item := range fp.allItems {
		if strings.Contains(strings.ToLower(item.name), strings.ToLower(fp.filter)) {
			fp.items = append(fp.items, item)
		}
	}
	fp.cursor = 0
	fp.offset = 0
	return fp
}
func (fp filePickerModel) visibleRows() int {
	rows := fp.height - 9
	if rows < 1 {
		rows = 1
	}
	return rows
}
func (fp filePickerModel) update(msg tea.KeyMsg) (filePickerModel, tea.Cmd, string) {
	if fp.pathActive {
		switch msg.String() {
		case "esc":
			fp.pathActive = false
			return fp, nil, ""
		case "enter":
			p := strings.TrimSpace(fp.pathInput.Value())
			if p != "" {
				if p == "~" || strings.HasPrefix(p, "~/") {
					home, err := os.UserHomeDir()
					if err == nil {
						p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
						if fp.pathInput.Value() == "~" {
							p = home
						}
					}
				}
				if !filepath.IsAbs(p) {
					p = filepath.Join(fp.path, p)
				}
				fp.path = filepath.Clean(p)
				fp.filter = ""
				fp = fp.loadDir()
			}
			fp.pathActive = false
			return fp, nil, ""
		}
		var cmd tea.Cmd
		fp.pathInput, cmd = fp.pathInput.Update(msg)
		return fp, cmd, ""
	}
	if fp.filterActive {
		switch msg.String() {
		case "esc":
			fp.filter = ""
			fp.filterActive = false
			fp = fp.applyFilter()
			return fp, nil, ""
		case "enter":
			fp.filterActive = false
			return fp, nil, ""
		case "backspace", "ctrl+h":
			r := []rune(fp.filter)
			if len(r) > 0 {
				fp.filter = string(r[:len(r)-1])
			}
			fp = fp.applyFilter()
			return fp, nil, ""
		case "up", "down":
		default:
			if msg.Type == tea.KeyRunes {
				fp.filter += string(msg.Runes)
				fp = fp.applyFilter()
			}
			return fp, nil, ""
		}
	}
	switch msg.String() {
	case "/":
		fp.filterActive = true
	case "p":
		fp.pathActive = true
		fp.pathInput = textinput.New()
		fp.pathInput.SetValue(fp.path)
		fp.pathInput.Focus()
		return fp, textinput.Blink, ""
	case ".":
		fp.showHidden = !fp.showHidden
		fp = fp.loadDir()
	case "up", "k":
		if fp.cursor > 0 {
			fp.cursor--
		}
	case "down", "j":
		if fp.cursor < len(fp.items)-1 {
			fp.cursor++
		}
	case "pgup":
		fp.cursor = max(0, fp.cursor-fp.visibleRows())
	case "pgdown":
		fp.cursor = max(0, min(len(fp.items)-1, fp.cursor+fp.visibleRows()))
	case "left", "h":
		parent := filepath.Dir(fp.path)
		if parent != fp.path {
			fp.path = parent
			fp.filter = ""
			fp = fp.loadDir()
		}
	case "right", "l", "enter":
		if fp.cursor >= 0 && fp.cursor < len(fp.items) {
			item := fp.items[fp.cursor]
			if item.isDir {
				fp.path = filepath.Join(fp.path, item.name)
				fp.filter = ""
				fp = fp.loadDir()
			} else if msg.String() == "enter" {
				return fp, nil, filepath.Join(fp.path, item.name)
			}
		}
	}
	if fp.cursor < fp.offset {
		fp.offset = fp.cursor
	}
	if fp.cursor >= fp.offset+fp.visibleRows() {
		fp.offset = fp.cursor - fp.visibleRows() + 1
	}
	return fp, nil, ""
}
func (fp filePickerModel) view(detailWidth int) string {
	width := max(12, detailWidth)
	s := breadcrumbStyle.Render(truncate("local > "+fp.path, width)) + "\n" + screenTitleStyle.Render("Select file to upload") + "\n" + separator(width) + "\n"
	if fp.pathActive {
		s += "Path: " + fp.pathInput.View() + "\n"
	} else {
		s += fmt.Sprintf("Filter: %s  (%d of %d)\n", fp.filter, len(fp.items), len(fp.allItems))
	}
	if fp.err != nil {
		return s + errorStyle.Render(truncate("Cannot read folder: "+fp.err.Error(), width)) + "\n" + helpStyle.Render("[p] Enter path  [left] Parent  [esc] Cancel")
	}
	if len(fp.items) == 0 {
		if fp.filter != "" {
			s += dimStyle.Render("No matching files. Escape clears the filter.") + "\n"
		} else {
			s += dimStyle.Render("This folder has no visible files.") + "\n"
		}
	}
	nameWidth := max(4, width-18)
	s += tableHeaderStyle.Render(pad("NAME", nameWidth)+"  "+padRight("SIZE", 10)) + "\n"
	end := min(len(fp.items), fp.offset+fp.visibleRows())
	for i := fp.offset; i < end; i++ {
		item := fp.items[i]
		name := item.name
		size := formatSize(item.size)
		if item.isDir {
			name += "/"
			size = "Folder"
		}
		row := pad(truncate(name, nameWidth), nameWidth) + "  " + padRight(size, 10)
		if i == fp.cursor {
			s += rowSelectedStyle.Render(row) + "\n"
		} else {
			s += rowStyle.Render(row) + "\n"
		}
	}
	s += helpStyle.Render(truncate("[enter] Select/open  [/] Filter  [p] Path  [.] Hidden  [esc] Cancel", width))
	return s
}
