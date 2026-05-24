# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project does

A game screenshot cataloger. `screenshot_catalog.py` scans a directory of screenshots, sends each image to a locally-running LM Studio vision model (OpenAI-compatible API), and writes structured metadata to a JSONL manifest. The primary interface is a Go + Bubbletea TUI (`tui_new.exe`) that calls the Python backend via subprocess. The original Textual TUI (`tui.py`) is kept as a fallback.

## Setup and running

```powershell
# Activate virtual environment (for Python backend)
.\venv\Scripts\Activate.ps1

# Install Python dependencies (if needed)
pip install openai pillow tqdm textual opencv-python imagehash send2trash

# Build the Go TUI (requires Go 1.21+)
cd tui_go
go build -o ..\tui_new.exe .

# Launch the Go TUI (recommended)
.\tui_new.exe --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --manifest logs\screenshot_catalog.jsonl

# Launch the Textual TUI (fallback)
python tui.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --manifest logs\screenshot_catalog.jsonl

# CLI — test on a small batch first
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --limit 10 --output logs\screenshot_catalog.jsonl

# CLI — full run (resumes from existing manifest)
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --output logs\screenshot_catalog.jsonl

# CLI — preview renames without applying them
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --rename --dry-run --output logs\screenshot_catalog.jsonl

# CLI — apply renames
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --rename --output logs\screenshot_catalog.jsonl
```

## Project structure

```
screenshot/
├── screenshot_catalog.py   # Python backend — LLM calls, organize, rename, dupes
├── video.py                # video frame extraction (ffmpeg + opencv fallback)
├── tui.py                  # original Textual TUI (kept as fallback)
└── tui_go/                 # Go + Bubbletea TUI (primary interface)
    ├── main.go
    ├── model.go            # root Bubbletea model + all state logic
    ├── filetree.go         # expandable directory tree
    ├── manifest.go         # JSONL read/write, entry struct, file ops
    ├── operations.go       # organize, rename, undo, CSV export (pure Go)
    ├── catalog.go          # Python subprocess runner + NDJSON streaming
    ├── library.go          # full-screen searchable library view
    ├── stats.go            # stats dashboard + on-this-day view
    ├── settings.go         # settings form + settings.json I/O
    └── styles.go           # all colors and styles (single source of truth)
```

## Go TUI architecture

The Go binary is the UI layer only — it handles file browsing, manifest I/O, organize/rename/stats/CSV, and all keyboard interaction. The two things it delegates to Python are:
- **LLM catalog calls** — PIL image encoding + LM Studio API (OpenAI-compatible)
- **Perceptual duplicate detection** — `imagehash` library

### NDJSON subprocess protocol

The Go TUI launches `screenshot_catalog.py` via `exec.CommandContext` and reads its stdout line-by-line. Python emits one JSON object per line, flushed immediately.

**Catalog** (`--stream`):
```json
{"type":"start","total":68}
{"type":"progress","file":"path","name":"filename","game":"Destiny 2","scene":"combat","status":"ok"}
{"type":"progress","file":"path","name":"filename","status":"error","error":"..."}
{"type":"done","success":65,"fail":3}
```

**Duplicates** (`--stream-dupes`):
```json
{"type":"group","files":["path1","path2"]}
{"type":"done","groups":3}
```

**Video** (`--stream-video`):
```json
{"type":"extract","msg":"ffmpeg scene detection..."}
{"type":"start","total":42}
{"type":"progress",...}
{"type":"done","success":40,"fail":2}
```

ESC during a run calls `context.CancelFunc`, which kills the subprocess immediately.

### Finding Python and the script

`catalog.go` uses `os.Executable()` to get the binary's own path, then resolves relative to that:
- `{binaryDir}/venv/Scripts/python.exe` — venv Python (preferred)
- `python` — system Python (fallback)
- `{binaryDir}/screenshot_catalog.py` — always relative to binary, not CWD

This means the TUI works regardless of what directory it's launched from.

### Multi-dir queue

When multiple dirs are queued, Go scans them itself with `ScanImages()` and passes an explicit file list to Python via `--files f1 f2 f3 ...`. This avoids Python re-scanning the tree and ensures only the right files are processed.

### Styles

`styles.go` is the single source of truth for all colors — warm plum-black base with peach/rose/gold/lavender accent palette. No hex values anywhere except in `styles.go`. If you need a new color, add a token there.

## Python backend

### Key configuration

| Setting | Where | Purpose |
|---|---|---|
| `api_base` | `settings.json` / `DefaultSettings()` | LM Studio endpoint |
| `model` | `settings.json` / `DefaultSettings()` | Vision model name |
| `max_tokens` | `settings.json` | Must be ≥2048 for thinking models |
| `PROMPT` | top of `screenshot_catalog.py` | Controls what JSON fields the model returns |

### Architecture phases

1. **Load manifest** (`load_manifest`) — reads existing JSONL into a dict keyed by absolute path; already-processed images are skipped on resume.
2. **Catalog** (`catalog_one` → `parse_response`) — base64 JPEG encode (≤2048px), POST to LM Studio, extract JSON from response. Failures stored as `{"error": ...}`, never crash. Results appended immediately (resume-safe).
3. **Rename** (`do_rename` → `suggest_filename`) — `game_scene_location_stem`, skips if target exists.
4. **Organize** (`organize_by_game_year`) — moves to `{game}/{year}/` subdirs, unmapped files go to `_unsorted/`.

### Streaming entry points (called by Go TUI)

- `--stream --dir PATH --manifest PATH [--all] [--files f1 f2 ...]` — NDJSON catalog progress
- `--stream-dupes --manifest PATH [--threshold 8]` — perceptual hash groups
- `--stream-video --file PATH --manifest PATH [--mode auto] [--threshold 0.3] [--interval 5.0]`

The existing CLI path (`--dir`, `--output`, `--rename`, etc.) is unchanged.

## LM Studio dependency

Requires LM Studio running locally with a vision-capable model loaded. Default endpoint: `http://169.254.83.107:1234/v1` (local network — update in Settings or `settings.json` if the host changes). `api_key` is `"lm-studio"` (LM Studio ignores it). Empty `raw_response` entries in the manifest = model returned no output; check that a vision model is actually loaded.

For thinking/reasoning models (like gemma 4), `max_tokens` must be ≥2048 — the model uses tokens for internal reasoning before producing output.
