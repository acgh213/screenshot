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

// StatsMsg signals to return from the stats view.
type StatsMsg struct{}

// OnThisDayMsg signals to return from the on-this-day view.
type OnThisDayMsg struct{}

// StatsModel renders the stats dashboard.
type StatsModel struct {
	vp      viewport.Model
	width   int
	height  int
}

func NewStatsModel(m Manifest, width, height int) StatsModel {
	vp := viewport.New(width, height-3)
	vp.SetContent(renderStats(m, width))
	return StatsModel{vp: vp, width: width, height: height}
}

func (sm StatsModel) Init() tea.Cmd { return nil }

func (sm StatsModel) Update(msg tea.Msg) (StatsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" || msg.String() == "p" {
			return sm, func() tea.Msg { return StatsMsg{} }
		}
	}
	var cmd tea.Cmd
	sm.vp, cmd = sm.vp.Update(msg)
	return sm, cmd
}

func (sm StatsModel) View() string {
	statusBar := styleStatusBar.Render(keyHint("↑↓", "scroll") + "  " + keyHint("ESC", "back"))
	return sm.vp.View() + "\n" + statusBar
}

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

func renderStats(m Manifest, width int) string {
	total := len(m)
	if total == 0 {
		return styleMuted.Render("  No entries in manifest yet.")
	}

	type kv struct {
		key string
		val int
	}
	games := map[string]int{}
	moods := map[string]int{}
	scenes := map[string]int{}
	monthly := map[string]int{}

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
			}
		}

		if e.Scene != "" {
			scene := strings.Split(e.Scene, ",")[0]
			scene = strings.Split(scene, "-")[0]
			scene = strings.TrimSpace(strings.ToLower(scene))
			if scene != "" {
				scenes[scene]++
			}
		}

		// File mtime for monthly
		if info, err := os.Stat(e.File); err == nil {
			monthly[info.ModTime().Format("2006-01")]++
		} else if len(e.CatalogedAt) >= 7 {
			monthly[e.CatalogedAt[:7]]++
		}
	}

	toSlice := func(m map[string]int) []kv {
		s := make([]kv, 0, len(m))
		for k, v := range m {
			s = append(s, kv{k, v})
		}
		sort.Slice(s, func(i, j int) bool { return s[i].val > s[j].val })
		return s
	}

	const barW = 24
	var lines []string

	lines = append(lines,
		styleTitle.Render(fmt.Sprintf("  Archive Statistics")),
		styleMuted.Render(fmt.Sprintf("  %d total entries\n", total)),
	)

	// Top games
	lines = append(lines, styleBold.Render("  Top Games"))
	topGames := toSlice(games)
	if len(topGames) > 15 {
		topGames = topGames[:15]
	}
	maxG := 1
	if len(topGames) > 0 {
		maxG = topGames[0].val
	}
	for _, kv := range topGames {
		pct := 100 * kv.val / total
		lines = append(lines, fmt.Sprintf("  %s %s  %s (%d%%)",
			styleLavender.Bold(true).Render(fmt.Sprintf("%-28s", truncate(kv.key, 28))),
			bar(kv.val, maxG, barW),
			styleGold.Render(fmt.Sprintf("%3d", kv.val)), pct,
		))
	}
	lines = append(lines, "")

	// Monthly activity
	if len(monthly) > 0 {
		lines = append(lines, styleBold.Render("  Monthly Activity"))
		months := make([]string, 0, len(monthly))
		for k := range monthly {
			months = append(months, k)
		}
		sort.Strings(months)
		if len(months) > 24 {
			months = months[len(months)-24:]
		}
		maxM := 1
		for _, k := range months {
			if monthly[k] > maxM {
				maxM = monthly[k]
			}
		}
		for _, month := range months {
			lines = append(lines, fmt.Sprintf("  %s  %s  %d",
				styleMuted.Render(month),
				bar(monthly[month], maxM, barW),
				monthly[month],
			))
		}
		lines = append(lines, "")
	}

	// Moods
	if len(moods) > 0 {
		lines = append(lines, styleBold.Render("  Moods"))
		topMoods := toSlice(moods)
		if len(topMoods) > 10 {
			topMoods = topMoods[:10]
		}
		maxMo := 1
		if len(topMoods) > 0 {
			maxMo = topMoods[0].val
		}
		for _, kv := range topMoods {
			lines = append(lines, fmt.Sprintf("  %-22s %s  %d",
				truncate(kv.key, 22),
				bar(kv.val, maxMo, 20),
				kv.val,
			))
		}
		lines = append(lines, "")
	}

	// Scenes
	if len(scenes) > 0 {
		lines = append(lines, styleBold.Render("  Top Scenes"))
		topScenes := toSlice(scenes)
		if len(topScenes) > 10 {
			topScenes = topScenes[:10]
		}
		maxSc := 1
		if len(topScenes) > 0 {
			maxSc = topScenes[0].val
		}
		for _, kv := range topScenes {
			if kv.key == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("  %-22s %s  %d",
				truncate(kv.key, 22),
				bar(kv.val, maxSc, 20),
				kv.val,
			))
		}
	}

	return strings.Join(lines, "\n")
}

// OnThisDayModel shows screenshots matching today's month/day.
type OnThisDayModel struct {
	rows    []otdRow
	cursor  int
	offset  int
	height  int
	width   int
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
