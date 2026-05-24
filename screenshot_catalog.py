#!/usr/bin/env python3
"""
Screenshot Cataloger — feeds images to local LM Studio (OpenAI-compatible endpoint)
and builds a searchable JSONL manifest of game screenshots.

Usage:
  pip install openai pillow tqdm

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
import os
import sys
import time
from pathlib import Path

from openai import OpenAI
from PIL import Image
from tqdm import tqdm

# ── Config ──────────────────────────────────────────────
API_BASE = "http://169.254.83.107:1234/v1"
MODEL = "gemma-4-e4b"  # or whatever name LM Studio shows
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


def load_manifest(path: Path) -> dict:
    """Load existing manifest, keyed by original file path."""
    manifest = {}
    if path.exists():
        for line in path.read_text().splitlines():
            if line.strip():
                rec = json.loads(line)
                manifest[rec["file"]] = rec
    return manifest


def encode_image(img_path: Path) -> str:
    """Read image, resize if needed, return base64 data URL."""
    img = Image.open(img_path).convert("RGB")
    if max(img.size) > 2048:
        img.thumbnail((2048, 2048))
    from io import BytesIO

    buf = BytesIO()
    img.save(buf, format="JPEG", quality=85)
    return base64.b64encode(buf.getvalue()).decode()


def catalog_one(client: OpenAI, img_path: Path) -> dict:
    """Send one image to LM Studio, return parsed response."""
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
    # Find JSON block
    start = raw.find("{")
    end = raw.rfind("}")
    if start >= 0 and end > start:
        try:
            return json.loads(raw[start : end + 1])
        except json.JSONDecodeError:
            pass
    # Fallback: store raw text
    return {"raw_response": raw}


def suggest_filename(entry: dict, orig_path: Path) -> str:
    """Build a descriptive filename from catalog entry."""
    game = entry.get("game", "unknown").replace(" ", "-").replace(":", "")
    scene = entry.get("scene", "screenshot").replace(" ", "-").replace(":", "")
    location = entry.get("location", "").replace(" ", "-").replace(":", "")
    # Use file's own timestamp
    stem = orig_path.stem
    parts = [p for p in [game, scene, location, stem] if p]
    name = "_".join(parts[:4])  # keep it manageable
    # Sanitize
    name = "".join(c for c in name if c.isalnum() or c in "_-")
    return f"{name}{orig_path.suffix}"


def main():
    parser = argparse.ArgumentParser(
        description="Catalog game screenshots with local Gemma 4"
    )
    parser.add_argument("--dir", required=True, help="Directory to scan recursively")
    parser.add_argument("--limit", type=int, help="Max images to process (for testing)")
    parser.add_argument(
        "--rename", action="store_true", help="Rename files based on catalog"
    )
    parser.add_argument(
        "--dry-run", action="store_true", help="Preview renames without doing them"
    )
    parser.add_argument("--output", default=MANIFEST_NAME, help="Manifest file path")
    args = parser.parse_args()

    scan_dir = Path(args.dir).expanduser().resolve()
    if not scan_dir.is_dir():
        sys.exit(f"Not a directory: {scan_dir}")

    manifest_path = Path(args.output)
    manifest = load_manifest(manifest_path)
    print(f"📋 Manifest: {len(manifest)} entries loaded")

    # Find all images
    all_images = sorted(
        f for f in scan_dir.rglob("*") if f.suffix.lower() in IMAGE_EXTS and f.is_file()
    )
    # Skip already cataloged
    new_images = [
        f
        for f in all_images
        if str(f) not in manifest and str(f.resolve()) not in manifest
    ]
    if args.limit:
        new_images = new_images[: args.limit]

    print(f"🖼️  Found {len(all_images)} images total, {len(new_images)} new")

    if not new_images:
        # ── Rename mode ──
        if args.rename:
            do_rename(manifest, args.dry_run)
        else:
            print("✅ Nothing new to catalog.")
        return

    # ── Catalog new images ──
    client = OpenAI(base_url=API_BASE, api_key="lm-studio")
    fail_count = 0

    for img_path in tqdm(new_images, desc="Cataloging"):
        entry = catalog_one(client, img_path)
        record = {
            "file": str(img_path),
            "cataloged_at": time.strftime("%Y-%m-%dT%H:%M:%S"),
            **entry,
        }
        manifest[str(img_path)] = record

        # Append immediately (resume-safe)
        with open(manifest_path, "a") as f:
            f.write(json.dumps(record) + "\n")

        if "error" in entry:
            fail_count += 1
            tqdm.write(f"  ⚠️  {img_path.name}: {entry['error']}")
        elif "suggested_filename" in entry:
            tqdm.write(
                f"  📸 {img_path.name} → {entry.get('game', '?')}: {entry.get('scene', '?')}"
            )

        time.sleep(0.2)  # gentle on LM Studio

    print(f"\n✅ Done. {len(new_images) - fail_count} cataloged, {fail_count} failed")
    print(f"   Manifest: {manifest_path.absolute()}")

    # ── Rename if requested ──
    if args.rename:
        do_rename(manifest, args.dry_run)


def do_rename(manifest: dict, dry_run: bool):
    """Rename files based on catalog entries."""
    renamed = 0
    for filepath, entry in manifest.items():
        orig = Path(filepath)
        if not orig.exists():
            continue
        new_name = suggest_filename(entry, orig)
        new_path = orig.parent / new_name
        if new_path == orig:
            continue
        if new_path.exists():
            print(f"  ⚠️  Target exists, skipping: {new_name}")
            continue
        if dry_run:
            print(f"  {orig.name} → {new_name}")
        else:
            orig.rename(new_path)
        renamed += 1

    verb = "Would rename" if dry_run else "Renamed"
    print(f"📁 {verb} {renamed} files")


if __name__ == "__main__":
    main()
