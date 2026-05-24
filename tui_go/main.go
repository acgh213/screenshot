package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	args := parseArgs()
	scanDir := args["dir"]
	manifestPath := args["manifest"]

	// Ensure scan dir exists
	if err := os.MkdirAll(scanDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create scan dir: %v\n", err)
		os.Exit(1)
	}

	// Ensure manifest parent dir exists
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create manifest dir: %v\n", err)
		os.Exit(1)
	}

	model := NewModel(scanDir, manifestPath)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseArgs() map[string]string {
	result := map[string]string{
		"dir":      defaultScanDir(),
		"manifest": "screenshot_catalog.jsonl",
	}
	args := os.Args[1:]
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--dir":
			result["dir"] = args[i+1]
			i++
		case "--manifest":
			result["manifest"] = args[i+1]
			i++
		}
	}
	// Resolve paths
	if abs, err := filepath.Abs(result["dir"]); err == nil {
		result["dir"] = abs
	}
	if abs, err := filepath.Abs(result["manifest"]); err == nil {
		result["manifest"] = abs
	}
	return result
}

func defaultScanDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "ShareX", "Screenshots")
}
