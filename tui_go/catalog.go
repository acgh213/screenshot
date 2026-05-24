package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// NdjsonMsg is one line from the Python subprocess stdout.
type NdjsonMsg struct {
	Type     string   `json:"type"`
	Total    int      `json:"total"`
	File     string   `json:"file"`
	Name     string   `json:"name"`
	Game     string   `json:"game"`
	Scene    string   `json:"scene"`
	Status   string   `json:"status"`
	Error    string   `json:"error"`
	Msg      string   `json:"msg"`
	Success  int      `json:"success"`
	Fail     int      `json:"fail"`
	Groups   int      `json:"groups"`
	Files    []string `json:"files"`
	Filtered int      `json:"filtered"`
	Frames   int      `json:"frames"`
}

// CatalogProgressMsg carries a single NDJSON line from the subprocess.
type CatalogProgressMsg NdjsonMsg

// CatalogDoneMsg signals that the subprocess has exited.
type CatalogDoneMsg struct {
	Success int
	Fail    int
	Err     error
}

// DupeGroupMsg carries one duplicate group.
type DupeGroupMsg struct {
	Files []string
}

// DupeDoneMsg signals duplicate detection finished.
type DupeDoneMsg struct {
	Groups int
	Err    error
}

// binaryDir returns the directory containing the running executable.
func binaryDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// pythonExe finds the python executable to use.
func pythonExe() string {
	// Prefer the venv sitting next to the binary.
	venvPy := filepath.Join(binaryDir(), "venv", "Scripts", "python.exe")
	if _, err := os.Stat(venvPy); err == nil {
		return venvPy
	}
	if path, err := exec.LookPath("python"); err == nil {
		return path
	}
	return "python"
}

// scriptPath returns the absolute path to screenshot_catalog.py.
func scriptPath() string {
	// It lives alongside the binary.
	p := filepath.Join(binaryDir(), "screenshot_catalog.py")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	// Fallback: look in CWD.
	abs, _ := filepath.Abs("screenshot_catalog.py")
	return abs
}

// StartCatalog launches the Python catalog subprocess and streams NDJSON messages.
// allFiles=true means re-catalog everything; files is an optional explicit list.
func StartCatalog(ctx context.Context, dir, manifest string, allFiles bool, files []string) tea.Cmd {
	return func() tea.Msg {
		args := []string{scriptPath(), "--stream", "--dir", dir, "--manifest", manifest}
		if allFiles {
			args = append(args, "--all")
		}
		for _, f := range files {
			args = append(args, "--files", f)
		}
		cmd := exec.CommandContext(ctx, pythonExe(), args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return CatalogDoneMsg{Err: err}
		}
		if err := cmd.Start(); err != nil {
			return CatalogDoneMsg{Err: err}
		}
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var msg NdjsonMsg
			if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
				// We can't directly send tea messages from here; this is handled
				// by the streaming command pattern below.
				_ = msg
			}
		}
		err = cmd.Wait()
		return CatalogDoneMsg{Err: err}
	}
}

// streamCatalog is the proper streaming command — returns a channel-based tea.Cmd.
func streamCatalog(ctx context.Context, dir, manifest string, allFiles bool, files []string) tea.Cmd {
	return func() tea.Msg {
		args := []string{scriptPath(), "--stream", "--dir", dir, "--manifest", manifest}
		if allFiles {
			args = append(args, "--all")
		}
		for _, f := range files {
			args = append(args, "--files", f)
		}
		cmd := exec.CommandContext(ctx, pythonExe(), args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return CatalogDoneMsg{Err: fmt.Errorf("pipe: %w", err)}
		}
		if err := cmd.Start(); err != nil {
			return CatalogDoneMsg{Err: fmt.Errorf("start: %w", err)}
		}
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			var msg NdjsonMsg
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				if msg.Type == "done" {
					cmd.Wait()
					return CatalogDoneMsg{Success: msg.Success, Fail: msg.Fail}
				}
				return CatalogProgressMsg(msg)
			}
		}
		cmd.Wait()
		return CatalogDoneMsg{}
	}
}

// continueCatalog reads the next line from an already-started subprocess.
// Bubbletea's Cmd model means we re-issue this after each message.
func continueCatalog(ctx context.Context, scanner *bufio.Scanner, cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if !scanner.Scan() {
			cmd.Wait()
			return CatalogDoneMsg{}
		}
		var msg NdjsonMsg
		if json.Unmarshal(scanner.Bytes(), &msg) == nil {
			if msg.Type == "done" {
				cmd.Wait()
				return CatalogDoneMsg{Success: msg.Success, Fail: msg.Fail}
			}
			return CatalogProgressMsg(msg)
		}
		return CatalogProgressMsg{Type: "progress"}
	}
}

// LaunchCatalogProcess starts the Python process and returns the scanner and cmd.
func LaunchCatalogProcess(ctx context.Context, dir, manifest string, allFiles bool, files []string) (*bufio.Scanner, *exec.Cmd, error) {
	args := []string{scriptPath(), "--stream", "--dir", dir, "--manifest", manifest}
	if allFiles {
		args = append(args, "--all")
	}
	if len(files) > 0 {
		args = append(args, "--files")
		args = append(args, files...)
	}
	cmd := exec.CommandContext(ctx, pythonExe(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)
	return scanner, cmd, nil
}

// LaunchDupesProcess starts the Python dupes subprocess.
func LaunchDupesProcess(ctx context.Context, manifest string, threshold int) (*bufio.Scanner, *exec.Cmd, error) {
	args := []string{
		scriptPath(), "--stream-dupes",
		"--manifest", manifest,
		"--threshold", fmt.Sprintf("%d", threshold),
	}
	cmd := exec.CommandContext(ctx, pythonExe(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)
	return scanner, cmd, nil
}

// LaunchVideoProcess starts the Python video worker subprocess.
func LaunchVideoProcess(ctx context.Context, videoFile, manifest, mode string, threshold float64, s Settings) (*bufio.Scanner, *exec.Cmd, error) {
	args := []string{
		scriptPath(), "--stream-video",
		"--file", videoFile,
		"--manifest", manifest,
		"--mode", mode,
		"--threshold", fmt.Sprintf("%.2f", threshold),
		"--interval-short", fmt.Sprintf("%.1f", s.VideoIntervalShort),
		"--interval-med", fmt.Sprintf("%.1f", s.VideoIntervalMed),
		"--interval-long", fmt.Sprintf("%.1f", s.VideoIntervalLong),
		"--length-med-min", fmt.Sprintf("%d", s.VideoLengthMedMin),
		"--length-long-min", fmt.Sprintf("%d", s.VideoLengthLongMin),
	}
	cmd := exec.CommandContext(ctx, pythonExe(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)
	return scanner, cmd, nil
}

// ReadNextMsg reads the next NDJSON line from a running subprocess.
func ReadNextMsg(scanner *bufio.Scanner, cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if !scanner.Scan() {
			cmd.Wait()
			return CatalogDoneMsg{}
		}
		var msg NdjsonMsg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
			if msg.Type == "done" {
				cmd.Wait()
				return CatalogDoneMsg{Success: msg.Success, Fail: msg.Fail}
			}
			if msg.Type == "group" {
				return DupeGroupMsg{Files: msg.Files}
			}
			return CatalogProgressMsg(msg)
		}
		return CatalogProgressMsg{Type: "line"}
	}
}
