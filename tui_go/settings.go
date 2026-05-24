package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Settings holds the persistent configuration.
type Settings struct {
	APIBase            string  `json:"api_base"`
	Model              string  `json:"model"`
	MaxTokens          int     `json:"max_tokens"`
	Temperature        float64 `json:"temperature"`
	SceneThreshold     float64 `json:"scene_threshold"`
	VideoIntervalShort float64 `json:"video_interval_short"`
	VideoIntervalMed   float64 `json:"video_interval_med"`
	VideoIntervalLong  float64 `json:"video_interval_long"`
	VideoLengthMedMin  int     `json:"video_length_med_min"`
	VideoLengthLongMin int     `json:"video_length_long_min"`
}

func DefaultSettings() Settings {
	return Settings{
		APIBase:            "http://169.254.83.107:1234/v1",
		Model:              "gemma-4-e4b",
		MaxTokens:          2048,
		Temperature:        0.3,
		SceneThreshold:     0.3,
		VideoIntervalShort: 10.0,
		VideoIntervalMed:   30.0,
		VideoIntervalLong:  60.0,
		VideoLengthMedMin:  5,
		VideoLengthLongMin: 30,
	}
}

func LoadSettings(manifestPath string) Settings {
	s := DefaultSettings()
	settingsPath := filepath.Join(filepath.Dir(manifestPath), "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return s
	}
	json.Unmarshal(data, &s)
	return s
}

func SaveSettings(s Settings, manifestPath string) error {
	settingsPath := filepath.Join(filepath.Dir(manifestPath), "settings.json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, data, 0644)
}

// SettingsMsg is returned when the settings form is saved or cancelled.
type SettingsMsg struct {
	Saved    bool
	Settings Settings
}

// SettingsModel is the full-screen settings form.
type SettingsModel struct {
	fields  []textinput.Model
	labels  []string
	focused int
	width   int
	height  int
	current Settings
}

func NewSettingsModel(s Settings, width, height int) SettingsModel {
	labels := []string{
		"API Base URL",
		"Model name",
		"Max tokens",
		"Temperature (0.0 – 1.0)",
		"Scene threshold (0.0 – 1.0)",
		"Video interval: short clip <5min (sec)",
		"Video interval: medium clip 5–30min (sec)",
		"Video interval: long recording >30min (sec)",
		"Video length threshold: short→medium (min)",
		"Video length threshold: medium→long (min)",
	}
	values := []string{
		s.APIBase,
		s.Model,
		fmt.Sprintf("%d", s.MaxTokens),
		fmt.Sprintf("%.2f", s.Temperature),
		fmt.Sprintf("%.2f", s.SceneThreshold),
		fmt.Sprintf("%.1f", s.VideoIntervalShort),
		fmt.Sprintf("%.1f", s.VideoIntervalMed),
		fmt.Sprintf("%.1f", s.VideoIntervalLong),
		fmt.Sprintf("%d", s.VideoLengthMedMin),
		fmt.Sprintf("%d", s.VideoLengthLongMin),
	}
	fields := make([]textinput.Model, len(labels))
	for i := range labels {
		ti := textinput.New()
		ti.SetValue(values[i])
		ti.Width = 50
		fields[i] = ti
	}
	fields[0].Focus()
	return SettingsModel{
		fields:  fields,
		labels:  labels,
		focused: 0,
		width:   width,
		height:  height,
		current: s,
	}
}

func (m SettingsModel) Init() tea.Cmd { return nil }

func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return SettingsMsg{Saved: false} }
		case "enter":
			s, err := m.collect()
			if err != nil {
				return m, nil
			}
			return m, func() tea.Msg { return SettingsMsg{Saved: true, Settings: s} }
		case "tab", "down":
			m.fields[m.focused].Blur()
			m.focused = (m.focused + 1) % len(m.fields)
			m.fields[m.focused].Focus()
		case "shift+tab", "up":
			m.fields[m.focused].Blur()
			m.focused = (m.focused - 1 + len(m.fields)) % len(m.fields)
			m.fields[m.focused].Focus()
		}
	}
	var cmd tea.Cmd
	m.fields[m.focused], cmd = m.fields[m.focused].Update(msg)
	return m, cmd
}

func (m SettingsModel) collect() (Settings, error) {
	var s Settings
	s.APIBase = strings.TrimSpace(m.fields[0].Value())
	s.Model = strings.TrimSpace(m.fields[1].Value())
	fmt.Sscanf(m.fields[2].Value(), "%d", &s.MaxTokens)
	fmt.Sscanf(m.fields[3].Value(), "%f", &s.Temperature)
	fmt.Sscanf(m.fields[4].Value(), "%f", &s.SceneThreshold)
	fmt.Sscanf(m.fields[5].Value(), "%f", &s.VideoIntervalShort)
	fmt.Sscanf(m.fields[6].Value(), "%f", &s.VideoIntervalMed)
	fmt.Sscanf(m.fields[7].Value(), "%f", &s.VideoIntervalLong)
	fmt.Sscanf(m.fields[8].Value(), "%d", &s.VideoLengthMedMin)
	fmt.Sscanf(m.fields[9].Value(), "%d", &s.VideoLengthLongMin)
	return s, nil
}

func (m SettingsModel) View() string {
	title := styleTitle.Render("⚙  Settings")
	hint := styleMuted.Render("  Tab/↑↓:navigate  Enter:save  ESC:cancel")

	var rows []string
	rows = append(rows, title+hint)
	rows = append(rows, "")

	for i, label := range m.labels {
		labelStr := styleMuted.Render(label)
		fieldStr := m.fields[i].View()
		if i == m.focused {
			labelStr = lipgloss.NewStyle().Foreground(colorPeach).Bold(true).Render(label)
		}
		rows = append(rows, "  "+labelStr)
		rows = append(rows, "  "+fieldStr)
		rows = append(rows, "")
	}

	rows = append(rows, "  "+styleMuted.Render("─────────────────────────────────────────────────────"))
	rows = append(rows, "  "+keyHint("Enter", "save")+"   "+keyHint("ESC", "cancel"))

	return strings.Join(rows, "\n")
}
