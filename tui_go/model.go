package main

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type viewState int

const (
	stateMain viewState = iota
	stateLibrary
	stateStats
	stateOnThisDay
	stateDupes
	stateSettings
	stateHelp
	stateConfirm
)

// ConfirmAction describes what to do when a confirm prompt is accepted.
type ConfirmAction int

const (
	confirmOrganize ConfirmAction = iota
	confirmRename
	confirmOrphan
)

// Model is the root Bubbletea model.
type Model struct {
	// Config
	scanDir      string
	manifestPath string
	settings     Settings

	// Data
	manifest   Manifest
	statsCache *StatsCache

	// UI state
	state        viewState
	tree         FileTree
	logLines     []string
	logOffset    int
	logHeight    int
	focusedPane  int // 0=tree, 1=log
	width        int
	height       int

	// Queue
	queue map[string]bool

	// Busy state
	busy            bool
	busyMsg         string
	progressN       int
	progressMax     int
	catalogStart    time.Time
	etaStr          string
	cancelFn        context.CancelFunc
	scanner         *bufio.Scanner
	subproc         *exec.Cmd

	// Confirm
	confirmMsg    string
	confirmAction ConfirmAction
	confirmPrev   viewState
	confirmMoves  [][2]string

	// Sub-models
	library    LibraryModel
	stats      StatsModel
	onThisDay  OnThisDayModel
	dupeGroups [][]string
	dupeMarked map[string]bool
	dupeCursor int
	dupeOffset int
	settings_m  SettingsModel
}

func NewModel(scanDir, manifestPath string) Model {
	settings := LoadSettings(manifestPath)
	manifest, _ := LoadManifest(manifestPath)
	tree := NewFileTree(scanDir, manifest)
	return Model{
		scanDir:      scanDir,
		manifestPath: manifestPath,
		settings:     settings,
		manifest:     manifest,
		statsCache:   BuildStatsCache(manifest),
		tree:         tree,
		queue:        map[string]bool{},
		dupeMarked:   map[string]bool{},
	}
}

// ── Init ─────────────────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return nil
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.tree.SetHeight(m.height - 4)
		m.logHeight = m.height - 4
		return m, nil

	case CatalogProgressMsg:
		line := m.formatProgressLine(NdjsonMsg(msg))
		m.appendLog(line)
		if msg.Type == "start" {
			m.progressMax = msg.Total
			m.progressN = 0
			m.catalogStart = time.Now()
			m.etaStr = ""
		} else if msg.Type == "progress" {
			m.progressN++
			if m.progressN > 0 && m.progressMax > 0 {
				elapsed := time.Since(m.catalogStart)
				if elapsed > 0 {
					perItem := elapsed / time.Duration(m.progressN)
					remaining := perItem * time.Duration(m.progressMax-m.progressN)
					if remaining > 0 {
						m.etaStr = fmt.Sprintf(" ETA %s", formatDuration(remaining))
					}
				}
			}
		}
		if m.scanner != nil {
			return m, ReadNextMsg(m.scanner, m.subproc)
		}
		return m, nil

	case CatalogDoneMsg:
		m.busy = false
		m.busyMsg = ""
		m.cancelFn = nil
		m.scanner = nil
		m.subproc = nil
		m.tree.SetManifest(m.manifest)
		m.tree.Refresh()
		// Reload manifest from disk (Python wrote to it)
		freshManifest, err := LoadManifest(m.manifestPath)
		if err == nil {
			m.manifest = freshManifest
			m.tree.SetManifest(m.manifest)
			m.statsCache = BuildStatsCache(m.manifest)
		}
		if msg.Err != nil && msg.Err.Error() != "" && !strings.Contains(msg.Err.Error(), "killed") {
			m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", msg.Err)))
		} else {
			m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Done: %d cataloged, %d failed", msg.Success, msg.Fail)))
		}
		return m, nil

	case DupeGroupMsg:
		m.dupeGroups = append(m.dupeGroups, msg.Files)
		if m.scanner != nil {
			return m, ReadNextMsg(m.scanner, m.subproc)
		}
		return m, nil

	case DupeDoneMsg:
		m.busy = false
		m.busyMsg = ""
		m.cancelFn = nil
		m.scanner = nil
		m.subproc = nil
		m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Found %d duplicate group(s)", msg.Groups)))
		if len(m.dupeGroups) > 0 {
			m.state = stateDupes
			m.dupeCursor = 0
		}
		return m, nil

	case SettingsMsg:
		if msg.Saved {
			m.settings = msg.Settings
			SaveSettings(m.settings, m.manifestPath)
			m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Settings saved (model: %s)", m.settings.Model)))
		}
		m.state = stateMain
		return m, nil

	case LibraryMsg:
		m.state = stateMain
		return m, nil

	case StatsMsg:
		m.state = stateMain
		return m, nil

	case OnThisDayMsg:
		m.state = stateMain
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Delegate to sub-models
	return m.delegateToSubmodel(msg)
}

func (m Model) delegateToSubmodel(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateLibrary:
		newLib, cmd := m.library.Update(msg)
		m.library = newLib
		return m, cmd
	case stateStats:
		newStats, cmd := m.stats.Update(msg)
		m.stats = newStats
		return m, cmd
	case stateOnThisDay:
		newOtd, cmd := m.onThisDay.Update(msg)
		m.onThisDay = newOtd
		return m, cmd
	case stateSettings:
		newSettings, cmd := m.settings_m.Update(msg)
		m.settings_m = newSettings
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Sub-model key handling
	if m.state != stateMain && m.state != stateConfirm && m.state != stateHelp {
		return m.delegateToSubmodel(msg)
	}

	// Confirm prompt
	if m.state == stateConfirm {
		switch key {
		case "y", "enter":
			return m.executeConfirm()
		case "n", "esc":
			m.state = m.confirmPrev
			m.appendLog(styleMuted.Render("  Cancelled."))
		}
		return m, nil
	}

	// Help overlay
	if m.state == stateHelp {
		m.state = stateMain
		return m, nil
	}

	// Cancel running operation
	if key == "esc" && m.busy {
		if m.cancelFn != nil {
			m.cancelFn()
			m.appendLog(styleWarning.Render("⏹  Cancelling…"))
		}
		return m, nil
	}

	// Global shortcuts (available when not busy)
	switch key {
	case "ctrl+c", "q":
		if !m.busy {
			return m, tea.Quit
		}
	case "?":
		m.state = stateHelp
		return m, nil
	case "l":
		m.library = NewLibraryModel(m.manifest, m.width, m.height)
		m.state = stateLibrary
		return m, nil
	case "p":
		m.stats = NewStatsModel(m.manifest, m.statsCache, m.width, m.height)
		m.state = stateStats
		return m, nil
	case "t":
		m.onThisDay = NewOnThisDayModel(m.manifest, m.width, m.height)
		m.state = stateOnThisDay
		return m, nil
	case "S":
		m.settings_m = NewSettingsModel(m.settings, m.width, m.height)
		m.state = stateSettings
		return m, nil
	}

	// Pane switching
	switch key {
	case "left", "h":
		m.focusedPane = 0
		return m, nil
	case "right", "l":
		if !m.busy {
			// Only 'l' for library is already handled above via global shortcuts
			// but here 'right' just switches pane
			if key == "right" {
				m.focusedPane = 1
			}
		}
		return m, nil
	}

	// Tree navigation (when left pane focused or no pane concept for up/down)
	if m.focusedPane == 0 {
		switch key {
		case "up", "k":
			m.tree.MoveUp()
			return m, nil
		case "down", "j":
			m.tree.MoveDown()
			return m, nil
		case "enter":
			item := m.tree.SelectedItem()
			if item != nil && item.IsDir {
				m.tree.Toggle()
			}
			return m, nil
		case " ":
			m.toggleQueue()
			return m, nil
		}
	} else {
		// Log pane scroll
		switch key {
		case "up", "k":
			if m.logOffset > 0 {
				m.logOffset--
			}
			return m, nil
		case "down", "j":
			maxOff := len(m.logLines) - m.logHeight
			if maxOff < 0 {
				maxOff = 0
			}
			if m.logOffset < maxOff {
				m.logOffset++
			}
			return m, nil
		}
	}

	// Dupes screen
	if m.state == stateDupes {
		switch key {
		case "esc":
			m.state = stateMain
		case "up", "k":
			if m.dupeCursor > 0 {
				m.dupeCursor--
			}
		case "down", "j":
			flatLen := 0
			for _, g := range m.dupeGroups {
				flatLen += len(g)
			}
			if m.dupeCursor < flatLen-1 {
				m.dupeCursor++
			}
		case "x", " ":
			fp := m.dupeFilePath(m.dupeCursor)
			if fp != "" {
				if m.dupeMarked[fp] {
					delete(m.dupeMarked, fp)
				} else {
					m.dupeMarked[fp] = true
				}
			}
		case "d":
			// TODO: confirm + delete marked
			m.appendLog(styleWarning.Render(fmt.Sprintf("  Would delete %d marked files", len(m.dupeMarked))))
		}
		return m, nil
	}

	// Action keys (require not busy)
	if m.busy {
		return m, nil
	}

	switch key {
	case "c":
		return m.startCatalog(false)
	case "C":
		return m.startCatalog(true)
	case "r":
		return m.doRename()
	case "o":
		return m.doOrganize()
	case "v":
		return m.doVideo()
	case "R":
		return m.doRecatalogSelected()
	case "x":
		return m.doRecatalogFailed()
	case "u", "ctrl+z":
		return m.doUndo()
	case "d":
		return m.doFindDupes()
	case "e":
		return m.doOrphanCheck()
	case "E":
		return m.doExportCSV()
	case "O":
		m.revealInExplorer()
	case "f":
		m.openInViewer()
	}

	return m, nil
}

// ── Actions ───────────────────────────────────────────────────────────────────

func (m *Model) toggleQueue() {
	sel := m.tree.Selected()
	if sel == "" {
		return
	}
	if m.queue[sel] {
		delete(m.queue, sel)
		m.appendLog(styleMuted.Render(fmt.Sprintf("  Removed from queue: %s", filepath.Base(sel))))
	} else {
		m.queue[sel] = true
		m.appendLog(styleWarning.Render(fmt.Sprintf("  Queued: %s", filepath.Base(sel))))
	}
}

func (m Model) startCatalog(allFiles bool) (tea.Model, tea.Cmd) {
	// Collect target directories
	var dirs []string
	if len(m.queue) > 0 {
		for p := range m.queue {
			dirs = append(dirs, p)
		}
	} else if sel := m.tree.Selected(); sel != "" {
		item := m.tree.SelectedItem()
		if item != nil && item.IsDir {
			dirs = append(dirs, sel)
		} else {
			dirs = append(dirs, filepath.Dir(sel))
		}
	} else {
		dirs = append(dirs, m.scanDir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	// When multiple dirs are queued, collect files explicitly in Go so Python
	// doesn't have to scan the entire tree.
	var explicitFiles []string
	var scanDir string
	if len(dirs) > 1 {
		scanDir = m.scanDir
		for _, d := range dirs {
			files, _ := ScanImages(d)
			for _, f := range files {
				if allFiles || m.manifest[f] == nil {
					explicitFiles = append(explicitFiles, f)
				}
			}
		}
		if len(explicitFiles) == 0 {
			cancel()
			m.appendLog(styleSuccess.Render("  Nothing new to catalog in queued folders."))
			return m, nil
		}
	} else {
		scanDir = dirs[0]
	}

	scanner, cmd, err := LaunchCatalogProcess(ctx, scanDir, m.manifestPath, allFiles, explicitFiles)
	if err != nil {
		cancel()
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  Failed to start: %v", err)))
		return m, nil
	}
	m.scanner = scanner
	m.subproc = cmd
	m.busy = true
	m.busyMsg = "Cataloging"
	m.progressN = 0
	m.progressMax = 0

	verb := "new"
	if allFiles {
		verb = "all"
	}
	queueNote := ""
	if len(dirs) > 1 {
		queueNote = fmt.Sprintf(" across %d queued folders (%d images)", len(dirs), len(explicitFiles))
	}
	m.appendLog(styleWarning.Render(fmt.Sprintf("  Cataloging %s%s…", verb, queueNote)))

	return m, ReadNextMsg(m.scanner, m.subproc)
}

func (m Model) doRename() (tea.Model, tea.Cmd) {
	_, renames, err := DoRename(m.manifest, true) // dry run for preview
	if err != nil {
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", err)))
		return m, nil
	}
	if len(renames) == 0 {
		m.appendLog(styleSuccess.Render("  All files already have descriptive names."))
		return m, nil
	}
	m.confirmMoves = renames
	m.confirmMsg = fmt.Sprintf("Rename %d file(s)? [y/n]", len(renames))
	m.confirmAction = confirmRename
	m.confirmPrev = stateMain
	m.state = stateConfirm
	// Show preview in log
	for i, mv := range renames {
		if i >= 8 {
			m.appendLog(styleMuted.Render(fmt.Sprintf("  … and %d more", len(renames)-8)))
			break
		}
		m.appendLog(styleMuted.Render(fmt.Sprintf("  %s  →  %s",
			filepath.Base(mv[0]), filepath.Base(mv[1]))))
	}
	return m, nil
}

func (m Model) doOrganize() (tea.Model, tea.Cmd) {
	base := m.scanDir
	if sel := m.tree.Selected(); sel != "" {
		if item := m.tree.SelectedItem(); item != nil && item.IsDir {
			base = sel
		}
	}
	moves, err := DoOrganize(m.manifest, base, true) // dry run
	if err != nil {
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", err)))
		return m, nil
	}
	if len(moves) == 0 {
		m.appendLog(styleSuccess.Render("  Everything is already organized."))
		return m, nil
	}
	m.confirmMoves = moves
	m.confirmMsg = fmt.Sprintf("Move %d file(s) into game/year folders? [y/n]", len(moves))
	m.confirmAction = confirmOrganize
	m.confirmPrev = stateMain
	m.state = stateConfirm
	for i, mv := range moves {
		if i >= 8 {
			m.appendLog(styleMuted.Render(fmt.Sprintf("  … and %d more", len(moves)-8)))
			break
		}
		rel, _ := filepath.Rel(base, mv[1])
		m.appendLog(styleMuted.Render(fmt.Sprintf("  %s  →  %s", filepath.Base(mv[0]), rel)))
	}
	return m, nil
}

func (m Model) executeConfirm() (tea.Model, tea.Cmd) {
	m.state = stateMain
	switch m.confirmAction {
	case confirmRename:
		count, _, err := DoRename(m.manifest, false)
		if err != nil {
			m.appendLog(styleError.Render(fmt.Sprintf("⚠  Rename failed: %v", err)))
		} else {
			SaveManifest(m.manifest, m.manifestPath)
			m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Renamed %d files", count)))
			m.tree.Refresh()
		}
	case confirmOrganize:
		base := m.scanDir
		if sel := m.tree.Selected(); sel != "" {
			if item := m.tree.SelectedItem(); item != nil && item.IsDir {
				base = sel
			}
		}
		moves, err := DoOrganize(m.manifest, base, false)
		if err != nil {
			m.appendLog(styleError.Render(fmt.Sprintf("⚠  Organize failed: %v", err)))
		} else {
			SaveManifest(m.manifest, m.manifestPath)
			m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Organized %d files", len(moves))))
			m.tree.Refresh()
		}
	case confirmOrphan:
		orphans := OrphanPaths(m.manifest)
		for _, p := range orphans {
			delete(m.manifest, p)
		}
		SaveManifest(m.manifest, m.manifestPath)
		m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Removed %d orphaned entries", len(orphans))))
	}
	return m, nil
}

func (m Model) doVideo() (tea.Model, tea.Cmd) {
	sel := m.tree.Selected()
	if sel == "" {
		m.appendLog(styleWarning.Render("  Select a video file first."))
		return m, nil
	}
	if !videoExts[strings.ToLower(filepath.Ext(sel))] {
		m.appendLog(styleWarning.Render("  Selected file is not a recognised video format."))
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	scanner, cmd, err := LaunchVideoProcess(ctx, sel, m.manifestPath,
		"auto", m.settings.SceneThreshold, m.settings)
	if err != nil {
		cancel()
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", err)))
		return m, nil
	}
	m.scanner = scanner
	m.subproc = cmd
	m.busy = true
	m.busyMsg = "Extracting video"
	m.appendLog(styleSuccess.Render(fmt.Sprintf("  Extracting frames from %s…", filepath.Base(sel))))
	return m, ReadNextMsg(m.scanner, m.subproc)
}

func (m Model) doRecatalogSelected() (tea.Model, tea.Cmd) {
	sel := m.tree.Selected()
	if sel == "" {
		return m, nil
	}
	delete(m.manifest, sel)
	m.appendLog(styleMuted.Render(fmt.Sprintf("  Re-cataloging %s…", filepath.Base(sel))))

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	scanner, cmd, err := LaunchCatalogProcess(ctx, filepath.Dir(sel), m.manifestPath, false, []string{sel})
	if err != nil {
		cancel()
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", err)))
		return m, nil
	}
	m.scanner = scanner
	m.subproc = cmd
	m.busy = true
	m.busyMsg = "Re-cataloging"
	return m, ReadNextMsg(m.scanner, m.subproc)
}

func (m Model) doRecatalogFailed() (tea.Model, tea.Cmd) {
	failed := FailedPaths(m.manifest)
	if len(failed) == 0 {
		m.appendLog(styleSuccess.Render("  No failed entries to re-catalog."))
		return m, nil
	}
	for _, fp := range failed {
		delete(m.manifest, fp)
	}
	m.appendLog(styleWarning.Render(fmt.Sprintf("  Re-cataloging %d failed entries…", len(failed))))

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	scanner, cmd, err := LaunchCatalogProcess(ctx, m.scanDir, m.manifestPath, false, failed)
	if err != nil {
		cancel()
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", err)))
		return m, nil
	}
	m.scanner = scanner
	m.subproc = cmd
	m.busy = true
	m.busyMsg = "Re-cataloging failures"
	return m, ReadNextMsg(m.scanner, m.subproc)
}

func (m Model) doUndo() (tea.Model, tea.Cmd) {
	msg, err := UndoLast(m.manifest, m.manifestPath)
	if err != nil {
		m.appendLog(styleWarning.Render(fmt.Sprintf("  %v", err)))
	} else {
		m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  %s", msg)))
		m.tree.Refresh()
	}
	return m, nil
}

func (m Model) doFindDupes() (tea.Model, tea.Cmd) {
	m.dupeGroups = nil
	m.dupeMarked = map[string]bool{}
	m.dupeCursor = 0

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	scanner, cmd, err := LaunchDupesProcess(ctx, m.manifestPath, 8)
	if err != nil {
		cancel()
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  %v", err)))
		return m, nil
	}
	m.scanner = scanner
	m.subproc = cmd
	m.busy = true
	m.busyMsg = "Finding duplicates"
	m.appendLog(styleMuted.Render("  Computing perceptual hashes…"))
	return m, ReadNextMsg(m.scanner, m.subproc)
}

func (m Model) doOrphanCheck() (tea.Model, tea.Cmd) {
	orphans := OrphanPaths(m.manifest)
	if len(orphans) == 0 {
		m.appendLog(styleSuccess.Render("  No orphaned entries (all files accounted for)."))
		return m, nil
	}
	m.appendLog(styleWarning.Render(fmt.Sprintf("  %d orphaned entries found:", len(orphans))))
	for i, p := range orphans {
		if i >= 6 {
			m.appendLog(styleMuted.Render(fmt.Sprintf("  … and %d more", len(orphans)-6)))
			break
		}
		m.appendLog(styleMuted.Render(fmt.Sprintf("    %s", filepath.Base(p))))
	}
	m.confirmMsg = fmt.Sprintf("Remove %d orphaned entries from manifest? [y/n]", len(orphans))
	m.confirmAction = confirmOrphan
	m.confirmPrev = stateMain
	m.state = stateConfirm
	return m, nil
}

func (m Model) doExportCSV() (tea.Model, tea.Cmd) {
	out := filepath.Join(filepath.Dir(m.manifestPath), "manifest_export.csv")
	n, err := ExportCSV(m.manifest, out)
	if err != nil {
		m.appendLog(styleError.Render(fmt.Sprintf("⚠  Export failed: %v", err)))
		return m, nil
	}
	m.appendLog(styleSuccess.Render(fmt.Sprintf("✓  Exported %d entries → %s", n, filepath.Base(out))))
	exec.Command("explorer", "/select,", out).Start()
	return m, nil
}

func (m *Model) revealInExplorer() {
	sel := m.tree.Selected()
	if sel != "" {
		exec.Command("explorer", "/select,", sel).Start()
	}
}

func (m *Model) openInViewer() {
	sel := m.tree.Selected()
	if sel != "" {
		exec.Command("cmd", "/c", "start", "", sel).Start()
	}
}

func (m *Model) appendLog(line string) {
	m.logLines = append(m.logLines, line)
	// Auto-scroll to bottom
	max := len(m.logLines) - m.logHeight
	if max < 0 {
		max = 0
	}
	m.logOffset = max
}

func (m *Model) formatProgressLine(msg NdjsonMsg) string {
	ts := styleMuted.Render(time.Now().Format("15:04:05") + "  ")
	switch msg.Type {
	case "start":
		return ts + styleInfo.Render(fmt.Sprintf("⬡ Starting: %d image(s)", msg.Total))
	case "progress":
		if msg.Status == "error" || msg.Status == "failed" {
			return ts + styleError.Render(fmt.Sprintf("  ✗ %s  %s", msg.Name, msg.Error))
		}
		gameInfo := styleLavender.Render(msg.Game)
		if msg.Scene != "" {
			gameInfo += styleMuted.Render(" · "+truncate(msg.Scene, 28))
		}
		return ts + styleSuccess.Render("  ✓ ") + stylePeach.Render(msg.Name) + styleMuted.Render("  →  ") + gameInfo
	case "extract":
		return ts + styleInfo.Render("  ⬡ "+msg.Msg)
	case "summary":
		return ts + styleMuted.Render("  ⬡ "+msg.Msg)
	case "transcript":
		base := filepath.Base(msg.File)
		filtered := ""
		if msg.Filtered > 0 {
			filtered = fmt.Sprintf("  (%d filtered)", msg.Filtered)
		}
		return ts + styleGold.Render("  ◉ transcript written → "+base) + styleMuted.Render(filtered)
	case "done":
		return ts + styleSuccess.Render(fmt.Sprintf("  ✓ Done: %d ok", msg.Success)) +
			styleMuted.Render(fmt.Sprintf(", %d failed", msg.Fail))
	}
	return ts + styleMuted.Render("  "+msg.Msg)
}

func (m *Model) dupeFilePath(cursor int) string {
	i := 0
	for _, group := range m.dupeGroups {
		for _, fp := range group {
			if i == cursor {
				return fp
			}
			i++
		}
	}
	return ""
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}

	switch m.state {
	case stateLibrary:
		return m.library.View()
	case stateStats:
		return m.stats.View()
	case stateOnThisDay:
		return m.onThisDay.View()
	case stateSettings:
		return m.settings_m.View()
	case stateHelp:
		return m.helpView()
	case stateDupes:
		return m.dupesView()
	}

	return m.mainView()
}

func (m Model) mainView() string {
	treeW := m.width * 35 / 100
	rightW := m.width - treeW - 1

	treeH := m.height - 3
	m.tree.SetHeight(treeH)
	m.logHeight = treeH - 5

	// ── Left pane: tree ──
	treeContent := m.tree.View(treeW)
	borderColor := colorPeachDim
	if m.focusedPane == 0 {
		borderColor = colorPeach
	}
	leftPane := lipgloss.NewStyle().
		Width(treeW).Height(treeH).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Background(colorBase).
		Render(treeContent)

	// ── Right pane: detail + log ──
	detail := m.detailView(rightW - 2)
	dividerLine := styleMuted.Render(strings.Repeat("─", rightW-2))
	logContent := m.logView(rightW-2, m.logHeight)
	rightPane := lipgloss.NewStyle().
		Width(rightW-2).
		Background(colorBase).
		Render(detail + "\n" + dividerLine + "\n" + logContent)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	// ── Header ──
	heart := styleRose.Render(" ♥ ")
	titleStr := lipgloss.NewStyle().Background(colorBase).Foreground(colorPeach).Bold(true).
		Render(" ✧ Screenshot Cataloger ✧")
	timeStr := styleMuted.Render(time.Now().Format("15:04:05") + " ")
	manifestStr := styleMuted.Render(filepath.Base(m.manifestPath))
	spacer := strings.Repeat(" ", max(0, m.width-lipgloss.Width(titleStr)-lipgloss.Width(heart)-
		lipgloss.Width(timeStr)-lipgloss.Width(manifestStr)-2))
	header := lipgloss.NewStyle().Background(colorBase).
		Render(titleStr + heart + manifestStr + spacer + timeStr)

	return header + "\n" + body + "\n" + m.statusBarView()
}

func (m Model) detailView(width int) string {
	sel := m.tree.Selected()
	if sel == "" {
		// Idle splash
		lines := []string{
			"",
			"         " + styleGold.Render("✦ ✦ ✦"),
			"    " + styleMuted.Render("drop a folder to begin"),
			"      " + styleMuted.Render("or press ") + stylePeach.Render("c") + styleMuted.Render(" to catalog"),
			"         " + styleGold.Render("✦ ✦ ✦"),
			"",
		}
		return strings.Join(lines, "\n")
	}
	item := m.tree.SelectedItem()
	if item == nil {
		return ""
	}

	if item.IsDir {
		total, uncataloged := m.tree.DirSummary(sel)
		queuedTag := ""
		if m.queue[sel] {
			queuedTag = "  " + styleWarning.Render("⬡ queued")
		}
		queueCount := ""
		if len(m.queue) > 0 {
			queueCount = "  " + styleInfo.Render(fmt.Sprintf("queue: %d", len(m.queue)))
		}
		return fmt.Sprintf("  %s\n  %s%s%s",
			styleFolderName.Render("📁 "+filepath.Base(sel)),
			styleMuted.Render(fmt.Sprintf("%d total, %d uncataloged", total, uncataloged)),
			queueCount,
			queuedTag,
		)
	}

	e, ok := m.manifest[sel]
	if !ok {
		return fmt.Sprintf("  %s\n  %s",
			stylePeach.Render(filepath.Base(sel)),
			styleMuted.Render("○  not yet cataloged"),
		)
	}
	if e.IsFailed() {
		return fmt.Sprintf("  %s\n  %s",
			styleError.Render(filepath.Base(sel)),
			styleError.Render("✗  catalog failed"),
		)
	}
	if isNonGamePath(sel) {
		return fmt.Sprintf("  %s\n  %s",
			styleWarning.Render(filepath.Base(sel)),
			styleWarning.Render("⚠  non-game capture"),
		)
	}
	game := e.Game
	if game == "" {
		game = "Unknown"
	}
	scene := truncate(e.Scene, 42)
	date := ""
	if len(e.CatalogedAt) >= 10 {
		date = e.CatalogedAt[:10]
	}
	if e.IsVideo {
		transcriptLine := ""
		if e.TranscriptFile != "" {
			transcriptLine = "\n  " + styleMuted.Render("◉ "+filepath.Base(e.TranscriptFile))
		}
		return fmt.Sprintf("  %s\n  %s  %s\n  %s%s",
			stylePeach.Render("▶  "+filepath.Base(sel)),
			styleLavender.Bold(true).Render(game),
			styleRose.Italic(true).Render(truncate(e.Mood, 20)),
			styleMuted.Render(scene+"  "+date),
			transcriptLine,
		)
	}
	return fmt.Sprintf("  %s\n  %s  %s\n  %s",
		styleSuccess.Render("✓  "+filepath.Base(sel)),
		styleLavender.Bold(true).Render(game),
		styleRose.Italic(true).Render(truncate(e.Mood, 20)),
		styleMuted.Render(scene+"  "+date),
	)
}

func (m Model) logView(width, height int) string {
	if height <= 0 {
		height = 5
	}
	start := m.logOffset
	end := start + height
	if end > len(m.logLines) {
		end = len(m.logLines)
	}
	if start > len(m.logLines) {
		start = len(m.logLines)
	}
	lines := m.logLines[start:end]

	// Progress bar line at top if busy
	var extra []string
	if m.busy && m.progressMax > 0 {
		barStr := ProgressBar(m.progressN, m.progressMax, 24)
		pctStr := stylePeach.Bold(true).Render(fmt.Sprintf("%3d%%", m.progressN*100/m.progressMax))
		extra = append(extra, fmt.Sprintf("  %s  %s  %s %d/%d",
			barStr, pctStr, styleMuted.Render(m.busyMsg), m.progressN, m.progressMax))
	} else if m.busy {
		extra = append(extra, styleMuted.Render(fmt.Sprintf("  %s…", m.busyMsg)))
	}

	all := append(extra, lines...)
	// Pad to height
	for len(all) < height {
		all = append(all, "")
	}
	return strings.Join(all, "\n")
}

func (m Model) statusBarView() string {
	if m.busy {
		progress := ""
		if m.progressMax > 0 {
			progress = fmt.Sprintf(" %d/%d", m.progressN, m.progressMax)
		}
		eta := ""
		if m.etaStr != "" {
			eta = styleInfo.Render(m.etaStr)
		}
		left := styleWarning.Render("  "+m.busyMsg+progress) + eta
		right := keyHint("ESC", "cancel") + "  "
		spacer := strings.Repeat(" ", max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
		return styleStatusBar.Render(left + spacer + right)
	}

	if m.state == stateConfirm {
		return styleStatusBar.Render(
			"  " + styleWarning.Render(m.confirmMsg) +
				"   " + keyHint("y/Enter", "yes") + "  " + keyHint("n/ESC", "no"),
		)
	}

	sel := m.tree.Selected()
	isCataloged := sel != "" && m.manifest[sel] != nil
	isVideo := sel != "" && videoExts[strings.ToLower(filepath.Ext(sel))]

	parts := []string{
		keyHint("c", "catalog"),
		keyHint("C", "all"),
		keyHint("r", "rename"),
		keyHint("o", "org"),
	}
	if isVideo {
		parts = append(parts, keyHint("v", "video"))
	}
	if isCataloged {
		parts = append(parts, keyHint("R", "re-catalog"))
	}
	queueLabel := "queue"
	if len(m.queue) > 0 {
		queueLabel = styleWarning.Render(fmt.Sprintf("queue(%d)", len(m.queue)))
	} else {
		queueLabel = keyHint("space", "queue")
	}
	parts = append(parts,
		queueLabel,
		keyHint("l", "lib"),
		keyHint("p", "stats"),
		styleGold.Render("t")+ styleKeyLabel.Render(":✧today"),
		keyHint("?", "help"),
		keyHint("q", "quit"),
	)
	return styleStatusBar.Render("  " + strings.Join(parts, "  "))
}

func (m Model) helpView() string {
	lines := []string{
		styleTitle.Render("  Keyboard Shortcuts") + styleMuted.Render("  (any key to close)"),
		"",
		styleBold.Render("  Navigation"),
		"  " + keyHint("↑↓ / k j", "navigate tree") + "   " + keyHint("←→ / h", "switch pane"),
		"  " + keyHint("Enter", "expand/collapse dir"),
		"",
		styleBold.Render("  Catalog"),
		"  " + keyHint("c", "catalog new files in selection"),
		"  " + keyHint("C", "re-catalog all files"),
		"  " + keyHint("R", "re-catalog selected file"),
		"  " + keyHint("x", "re-catalog all failures"),
		"  " + keyHint("ESC", "cancel running operation"),
		"",
		styleBold.Render("  File Operations"),
		"  " + keyHint("r", "rename files") + "   " + keyHint("o", "organize by game/year"),
		"  " + keyHint("u / ctrl+z", "undo") + "   " + keyHint("e", "orphan check"),
		"  " + keyHint("E", "export CSV") + "   " + keyHint("v", "extract video frames"),
		"  " + keyHint("d", "find duplicates"),
		"",
		styleBold.Render("  Views"),
		"  " + keyHint("l", "library search") + "   " + keyHint("p", "stats dashboard"),
		"  " + keyHint("t", "on this day") + "   " + keyHint("S", "settings"),
		"",
		styleBold.Render("  File"),
		"  " + keyHint("space", "toggle queue") + "   " + keyHint("f", "open in viewer"),
		"  " + keyHint("O", "reveal in explorer"),
		"",
		"  " + keyHint("q / ctrl+c", "quit"),
	}
	return strings.Join(lines, "\n")
}

func (m Model) dupesView() string {
	title := styleTitle.Render("  Duplicate Groups")
	sub := styleMuted.Render(fmt.Sprintf("  %d groups found", len(m.dupeGroups)))
	divider := styleMuted.Render(strings.Repeat("─", m.width))

	lines := []string{title + "  " + sub, divider}

	cursor := 0
	for gi, group := range m.dupeGroups {
		lines = append(lines, styleBold.Render(fmt.Sprintf("  Group %d — %d similar files", gi+1, len(group))))
		for _, fp := range group {
			mark := "  "
			if m.dupeMarked[fp] {
				mark = styleError.Render("✗ ")
			}
			line := "    " + mark + filepath.Base(fp)
			if cursor == m.dupeCursor {
				line = styleSelected.Render(fmt.Sprintf("%-*s", m.width-1, strings.TrimPrefix(line, "  ")))
			}
			lines = append(lines, line)
			cursor++
		}
		lines = append(lines, "")
	}

	marked := len(m.dupeMarked)
	statusBar := styleStatusBar.Render(
		keyHint("↑↓", "navigate") + "  " +
			keyHint("x/space", "mark") + "  " +
			keyHint("d", fmt.Sprintf("delete(%d)", marked)) + "  " +
			keyHint("ESC", "back"),
	)
	return strings.Join(lines, "\n") + "\n" + statusBar
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m >= 60 {
		h := m / 60
		m = m % 60
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
