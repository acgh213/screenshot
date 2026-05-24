package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// LibraryMsg signals to return from the library view.
type LibraryMsg struct{}

// LibraryModel is the full-screen searchable library.
type LibraryModel struct {
	manifest    Manifest
	rows        []libraryRow
	filtered    []libraryRow
	search      textinput.Model
	cursor      int
	offset      int
	height      int
	width       int
	sessionMode bool
}

type libraryRow struct {
	entry   *Entry
	session int // 0 if not computed
	path    string
}

func NewLibraryModel(m Manifest, width, height int) LibraryModel {
	search := textinput.New()
	search.Placeholder = "search game, scene, keyword…"
	search.Width = width - 20
	search.Focus()

	lm := LibraryModel{
		manifest: m,
		search:   search,
		width:    width,
		height:   height - 4, // reserve header + status bar
	}
	lm.buildRows()
	lm.applyFilter("")
	return lm
}

func (lm *LibraryModel) buildRows() {
	lm.rows = nil
	for path, e := range lm.manifest {
		lm.rows = append(lm.rows, libraryRow{entry: e, path: path})
	}
	sort.Slice(lm.rows, func(i, j int) bool {
		return lm.rows[i].entry.CatalogedAt < lm.rows[j].entry.CatalogedAt
	})
}

func (lm *LibraryModel) applyFilter(q string) {
	lm.filtered = nil
	q = strings.ToLower(q)
	for _, row := range lm.rows {
		if q == "" {
			lm.filtered = append(lm.filtered, row)
			continue
		}
		haystack := strings.ToLower(row.entry.Game + " " + row.entry.Scene +
			" " + row.entry.Location + " " + row.path +
			" " + strings.Join(row.entry.Keywords, " "))
		if strings.Contains(haystack, q) {
			lm.filtered = append(lm.filtered, row)
		}
	}
	lm.cursor = 0
	lm.offset = 0
}

func (lm LibraryModel) Init() tea.Cmd { return nil }

func (lm LibraryModel) Update(msg tea.Msg) (LibraryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return lm, func() tea.Msg { return LibraryMsg{} }
		case "up":
			if lm.cursor > 0 {
				lm.cursor--
				if lm.cursor < lm.offset {
					lm.offset = lm.cursor
				}
			}
		case "down":
			if lm.cursor < len(lm.filtered)-1 {
				lm.cursor++
				if lm.cursor >= lm.offset+lm.height {
					lm.offset = lm.cursor - lm.height + 1
				}
			}
		case "s":
			lm.sessionMode = !lm.sessionMode
			if lm.sessionMode {
				lm.computeSessions()
			}
		}
	}
	var cmd tea.Cmd
	prev := lm.search.Value()
	lm.search, cmd = lm.search.Update(msg)
	if lm.search.Value() != prev {
		lm.applyFilter(lm.search.Value())
	}
	return lm, cmd
}

func (lm *LibraryModel) computeSessions() {
	type timed struct {
		idx  int
		mtime time.Time
	}
	var items []timed
	for i, row := range lm.rows {
		// Use cataloged_at as proxy
		t, err := time.Parse("2006-01-02T15:04:05", row.entry.CatalogedAt)
		if err != nil {
			continue
		}
		items = append(items, timed{i, t})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })

	sessionNum := 0
	var prevTime time.Time
	sessions := make(map[int]int)
	for _, item := range items {
		if prevTime.IsZero() || item.mtime.Sub(prevTime) > 30*time.Minute {
			sessionNum++
		}
		sessions[item.idx] = sessionNum
		prevTime = item.mtime
	}
	for i := range lm.rows {
		lm.rows[i].session = sessions[i]
	}
}

func (lm LibraryModel) View() string {
	// Header line
	searchLine := "  🔍 " + lm.search.View()
	countLabel := styleMuted.Render(fmt.Sprintf("%d / %d", len(lm.filtered), len(lm.rows)))
	sessionToggle := styleMuted.Render("s:sessions")
	if lm.sessionMode {
		sessionToggle = styleSuccess.Render("s:sessions[on]")
	}
	header := searchLine + strings.Repeat(" ", 4) + countLabel + "  " + sessionToggle
	divider := styleMuted.Render(strings.Repeat("─", lm.width))

	// Column headers
	var colHdr string
	if lm.sessionMode {
		colHdr = styleBold.Render(fmt.Sprintf("  %-4s %-24s %-22s %-18s %-10s %s",
			"#", "Game", "Scene", "Location", "Date", "File"))
	} else {
		colHdr = styleBold.Render(fmt.Sprintf("  %-24s %-22s %-18s %-10s %s",
			"Game", "Scene", "Location", "Date", "File"))
	}

	lines := []string{header, divider, colHdr, divider}

	end := lm.offset + lm.height
	if end > len(lm.filtered) {
		end = len(lm.filtered)
	}
	for i := lm.offset; i < end; i++ {
		row := lm.filtered[i]
		e := row.entry
		status := styleSuccess.Render("✓")
		if e.IsFailed() {
			status = styleError.Render("✗")
		} else if isNonGamePath(row.path) {
			status = styleWarning.Render("⚠")
		}

		game := truncate(e.Game, 24)
		scene := truncate(e.Scene, 22)
		loc := truncate(e.Location, 18)
		date := ""
		if len(e.CatalogedAt) >= 10 {
			date = e.CatalogedAt[:10]
		}
		name := filepath.Base(row.path)

		var line string
		if lm.sessionMode {
			sess := ""
			if row.session > 0 {
				sess = fmt.Sprintf("#%02d", row.session)
			}
			line = fmt.Sprintf("%s %-4s %-24s %-22s %-18s %-10s %s",
				status, sess, game, scene, loc, date, name)
		} else {
			line = fmt.Sprintf("%s %-24s %-22s %-18s %-10s %s",
				status, game, scene, loc, date, name)
		}

		if i == lm.cursor {
			line = styleSelected.Render(fmt.Sprintf("%-*s", lm.width-1, line))
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}

	statusBar := styleStatusBar.Render(
		keyHint("↑↓", "navigate") + "  " +
			keyHint("s", "sessions") + "  " +
			keyHint("ESC", "back"),
	)
	return strings.Join(lines, "\n") + "\n" + statusBar
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
