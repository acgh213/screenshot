"""
Video frame extraction for the screenshot cataloger.

Two modes:
  uniform   — one frame every N seconds via opencv (always available)
  scene     — keyframes at scene changes via ffmpeg (preferred, requires ffmpeg in PATH)
  auto      — scene if ffmpeg available, otherwise uniform
"""

import subprocess
from pathlib import Path
from typing import Callable

import cv2

VIDEO_EXTS = {".mp4", ".avi", ".mov", ".mkv", ".webm", ".mp4v", ".wmv", ".m4v", ".flv"}


def ffmpeg_available() -> bool:
    """Return True if ffmpeg is reachable on PATH."""
    try:
        result = subprocess.run(
            ["ffmpeg", "-version"],
            capture_output=True,
            timeout=5,
        )
        return result.returncode == 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


def extract_frames_uniform(
    video_path: Path,
    output_dir: Path,
    interval_sec: float = 5.0,
    on_progress: Callable | None = None,
) -> list[Path]:
    """Extract one frame every interval_sec seconds using opencv."""
    output_dir.mkdir(parents=True, exist_ok=True)
    cap = cv2.VideoCapture(str(video_path))  # str() required on Windows
    if not cap.isOpened():
        raise RuntimeError(f"Cannot open video: {video_path.name}")

    fps = cap.get(cv2.CAP_PROP_FPS) or 30.0
    total = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))
    interval = max(1, int(fps * interval_sec))

    saved: list[Path] = []
    frame_num = 0
    while True:
        ret, frame = cap.read()
        if not ret:
            break
        if frame_num % interval == 0:
            out = output_dir / f"frame_{frame_num:06d}.jpg"
            cv2.imwrite(str(out), frame)
            saved.append(out)
            if on_progress and total:
                pct = int(frame_num / total * 100)
                on_progress(f"  uniform: frame {frame_num}/{total} ({pct}%) → {out.name}")
        frame_num += 1

    cap.release()
    return saved


def extract_frames_scene(
    video_path: Path,
    output_dir: Path,
    threshold: float = 0.3,
    on_progress: Callable | None = None,
) -> list[Path]:
    """Extract scene-change keyframes using ffmpeg."""
    output_dir.mkdir(parents=True, exist_ok=True)
    if on_progress:
        on_progress(f"  ffmpeg scene detection (threshold={threshold})…")

    result = subprocess.run(
        [
            "ffmpeg", "-y",
            "-i", str(video_path),
            "-vf", f"select='gt(scene,{threshold})'",
            "-vsync", "vfr",
            str(output_dir / "frame_%04d.jpg"),
        ],
        capture_output=True,
        text=True,
    )

    if result.returncode != 0:
        raise RuntimeError(f"ffmpeg error: {result.stderr[-400:]}")

    frames = sorted(output_dir.glob("frame_*.jpg"))
    if on_progress:
        on_progress(f"  extracted {len(frames)} scene-change frames")
    return frames


def extract_frames(
    video_path: Path,
    output_dir: Path | None = None,
    mode: str = "auto",
    interval_sec: float = 5.0,
    scene_threshold: float = 0.3,
    on_progress: Callable | None = None,
) -> list[Path]:
    """
    Extract frames from a video file.

    mode:
      "auto"    — scene detection if ffmpeg available, else uniform
      "scene"   — force ffmpeg scene detection
      "uniform" — force opencv uniform sampling
    """
    if output_dir is None:
        output_dir = video_path.parent / f"{video_path.stem}_frames"

    use_scene = mode == "scene" or (mode == "auto" and ffmpeg_available())

    if use_scene:
        try:
            return extract_frames_scene(video_path, output_dir, scene_threshold, on_progress)
        except Exception as e:
            if on_progress:
                on_progress(f"⚠  scene detection failed ({e}), falling back to uniform")

    return extract_frames_uniform(video_path, output_dir, interval_sec, on_progress)
