package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one record in the JSONL manifest.
type Entry struct {
	File        string   `json:"file"`
	CatalogedAt string   `json:"cataloged_at"`
	Game        string   `json:"game"`
	Scene       string   `json:"scene"`
	Location    string   `json:"location"`
	Mood        string   `json:"mood"`
	Keywords    []string `json:"keywords"`
	SuggestedFn string   `json:"suggested_filename,omitempty"`
	Error       string   `json:"error,omitempty"`
	RawResponse string   `json:"raw_response,omitempty"`
}

// IsFailed returns true if the entry represents a catalog failure.
func (e *Entry) IsFailed() bool {
	return e.Error != "" || e.RawResponse != ""
}

// IsNonGame returns true if the filename matches the non-game blocklist.
func (e *Entry) IsNonGame() bool {
	return isNonGamePath(e.File)
}

// ShortGame returns up to N chars of the game name.
func (e *Entry) ShortGame(n int) string {
	g := e.Game
	if g == "" {
		return ""
	}
	runes := []rune(g)
	if len(runes) > n {
		return string(runes[:n-1]) + "…"
	}
	return g
}

// Manifest is keyed by absolute file path.
type Manifest map[string]*Entry

var nonGamePrefixes = []string{
	"discord", "nvcontainer", "applicationframehost", "sharex",
	"obs64", "obs32", "streamlabs", "nvidia", "msi ",
	"afterburner", "nvdisplay", "nvcplui", "dwm", "explorer",
	"taskmgr", "firefox", "opera", "chrome",
}

func isNonGamePath(path string) bool {
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	for _, prefix := range nonGamePrefixes {
		if strings.HasPrefix(stem, prefix) {
			return true
		}
	}
	return false
}

// LoadManifest reads a JSONL manifest file.
func LoadManifest(path string) (Manifest, error) {
	m := make(Manifest)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		m[e.File] = &e
	}
	return m, scanner.Err()
}

// SaveManifest rewrites the manifest atomically.
func SaveManifest(m Manifest, path string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, e := range m {
		if err := enc.Encode(e); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	f.Close()
	return os.Rename(tmp, path)
}

// ExportCSV writes manifest data to a CSV file.
func ExportCSV(m Manifest, outPath string) (int, error) {
	f, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"file", "game", "scene", "location", "mood", "keywords", "cataloged_at"})
	n := 0
	for _, e := range m {
		w.Write([]string{
			e.File,
			e.Game,
			e.Scene,
			e.Location,
			e.Mood,
			strings.Join(e.Keywords, ", "),
			e.CatalogedAt,
		})
		n++
	}
	w.Flush()
	return n, w.Error()
}

// OrphanPaths returns manifest keys whose files no longer exist on disk.
func OrphanPaths(m Manifest) []string {
	var orphans []string
	for path := range m {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			orphans = append(orphans, path)
		}
	}
	return orphans
}

// FailedPaths returns keys for entries that have error or raw_response fields.
func FailedPaths(m Manifest) []string {
	var failed []string
	for path, e := range m {
		if e.IsFailed() {
			if _, err := os.Stat(path); err == nil {
				failed = append(failed, path)
			}
		}
	}
	return failed
}

// UndoEntry records a reversible file move.
type UndoEntry struct {
	Src string
	Dst string
}

// SuggestFilename builds a descriptive filename for an entry.
func SuggestFilename(e *Entry, orig string) string {
	sanitize := func(s string) string {
		s = strings.ReplaceAll(s, " ", "-")
		s = strings.ReplaceAll(s, ":", "")
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	ext := filepath.Ext(orig)
	stem := strings.TrimSuffix(filepath.Base(orig), ext)
	parts := []string{}
	if e.Game != "" {
		parts = append(parts, sanitize(e.Game))
	}
	if e.Scene != "" {
		parts = append(parts, sanitize(e.Scene))
	}
	if e.Location != "" {
		parts = append(parts, sanitize(e.Location))
	}
	parts = append(parts, stem)
	if len(parts) > 4 {
		parts = parts[:4]
	}
	return strings.Join(parts, "_") + ext
}

// SanitizeFolderName strips Windows-reserved chars from a name.
func SanitizeFolderName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			// skip
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	if s == "" {
		return "_unnamed"
	}
	return s
}

// Year extracts the 4-digit year from cataloged_at.
func (e *Entry) Year() string {
	if len(e.CatalogedAt) >= 4 {
		return e.CatalogedAt[:4]
	}
	info, err := os.Stat(e.File)
	if err == nil {
		return info.ModTime().Format("2006")
	}
	return time.Now().Format("2006")
}
