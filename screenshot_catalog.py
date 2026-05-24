#!/usr/bin/env python3
"""
Screenshot Cataloger — feeds images to local LM Studio (OpenAI-compatible endpoint)
and builds a searchable JSONL manifest of game screenshots.

Usage:
  pip install openai pillow tqdm textual opencv-python imagehash send2trash

  # Test on 10 images first
  python screenshot_catalog.py --dir ~/Pictures/SteamScreenshots --limit 10

  # Full run (resumes from existing manifest)
  python screenshot_catalog.py --dir ~/Pictures/SteamScreenshots

  # Preview what renaming would do
  python screenshot_catalog.py --dir ~/Pictures/SteamScreenshots --rename --dry-run

  # Actually rename files
  python screenshot_catalog.py --dir ~/Pictures/SteamScreenshots --rename
"""

import argparse
import base64
import json
import re
import shutil
import sys
import time
from io import BytesIO
from pathlib import Path

from openai import OpenAI
from PIL import Image
from tqdm import tqdm

# ── Config ──────────────────────────────────────────────
API_BASE = "http://169.254.83.107:1234/v1"
MODEL = "gemma-4-e4b"
MAX_TOKENS = 2048
TEMPERATURE = 0.3
MANIFEST_NAME = "screenshot_catalog.jsonl"
IMAGE_EXTS = {".png", ".jpg", ".jpeg", ".webp", ".bmp"}

PROMPT = """Analyze this game screenshot. Return ONLY valid JSON, no other text.

Filename hint: {stem}
Treat this as a weak signal for game identification — useful if it contains a recognizable game name (e.g. "destiny2", "Cyberpunk2077"), but ignore it if it's a system process or overlay tool (e.g. Discord, NVIDIAContainer, ApplicationFrameHost, ShareX).

{{
  "game": "name of the game",
  "scene": "what's happening - combat, exploration, cutscene, menu, etc",
  "characters": ["character names visible"],
  "location": "where in the game world",
  "ui_elements": ["HUD", "minimap", "health bar", "dialog", etc],
  "mood": "dark, bright, tense, peaceful, etc",
  "keywords": ["search terms"],
  "suggested_filename": "descriptive-filename-without-extension"
}}"""

# ── Filename blocklist for non-game captures ─────────────
_NON_GAME_PREFIXES = (
    "discord", "nvcontainer", "applicationframehost", "sharex",
    "obs64", "obs32", "streamlabs", "nvidia", "msi ", "afterburner",
    "nvdisplay", "nvcplui", "dwm", "explorer", "taskmgr",
)

# ── Undo stack ───────────────────────────────────────────
# Each entry: ("move", src: Path, dst: Path)
undo_stack: list[tuple[str, Path, Path]] = []


# ── Manifest helpers ─────────────────────────────────────

def load_manifest(path: Path) -> dict:
    """Load existing manifest, keyed by absolute file path string."""
    manifest: dict = {}
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                rec = json.loads(line)
                manifest[rec["file"]] = rec
    return manifest


def save_manifest(manifest: dict, path: Path) -> None:
    """Rewrite the entire manifest file from the in-memory dict."""
    path.write_text(
        "\n".join(json.dumps(r) for r in manifest.values()),
        encoding="utf-8",
    )


# ── Image encoding ───────────────────────────────────────

def encode_image(img_path: Path) -> str:
    """Read image, resize if needed, return base64 JPEG string."""
    img = Image.open(img_path).convert("RGB")
    if max(img.size) > 2048:
        img.thumbnail((2048, 2048))
    buf = BytesIO()
    img.save(buf, format="JPEG", quality=85)
    return base64.b64encode(buf.getvalue()).decode()


# ── LM Studio interaction ────────────────────────────────

def catalog_one(client: OpenAI, img_path: Path) -> dict:
    """Send one image to LM Studio, return parsed response dict."""
    b64 = encode_image(img_path)
    try:
        response = client.chat.completions.create(
            model=MODEL,
            messages=[
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": PROMPT.format(stem=img_path.stem)},
                        {
                            "type": "image_url",
                            "image_url": {"url": f"data:image/jpeg;base64,{b64}"},
                        },
                    ],
                }
            ],
            temperature=TEMPERATURE,
            max_tokens=MAX_TOKENS,
        )
        raw = response.choices[0].message.content
        return parse_response(raw)
    except Exception as e:
        return {"error": str(e), "raw": ""}


def parse_response(raw: str) -> dict:
    """Extract JSON from model response, with fallback."""
    raw = raw.strip()
    start = raw.find("{")
    end = raw.rfind("}")
    if start >= 0 and end > start:
        try:
            return json.loads(raw[start: end + 1])
        except json.JSONDecodeError:
            pass
    return {"raw_response": raw}


def catalog_batch(
    images: list,
    manifest_path: Path,
    client: OpenAI,
    manifest: dict,
    on_progress=None,
    cancel_check=None,
) -> tuple:
    """
    Catalog a list of image Paths. Appends to JSONL immediately (resume-safe).
    on_progress(msg) is called after each image if provided.
    cancel_check() is polled before each image; return True to stop early.
    Returns (success_count, fail_count).
    """
    success = 0
    fail = 0
    for img_path in images:
        if cancel_check and cancel_check():
            if on_progress:
                on_progress("⏹  Cancelled.")
            break
        entry = catalog_one(client, img_path)
        record = {
            "file": str(img_path),
            "cataloged_at": time.strftime("%Y-%m-%dT%H:%M:%S"),
            **entry,
        }
        manifest[str(img_path)] = record
        with open(manifest_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(record) + "\n")

        if "error" in entry:
            fail += 1
            msg = f"⚠  {img_path.name}: {entry['error']}"
        elif "raw_response" in entry:
            fail += 1
            msg = f"⚠  {img_path.name}: model returned no JSON"
        else:
            success += 1
            game = entry.get("game", "?")
            scene = entry.get("scene", "?")
            flag = " [non-game]" if is_non_game_capture(img_path) else ""
            msg = f"✓  {img_path.name} → {game}: {scene}{flag}"

        if on_progress:
            on_progress(msg)

        time.sleep(0.2)

    return success, fail


# ── Filename utilities ───────────────────────────────────

def suggest_filename(entry: dict, orig_path: Path) -> str:
    """Build a descriptive filename from catalog entry."""
    game = entry.get("game", "unknown").replace(" ", "-").replace(":", "")
    scene = entry.get("scene", "screenshot").replace(" ", "-").replace(":", "")
    location = entry.get("location", "").replace(" ", "-").replace(":", "")
    stem = orig_path.stem
    parts = [p for p in [game, scene, location, stem] if p]
    name = "_".join(parts[:4])
    name = "".join(c for c in name if c.isalnum() or c in "_-")
    return f"{name}{orig_path.suffix}"


_WIN_RESERVED = re.compile(r'[\\/:*?"<>|]')


def sanitize_folder_name(name: str) -> str:
    """Strip Windows-reserved chars and collapse whitespace."""
    cleaned = _WIN_RESERVED.sub("", name).strip()
    return " ".join(cleaned.split()) or "_unnamed"


def is_non_game_capture(file_path: Path) -> bool:
    """True if filename suggests a system/overlay capture rather than a game."""
    stem_lower = file_path.stem.lower()
    return any(stem_lower.startswith(kw) for kw in _NON_GAME_PREFIXES)


# ── File organisation ────────────────────────────────────

def organize_by_game_year(
    manifest: dict,
    base_dir: Path,
    dry_run: bool = False,
) -> list:
    """
    Move cataloged images into base_dir/{game}/{year}/ subfolders.
    Non-game captures go to base_dir/_unsorted/.
    Mutates manifest dict in-place (but does NOT call save_manifest).
    Caller must call save_manifest() after this.
    Returns list of (src, dst) Path tuples.
    """
    moves = []
    keys_to_update = {}

    for filepath, entry in list(manifest.items()):
        src = Path(filepath)
        if not src.exists():
            continue

        if is_non_game_capture(src):
            dst_dir = base_dir / "_unsorted"
        else:
            game = entry.get("game", "").strip()
            if not game or game.lower() in ("unknown", "n/a", ""):
                dst_dir = base_dir / "_unsorted"
            else:
                year = entry.get("cataloged_at", "0000")[:4]
                dst_dir = base_dir / sanitize_folder_name(game) / year

        dst = dst_dir / src.name
        if dst.resolve() == src.resolve():
            continue
        if dst.exists():
            continue

        moves.append((src, dst))
        keys_to_update[filepath] = str(dst)

    if not dry_run:
        for src, dst in moves:
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.move(str(src), str(dst))
            undo_stack.append(("move", src, dst))

        for old_key, new_key in keys_to_update.items():
            rec = manifest.pop(old_key)
            rec["file"] = new_key
            manifest[new_key] = rec

    return moves


def do_rename(manifest: dict, dry_run: bool) -> int:
    """Rename files based on catalog entries. Returns count of (would-be) renames."""
    renamed = 0
    keys_to_update = {}

    for filepath, entry in list(manifest.items()):
        orig = Path(filepath)
        if not orig.exists():
            continue
        new_name = suggest_filename(entry, orig)
        new_path = orig.parent / new_name
        if new_path.resolve() == orig.resolve():
            continue
        if new_path.exists():
            continue
        if dry_run:
            print(f"  {orig.name} → {new_name}")
        else:
            shutil.move(str(orig), str(new_path))
            undo_stack.append(("move", orig, new_path))
            keys_to_update[filepath] = str(new_path)
        renamed += 1

    if not dry_run:
        for old_key, new_key in keys_to_update.items():
            rec = manifest.pop(old_key)
            rec["file"] = new_key
            manifest[new_key] = rec

    verb = "Would rename" if dry_run else "Renamed"
    print(f"📁 {verb} {renamed} files")
    return renamed


# ── Undo ─────────────────────────────────────────────────

def undo_last(manifest: dict, manifest_path: Path) -> str | None:
    """Reverse the last file operation. Returns description string or None."""
    if not undo_stack:
        return None
    action, src, dst = undo_stack.pop()
    if action == "move" and dst.exists():
        src.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(dst), str(src))
        old_key = str(dst)
        new_key = str(src)
        if old_key in manifest:
            rec = manifest.pop(old_key)
            rec["file"] = new_key
            manifest[new_key] = rec
        save_manifest(manifest, manifest_path)
        return f"Undone: {dst.name} ← {src.parent.name}/"
    return None


# ── Duplicate detection ───────────────────────────────────

def find_duplicates(manifest: dict, threshold: int = 8) -> list:
    """
    Find near-duplicate images using perceptual hashing (imagehash).
    Returns list of groups; each group is a list of file path strings.
    Raises ImportError if imagehash is not installed.
    """
    import imagehash  # optional dependency

    hashes: list = []
    for filepath in manifest:
        p = Path(filepath)
        if not p.exists() or p.suffix.lower() not in IMAGE_EXTS:
            continue
        try:
            h = imagehash.phash(Image.open(p))
            hashes.append((filepath, h))
        except Exception:
            continue

    groups: list = []
    used: set = set()
    for i, (path_i, hash_i) in enumerate(hashes):
        if path_i in used:
            continue
        group = [path_i]
        for j, (path_j, hash_j) in enumerate(hashes):
            if i == j or path_j in used:
                continue
            if (hash_i - hash_j) <= threshold:
                group.append(path_j)
        if len(group) > 1:
            for p in group:
                used.add(p)
            groups.append(group)

    return groups


# ── Streaming modes (called by the Go TUI) ───────────────

def _emit(obj: dict) -> None:
    """Write one NDJSON line to stdout, flushed immediately."""
    print(json.dumps(obj), flush=True)


def stream_catalog(scan_dir: Path, manifest_path: Path, all_files: bool, explicit_files: list) -> None:
    """Stream catalog progress as NDJSON to stdout."""
    manifest = load_manifest(manifest_path)
    client = OpenAI(base_url=API_BASE, api_key="lm-studio")

    if explicit_files:
        images = [Path(f) for f in explicit_files if Path(f).exists()]
    else:
        all_imgs = sorted(
            f for f in scan_dir.rglob("*")
            if f.suffix.lower() in IMAGE_EXTS and f.is_file()
        )
        if all_files:
            images = all_imgs
        else:
            images = [f for f in all_imgs if str(f) not in manifest and str(f.resolve()) not in manifest]

    _emit({"type": "start", "total": len(images)})
    success = fail = 0
    for img_path in images:
        entry = catalog_one(client, img_path)
        record = {"file": str(img_path), "cataloged_at": time.strftime("%Y-%m-%dT%H:%M:%S"), **entry}
        manifest[str(img_path)] = record
        with open(manifest_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(record) + "\n")

        if "error" in entry or "raw_response" in entry:
            fail += 1
            _emit({
                "type": "progress", "file": str(img_path), "name": img_path.name,
                "status": "error", "error": entry.get("error", entry.get("raw_response", "")),
            })
        else:
            success += 1
            _emit({
                "type": "progress", "file": str(img_path), "name": img_path.name,
                "game": entry.get("game", ""), "scene": entry.get("scene", ""),
                "status": "ok",
            })
        time.sleep(0.2)

    _emit({"type": "done", "success": success, "fail": fail})


def stream_dupes(manifest_path: Path, threshold: int) -> None:
    """Stream duplicate groups as NDJSON to stdout."""
    manifest = load_manifest(manifest_path)
    try:
        groups = find_duplicates(manifest, threshold=threshold)
    except ImportError:
        _emit({"type": "error", "error": "imagehash not installed: pip install imagehash"})
        return
    for group in groups:
        _emit({"type": "group", "files": group})
    _emit({"type": "done", "groups": len(groups)})


def stream_video(video_file: Path, manifest_path: Path, mode: str, threshold: float, interval: float) -> None:
    """Extract frames then catalog them, streaming NDJSON."""
    from video import extract_frames

    def _on_progress(msg: str) -> None:
        _emit({"type": "extract", "msg": msg})

    try:
        frames = extract_frames(video_file, mode=mode, scene_threshold=threshold,
                                interval_sec=interval, on_progress=_on_progress)
    except Exception as e:
        _emit({"type": "error", "error": str(e)})
        return

    stream_catalog(video_file.parent, manifest_path, all_files=False, explicit_files=[str(f) for f in frames])


# ── CLI entry point ───────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Catalog game screenshots with local Gemma 4"
    )
    parser.add_argument("--dir", default=".", help="Directory to scan recursively")
    parser.add_argument("--limit", type=int, help="Max images to process (for testing)")
    parser.add_argument("--rename", action="store_true", help="Rename files based on catalog")
    parser.add_argument("--dry-run", action="store_true", help="Preview renames without doing them")
    parser.add_argument("--output", default=MANIFEST_NAME, help="Manifest file path")
    # Streaming modes (used by Go TUI)
    parser.add_argument("--stream", action="store_true", help="Stream catalog progress as NDJSON")
    parser.add_argument("--stream-dupes", action="store_true", help="Stream duplicate groups as NDJSON")
    parser.add_argument("--stream-video", action="store_true", help="Extract video frames + catalog, stream NDJSON")
    parser.add_argument("--manifest", default=MANIFEST_NAME, help="Manifest path (for streaming modes)")
    parser.add_argument("--all", action="store_true", help="Re-catalog all files (not just new)")
    parser.add_argument("--files", nargs="*", default=[], help="Explicit file list to catalog")
    parser.add_argument("--threshold", type=float, default=8.0, help="Dupe hash threshold / scene threshold")
    parser.add_argument("--interval", type=float, default=5.0, help="Video frame interval (seconds)")
    parser.add_argument("--mode", default="auto", help="Video extraction mode: auto/scene/uniform")
    parser.add_argument("--file", help="Video file path (for --stream-video)")
    args = parser.parse_args()

    # ── Streaming modes ──
    if args.stream:
        scan_dir = Path(args.dir).expanduser().resolve()
        manifest_path = Path(args.manifest)
        stream_catalog(scan_dir, manifest_path, all_files=args.all, explicit_files=args.files)
        return

    if args.stream_dupes:
        manifest_path = Path(args.manifest)
        stream_dupes(manifest_path, threshold=int(args.threshold))
        return

    if args.stream_video:
        if not args.file:
            sys.exit("--stream-video requires --file PATH")
        video_file = Path(args.file)
        manifest_path = Path(args.manifest)
        stream_video(video_file, manifest_path, mode=args.mode,
                     threshold=args.threshold, interval=args.interval)
        return

    scan_dir = Path(args.dir).expanduser().resolve()
    if not scan_dir.is_dir():
        sys.exit(f"Not a directory: {scan_dir}")

    manifest_path = Path(args.output)
    manifest = load_manifest(manifest_path)
    print(f"📋 Manifest: {len(manifest)} entries loaded")

    all_images = sorted(
        f for f in scan_dir.rglob("*") if f.suffix.lower() in IMAGE_EXTS and f.is_file()
    )
    new_images = [
        f for f in all_images
        if str(f) not in manifest and str(f.resolve()) not in manifest
    ]
    if args.limit:
        new_images = new_images[: args.limit]

    print(f"🖼️  Found {len(all_images)} images total, {len(new_images)} new")

    if not new_images:
        if args.rename:
            do_rename(manifest, args.dry_run)
            if not args.dry_run:
                save_manifest(manifest, manifest_path)
        else:
            print("✅ Nothing new to catalog.")
        return

    client = OpenAI(base_url=API_BASE, api_key="lm-studio")

    def _progress(msg: str):
        tqdm.write(f"  {msg}")

    images_iter = tqdm(new_images, desc="Cataloging")
    # Wrap tqdm so catalog_batch gets the plain list, progress comes via callback
    success, fail = catalog_batch(
        list(images_iter), manifest_path, client, manifest, on_progress=_progress
    )

    print(f"\n✅ Done. {success} cataloged, {fail} failed")
    print(f"   Manifest: {manifest_path.absolute()}")

    if args.rename:
        do_rename(manifest, args.dry_run)
        if not args.dry_run:
            save_manifest(manifest, manifest_path)


if __name__ == "__main__":
    main()
