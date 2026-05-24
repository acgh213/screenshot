package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// TreeItem represents one row in the file tree.
type TreeItem struct {
	Path     string
	IsDir    bool
	Expanded bool
	Depth    int
}

// FileTree is the left-pane expandable directory tree.
type FileTree struct {
	items    []TreeItem
	cursor   int
	root     string
	manifest Manifest
	height   int
	offset   int // scroll offset
}

func NewFileTree(root string, m Manifest) FileTree {
	ft := FileTree{root: root, manifest: m}
	ft.loadDir(root, 0)
	return ft
}

func (ft *FileTree) loadDir(dir string, depth int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	// Dirs first, then files
	var dirs, files []os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else if imageExts[strings.ToLower(filepath.Ext(e.Name()))] ||
			videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	for _, e := range dirs {
		ft.items = append(ft.items, TreeItem{
			Path:  filepath.Join(dir, e.Name()),
			IsDir: true,
			Depth: depth,
		})
	}
	for _, e := range files {
		ft.items = append(ft.items, TreeItem{
			Path:  filepath.Join(dir, e.Name()),
			IsDir: false,
			Depth: depth,
		})
	}
}

// Selected returns the path of the currently highlighted item.
func (ft *FileTree) Selected() string {
	if ft.cursor < len(ft.items) {
		return ft.items[ft.cursor].Path
	}
	return ""
}

// SelectedItem returns the currently highlighted TreeItem.
func (ft *FileTree) SelectedItem() *TreeItem {
	if ft.cursor < len(ft.items) {
		return &ft.items[ft.cursor]
	}
	return nil
}

// MoveUp moves the cursor up one row.
func (ft *FileTree) MoveUp() {
	if ft.cursor > 0 {
		ft.cursor--
		if ft.cursor < ft.offset {
			ft.offset = ft.cursor
		}
	}
}

// MoveDown moves the cursor down one row.
func (ft *FileTree) MoveDown() {
	if ft.cursor < len(ft.items)-1 {
		ft.cursor++
		if ft.cursor >= ft.offset+ft.height {
			ft.offset = ft.cursor - ft.height + 1
		}
	}
}

// Toggle expands or collapses the selected directory.
func (ft *FileTree) Toggle() {
	if ft.cursor >= len(ft.items) {
		return
	}
	item := &ft.items[ft.cursor]
	if !item.IsDir {
		return
	}
	if item.Expanded {
		ft.collapse(ft.cursor)
	} else {
		ft.expand(ft.cursor)
	}
}

func (ft *FileTree) expand(idx int) {
	item := &ft.items[idx]
	item.Expanded = true

	entries, err := os.ReadDir(item.Path)
	if err != nil {
		return
	}
	var dirs, files []os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else if imageExts[strings.ToLower(filepath.Ext(e.Name()))] ||
			videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	newItems := make([]TreeItem, 0, len(dirs)+len(files))
	for _, e := range dirs {
		newItems = append(newItems, TreeItem{
			Path:  filepath.Join(item.Path, e.Name()),
			IsDir: true,
			Depth: item.Depth + 1,
		})
	}
	for _, e := range files {
		newItems = append(newItems, TreeItem{
			Path:  filepath.Join(item.Path, e.Name()),
			IsDir: false,
			Depth: item.Depth + 1,
		})
	}

	tail := make([]TreeItem, len(ft.items)-idx-1)
	copy(tail, ft.items[idx+1:])
	ft.items = append(ft.items[:idx+1], append(newItems, tail...)...)
}

func (ft *FileTree) collapse(idx int) {
	item := &ft.items[idx]
	item.Expanded = false
	depth := item.Depth
	end := idx + 1
	for end < len(ft.items) && ft.items[end].Depth > depth {
		end++
	}
	ft.items = append(ft.items[:idx+1], ft.items[end:]...)
	if ft.cursor >= len(ft.items) {
		ft.cursor = len(ft.items) - 1
	}
}

// SetManifest updates the manifest reference (called after catalog).
func (ft *FileTree) SetManifest(m Manifest) {
	ft.manifest = m
}

// Refresh reloads the root directory and re-expands previously expanded dirs.
func (ft *FileTree) Refresh() {
	// Remember which dirs were expanded and cursor position.
	expanded := map[string]bool{}
	for _, item := range ft.items {
		if item.IsDir && item.Expanded {
			expanded[item.Path] = true
		}
	}
	selectedPath := ft.Selected()
	ft.items = nil
	ft.loadDir(ft.root, 0)
	// Re-expand in a single pass.
	for i := 0; i < len(ft.items); i++ {
		if ft.items[i].IsDir && expanded[ft.items[i].Path] {
			ft.expand(i)
		}
	}
	// Restore cursor.
	ft.cursor = 0
	for i, item := range ft.items {
		if item.Path == selectedPath {
			ft.cursor = i
			break
		}
	}
	ft.clampScroll()
}

func (ft *FileTree) clampScroll() {
	if ft.cursor < ft.offset {
		ft.offset = ft.cursor
	}
	if ft.height > 0 && ft.cursor >= ft.offset+ft.height {
		ft.offset = ft.cursor - ft.height + 1
	}
}

// SetHeight sets the visible height of the tree.
func (ft *FileTree) SetHeight(h int) {
	ft.height = h
}

// itemStatus returns the status icon string for a tree item.
func (ft *FileTree) itemStatus(item TreeItem) string {
	if item.IsDir {
		return ""
	}
	if _, ok := ft.manifest[item.Path]; ok {
		e := ft.manifest[item.Path]
		if isNonGamePath(item.Path) {
			return styleWarning.Render("⚠")
		}
		if e.IsFailed() {
			return styleError.Render("✗")
		}
		if e.IsVideo {
			return stylePeach.Render("▶")
		}
		return styleSuccess.Render("✓")
	}
	return styleMuted.Render("○")
}

// View renders the file tree for the given width.
func (ft *FileTree) View(width int) string {
	if ft.height == 0 {
		ft.height = 20
	}
	var lines []string

	end := ft.offset + ft.height
	if end > len(ft.items) {
		end = len(ft.items)
	}

	for i := ft.offset; i < end; i++ {
		item := ft.items[i]
		indent := strings.Repeat("  ", item.Depth)
		var icon string
		if item.IsDir {
			if item.Expanded {
				icon = styleLavender.Render("▾ ")
			} else {
				icon = styleMuted.Render("▸ ")
			}
		} else {
			icon = "  "
		}

		name := filepath.Base(item.Path)
		status := ft.itemStatus(item)

		// Game label for cataloged files
		gameLabel := ""
		if !item.IsDir {
			if e, ok := ft.manifest[item.Path]; ok && !e.IsFailed() && e.Game != "" {
				short := e.ShortGame(12)
				gameLabel = "  " + styleMuted.Render(short)
			}
		}

		// Directory: name in lavender, show count
		dirSuffix := ""
		if item.IsDir {
			name = styleFolderName.Render(name)
			count := ft.countMediaUnder(item.Path)
			if count > 0 {
				dirSuffix = styleMuted.Render(fmt.Sprintf(" (%d)", count))
			}
		}

		// Truncate name to fit
		availName := width - len(indent) - 2 - 3 - 14 // indent+icon+status+game
		if availName < 8 {
			availName = 8
		}
		runes := []rune(name)
		if len(runes) > availName {
			name = string(runes[:availName-1]) + "…"
		}

		line := indent + icon + name + dirSuffix + "  " + status + gameLabel
		if i == ft.cursor {
			// Pad to full width for selection highlight
			padded := fmt.Sprintf("%-*s", width-1, line)
			line = styleSelected.Render(padded)
		}
		lines = append(lines, line)
	}

	// Pad remaining lines
	for len(lines) < ft.height {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func (ft *FileTree) countMediaUnder(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if imageExts[ext] || videoExts[ext] {
				count++
			}
		}
	}
	return count
}

// DirSummary returns the count of total and uncataloged images under a dir.
func (ft *FileTree) DirSummary(dir string) (total, uncataloged int) {
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if imageExts[strings.ToLower(filepath.Ext(path))] {
			total++
			if _, ok := ft.manifest[path]; !ok {
				uncataloged++
			}
		}
		return nil
	})
	return
}

// colorForPath returns the lipgloss color for a tree item based on manifest status.
func colorForPath(path string, m Manifest) lipgloss.Color {
	e, ok := m[path]
	if !ok {
		return colorText
	}
	if isNonGamePath(path) {
		return colorWarning
	}
	if e.IsFailed() {
		return colorError
	}
	return colorSuccess
}
