# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project does

`screenshot_catalog.py` is a single-file tool that scans a directory of game screenshots, sends each image to a locally-running LM Studio instance (OpenAI-compatible API), and writes structured metadata to a JSONL manifest. It also supports renaming files based on the generated metadata.

## Setup and running

```powershell
# Activate virtual environment
.\venv\Scripts\Activate.ps1

# Install dependencies (if needed)
pip install openai pillow tqdm

# Test on a small batch first
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --limit 10

# Full run (resumes from existing manifest — already-cataloged files are skipped)
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots"

# Preview renames without applying them
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --rename --dry-run

# Apply renames
python screenshot_catalog.py --dir "C:\Users\Cassie\Documents\ShareX\Screenshots" --rename
```

## Key configuration (top of script)

| Constant | Purpose |
|---|---|
| `API_BASE` | LM Studio endpoint — change if the host IP shifts |
| `MODEL` | Model name as shown in LM Studio (currently `gemma-4-e4b`) |
| `MANIFEST_NAME` | Output JSONL file (default: `screenshot_catalog.jsonl`) |
| `PROMPT` | The vision prompt — controls what JSON fields are returned |

## Architecture

The script has four logical phases, all in `main()`:

1. **Load manifest** (`load_manifest`) — reads the existing JSONL into a dict keyed by absolute file path, so already-processed images are skipped on resume.
2. **Catalog** (`catalog_one` → `parse_response`) — encodes each image as base64 JPEG (resized to ≤2048px), sends it to the LM Studio chat completions endpoint, and extracts the JSON block from the raw response. Failures are stored as `{"error": ...}` rather than crashing. Results are appended to the JSONL immediately (resume-safe).
3. **Rename** (`do_rename` → `suggest_filename`) — builds a new filename from `game_scene_location_originalStem` and renames in-place. Skips if the target already exists.
4. **Output** — `screenshot_catalog.jsonl` — one JSON object per line; each record has `file`, `cataloged_at`, plus whatever fields the model returned.

## LM Studio dependency

The script requires LM Studio running locally with a vision-capable model loaded. The current API base is `http://169.254.83.107:1234/v1` (a local network address — update `API_BASE` if the host changes). The `api_key` is hardcoded as `"lm-studio"` (LM Studio ignores it). Empty `raw_response` entries in the manifest indicate the model returned no output — check that LM Studio has a vision model loaded and the endpoint is reachable.
