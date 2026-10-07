package tui

import (
	"fmt"
	"strings"
)

func (m bucketsModel) statusText() string {
	if m.err != nil {
		return errorStyle.Render("Error: " + m.err.Error() + ". Retry or Escape to return.")
	}
	if m.cancelling {
		return warningStyle.Render("Cancelling. Waiting for completion...")
	}
	if m.mode == bucketsList {
		return m.message
	}
	return m.detailMessage
}

func (m bucketsModel) filterView(count, total int) string {
	if m.filterScope != "" {
		value := m.filterInput.Value()
		if m.filterActive {
			value = m.filterInput.View()
		}
		return fmt.Sprintf("Filter: %s  (%d of %d)", value, count, total)
	}
	return fmt.Sprintf("%d entries  /: Filter", count)
}

func (m bucketsModel) viewList() string {
	title := "Buckets"
	if m.mode != bucketsList {
		body := ""
		footer := "Enter: Continue  Esc: Cancel"
		switch m.mode {
		case bucketsCreate:
			title = "Create bucket"
			body = "Name: " + m.nameInput.View() + "\nRegion: " + m.clientRegion() + "\nUse 3-63 lowercase letters, numbers, dots, or hyphens.\n" + m.statusText()
			if m.partialBucketCreated {
				body += "\nThe bucket exists. Inspect Access before retrying setup."
				footer = "Esc: Return and inspect created bucket"
			}
		case bucketsTypeDelete, bucketsConfirmDelete, bucketsConfirmDeleteNonEmpty:
			title = "Delete bucket"
			body = "Type the bucket name: " + m.confirmInput.View() + "\nTarget: s3://" + m.currentBucketName() + "\nDeletes the bucket and all object versions. This cannot be undone.\n" + m.statusText()
			footer = "Enter: Delete bucket and contents  Esc: Cancel"
		}
		return renderScrollablePanel(title, body, footer, m.width, m.height, m.dialogOffset)
	}
	if m.loading {
		body := "Loading buckets..."
		if m.deleteProgress != "" {
			body = m.deleteProgress
		}
		return renderPanel(title, m.spinner.View()+" "+body+"\n"+m.statusText(), "Esc: Cancel operation  ?: Help", m.width, m.height)
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	rows := m.visibleRows()
	cursor, offset := viewportBounds(m.cursor, m.offset, len(m.items), rows)
	total := len(m.items)
	if m.filterScope == "buckets" {
		total = len(m.fullBuckets)
	}
	body := m.filterView(len(m.items), total) + "\n" + truncate(m.statusText(), width) + "\n"
	nameWidth := max(20, width-18)
	body += pad("  NAME", nameWidth) + " REGION\n"
	if len(m.items) == 0 {
		if m.filterScope != "" {
			body += "No matching buckets. Esc clears the filter.\n"
		} else {
			body += "No buckets found. Press c to create one, or r to retry.\n"
		}
	}
	for i := offset; i < min(len(m.items), offset+rows); i++ {
		b := m.items[i]
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		row := pad(marker+b.name, nameWidth) + " " + b.region
		if i == cursor {
			row = rowSelectedStyle.Render(truncate(row, width))
		}
		body += row + "\n"
	}
	if len(m.items) > 0 {
		body += "Selected: " + m.items[cursor].name
	}
	footer := "Enter: Open  /: Filter  c: Create  d: Delete  u: Users  ?: Help"
	if m.filterActive {
		footer = "Type to filter  Enter: Keep results  Esc: Clear"
	}
	return renderPanel(title, body, footer, width, m.height)
}

func (m bucketsModel) clientRegion() string {
	if m.client == nil || m.client.Region == "" {
		return "Unknown"
	}
	return m.client.Region
}

func (m bucketsModel) viewDetail() string {
	if m.inspectText != "" {
		return renderScrollablePanel("File details", m.inspectText, "Up/Down: Scroll  Esc: Back", m.width, m.height, m.dialogOffset)
	}
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return "No bucket selected. Esc: Back"
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	b := m.items[m.cursor]
	tabs := []string{"Files", "Access", "Details"}
	for i := range tabs {
		if i == m.detailTab {
			tabs[i] = "[" + tabs[i] + "]"
		}
	}
	title := fmt.Sprintf("%s  %s", b.name, strings.Join(tabs, "  "))
	if m.transferReview != nil {
		return m.viewTransferReview()
	}
	if m.share != nil {
		return m.share.View(width, m.height)
	}
	if m.urlUpload != nil {
		return fitTerminal(m.urlUpload.View(), width, m.height)
	}
	if m.showFilePicker {
		return renderPanel("Upload to s3://"+b.name+"/"+m.browsePrefix, m.filePicker.view(width), "", width, m.height)
	}
	if m.mode != bucketDetail {
		body := ""
		footer := "Enter: Apply  Esc: Cancel"
		switch m.mode {
		case bucketDetailAddPrefix, bucketDetailAddFolder:
			body = "Folder name: " + m.prefixInput.View() + "\nDestination: s3://" + b.name + "/" + m.browsePrefix
			footer = "Enter: Create folder  Esc: Cancel"
		case bucketDetailConfirm:
			body = "Type yes to apply: " + m.confirmInput2.View() + "\n" + m.confirmAction
		case bucketDetailDeleteFolder:
			body = "Type delete: " + m.deleteInput.View() + fmt.Sprintf("\nDelete %d current objects under s3://%s/%s?", m.folderDeleteCnt, b.name, m.folderDeleteKey) + "\nPrevious versions may remain. Deletion cannot be undone here."
		case bucketDetailDeleteSelection:
			body = "Type delete: " + m.deleteInput.View() + "\n" + m.confirmAction + "\nDestination: s3://" + b.name + "/" + m.browsePrefix + "\nDeletes current objects recursively. Previous versions may remain."
		}
		return renderScrollablePanel(title, body+"\n"+m.statusText(), footer+"  PgUp/PgDn: Scroll", width, m.height, m.dialogOffset)
	}
	if m.transferSnap != nil {
		return renderPanel(title, m.renderTransferProgress()+"\n"+m.statusText(), "Esc / Ctrl+C: Cancel and wait", width, m.height)
	}
	if m.bulkDeleting {
		return renderPanel(title, m.deleteProgress+"\n"+m.statusText(), "Esc / Ctrl+C: Cancel and wait", width, m.height)
	}
	if m.detailTab == 2 {
		stats := "Unknown"
		if b.statsKnown {
			stats = fmt.Sprintf("%s objects, %s", formatCount(b.objects), formatSize(b.sizeBytes))
		}
		created := b.created
		if created == "" {
			created = "Unknown"
		}
		asOf := "Unknown"
		if !b.statsUpdated.IsZero() {
			asOf = b.statsUpdated.Local().Format("2006-01-02 15:04 MST")
		}
		return renderScrollablePanel(title, fmt.Sprintf("Region: %s\nCreated: %s\nDaily CloudWatch statistics: %s\nSize covers Standard storage only.\nSamples as of: %s\nStatistics can lag behind recent changes.\n%s", b.region, created, stats, asOf, m.statusText()), "Tab: Next view  r: Refresh  Esc: Back", width, m.height, m.dialogOffset)
	}
	if m.detailTab == 1 {
		return m.viewAccess(title, width)
	}
	if m.loading {
		return renderPanel(title, m.spinner.View()+" Loading s3://"+b.name+"/"+m.browsePrefix+"\n"+m.statusText(), "Esc: Back  Tab: Access", width, m.height)
	}
	total := len(m.browseItems)
	if m.filterScope == "files" {
		total = len(m.fullBrowse)
	}
	body := truncate("s3://"+b.name+"/"+m.browsePrefix, width) + "\n" + m.filterView(len(m.browseItems), total) + fmt.Sprintf("  Selected: %d\n", len(m.browseSelected))
	if status := m.statusText(); status != "" {
		body += truncate(status, width) + "\n"
	}
	rows := m.browseVisibleRows()
	cursor, offset := viewportBounds(m.browseCursor, m.browseOffset, len(m.browseItems), rows)
	nameWidth := max(12, width-16)
	body += pad("  NAME", nameWidth) + " SIZE\n"
	if len(m.browseItems) == 0 {
		if m.filterScope != "" {
			body += "No matches. Esc clears the filter.\n"
		} else {
			body += "This folder is empty. Press p to upload or n for a folder.\n"
		}
	}
	for i := offset; i < min(len(m.browseItems), offset+rows); i++ {
		item := m.browseItems[i]
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		check := "[ ] "
		if m.browseSelected[item.Key] {
			check = "[x] "
		}
		size := formatSize(item.Size)
		if item.IsFolder {
			size = "Folder"
		}
		row := pad(marker+check+item.Name, nameWidth) + " " + size
		if i == cursor {
			row = rowSelectedStyle.Render(truncate(row, width))
		}
		body += row + "\n"
	}
	if len(m.browseItems) > 0 {
		body += "Selected: " + m.browseItems[cursor].Key
	}
	footer := "Enter: Open  /: Filter  Space: Select  m: Actions  Tab: Access"
	if m.filterActive {
		footer = "Type to filter  Enter: Keep results  Esc: Clear"
	}
	return renderPanel(title, body, footer, width, m.height)
}

func (m bucketsModel) viewAccess(title string, width int) string {
	b := m.items[m.cursor]
	access := "Unknown"
	if b.accessKnown {
		access = "Public settings not fully blocked"
		if !b.isPublic {
			access = "Public access blocked"
		}
	}
	grant := "Unknown"
	if b.policyKnown {
		grant = "No managed public read grant"
		if b.managedPublic {
			grant = "Managed public read grant"
		}
	}
	entries := []string{"Bucket: " + access + "; " + grant}
	for _, u := range m.bucketUsers {
		entries = append(entries, u.username+"  "+string(u.permission))
	}
	for _, p := range m.prefixes {
		label := "No managed public grant"
		if p.isPublic {
			label = "Managed public grant"
		}
		entries = append(entries, p.prefix+"  "+label)
	}
	_, offset := viewportBounds(m.detailCursor, m.accessOffset, len(entries), max(1, m.height-9))
	body := "Settings and managed grants do not prove public reachability.\n" + m.statusText() + "\n"
	if m.bucketUsersLoading {
		body += "Loading assigned users...\n"
	}
	if m.bucketUsersError != "" {
		body += m.bucketUsersError + "\n"
	}
	for i := offset; i < min(len(entries), offset+max(1, m.height-9)); i++ {
		marker := "  "
		if i == m.detailCursor {
			marker = "> "
		}
		body += marker + entries[i] + "\n"
	}
	return renderPanel(title, body, "Enter: Review edit  a: Add user  d: Remove  Tab: Details", width, m.height)
}
