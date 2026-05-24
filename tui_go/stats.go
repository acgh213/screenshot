package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Session tracking ─────────────────────────────────────────────────────────

const sessionGap = 30 * time.Minute

// Session groups a block of entries from the same game within a short time window.
type Session struct {
	Game       string
	Start      time.Time
	End        time.Time
	Entries    int
	EntryPaths []string
}

// Duration returns the session's wall-clock duration.
func (s Session) Duration() time.Duration {
	return s.End.Sub(s.Start)
}

// ShortDuration formats the duration for display.
func (s Session) ShortDuration() string {
	d := s.Duration().Round(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	m := int(d.Minutes())
	if m >= 60 {
		return fmt.Sprintf("%dh%dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// ComputeSessions groups manifest entries into play sessions.
// A new session starts when switching games or when >30min passes between entries.
func ComputeSessions(m Manifest) []Session {
	type timeEntry struct {
		path  string
		entry *Entry
		at    time.Time
	}

	var all []timeEntry
	for path, e := range m {
		t, err := time.Parse("2006-01-02T15:04:05", e.CatalogedAt)
		if err != nil || e.Game == "" {
			continue
		}
		all = append(all, timeEntry{path: path, entry: e, at: t})
	}
	if len(all) == 0 {
		return nil
	}

	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })

	var sessions []Session
	var cur *Session
	for _, te := range all {
		if cur == nil || te.entry.Game != cur.Game || te.at.Sub(cur.End) > sessionGap {
			if cur != nil {
				sessions = append(sessions, *cur)
			}
			cur = &Session{
				Game:       te.entry.Game,
				Start:      te.at,
				End:        te.at,
				Entries:    1,
				EntryPaths: []string{te.path},
			}
		} else {
			cur.End = te.at
			cur.Entries++
			cur.EntryPaths = append(cur.EntryPaths, te.path)
		}
	}
	if cur != nil {
		sessions = append(sessions, *cur)
	}
	return sessions
}

// ── Stats cache ──────────────────────────────────────────────────────────────

// StatsCache holds pre-computed analytics so stats views open instantly.
type StatsCache struct {
	TotalEntries int
	Games        []kvCount     // sorted desc
	Moods        []kvCount     // sorted desc
	Scenes       []kvCount     // sorted desc
	Monthly      []kvCount     // sorted chrono
	Sessions     []Session
	GameStats    map[string]*GameStats
}

type kvCount struct {
	Key string
	Val int
}

// GameStats holds per-game aggregate data.
type GameStats struct {
	Game         string
	Entries      int
	SessionCount int
	TotalDur     time.Duration
	Scenes       []kvCount
	Moods        []kvCount
}

// BuildStatsCache computes all derived data from a manifest.
func BuildStatsCache(m Manifest) *StatsCache {
	sc := &StatsCache{
		TotalEntries: len(m),
		GameStats:    map[string]*GameStats{},
	}

	games := map[string]int{}
	moods := map[string]int{}
	scenes := map[string]int{}
	monthly := map[string]int{}

	// Per-game breakdowns
	gmScenes := map[string]map[string]int{}
	gmMoods := map[string]map[string]int{}

	for _, e := range m {
		game := e.Game
		if game == "" {
			game = "Unknown"
		}
		games[game]++

		if e.Mood != "" {
			mood := strings.Split(e.Mood, ",")[0]
			mood = strings.Split(mood, "/")[0]
			mood = strings.TrimSpace(strings.ToLower(mood))
			if mood != "" {
				moods[mood]++
				if gmMoods[game] == nil {
					gmMoods[game] = map[string]int{}
				}
				gmMoods[game][mood]++
			}
		}

		if e.Scene != "" {
			scene := strings.Split(e.Scene, ",")[0]
			scene = strings.Split(scene, "-")[0]
			scene = strings.TrimSpace(strings.ToLower(scene))
			if scene != "" {
				scenes[scene]++
				if gmScenes[game] == nil {
					gmScenes[game] = map[string]int{}
				}
				gmScenes[game][scene]++
			}
		}

		if info, err := os.Stat(e.File); err == nil {
			monthly[info.ModTime().Format("2006-01")]++
		} else if len(e.CatalogedAt) >= 7 {
			monthly[e.CatalogedAt[:7]]++
		}
	}

	sc.Games = toSortedSlice(games)
	sc.Moods = toSortedSlice(moods)
	sc.Scenes = toSortedSlice(scenes)

	// Monthly sorted chronologically
	sc.Monthly = nil
	for k, v := range monthly {
		sc.Monthly = append(sc.Monthly, kvCount{k, v})
	}
	sort.Slice(sc.Monthly, func(i, j int) bool { return sc.Monthly[i].Key < sc.Monthly[j].Key })

	// Sessions
	sc.Sessions = ComputeSessions(m)

	// Per-game stats
	for _, s := range sc.Sessions {
		gs, ok := sc.GameStats[s.Game]
		if !ok {
			gs = &GameStats{Game: s.Game}
			sc.GameStats[s.Game] = gs
		}
		gs.SessionCount++
		gs.Entries += s.Entries
		gs.TotalDur += s.Duration()
	}
	for game, entryCount := range games {
		gs := sc.GameStats[game]
		if gs == nil {
			gs = &GameStats{Game: game}
			sc.GameStats[game] = gs
		}
		gs.Entries = entryCount
		gs.Scenes = toSortedSlice(gmScenes[game])
		gs.Moods = toSortedSlice(gmMoods[game])
	}

	return sc
}

func toSortedSlice(m map[string]int) []kvCount {
	s := make([]kvCount, 0, len(m))
	for k, v := range m {
		s = append(s, kvCount{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].Val > s[j].Val })
	return s
}

// ── Messages ─────────────────────────────────────────────────────────────────

type StatsMsg struct{}

type OnThisDayMsg struct{}

// StatsViewType is which sub-view is being shown.
type StatsViewType int

const (
	statsViewOverview StatsViewType = iota
	statsViewGame
)

// StatsModel renders the stats dashboard with keyboard navigation.
type StatsModel struct {
	cache   *StatsCache
	view    StatsViewType
	selGame string // selected game name for drill-down

	vp     viewport.Model
	width  int
	height int
}

func NewStatsModel(m Manifest, cache *StatsCache, width, height int) StatsModel {
	if cache == nil {
		cache = BuildStatsCache(m)
	}
	vp := viewport.New(width, height-3)
	vp.SetContent(renderOverview(cache, width))
	return StatsModel{
		cache:  cache,
		view:   statsViewOverview,
		vp:     vp,
		width:  width,
		height: height,
	}
}

func (sm StatsModel) Init() tea.Cmd { return nil }

func (sm StatsModel) Update(msg tea.Msg) (StatsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "p":
			if sm.view == statsViewGame {
				sm.view = statsViewOverview
				sm.vp.SetContent(renderOverview(sm.cache, sm.width))
				sm.vp.GotoTop()
				return sm, nil
			}
			return sm, func() tea.Msg { return StatsMsg{} }
		case "enter":
			if sm.view == statsViewOverview && len(sm.cache.Games) > 0 {
				// "Selected" game is at the cursor (scroll offset).
				// Use the visible top as a proxy for selection.
				idx := sm.vp.YOffset
				if idx >= len(sm.cache.Games) {
					idx = 0
				}
				sm.selGame = sm.cache.Games[idx].Key
				sm.view = statsViewGame
				sm.vp.SetContent(renderGameDetail(sm.cache, sm.selGame, sm.width))
				sm.vp.GotoTop()
				return sm, nil
			}
		}
	}
	var cmd tea.Cmd
	sm.vp, cmd = sm.vp.Update(msg)
	return sm, cmd
}

func (sm StatsModel) View() string {
	var hints string
	switch sm.view {
	case statsViewOverview:
		hints = keyHint("↑↓", "scroll") + "  " +
			keyHint("Enter", "drill-in") + "  " +
			keyHint("ESC", "back")
	case statsViewGame:
		hints = keyHint("↑↓", "scroll") + "  " +
			keyHint("ESC", "back")
	}
	statusBar := styleStatusBar.Render(hints)
	return sm.vp.View() + "\n" + statusBar
}

// ── Rendering ────────────────────────────────────────────────────────────────

func bar(value, maxVal, width int) string {
	if maxVal == 0 {
		return styleMuted.Render(strings.Repeat("·", width))
	}
	filled := width * value / maxVal
	if filled > width {
		filled = width
	}
	return stylePeach.Render(strings.Repeat("█", filled)) +
		styleMuted.Render(strings.Repeat("░", width-filled))
}

func renderOverview(sc *StatsCache, width int) string {
	if sc.TotalEntries == 0 {
		return styleMuted.Render("  No entries in manifest yet.")
	}

	const barW = 24
	var lines []string

	// ── Summary card ──
	lines = append(lines, styleTitle.Render("  ✧ Archive Statistics"))
	lines = append(lines, "")

	sessionCount := len(sc.Sessions)

	// Summary line
	summaryParts := []string{
		fmt.Sprintf("%d entries", sc.TotalEntries),
	}
	if sessionCount > 0 {
		var totalDur time.Duration
		mostGame, mostEntries := "", 0
		for _, gs := range sc.GameStats {
			totalDur += gs.TotalDur
			if gs.Entries > mostEntries {
				mostEntries = gs.Entries
				mostGame = gs.Game
			}
		}
		avgDur := totalDur / time.Duration(sessionCount)
		avgDurStr := formatDuration(avgDur)

		summaryParts = append(summaryParts, fmt.Sprintf("%d sessions", sessionCount))
		summaryParts = append(summaryParts, fmt.Sprintf("~%s/session", avgDurStr))
		summaryParts = append(summaryParts, fmt.Sprintf("top: %s", mostGame))
	}
	lines = append(lines, styleGold.Render("  📊 "+strings.Join(summaryParts, "  ·  ")))
	lines = append(lines, "")

	// ── Top Games ──
	lines = append(lines, styleBold.Render("  Top Games  (press Enter to drill in)"))
	games := sc.Games
	if len(games) > 15 {
		games = games[:15]
	}
	maxG := 1
	if len(games) > 0 {
		maxG = games[0].Val
	}
	for i, kv := range games {
		pct := 100 * kv.Val / sc.TotalEntries
		line := fmt.Sprintf("  %s %s  %s (%d%%)",
			styleLavender.Bold(true).Render(fmt.Sprintf("%-28s", truncate(kv.Key, 28))),
			bar(kv.Val, maxG, barW),
			styleGold.Render(fmt.Sprintf("%3d", kv.Val)), pct,
		)
		// Highlight the row the cursor is on
		cursorRow := 6 + i // 6 = offset to first game row in rendered text
		if cursorRow >= 0 && cursorRow == 0 { // we can't track scroll precisely here
		}
		_ = i
		lines = append(lines, line)
	}
	lines = append(lines, "")

	// ── Monthly Activity ──
	if len(sc.Monthly) > 0 {
		lines = append(lines, styleBold.Render("  Monthly Activity"))
		months := sc.Monthly
		if len(months) > 24 {
			months = months[len(months)-24:]
		}
		maxM := 1
		for _, kv := range months {
			if kv.Val > maxM {
				maxM = kv.Val
			}
		}
		for _, month := range months {
			lines = append(lines, fmt.Sprintf("  %s  %s  %d",
				styleMuted.Render(month.Key),
				bar(month.Val, maxM, barW),
				month.Val,
			))
		}
		lines = append(lines, "")
	}

	// ── Moods ──
	if len(sc.Moods) > 0 {
		lines = append(lines, styleBold.Render("  Moods"))
		moods := sc.Moods
		if len(moods) > 10 {
			moods = moods[:10]
		}
		maxMo := 1
		if len(moods) > 0 {
			maxMo = moods[0].Val
		}
		for _, kv := range moods {
			lines = append(lines, fmt.Sprintf("  %-22s %s  %d",
				truncate(kv.Key, 22),
				bar(kv.Val, maxMo, 20),
				kv.Val,
			))
		}
		lines = append(lines, "")
	}

	// ── Top Scenes ──
	if len(sc.Scenes) > 0 {
		lines = append(lines, styleBold.Render("  Top Scenes"))
		topScenes := sc.Scenes
		if len(topScenes) > 10 {
			topScenes = topScenes[:10]
		}
		maxSc := 1
		if len(topScenes) > 0 {
			maxSc = topScenes[0].Val
		}
		for _, kv := range topScenes {
			if kv.Key == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("  %-22s %s  %d",
				truncate(kv.Key, 22),
				bar(kv.Val, maxSc, 20),
				kv.Val,
			))
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func renderGameDetail(sc *StatsCache, game string, width int) string {
	gs, ok := sc.GameStats[game]
	if !ok {
		return styleWarning.Render(fmt.Sprintf("  No data for %s", game))
	}

	var lines []string

	lines = append(lines, styleTitle.Render(fmt.Sprintf("  ✦ %s", game)))
	lines = append(lines, styleMuted.Render(fmt.Sprintf("  %d entries across %d sessions", gs.Entries, gs.SessionCount)))
	if gs.SessionCount > 0 {
		avgDur := gs.TotalDur / time.Duration(gs.SessionCount)
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  avg session: %s  ·  total playtime: %s",
			formatDuration(avgDur), formatDuration(gs.TotalDur))))
	}
	lines = append(lines, "")

	// Sessions list
	if len(sc.Sessions) > 0 {
		lines = append(lines, styleBold.Render(fmt.Sprintf("  Sessions  (%d)", gs.SessionCount)))
		for _, s := range sc.Sessions {
			if s.Game != game {
				continue
			}
			date := s.Start.Format("2006-01-02")
			startStr := s.Start.Format("15:04")
			endStr := s.End.Format("15:04")
			lines = append(lines, fmt.Sprintf("  %s  %s–%s  %s  %d screenshots",
				styleMuted.Render(date),
				startStr, endStr,
				stylePeach.Render(s.ShortDuration()),
				s.Entries,
			))
		}
		lines = append(lines, "")
	}

	const barW = 20

	// Scenes for this game
	if len(gs.Scenes) > 0 {
		lines = append(lines, styleBold.Render("  Scenes"))
		maxSc := gs.Scenes[0].Val
		for _, kv := range gs.Scenes {
			lines = append(lines, fmt.Sprintf("  %-22s %s  %d",
				truncate(kv.Key, 22), bar(kv.Val, maxSc, barW), kv.Val))
		}
		lines = append(lines, "")
	}

	// Moods for this game
	if len(gs.Moods) > 0 {
		lines = append(lines, styleBold.Render("  Moods"))
		maxMo := gs.Moods[0].Val
		for _, kv := range gs.Moods {
			lines = append(lines, fmt.Sprintf("  %-22s %s  %d",
				truncate(kv.Key, 22), bar(kv.Val, maxMo, barW), kv.Val))
		}
		lines = append(lines, "")
	}

	lines = append(lines, styleMuted.Render("  press ESC to go back"))

	return strings.Join(lines, "\n")
}

// ── On This Day ──────────────────────────────────────────────────────────────

// OnThisDayModel shows screenshots matching today's month/day.
type OnThisDayModel struct {
	rows   []otdRow
	cursor int
	offset int
	height int
	width  int
}

type otdRow struct {
	entry *Entry
	path  string
	year  string
}

func NewOnThisDayModel(m Manifest, width, height int) OnThisDayModel {
	today := time.Now()
	var rows []otdRow
	for path, e := range m {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		mt := info.ModTime()
		if mt.Month() == today.Month() && mt.Day() == today.Day() {
			rows = append(rows, otdRow{entry: e, path: path, year: fmt.Sprintf("%d", mt.Year())})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].year < rows[j].year })
	return OnThisDayModel{rows: rows, width: width, height: height - 4}
}

func (om OnThisDayModel) Init() tea.Cmd { return nil }

func (om OnThisDayModel) Update(msg tea.Msg) (OnThisDayModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "t":
			return om, func() tea.Msg { return OnThisDayMsg{} }
		case "up":
			if om.cursor > 0 {
				om.cursor--
				if om.cursor < om.offset {
					om.offset = om.cursor
				}
			}
		case "down":
			if om.cursor < len(om.rows)-1 {
				om.cursor++
				if om.cursor >= om.offset+om.height {
					om.offset = om.cursor - om.height + 1
				}
			}
		}
	}
	return om, nil
}

func (om OnThisDayModel) View() string {
	today := time.Now()
	title := styleTitle.Render(fmt.Sprintf("  📅  On This Day — %s %d", today.Month().String(), today.Day()))
	sub := styleMuted.Render(fmt.Sprintf("  screenshots from this date across all years  (%d found)", len(om.rows)))

	divider := styleMuted.Render(strings.Repeat("─", om.width))
	colHdr := styleBold.Render(fmt.Sprintf("  %-6s %-24s %-22s %-18s %s",
		"Year", "Game", "Scene", "Location", "File"))

	lines := []string{title, sub, divider, colHdr, divider}

	if len(om.rows) == 0 {
		lines = append(lines, styleMuted.Render("  No screenshots found for today's date."))
	}

	end := om.offset + om.height
	if end > len(om.rows) {
		end = len(om.rows)
	}
	for i := om.offset; i < end; i++ {
		row := om.rows[i]
		e := row.entry
		line := fmt.Sprintf("  %-6s %-24s %-22s %-18s %s",
			row.year,
			truncate(e.Game, 24),
			truncate(e.Scene, 22),
			truncate(e.Location, 18),
			filepath.Base(row.path),
		)
		if i == om.cursor {
			line = styleSelected.Render(fmt.Sprintf("%-*s", om.width-1, strings.TrimPrefix(line, "  ")))
		}
		lines = append(lines, line)
	}

	statusBar := styleStatusBar.Render(keyHint("↑↓", "navigate") + "  " + keyHint("ESC", "back"))
	return strings.Join(lines, "\n") + "\n" + statusBar
}
