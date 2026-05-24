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
├── screenshot_catalog.py   # Python backend — LLM calls, organize, rename, dupes, video
├── video.py                # video frame extraction (ffmpeg + opencv fallback)
├── tui.py                  # original Textual TUI (kept as fallback)
└── tui_go/                 # Go + Bubbletea TUI (primary interface)
    ├── main.go
    ├── model.go            # root Bubbletea model + all state logic
    ├── filetree.go         # expandable directory tree
    ├── manifest.go         # JSONL read/write, Entry struct, file ops, SuggestFilename
    ├── operations.go       # organize, rename, undo, CSV export (pure Go)
    ├── catalog.go          # Python subprocess runner + NDJSON streaming
    ├── library.go          # full-screen searchable library view
    ├── stats.go            # stats dashboard + on-this-day view + session tracking
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

### ETA during catalog

`model.go` tracks `catalogStart` and computes a running ETA from `elapsed / progressN * remaining`. Displayed in the status bar as `ETA 3m24s` or `ETA 1h5m` during active catalog runs.

### Session tracking

`ComputeSessions()` in `stats.go` groups manifest entries into play sessions by game, using a 30-minute gap threshold (switching games or >30min idle = new session). Session data is cached in `StatsCache` and displayed in the stats overview and per-game drill-down views.

### StatsCache

`BuildStatsCache()` pre-computes all derived analytics (top games, moods, scenes, monthly, sessions, per-game breakdowns) from the manifest. The cache is built once on launch and refreshed after every catalog run. Stats screens open instantly by using the cache rather than re-scanning entries.

### Stats dashboard drill-down

Press `p` for the overview (summary card, top games, monthly activity, moods, scenes). Press `Enter` on a game to drill into per-game detail (sessions list, per-game scenes & moods with bar charts). `ESC` goes back to overview.

### Filename truncation

`SuggestFilename()` in `manifest.go` now caps individual segments at 28 chars and total filename (sans extension) at 72 chars. Long names are shortened from the middle — the game name and original stem are preserved, middle parts are truncated proportionally.

## Python backend

### Key configuration

| Setting | Where | Purpose |
|---|---|---|
| `api_base` | `settings.json` / `DefaultSettings()` | LM Studio endpoint |
| `model` | `settings.json` / `DefaultSettings()` | Vision model name |
| `max_tokens` | `settings.json` | Must be ≥2048 for thinking models. Default 2048 (minimum) |
| `temperature` | `settings.json` | Default 0.3 |
| `scene_threshold` | `settings.json` | Default 0.3 — ffmpeg scene detection sensitivity |
| `video_interval_short` | `settings.json` | Frame interval for clips <5min (default 10.0s) |
| `video_interval_med` | `settings.json` | Frame interval for clips 5-30min (default 30.0s) |
| `video_interval_long` | `settings.json` | Frame interval for recordings >30min (default 60.0s) |
| `video_length_med_min` | `settings.json` | Minutes threshold: short→medium (default 5) |
| `video_length_long_min` | `settings.json` | Minutes threshold: medium→long (default 30) |
| `PROMPT` | top of `screenshot_catalog.py` | Controls what JSON fields the model returns |

### Architecture phases

1. **Load manifest** (`load_manifest`) — reads existing JSONL into a dict keyed by absolute path; already-processed images are skipped on resume.
2. **Catalog** (`catalog_one` → `parse_response`) — base64 JPEG encode (≤2048px), POST to LM Studio, extract JSON from response. Failures stored as `{"error": ...}`, never crash. Results appended immediately (resume-safe).
3. **Rename** (`do_rename` → `suggest_filename`) — `game_scene_location_stem`, skips if target exists. The Go-side `SuggestFilename` in `manifest.go` handles the actual renaming with length caps.
4. **Organize** (`organize_by_game_year`) — moves to `{game}/{year}/` subdirs, unmapped files go to `_unsorted/`.

### Streaming entry points (called by Go TUI)

- `--stream --dir PATH --manifest PATH [--all] [--files f1 f2 ...]` — NDJSON catalog progress
- `--stream-dupes --manifest PATH [--threshold 8]` — perceptual hash groups
- `--stream-video --file PATH --manifest PATH [--mode auto] [--threshold 0.3] [--interval 5.0]`

The existing CLI path (`--dir`, `--output`, `--rename`, etc.) is unchanged.

## Manifest Entry fields

Each JSONL line represents one `Entry` struct:

| Field | Type | From | Purpose |
|---|---|---|---|
| `file` | string | system | Absolute path to the screenshot |
| `cataloged_at` | string | system | ISO-8601 timestamp when cataloged |
| `game` | string | LLM | Detected game name |
| `scene` | string | LLM | What's happening (combat, menu, cutscene...) |
| `location` | string | LLM | In-game location |
| `mood` | string | LLM | Mood description |
| `characters` | []string | LLM | Visible character names |
| `ui_elements` | []string | LLM | Visible UI components |
| `keywords` | []string | LLM | Search terms |
| `suggested_filename` | string | LLM | Clean filename (truncated by Go SuggestFilename) |
| `error` | string | system | Set on catalog failure |
| `raw_response` | string | system | Raw LLM output on failure (empty/non-JSON) |
| `is_video` | bool | system | True if entry came from video frame extraction |
| `frames_dir` | string | system | Directory containing extracted video frames |
| `transcript_file` | string | system | Path to video transcript text file |

## LM Studio dependency

Requires LM Studio running locally with a vision-capable model loaded. Default endpoint: `http://169.254.83.107:1234/v1` (local network — update in Settings or `settings.json` if the host changes). `api_key` is `"lm-studio"` (LM Studio ignores it). Empty `raw_response` entries in the manifest = model returned no output; check that a vision model is actually loaded.

For thinking/reasoning models (like gemma 4), `max_tokens` must be ≥2048 — the model uses tokens for internal reasoning before producing output. 2048 is the bare minimum; higher values (4096+) give the model more room to reason before generating output.
