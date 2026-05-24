package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var undoStack []UndoEntry

// DoOrganize moves cataloged files into baseDir/{game}/{year}/ subfolders.
// Returns list of (src, dst) pairs. If dryRun, files are not moved.
func DoOrganize(m Manifest, baseDir string, dryRun bool) ([][2]string, error) {
	var moves [][2]string
	keysToUpdate := map[string]string{}

	for path, e := range m {
		src := path
		if _, err := os.Stat(src); err != nil {
			continue
		}
		var dstDir string
		if isNonGamePath(src) || e.Game == "" ||
			strings.ToLower(e.Game) == "unknown" ||
			strings.ToLower(e.Game) == "n/a" {
			dstDir = filepath.Join(baseDir, "_unsorted")
		} else {
			dstDir = filepath.Join(baseDir, SanitizeFolderName(e.Game), e.Year())
		}
		dst := filepath.Join(dstDir, filepath.Base(src))
		if dst == src {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue // already exists
		}
		moves = append(moves, [2]string{src, dst})
		keysToUpdate[path] = dst
	}

	if !dryRun {
		for _, mv := range moves {
			src, dst := mv[0], mv[1]

			// Identify companion transcript before moving
			txSrc, txDst := "", ""
			if e := m[src]; e != nil && e.TranscriptFile != "" {
				if _, err := os.Stat(e.TranscriptFile); err == nil {
					txSrc = e.TranscriptFile
					txDst = filepath.Join(filepath.Dir(dst), filepath.Base(e.TranscriptFile))
				}
			}

			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return moves, err
			}
			if err := os.Rename(src, dst); err != nil {
				return moves, err
			}
			undoStack = append(undoStack, UndoEntry{Src: src, Dst: dst})

			if txSrc != "" && txDst != "" {
				if err := os.Rename(txSrc, txDst); err == nil {
					undoStack = append(undoStack, UndoEntry{Src: txSrc, Dst: txDst})
				}
			}
		}
		for oldKey, newKey := range keysToUpdate {
			e := m[oldKey]
			delete(m, oldKey)
			e.File = newKey
			if e.TranscriptFile != "" {
				e.TranscriptFile = filepath.Join(filepath.Dir(newKey), filepath.Base(e.TranscriptFile))
			}
			m[newKey] = e
		}
	}
	return moves, nil
}

// DoRename renames files based on their catalog entries.
// Returns count of (would-be) renames.
func DoRename(m Manifest, dryRun bool) (int, [][2]string, error) {
	var renames [][2]string
	keysToUpdate := map[string]string{}

	for path, e := range m {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		newName := SuggestFilename(e, path)
		newPath := filepath.Join(filepath.Dir(path), newName)
		if newPath == path {
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			continue
		}
		renames = append(renames, [2]string{path, newPath})
		keysToUpdate[path] = newPath
	}

	if !dryRun {
		for _, mv := range renames {
			src, dst := mv[0], mv[1]

			// Identify companion transcript before renaming
			txSrc, txDst := "", ""
			if e := m[src]; e != nil && e.TranscriptFile != "" {
				if _, err := os.Stat(e.TranscriptFile); err == nil {
					newStem := strings.TrimSuffix(filepath.Base(dst), filepath.Ext(dst))
					txSrc = e.TranscriptFile
					txDst = filepath.Join(filepath.Dir(dst), newStem+".txt")
				}
			}

			if err := os.Rename(src, dst); err != nil {
				return len(renames), renames, err
			}
			undoStack = append(undoStack, UndoEntry{Src: src, Dst: dst})

			if txSrc != "" && txDst != "" {
				if err := os.Rename(txSrc, txDst); err == nil {
					undoStack = append(undoStack, UndoEntry{Src: txSrc, Dst: txDst})
				}
			}
		}
		for oldKey, newKey := range keysToUpdate {
			e := m[oldKey]
			delete(m, oldKey)
			e.File = newKey
			if e.TranscriptFile != "" {
				oldStem := strings.TrimSuffix(filepath.Base(oldKey), filepath.Ext(oldKey))
				newStem := strings.TrimSuffix(filepath.Base(newKey), filepath.Ext(newKey))
				e.TranscriptFile = strings.ReplaceAll(e.TranscriptFile, oldStem+".txt", newStem+".txt")
			}
			m[newKey] = e
		}
	}
	return len(renames), renames, nil
}

// UndoLast reverses the most recent file operation.
func UndoLast(m Manifest, manifestPath string) (string, error) {
	if len(undoStack) == 0 {
		return "", fmt.Errorf("nothing to undo")
	}
	last := undoStack[len(undoStack)-1]
	undoStack = undoStack[:len(undoStack)-1]

	if _, err := os.Stat(last.Dst); err != nil {
		return "", fmt.Errorf("destination file missing: %s", last.Dst)
	}
	if err := os.MkdirAll(filepath.Dir(last.Src), 0755); err != nil {
		return "", err
	}
	if err := os.Rename(last.Dst, last.Src); err != nil {
		return "", err
	}

	// Update manifest key
	if e, ok := m[last.Dst]; ok {
		delete(m, last.Dst)
		e.File = last.Src
		m[last.Src] = e
	}
	if err := SaveManifest(m, manifestPath); err != nil {
		return "", err
	}
	return fmt.Sprintf("Undone: %s ← %s", filepath.Base(last.Dst), filepath.Dir(last.Src)), nil
}

// imageExts is the set of supported image extensions.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".webp": true, ".bmp": true,
}

// videoExts is the set of supported video extensions.
var videoExts = map[string]bool{
	".mp4": true, ".avi": true, ".mov": true, ".mkv": true,
	".webm": true, ".mp4v": true, ".wmv": true, ".m4v": true, ".flv": true,
}

// ScanImages returns all image files under root, sorted by path.
func ScanImages(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if imageExts[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}
