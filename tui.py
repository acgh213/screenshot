#!/usr/bin/env python3
"""
Screenshot Cataloger TUI — interactive interface for cataloging, organizing,
and browsing game screenshots.

Usage:
  python tui.py --dir "C:\\Users\\Cassie\\Documents\\ShareX\\Screenshots"
  python tui.py --dir <path> --manifest logs\\screenshot_catalog.jsonl

Keyboard shortcuts (main screen):
  Tab / Shift+Tab  — cycle focus between panels
  \\ (backslash)   — jump focus to file tree
  ]                — jump focus to action buttons
  ↑ / ↓           — navigate buttons when right panel is focused
  q                — toggle selected path in/out of queue
  o                — open selected file in default viewer
  l                — Library screen
  f                — Find duplicates
  p                — Stats dashboard
  t                — On This Day
  Ctrl+,           — Settings
  Ctrl+Z           — Undo last file operation
  Ctrl+Q           — Quit
"""

import argparse
import csv
import datetime
import json
import os
import subprocess
import threading
from collections import Counter
from pathlib import Path

from openai import OpenAI
from textual import events, work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, ScrollableContainer, Vertical
from textual.reactive import reactive
from textual.screen import ModalScreen, Screen
from textual.widgets import (
    Button,
    DataTable,
    DirectoryTree,
    Footer,
    Header,
    Input,
    Label,
    ProgressBar,
    RichLog,
    Static,
)

import screenshot_catalog as sc
from screenshot_catalog import (
    IMAGE_EXTS,
    MANIFEST_NAME,
    catalog_batch,
    find_duplicates,
    is_non_game_capture,
    load_manifest,
    organize_by_game_year,
    save_manifest,
    undo_last,
    undo_stack,
)
from video import VIDEO_EXTS, extract_frames

try:
    from send2trash import send2trash
    _HAS_TRASH = True
except ImportError:
    _HAS_TRASH = False

_SETTINGS_FILE = "settings.json"


# ── Module-level helpers ──────────────────────────────────────────────────────

def _file_mtime(path: Path) -> datetime.datetime | None:
    try:
        return datetime.datetime.fromtimestamp(path.stat().st_mtime)
    except (OSError, ValueError):
        return None


def _compute_sessions(
    filepaths: list[str], gap_minutes: int = 30
) -> dict[str, int]:
    timed: list[tuple[str, datetime.datetime]] = []
    for fp in filepaths:
        mt = _file_mtime(Path(fp))
        if mt:
            timed.append((fp, mt))
    timed.sort(key=lambda x: x[1])
    sessions: dict[str, int] = {}
    session_num = 0
    prev_time: datetime.datetime | None = None
    for fp, mtime in timed:
        if prev_time is None or (mtime - prev_time).total_seconds() > gap_minutes * 60:
            session_num += 1
        sessions[fp] = session_num
        prev_time = mtime
    return sessions


def _bar(value: int, max_val: int, width: int = 28) -> str:
    if max_val == 0:
        return " " * width
    filled = int(width * value / max_val)
    return "█" * filled + "░" * (width - filled)


def _load_settings(settings_path: Path) -> dict:
    if settings_path.exists():
        try:
            return json.loads(settings_path.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {}


def _apply_settings(s: dict) -> None:
    """Push settings dict onto screenshot_catalog module globals."""
    if "api_base" in s:
        sc.API_BASE = s["api_base"]
    if "model" in s:
        sc.MODEL = s["model"]
    if "max_tokens" in s:
        sc.MAX_TOKENS = int(s["max_tokens"])
    if "temperature" in s:
        sc.TEMPERATURE = float(s["temperature"])


def _save_settings(settings_path: Path, s: dict) -> None:
    settings_path.write_text(json.dumps(s, indent=2), encoding="utf-8")


# ── CSS ───────────────────────────────────────────────────────────────────────

APP_CSS = """
Screen {
    background: $surface;
}

Header {
    background: $primary-darken-2;
}

/* ── Main layout ── */
#body {
    layout: vertical;
    height: 1fr;
}

#upper {
    layout: horizontal;
    height: 1fr;
}

#tree-panel {
    width: 32%;
    border-right: thick $primary-darken-3;
    padding: 0;
}

#tree-panel DirectoryTree {
    height: 1fr;
}

#right-panel {
    width: 68%;
    layout: vertical;
    padding: 1 2;
    overflow-y: auto;
}

#file-info {
    height: 4;
    border: round $primary-darken-2;
    padding: 0 1;
    margin-bottom: 1;
    content-align: left middle;
    color: $text-muted;
}

#file-info.cataloged {
    border: round $success-darken-1;
    color: $text;
}

#file-info.non-game {
    border: round $warning;
    color: $warning;
}

/* ── Queue area ── */
#queue-area {
    height: auto;
    layout: horizontal;
    margin-bottom: 1;
    display: none;
}

#queue-area.has-items {
    display: block;
    layout: horizontal;
}

#queue-label {
    width: 1fr;
    content-align: left middle;
    color: $warning;
    text-style: bold;
}

#btn-clear-queue {
    width: auto;
    min-width: 14;
}

/* ── Actions grid ── */
#actions {
    layout: grid;
    grid-size: 2;
    grid-gutter: 1 2;
    height: auto;
    margin-bottom: 1;
}

#actions Button {
    width: 1fr;
}

/* ── Context-sensitive buttons ── */
#btn-recatalog {
    margin-bottom: 1;
    display: none;
}

#btn-recatalog.visible {
    display: block;
}

#btn-cancel {
    margin-bottom: 1;
    display: none;
    background: $error-darken-2;
}

#btn-cancel.visible {
    display: block;
}

#btn-cancel:hover {
    background: $error;
}

#progress-bar {
    margin-bottom: 1;
    display: none;
}

#progress-bar.visible {
    display: block;
}

/* ── Log panel ── */
#log-panel {
    height: 9;
    border-top: thick $primary-darken-3;
    background: $surface-darken-1;
}

/* ── Library screen ── */
#library-header {
    height: 3;
    layout: horizontal;
    padding: 0 1;
    background: $primary-darken-3;
    border-bottom: thick $primary-darken-2;
}

#library-header Input {
    width: 1fr;
    margin-right: 1;
}

#library-header Label {
    width: auto;
    content-align: left middle;
    padding: 0 1;
}

#session-toggle {
    width: auto;
    min-width: 18;
    margin-left: 1;
}

#library-table {
    height: 1fr;
}

Button.active {
    background: $success-darken-2;
}

/* ── Stats screen ── */
#stats-scroll {
    height: 1fr;
}

#stats-body {
    padding: 1 3;
    height: auto;
}

/* ── On This Day screen ── */
#otd-header {
    height: 3;
    padding: 0 2;
    background: $primary-darken-3;
    content-align: left middle;
    color: $warning;
    text-style: bold;
    border-bottom: thick $primary-darken-2;
}

#otd-table {
    height: 1fr;
}

/* ── Dupes screen ── */
#dupes-scroll {
    height: 1fr;
    padding: 1 2;
}

.dupe-group {
    border: round $warning-darken-2;
    margin-bottom: 1;
    padding: 0 1;
    height: auto;
}

.dupe-group Label {
    color: $warning;
    text-style: bold;
}

/* ── Confirm modal ── */
ConfirmModal {
    align: center middle;
}

#confirm-dialog {
    width: 60;
    height: auto;
    border: thick $primary;
    background: $surface;
    padding: 1 2;
}

#confirm-dialog Label {
    margin-bottom: 1;
}

#confirm-buttons {
    layout: horizontal;
    height: auto;
    margin-top: 1;
}

#confirm-buttons Button {
    width: 1fr;
    margin: 0 1;
}

/* ── Settings modal ── */
SettingsModal {
    align: center middle;
}

#settings-dialog {
    width: 64;
    height: auto;
    border: thick $primary;
    background: $surface;
    padding: 1 2;
}

#settings-dialog Label.field-label {
    color: $text-muted;
    margin-top: 1;
}

#settings-buttons {
    layout: horizontal;
    height: auto;
    margin-top: 1;
}

#settings-buttons Button {
    width: 1fr;
    margin: 0 1;
}
"""


# ── Modals ────────────────────────────────────────────────────────────────────

class ConfirmModal(ModalScreen):
    def __init__(self, message: str, preview_lines: list[str] | None = None):
        super().__init__()
        self._message = message
        self._preview = preview_lines or []

    def compose(self) -> ComposeResult:
        text = self._message
        if self._preview:
            shown = self._preview[:12]
            extra = len(self._preview) - len(shown)
            text += "\n" + "\n".join(f"  {l}" for l in shown)
            if extra:
                text += f"\n  … and {extra} more"
        with Vertical(id="confirm-dialog"):
            yield Label(text)
            with Horizontal(id="confirm-buttons"):
                yield Button("Proceed", id="yes", variant="primary")
                yield Button("Cancel", id="no")

    def on_button_pressed(self, event: Button.Pressed) -> None:
        self.dismiss(event.button.id == "yes")


class SettingsModal(ModalScreen):
    def compose(self) -> ComposeResult:
        with Vertical(id="settings-dialog"):
            yield Label("⚙  Settings", markup=False)
            yield Label("API Base URL", classes="field-label", markup=False)
            yield Input(value=sc.API_BASE, id="s-api-base")
            yield Label("Model", classes="field-label", markup=False)
            yield Input(value=sc.MODEL, id="s-model")
            yield Label("Max Tokens", classes="field-label", markup=False)
            yield Input(value=str(sc.MAX_TOKENS), id="s-max-tokens")
            yield Label("Temperature  (0.0 – 1.0)", classes="field-label", markup=False)
            yield Input(value=str(sc.TEMPERATURE), id="s-temperature")
            yield Label("Scene-change threshold  (0.0 – 1.0)", classes="field-label", markup=False)
            yield Input(value="0.3", id="s-scene-threshold")
            yield Label("Frame interval  (seconds, uniform mode)", classes="field-label", markup=False)
            yield Input(value="5.0", id="s-frame-interval")
            with Horizontal(id="settings-buttons"):
                yield Button("Save", id="save", variant="primary")
                yield Button("Cancel", id="cancel")

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "cancel":
            self.dismiss(None)
            return
        s = {
            "api_base": self.query_one("#s-api-base", Input).value.strip(),
            "model": self.query_one("#s-model", Input).value.strip(),
            "max_tokens": self.query_one("#s-max-tokens", Input).value.strip(),
            "temperature": self.query_one("#s-temperature", Input).value.strip(),
            "scene_threshold": self.query_one("#s-scene-threshold", Input).value.strip(),
            "frame_interval_sec": self.query_one("#s-frame-interval", Input).value.strip(),
        }
        self.dismiss(s)


# ── Library screen ────────────────────────────────────────────────────────────

class LibraryScreen(Screen):
    BINDINGS = [
        Binding("escape", "app.pop_screen", "Back"),
        Binding("enter", "reveal", "Open in Explorer"),
        Binding("o", "open_file", "Open file"),
        Binding("s", "toggle_sessions", "Session view"),
    ]

    def __init__(self, manifest: dict):
        super().__init__()
        self._manifest = manifest
        self._all_rows: list[str] = []
        self._sessions_on = False

    def compose(self) -> ComposeResult:
        yield Header(show_clock=False)
        with Horizontal(id="library-header"):
            yield Label("🔍")
            yield Input(placeholder="Search game, scene, keyword…", id="search")
            yield Label(f"{len(self._manifest)} items", id="count-label")
            yield Button("⏱ Sessions", id="session-toggle")
        yield DataTable(id="library-table", cursor_type="row", zebra_stripes=True)
        yield Footer()

    def on_mount(self) -> None:
        table = self.query_one(DataTable)
        table.add_columns("Game", "Scene", "Location", "Date", "Status", "File")
        self._populate()

    def _populate(self, filter_text: str = "") -> None:
        table = self.query_one(DataTable)
        table.clear()
        self._all_rows = []
        ft = filter_text.lower()

        sessions: dict[str, int] = {}
        if self._sessions_on:
            sessions = _compute_sessions(list(self._manifest.keys()))

        items = list(self._manifest.items())
        if self._sessions_on:
            def _sort_key(item: tuple) -> tuple:
                fp, _ = item
                return (sessions.get(fp, 999999), _file_mtime(Path(fp)) or datetime.datetime.min)
            items.sort(key=_sort_key)

        for filepath, entry in items:
            game = entry.get("game", "—")
            scene = entry.get("scene", "—")
            location = entry.get("location", "—")
            date = entry.get("cataloged_at", "")[:10]
            status = "⚠" if ("error" in entry or "raw_response" in entry) else "✓"
            searchable = f"{game} {scene} {location} {filepath}".lower()
            if ft and ft not in searchable:
                continue
            if self._sessions_on:
                sn = sessions.get(filepath)
                row = (f"#{sn:02d}" if sn else "—", game, scene, location, date, status, Path(filepath).name)
            else:
                row = (game, scene, location, date, status, Path(filepath).name)
            self._all_rows.append(filepath)
            table.add_row(*row)

        self.query_one("#count-label", Label).update(f"{table.row_count} items")

    def action_toggle_sessions(self) -> None:
        self._sessions_on = not self._sessions_on
        btn = self.query_one("#session-toggle", Button)
        table = self.query_one(DataTable)
        table.clear(columns=True)
        if self._sessions_on:
            table.add_columns("Session", "Game", "Scene", "Location", "Date", "Status", "File")
            btn.add_class("active")
        else:
            table.add_columns("Game", "Scene", "Location", "Date", "Status", "File")
            btn.remove_class("active")
        self._populate(self.query_one("#search", Input).value)

    def on_button_pressed(self, event: Button.Pressed) -> None:
        if event.button.id == "session-toggle":
            self.action_toggle_sessions()

    def on_input_changed(self, event: Input.Changed) -> None:
        self._populate(event.value)

    def action_reveal(self) -> None:
        table = self.query_one(DataTable)
        if 0 <= table.cursor_row < len(self._all_rows):
            p = Path(self._all_rows[table.cursor_row])
            if p.exists():
                subprocess.Popen(["explorer", "/select,", str(p)])

    def action_open_file(self) -> None:
        table = self.query_one(DataTable)
        if 0 <= table.cursor_row < len(self._all_rows):
            p = Path(self._all_rows[table.cursor_row])
            if p.exists():
                os.startfile(str(p))


# ── Stats screen ──────────────────────────────────────────────────────────────

class StatsScreen(Screen):
    BINDINGS = [Binding("escape", "app.pop_screen", "Back")]

    def __init__(self, manifest: dict):
        super().__init__()
        self._manifest = manifest

    def compose(self) -> ComposeResult:
        yield Header(show_clock=False)
        with ScrollableContainer(id="stats-scroll"):
            yield Static(id="stats-body")
        yield Footer()

    def on_mount(self) -> None:
        entries = list(self._manifest.values())
        total = len(entries)
        if total == 0:
            self.query_one("#stats-body", Static).update("[dim]No entries yet.[/dim]")
            return

        games = Counter(e.get("game") or "Unknown" for e in entries)
        moods = Counter(
            (e.get("mood") or "").split(",")[0].strip().lower()
            for e in entries if e.get("mood")
        )
        scenes = Counter(
            (e.get("scene") or "").split(",")[0].split("-")[0].strip().lower()
            for e in entries if e.get("scene")
        )
        monthly: Counter = Counter()
        for e in entries:
            mt = _file_mtime(Path(e["file"]))
            key = mt.strftime("%Y-%m") if mt else e.get("cataloged_at", "")[:7]
            if key:
                monthly[key] += 1

        lines: list[str] = []
        BAR = 28
        lines.append(
            f"\n[bold cyan]Archive Statistics[/bold cyan]  [dim]({total} entries)[/dim]\n"
        )

        lines.append("[bold white]Top Games[/bold white]")
        top_g = games.most_common(15)
        max_g = top_g[0][1] if top_g else 1
        for game, count in top_g:
            pct = count / total * 100
            lines.append(
                f"  [cyan]{game:<28}[/cyan] [green]{_bar(count, max_g, BAR)}[/green]"
                f"  [dim]{count:>4} ({pct:.0f}%)[/dim]"
            )
        lines.append("")

        if monthly:
            lines.append("[bold white]Monthly Activity[/bold white]")
            sorted_months = sorted(monthly)[-24:]
            max_m = max(monthly[m] for m in sorted_months) or 1
            for month in sorted_months:
                count = monthly[month]
                lines.append(
                    f"  [dim]{month}[/dim]  [blue]{_bar(count, max_m, BAR)}[/blue]  [dim]{count}[/dim]"
                )
            lines.append("")

        if moods:
            lines.append("[bold white]Moods[/bold white]")
            top_m = moods.most_common(10)
            max_mo = top_m[0][1] if top_m else 1
            for mood, count in top_m:
                lines.append(
                    f"  [magenta]{mood:<22}[/magenta] [magenta]{_bar(count, max_mo, 20)}[/magenta]"
                    f"  [dim]{count}[/dim]"
                )
            lines.append("")

        if scenes:
            lines.append("[bold white]Top Scenes[/bold white]")
            top_sc = [(s, c) for s, c in scenes.most_common(10) if s]
            max_sc = top_sc[0][1] if top_sc else 1
            for scene, count in top_sc:
                lines.append(
                    f"  [yellow]{scene:<22}[/yellow] [yellow]{_bar(count, max_sc, 20)}[/yellow]"
                    f"  [dim]{count}[/dim]"
                )

        self.query_one("#stats-body", Static).update("\n".join(lines))


# ── On This Day screen ────────────────────────────────────────────────────────

class OnThisDayScreen(Screen):
    BINDINGS = [
        Binding("escape", "app.pop_screen", "Back"),
        Binding("enter", "reveal", "Open in Explorer"),
        Binding("o", "open_file", "Open file"),
    ]

    def __init__(self, manifest: dict):
        super().__init__()
        self._manifest = manifest
        self._paths: list[str] = []

    def compose(self) -> ComposeResult:
        today = datetime.date.today()
        yield Header(show_clock=False)
        yield Static(
            f"  📅  On This Day — {today.strftime('%B')} {today.day}  [dim](across all years)[/dim]",
            id="otd-header",
        )
        yield DataTable(id="otd-table", cursor_type="row", zebra_stripes=True)
        yield Footer()

    def on_mount(self) -> None:
        today = datetime.date.today()
        table = self.query_one(DataTable)
        table.add_columns("Year", "Game", "Scene", "Location", "File")
        rows: list[tuple] = []
        for filepath, entry in self._manifest.items():
            mt = _file_mtime(Path(filepath))
            if mt and mt.month == today.month and mt.day == today.day:
                rows.append((
                    str(mt.year),
                    entry.get("game", "—"),
                    entry.get("scene", "—"),
                    entry.get("location", "—"),
                    Path(filepath).name,
                    filepath,
                ))
        rows.sort(key=lambda r: r[0])
        for row in rows:
            table.add_row(*row[:5])
            self._paths.append(row[5])
        if not rows:
            table.add_row("—", "No screenshots found for today's date.", "", "", "")

    def action_reveal(self) -> None:
        table = self.query_one(DataTable)
        if 0 <= table.cursor_row < len(self._paths):
            p = Path(self._paths[table.cursor_row])
            if p.exists():
                subprocess.Popen(["explorer", "/select,", str(p)])

    def action_open_file(self) -> None:
        table = self.query_one(DataTable)
        if 0 <= table.cursor_row < len(self._paths):
            p = Path(self._paths[table.cursor_row])
            if p.exists():
                os.startfile(str(p))


# ── Dupes screen ──────────────────────────────────────────────────────────────

class DupesScreen(Screen):
    BINDINGS = [
        Binding("escape", "app.pop_screen", "Back"),
        Binding("d", "delete_marked", "Delete marked"),
    ]

    def __init__(self, groups: list[list[str]], manifest: dict, manifest_path: Path):
        super().__init__()
        self._groups = groups
        self._manifest = manifest
        self._manifest_path = manifest_path
        self._marked: set[str] = set()

    def compose(self) -> ComposeResult:
        yield Header(show_clock=False)
        if not self._groups:
            yield Label("  No duplicates found.")
        else:
            with ScrollableContainer(id="dupes-scroll"):
                for i, group in enumerate(self._groups):
                    with Vertical(classes="dupe-group"):
                        yield Label(f"Group {i + 1} — {len(group)} similar files")
                        for fp in group:
                            yield Button(
                                f"  {'[MARK] ' if fp in self._marked else '       '}{Path(fp).name}",
                                id=f"dupe-{abs(hash(fp))}",
                                name=fp,
                            )
        yield Footer()

    def on_button_pressed(self, event: Button.Pressed) -> None:
        fp = event.button.name
        if fp:
            if fp in self._marked:
                self._marked.discard(fp)
            else:
                self._marked.add(fp)
            event.button.label = (
                f"  {'[MARK] ' if fp in self._marked else '       '}{Path(fp).name}"
            )

    async def action_delete_marked(self) -> None:
        if not self._marked:
            return
        if not _HAS_TRASH:
            self.notify("send2trash not installed", severity="error")
            return

        def _do_delete(confirmed: bool) -> None:
            if not confirmed:
                return
            deleted = 0
            for fp in list(self._marked):
                p = Path(fp)
                if p.exists():
                    send2trash(str(p))
                    self._manifest.pop(fp, None)
                    deleted += 1
            save_manifest(self._manifest, self._manifest_path)
            self.notify(f"Moved {deleted} files to Recycle Bin")
            self._marked.clear()
            self.app.pop_screen()

        await self.app.push_screen(
            ConfirmModal(
                f"Move {len(self._marked)} marked files to Recycle Bin?",
                [Path(fp).name for fp in self._marked],
            ),
            _do_delete,
        )


# ── Main app ──────────────────────────────────────────────────────────────────

class ScreenCatalogApp(App):
    TITLE = "Screenshot Cataloger"
    CSS = APP_CSS

    BINDINGS = [
        Binding("ctrl+z", "undo", "Undo"),
        Binding("l", "library", "Library"),
        Binding("f", "find_dupes", "Dupes"),
        Binding("p", "stats", "Stats"),
        Binding("t", "on_this_day", "On This Day"),
        Binding("q", "toggle_queue", "Queue"),
        Binding("o", "open_file", "Open"),
        Binding("backslash", "focus_tree", "Tree"),
        Binding("ctrl+comma", "settings", "Settings"),
        Binding("ctrl+q", "quit", "Quit"),
    ]

    _busy: reactive[bool] = reactive(False)
    _selected: reactive[str] = reactive("")

    def __init__(self, scan_dir: Path, manifest_path: Path):
        super().__init__()
        self.scan_dir = scan_dir
        self.manifest_path = manifest_path
        self.settings_path = manifest_path.parent / _SETTINGS_FILE
        self._queue: set[str] = set()
        self._cancel_event = threading.Event()
        self._catalog_worker = None

        # Load saved settings before creating the client
        s = _load_settings(self.settings_path)
        _apply_settings(s)

        self.manifest = load_manifest(manifest_path)
        self.client = OpenAI(base_url=sc.API_BASE, api_key="lm-studio")

    # ── Layout ────────────────────────────────────────────

    def compose(self) -> ComposeResult:
        yield Header(show_clock=True)
        with Vertical(id="body"):
            with Horizontal(id="upper"):
                with Vertical(id="tree-panel"):
                    yield DirectoryTree(str(self.scan_dir))
                with Vertical(id="right-panel"):
                    yield Static("Select a file or folder from the tree.", id="file-info")
                    # Queue indicator
                    with Horizontal(id="queue-area"):
                        yield Static("", id="queue-label")
                        yield Button("✕ Clear", id="btn-clear-queue", variant="warning")
                    # Main action grid
                    with Vertical(id="actions"):
                        yield Button("📥 Catalog New", id="btn-catalog-new", variant="primary")
                        yield Button("🔄 Catalog All", id="btn-catalog-all")
                        yield Button("✏️  Rename Files", id="btn-rename")
                        yield Button("📁 Organize by Game/Year", id="btn-organize")
                        yield Button("🎬 Extract Video Frames", id="btn-video")
                        yield Button("↩  Undo", id="btn-undo")
                        yield Button("📊 Stats Dashboard", id="btn-stats")
                        yield Button("📅 On This Day", id="btn-otd")
                        yield Button("🔍 Orphan Check", id="btn-orphan-check")
                        yield Button("🔁 Re-catalog Failures", id="btn-recatalog-failed")
                        yield Button("📄 Export CSV", id="btn-export-csv")
                        yield Button("⚙  Settings", id="btn-settings")
                    # Context-sensitive buttons
                    yield Button("↺  Re-catalog This File", id="btn-recatalog")
                    yield Button("⏹  Cancel", id="btn-cancel")
                    yield ProgressBar(id="progress-bar", total=100, show_eta=False)
            yield RichLog(id="log-panel", highlight=True, markup=True, wrap=True)
        yield Footer()

    def on_mount(self) -> None:
        self._log(f"[dim]Manifest: {self.manifest_path.absolute()} ({len(self.manifest)} entries)[/dim]")
        self._log(f"[dim]Scan dir: {self.scan_dir}[/dim]")
        self._log("[dim]Tip: press \\ to focus tree · ] to focus actions · q to queue folders[/dim]")

    # ── Reactivity ────────────────────────────────────────

    def watch__busy(self, busy: bool) -> None:
        for btn_id in ("btn-catalog-new", "btn-catalog-all", "btn-rename",
                       "btn-organize", "btn-video", "btn-recatalog-failed", "btn-recatalog"):
            self.query_one(f"#{btn_id}", Button).disabled = busy

        cancel_btn = self.query_one("#btn-cancel", Button)
        bar = self.query_one("#progress-bar", ProgressBar)
        if busy:
            cancel_btn.add_class("visible")
            bar.add_class("visible")
        else:
            cancel_btn.remove_class("visible")
            bar.remove_class("visible")

    def watch__selected(self, path: str) -> None:
        info = self.query_one("#file-info", Static)
        info.remove_class("cataloged", "non-game")
        recatalog_btn = self.query_one("#btn-recatalog", Button)
        recatalog_btn.remove_class("visible")

        if not path:
            info.update("Select a file or folder from the tree.")
            return
        p = Path(path)
        if p.is_dir():
            count = sum(
                1 for f in p.rglob("*")
                if f.suffix.lower() in IMAGE_EXTS | VIDEO_EXTS and f.is_file()
            )
            queued = "  [yellow][queued][/yellow]" if path in self._queue else ""
            info.update(f"📁 {p.name}  ({count} media files){queued}")
            return

        key = str(p)
        entry = self.manifest.get(key) or self.manifest.get(str(p.resolve()))
        if entry:
            game = entry.get("game", "?")
            scene = entry.get("scene", "?")
            date = entry.get("cataloged_at", "")[:10]
            if is_non_game_capture(p):
                info.update(f"⚠  {p.name}\n[non-game capture]")
                info.add_class("non-game")
            else:
                info.update(f"✓  {p.name}\n{game} · {scene} · {date}")
                info.add_class("cataloged")
            recatalog_btn.add_class("visible")
        else:
            info.update(f"○  {p.name}\n[not yet cataloged]")

    # ── Key handling ──────────────────────────────────────

    def on_key(self, event: events.Key) -> None:
        focused = self.focused
        if isinstance(focused, Button):
            if event.key == "down":
                self.screen.focus_next()
                event.stop()
            elif event.key == "up":
                self.screen.focus_previous()
                event.stop()
            elif event.key == "right":
                # Right arrow from button panel jumps back to tree
                self.action_focus_tree()
                event.stop()
        elif isinstance(focused, DirectoryTree):
            if event.key == "right":
                self.action_focus_actions()
                event.stop()

    # ── Tree events ───────────────────────────────────────

    def on_directory_tree_file_selected(self, event: DirectoryTree.FileSelected) -> None:
        self._selected = str(event.path)

    def on_directory_tree_directory_selected(self, event: DirectoryTree.DirectorySelected) -> None:
        self._selected = str(event.path)

    # ── Button events ─────────────────────────────────────

    def on_button_pressed(self, event: Button.Pressed) -> None:
        match event.button.id:
            case "btn-catalog-new":
                self._catalog(all_files=False)
            case "btn-catalog-all":
                self._catalog(all_files=True)
            case "btn-rename":
                self._rename()
            case "btn-organize":
                self._organize()
            case "btn-video":
                self._extract_video()
            case "btn-undo":
                self.action_undo()
            case "btn-stats":
                self.action_stats()
            case "btn-otd":
                self.action_on_this_day()
            case "btn-orphan-check":
                self._orphan_check()
            case "btn-recatalog-failed":
                self._recatalog_failed()
            case "btn-export-csv":
                self._export_csv()
            case "btn-settings":
                self.action_settings()
            case "btn-recatalog":
                self._recatalog_selected()
            case "btn-cancel":
                self._cancel_event.set()
                self._log("[yellow]Cancel requested — finishing current image…[/yellow]")
            case "btn-clear-queue":
                self._queue.clear()
                self._update_queue_ui()

    # ── Focus actions ─────────────────────────────────────

    def action_focus_tree(self) -> None:
        self.query_one(DirectoryTree).focus()

    def action_focus_actions(self) -> None:
        self.query_one("#btn-catalog-new", Button).focus()

    # ── Queue ─────────────────────────────────────────────

    def action_toggle_queue(self) -> None:
        if not self._selected:
            return
        if self._selected in self._queue:
            self._queue.discard(self._selected)
            self._log(f"[dim]Removed from queue: {Path(self._selected).name}[/dim]")
        else:
            self._queue.add(self._selected)
            self._log(f"[cyan]Queued: {Path(self._selected).name}[/cyan]")
        self._update_queue_ui()
        # Refresh info panel to show/hide [queued] tag
        if self._selected:
            self._selected = self._selected

    def _update_queue_ui(self) -> None:
        area = self.query_one("#queue-area")
        label = self.query_one("#queue-label", Static)
        if self._queue:
            label.update(f"Queue: {len(self._queue)} path(s)")
            area.add_class("has-items")
        else:
            label.update("")
            area.remove_class("has-items")

    # ── Catalog ───────────────────────────────────────────

    def _catalog(self, all_files: bool) -> None:
        if self._queue:
            targets = list(self._queue)
        elif self._selected:
            targets = [self._selected]
        else:
            targets = [str(self.scan_dir)]

        all_imgs: list[Path] = []
        for t in targets:
            p = Path(t)
            scan = p if p.is_dir() else p.parent
            all_imgs.extend(
                f for f in scan.rglob("*")
                if f.suffix.lower() in IMAGE_EXTS and f.is_file()
            )
        all_imgs = sorted(set(all_imgs))

        images = all_imgs if all_files else [
            f for f in all_imgs
            if str(f) not in self.manifest and str(f.resolve()) not in self.manifest
        ]
        if not images:
            self._log("[green]Nothing new to catalog.[/green]")
            return

        queue_note = f" across {len(targets)} queued path(s)" if self._queue else ""
        self._log(f"[cyan]Cataloging {len(images)} image(s){queue_note}…[/cyan]")
        self._cancel_event.clear()
        self._catalog_worker = self._run_catalog(images)

    @work(thread=True)
    def _run_catalog(self, images: list) -> None:
        self.app.call_from_thread(setattr, self, "_busy", True)
        bar = self.query_one("#progress-bar", ProgressBar)
        self.app.call_from_thread(bar.update, total=len(images), progress=0)
        done = [0]

        def _progress(msg: str) -> None:
            done[0] += 1
            self.app.call_from_thread(self._log, msg)
            self.app.call_from_thread(bar.update, progress=done[0])

        success, fail = catalog_batch(
            images, self.manifest_path, self.client, self.manifest,
            on_progress=_progress,
            cancel_check=self._cancel_event.is_set,
        )
        self.app.call_from_thread(
            self._log, f"[green]Done: {success} cataloged, {fail} failed.[/green]"
        )
        self.app.call_from_thread(self._cancel_event.clear)
        self.app.call_from_thread(setattr, self, "_busy", False)
        self.app.call_from_thread(self._refresh_tree)

    # ── Rename ────────────────────────────────────────────

    def _rename(self) -> None:
        from screenshot_catalog import do_rename, suggest_filename
        target = Path(self._selected) if self._selected else self.scan_dir
        scan = target if target.is_dir() else target.parent
        relevant = {k: v for k, v in self.manifest.items() if Path(k).is_relative_to(scan)}
        if not relevant:
            self._log("[yellow]No cataloged files in selected folder.[/yellow]")
            return
        preview = [
            f"{Path(fp).name}  →  {suggest_filename(entry, Path(fp))}"
            for fp, entry in relevant.items()
            if Path(fp).exists()
            and (Path(fp).parent / suggest_filename(entry, Path(fp))).resolve() != Path(fp).resolve()
        ]
        if not preview:
            self._log("[green]All files already have descriptive names.[/green]")
            return

        async def _confirm() -> None:
            if await self.push_screen_wait(ConfirmModal(f"Rename {len(preview)} file(s)?", preview)):
                count = do_rename(self.manifest, dry_run=False)
                save_manifest(self.manifest, self.manifest_path)
                self._log(f"[green]Renamed {count} files.[/green]")
                self._refresh_tree()

        self.run_worker(_confirm(), exclusive=True)

    # ── Organize ──────────────────────────────────────────

    def _organize(self) -> None:
        target = Path(self._selected) if self._selected else self.scan_dir
        base = target if target.is_dir() else target.parent
        preview_moves = organize_by_game_year(self.manifest, base, dry_run=True)
        if not preview_moves:
            self._log("[green]Everything is already organized (or no cataloged files found).[/green]")
            return
        lines = [f"{src.name}  →  {dst.parent.relative_to(base)}\\" for src, dst in preview_moves]

        async def _confirm() -> None:
            if await self.push_screen_wait(ConfirmModal(f"Move {len(preview_moves)} file(s)?", lines)):
                organize_by_game_year(self.manifest, base, dry_run=False)
                save_manifest(self.manifest, self.manifest_path)
                self._log(f"[green]Organized {len(preview_moves)} files.[/green]")
                self._refresh_tree()

        self.run_worker(_confirm(), exclusive=True)

    # ── Video extraction ──────────────────────────────────

    def _extract_video(self) -> None:
        if not self._selected:
            self._log("[yellow]Select a video file in the tree first.[/yellow]")
            return
        p = Path(self._selected)
        if p.suffix.lower() not in VIDEO_EXTS:
            self._log("[yellow]Selected file is not a recognised video format.[/yellow]")
            return
        self._log(f"[cyan]Extracting frames from {p.name}…[/cyan]")
        self._cancel_event.clear()
        self._catalog_worker = self._run_video(p)

    @work(thread=True)
    def _run_video(self, video_path: Path) -> None:
        self.app.call_from_thread(setattr, self, "_busy", True)

        def _progress(msg: str) -> None:
            self.app.call_from_thread(self._log, msg)

        try:
            frames = extract_frames(video_path, on_progress=_progress)
        except Exception as e:
            self.app.call_from_thread(self._log, f"[red]Frame extraction failed: {e}[/red]")
            self.app.call_from_thread(setattr, self, "_busy", False)
            return

        self.app.call_from_thread(self._log, f"[cyan]Cataloging {len(frames)} extracted frame(s)…[/cyan]")
        bar = self.query_one("#progress-bar", ProgressBar)
        self.app.call_from_thread(bar.update, total=len(frames), progress=0)
        done = [0]

        def _prog2(msg: str) -> None:
            done[0] += 1
            self.app.call_from_thread(self._log, msg)
            self.app.call_from_thread(bar.update, progress=done[0])

        success, fail = catalog_batch(
            frames, self.manifest_path, self.client, self.manifest,
            on_progress=_prog2,
            cancel_check=self._cancel_event.is_set,
        )
        self.app.call_from_thread(
            self._log, f"[green]Video done: {success} frames cataloged, {fail} failed.[/green]"
        )
        self.app.call_from_thread(self._cancel_event.clear)
        self.app.call_from_thread(setattr, self, "_busy", False)
        self.app.call_from_thread(self._refresh_tree)

    # ── Re-catalog selected file ──────────────────────────

    def _recatalog_selected(self) -> None:
        if not self._selected:
            return
        p = Path(self._selected)
        if not p.is_file():
            return
        key = str(p)
        if key in self.manifest:
            del self.manifest[key]
        elif str(p.resolve()) in self.manifest:
            del self.manifest[str(p.resolve())]
        self._log(f"[cyan]Re-cataloging {p.name}…[/cyan]")
        self._cancel_event.clear()
        self._catalog_worker = self._run_catalog([p])

    # ── Undo ──────────────────────────────────────────────

    def action_undo(self) -> None:
        if not undo_stack:
            self._log("[yellow]Nothing to undo.[/yellow]")
            return
        msg = undo_last(self.manifest, self.manifest_path)
        if msg:
            self._log(f"[cyan]{msg}[/cyan]")
            self._refresh_tree()
        else:
            self._log("[yellow]Undo failed (file may have moved).[/yellow]")

    # ── Open in default viewer ────────────────────────────

    def action_open_file(self) -> None:
        if not self._selected:
            return
        p = Path(self._selected)
        if p.is_file() and p.exists():
            os.startfile(str(p))

    # ── Orphan check ──────────────────────────────────────

    def _orphan_check(self) -> None:
        orphans = [fp for fp in self.manifest if not Path(fp).exists()]
        if not orphans:
            self._log("[green]No orphaned entries (all files accounted for).[/green]")
            return
        self._log(f"[yellow]{len(orphans)} orphaned entries (files no longer on disk).[/yellow]")

        async def _confirm() -> None:
            if await self.push_screen_wait(
                ConfirmModal(
                    f"Remove {len(orphans)} orphaned entries from manifest?",
                    [Path(fp).name for fp in orphans],
                )
            ):
                for fp in orphans:
                    del self.manifest[fp]
                save_manifest(self.manifest, self.manifest_path)
                self._log(f"[green]Removed {len(orphans)} orphaned entries.[/green]")

        self.run_worker(_confirm(), exclusive=True)

    # ── Bulk re-catalog failures ──────────────────────────

    def _recatalog_failed(self) -> None:
        failed_paths = [
            fp for fp, entry in self.manifest.items()
            if ("error" in entry or "raw_response" in entry) and Path(fp).exists()
        ]
        if not failed_paths:
            self._log("[green]No failed entries to re-catalog.[/green]")
            return
        for fp in failed_paths:
            del self.manifest[fp]
        self._log(f"[cyan]Re-cataloging {len(failed_paths)} previously failed entries…[/cyan]")
        self._cancel_event.clear()
        self._catalog_worker = self._run_catalog([Path(fp) for fp in failed_paths])

    # ── Export CSV ────────────────────────────────────────

    def _export_csv(self) -> None:
        out = self.manifest_path.parent / "manifest_export.csv"
        fields = ["file", "game", "scene", "location", "mood", "keywords", "cataloged_at"]
        with open(out, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=fields, extrasaction="ignore")
            writer.writeheader()
            for entry in self.manifest.values():
                row = {k: entry.get(k, "") for k in fields}
                if isinstance(row.get("keywords"), list):
                    row["keywords"] = ", ".join(row["keywords"])
                writer.writerow(row)
        self._log(f"[green]Exported {len(self.manifest)} entries → {out.name}[/green]")
        subprocess.Popen(["explorer", "/select,", str(out)])

    # ── Settings ──────────────────────────────────────────

    def action_settings(self) -> None:
        async def _open() -> None:
            result = await self.push_screen_wait(SettingsModal())
            if not result:
                return
            try:
                _apply_settings(result)
                _save_settings(self.settings_path, result)
                self.client = OpenAI(base_url=sc.API_BASE, api_key="lm-studio")
                self._log(f"[green]Settings saved → model: {sc.MODEL}, endpoint: {sc.API_BASE}[/green]")
            except Exception as e:
                self._log(f"[red]Settings error: {e}[/red]")

        self.run_worker(_open(), exclusive=True)

    # ── Library ───────────────────────────────────────────

    def action_library(self) -> None:
        self.push_screen(LibraryScreen(self.manifest))

    # ── Find dupes ────────────────────────────────────────

    def action_find_dupes(self) -> None:
        self._log("[cyan]Computing perceptual hashes…[/cyan]")
        self._run_find_dupes()

    @work(thread=True)
    def _run_find_dupes(self) -> None:
        self.app.call_from_thread(setattr, self, "_busy", True)
        try:
            groups = find_duplicates(self.manifest)
        except ImportError:
            self.app.call_from_thread(
                self._log, "[red]imagehash not installed: pip install imagehash[/red]"
            )
            self.app.call_from_thread(setattr, self, "_busy", False)
            return
        self.app.call_from_thread(setattr, self, "_busy", False)
        count = sum(len(g) for g in groups)
        self.app.call_from_thread(
            self._log,
            f"[cyan]Found {len(groups)} duplicate group(s) ({count} files total).[/cyan]",
        )
        self.app.call_from_thread(
            self.push_screen,
            DupesScreen(groups, self.manifest, self.manifest_path),
        )

    # ── Stats / On This Day ───────────────────────────────

    def action_stats(self) -> None:
        self.push_screen(StatsScreen(self.manifest))

    def action_on_this_day(self) -> None:
        self.push_screen(OnThisDayScreen(self.manifest))

    # ── Helpers ───────────────────────────────────────────

    def _log(self, msg: str) -> None:
        self.query_one("#log-panel", RichLog).write(msg)

    def _refresh_tree(self) -> None:
        self.query_one(DirectoryTree).reload()
        if self._selected:
            self._selected = self._selected


# ── Entry point ───────────────────────────────────────────────────────────────

def main() -> None:
    parser = argparse.ArgumentParser(description="Screenshot Cataloger TUI")
    parser.add_argument(
        "--dir",
        default=str(Path.home() / "Documents" / "ShareX" / "Screenshots"),
        help="Root directory to browse and scan",
    )
    parser.add_argument(
        "--manifest",
        default=MANIFEST_NAME,
        help="Path to the JSONL manifest file",
    )
    args = parser.parse_args()

    scan_dir = Path(args.dir).expanduser().resolve()
    if not scan_dir.exists():
        scan_dir.mkdir(parents=True, exist_ok=True)

    manifest_path = Path(args.manifest).resolve()
    ScreenCatalogApp(scan_dir, manifest_path).run()


if __name__ == "__main__":
    main()
