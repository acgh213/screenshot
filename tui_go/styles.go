package main

import "github.com/charmbracelet/lipgloss"

// ── Palette ───────────────────────────────────────────────────────────────────
// Single source of truth. No hex values anywhere else in the codebase.

var (
	colorBase     = lipgloss.Color("#1a141a") // warm plum-black bg
	colorSurface  = lipgloss.Color("#231d24") // elevated panels
	colorOverlay  = lipgloss.Color("#2a232c") // modals, hover states
	colorText     = lipgloss.Color("#e8dce4") // soft cream-white
	colorTextDim  = lipgloss.Color("#8a7d8a") // muted secondary
	colorPeach    = lipgloss.Color("#f0a68c") // primary accent
	colorPeachDim = lipgloss.Color("#c47a66") // pressed/disabled
	colorRose     = lipgloss.Color("#e8919e") // secondary, error
	colorGold     = lipgloss.Color("#dbb87c") // stars, highlights
	colorLavender = lipgloss.Color("#b8a0d4") // folders, game names
	colorSuccess  = lipgloss.Color("#8cc4a0") // cataloged ✓
	colorWarning  = colorGold                 // queued, pending
	colorError    = colorRose                 // failures
	colorInfo     = lipgloss.Color("#a8c8e8") // neutral status
)

// ── Pre-built styles ──────────────────────────────────────────────────────────

var (
	// Text styles
	styleText    = lipgloss.NewStyle().Foreground(colorText)
	styleMuted   = lipgloss.NewStyle().Foreground(colorTextDim)
	styleBold    = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	styleTitle   = lipgloss.NewStyle().Foreground(colorPeach).Bold(true)
	styleSuccess = lipgloss.NewStyle().Foreground(colorSuccess)
	styleWarning = lipgloss.NewStyle().Foreground(colorWarning)
	styleError   = lipgloss.NewStyle().Foreground(colorError)
	styleInfo    = lipgloss.NewStyle().Foreground(colorInfo)
	styleGold    = lipgloss.NewStyle().Foreground(colorGold)
	styleLavender = lipgloss.NewStyle().Foreground(colorLavender)
	styleRose    = lipgloss.NewStyle().Foreground(colorRose)
	stylePeach   = lipgloss.NewStyle().Foreground(colorPeach)

	// Selected / highlighted row
	styleSelected = lipgloss.NewStyle().
			Background(colorPeach).
			Foreground(colorBase).
			Bold(true)

	// File tree folder names
	styleFolderName = lipgloss.NewStyle().
			Foreground(colorLavender).
			Bold(true)

	// Status bar at the bottom
	styleStatusBar = lipgloss.NewStyle().
			Background(colorOverlay).
			Foreground(colorText).
			Padding(0, 1)

	// Key hint: the key itself is peach, the label is text-dim
	styleKeyCap   = lipgloss.NewStyle().Foreground(colorPeach).Bold(true)
	styleKeyLabel = lipgloss.NewStyle().Foreground(colorTextDim)

	// Borders
	styleBorderFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPeach)
	styleBorderUnfocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPeachDim)
)

// keyHint renders a keybinding hint: `c:catalog`
func keyHint(key, desc string) string {
	return styleKeyCap.Render(key) + styleKeyLabel.Render(":"+desc)
}

// spinner frames used during active catalog runs
var spinnerFrames = []string{"◌", "◍", "◎", "●", "○"}

// Spinner returns the current spinner character for a given tick count.
func Spinner(tick int) string {
	return stylePeach.Render(spinnerFrames[tick%len(spinnerFrames)])
}

// ProgressBar renders a peach/gold shimmer progress bar.
func ProgressBar(n, total, width int) string {
	if total == 0 || width <= 0 {
		return styleMuted.Render("· · · · ·")
	}
	filled := n * width / total
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < filled; i++ {
		// Alternate peach/gold for shimmer
		if i%3 == 1 {
			bar += styleGold.Render("█")
		} else {
			bar += stylePeach.Render("█")
		}
	}
	bar += styleMuted.Render(lipgloss.NewStyle().Foreground(colorOverlay).Render("") +
		lipgloss.NewStyle().Foreground(colorTextDim).Render(repeat("░", width-filled)))
	return bar
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
