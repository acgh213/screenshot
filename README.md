# ✧ screenshot cataloger ✧

okay so. i have like. 40,000 game screenshots and i cannot find anything. ever. i took a screenshot of something cool in destiny 2 in 2021 and it is GONE, lost to a folder called `2021-07` with 847 other files. this tool is my attempt to fix that. it uses a local vision LLM to look at every screenshot and go "hey that's destiny 2, specifically the iron banner, specifically when you got the good shotgun" and then writes that down so you can actually search for it later.

it runs entirely locally. no cloud, no subscription, no uploading your screenshots of whatever you were doing at 2am to a server somewhere. just you, your GPU, and a model that is trying its best.

---

## what it does

- **catalogs screenshots with a local vision model** (LM Studio, OpenAI-compatible) — sends each image, gets back structured metadata: game name, scene, location, mood, characters, keywords
- **searchable library** — full-text search across everything it's ever cataloged
- **organizes by game/year** — moves files into `Destiny 2/2021/` etc so your filesystem actually makes sense
- **renames files descriptively** — `destiny2_Da2ZyA0VLy.png` becomes something actually human
- **stats dashboard** — bar charts of your most-captured games, monthly activity, mood breakdowns (mine is apparently 40% "tense" which. yeah.)
- **on this day** — see what you were playing on this date in previous years. it's a little haunted in a good way
- **duplicate detection** — perceptual hashing to find near-identical screenshots you took 12 times because the first 11 weren't quite right
- **video frame extraction** — pull keyframes from clips using ffmpeg scene detection, then catalog those too
- **undo** — for when you organize everything and immediately regret it
- **export to CSV** — in case you want to do something cursed with the data in excel

---

## the TUI

the main interface is a Go + [Bubbletea](https://github.com/charmbracelet/bubbletea) TUI. it looks like this:

```
 ✧ Screenshot Cataloger ✧ ♥ screenshot_catalog.jsonl             11:08:44
 ▸ 2021-01/ (21)            │  ✓  destiny2_Da2ZyA0VLy.png
 ▾ 2021-02/ (54)            │  Destiny 2  tense
   destiny2_abc.png  ✓ D2   │  post-match victory screen  2021-02-09
   discord_xyz.png   ⚠      │ ─────────────────────────────────────────
   cyberpunk_123.png ✓ CP   │  ████████████████░░░░  67%  Cataloging 8/12
   unknown.png       ○      │  11:08:31  ✓ destiny2_abc.png  →  Destiny 2
 ▸ 2021-03/ (33)            │  11:08:39  ✓ cyberpunk.png  →  Cyberpunk 2077
                             │  11:08:44  ✗ discord.png  [non-game]
──────────────────────────────────────────────────────────────────────────
  c:catalog  C:all  r:rename  o:org  space:queue  l:lib  p:stats  t:✧today
```

no buttons. everything is a keyboard shortcut. files show their status inline (`✓` cataloged · `○` not yet · `⚠` non-game capture). the progress bar has a little shimmer because i thought it was cute.

### keyboard shortcuts

| key | what it does |
|-----|-------------|
| `↑` `↓` | navigate the tree |
| `←` `→` | switch focus between tree / log pane |
| `enter` | expand/collapse directory |
| `space` | toggle folder/file in queue |
| `c` | catalog new files (uses queue if set) |
| `C` | re-catalog everything |
| `r` | rename files based on metadata |
| `o` | organize into game/year folders |
| `v` | extract frames from selected video |
| `R` | re-catalog this specific file |
| `x` | re-catalog all previously failed files |
| `u` / `ctrl+z` | undo last file operation |
| `d` | find duplicates |
| `e` | orphan check (manifest entries with missing files) |
| `E` | export manifest to CSV |
| `l` | library (searchable full-screen view) |
| `p` | stats dashboard |
| `t` | on this day |
| `S` | settings |
| `f` | open selected file in default viewer |
| `O` | reveal in explorer |
| `?` | help |
| `ESC` | cancel running operation / go back |
| `q` | quit |

the queue system is really useful for selective cataloging — space through a few folders you care about, then `c` to catalog just those. handy when you don't want to wait 4 hours for it to process 2021 in full.

---

## setup

### you need

- **Python 3.10+** with these packages:
  ```
  pip install openai pillow tqdm textual opencv-python imagehash send2trash
  ```
- **[LM Studio](https://lmstudio.ai/)** running locally with a vision-capable model loaded. i use `gemma-4-e4b` which is what the defaults are set to. any openai-compatible vision model endpoint works.
- **Go 1.21+** to build the TUI (or just grab the `.exe` from releases)
- **ffmpeg** in PATH for scene-detection frame extraction (optional — falls back to opencv uniform sampling if not available)

### build the TUI

```powershell
cd tui_go
go build -o ..\tui_new.exe .
```

### run it

```powershell
# the good way (bubbletea TUI)
.\tui_new.exe --dir "C:\path\to\Screenshots" --manifest logs\screenshot_catalog.jsonl

# the old way (textual TUI, still works)
python tui.py --dir "C:\path\to\Screenshots" --manifest logs\screenshot_catalog.jsonl

# the terminal way (no UI, just processes and exits)
python screenshot_catalog.py --dir "C:\path\to\Screenshots" --output logs\screenshot_catalog.jsonl
```

the manifest is a JSONL file (one JSON object per line) that grows as you catalog things. it's append-only during runs so if it crashes halfway through, you pick up where you left off. ✨

---

## configuration

first run creates a `settings.json` next to your manifest file. or press `S` in the TUI to edit it live.

```json
{
  "api_base": "http://localhost:1234/v1",
  "model": "gemma-4-e4b",
  "max_tokens": 2048,
  "temperature": 0.3,
  "scene_threshold": 0.3,
  "frame_interval_sec": 5.0
}
```

> **important**: if you're using a thinking/reasoning model (like gemma 4), set `max_tokens` to at least 2048. the model uses tokens for its internal reasoning before outputting anything, and if the budget is too small you get empty responses. i spent an embarrassing amount of time debugging this.

---

## project structure

```
screenshot/
├── screenshot_catalog.py   # backend — catalog, organize, rename, undo, dupes
├── video.py                # video frame extraction (ffmpeg + opencv)
├── tui.py                  # original textual TUI (kept as fallback)
└── tui_go/                 # bubbletea TUI (the one you should use)
    ├── main.go
    ├── model.go            # root bubbletea model + all state logic
    ├── filetree.go         # expandable directory tree
    ├── manifest.go         # JSONL read/write, file operations
    ├── operations.go       # organize, rename, undo, CSV export
    ├── catalog.go          # python subprocess runner + NDJSON streaming
    ├── library.go          # full-screen searchable library
    ├── stats.go            # stats + on this day views
    ├── settings.go         # settings form
    └── styles.go           # all colors and styles (single source of truth)
```

the Go TUI talks to the Python backend via subprocess with NDJSON streaming — Python handles the LLM calls and image encoding (PIL), Go handles everything else. if you want to switch to a fully standalone binary someday, the main things blocking you are PIL for image preprocessing and `imagehash` for perceptual hashing, both of which have Go equivalents.

---

## the manifest format

each line in the JSONL looks like:

```json
{
  "file": "C:\\path\\to\\destiny2_abc.png",
  "cataloged_at": "2026-05-24T11:08:31",
  "game": "Destiny 2",
  "scene": "post-match victory screen",
  "location": "Distant Shore (UI overlay)",
  "mood": "triumphant, victorious",
  "characters": [],
  "ui_elements": ["scoreboard", "victory message", "gear showcase"],
  "keywords": ["pvp", "iron banner", "victory", "scoreboard"],
  "suggested_filename": "destiny2-victory-screen-distant-shore"
}
```

failed entries have `"error"` or `"raw_response"` fields instead. you can re-run just the failures with `x` in the TUI.

---

## notes / known issues

- the model sometimes hallucinates game names. `destiny2_*.png` usually gets correctly identified as Destiny 2 (there's a filename hint in the prompt), but something called `ApplicationFrameHost_xyz.png` will probably get categorized as whatever was in the background. that's what the `⚠` non-game flag is for.

- organizing files is irreversible except for the in-memory undo stack (which clears when you close the app). be careful with `o`. it's not going to delete anything but it will move a lot of files.

- the stats screen says "hours of footage" but it doesn't actually compute that yet. it's aspirational.

- on this day will be empty for a while. give it a year.

---

## why

i have adhd and executive dysfunction and the prospect of manually sorting 40,000 files makes me want to lie down forever. this is the tool i needed to exist so i built it. maybe it's useful for you too. 

if you find it helpful or you're doing something cool with it, i'd genuinely love to know. find me somewhere on the internet.

---

*built with [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lipgloss](https://github.com/charmbracelet/lipgloss), [LM Studio](https://lmstudio.ai/), and a concerning amount of time spent on color palettes at 1am*
