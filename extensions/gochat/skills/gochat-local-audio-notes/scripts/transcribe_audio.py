#!/usr/bin/env python3
import argparse
import json
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


def eprint(*args: object) -> None:
    print(*args, file=sys.stderr)


def detect_default_device() -> str:
    system = platform.system().lower()
    machine = platform.machine().lower()
    if system == "darwin" and machine in {"arm64", "aarch64"}:
      return "mps"
    return "cpu"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Local audio transcription wrapper for GoChat/OpenClaw skills.",
    )
    parser.add_argument("input", help="Path to the local audio file")
    parser.add_argument(
        "--engine",
        default="auto",
        choices=["auto", "whisper", "faster-whisper", "mlx-whisper", "whisper-cpp"],
        help="Transcription engine to use",
    )
    parser.add_argument(
        "--model",
        default=os.environ.get("GOCHAT_AUDIO_MODEL", "base"),
        help="Model name, e.g. tiny/base/small/medium/large-v3",
    )
    parser.add_argument(
        "--language",
        default=None,
        help="Language hint like zh, en, ja. Omit for auto-detect.",
    )
    parser.add_argument(
        "--task",
        default="transcribe",
        choices=["transcribe", "translate"],
        help="Whether to transcribe or translate to English when supported",
    )
    parser.add_argument(
        "--device",
        default=os.environ.get("GOCHAT_AUDIO_DEVICE") or "auto",
        help="Device hint: auto/cpu/cuda/mps",
    )
    parser.add_argument(
        "--compute-type",
        default=os.environ.get("GOCHAT_AUDIO_COMPUTE_TYPE", "auto"),
        help="Backend-specific compute type, useful for faster-whisper",
    )
    parser.add_argument(
        "--beam-size",
        type=int,
        default=int(os.environ.get("GOCHAT_AUDIO_BEAM_SIZE", "5")),
        help="Beam size for decoding",
    )
    parser.add_argument(
        "--word-timestamps",
        action="store_true",
        help="Request word timestamps when the backend supports them",
    )
    parser.add_argument(
        "--output-format",
        default="json",
        choices=["json", "text"],
        help="Output format",
    )
    return parser


def resolve_device(device: str) -> str:
    if device != "auto":
        return device
    return detect_default_device()


def module_available(name: str) -> bool:
    try:
        __import__(name)
        return True
    except Exception:
        return False


def choose_engine(preferred: str) -> str:
    if preferred != "auto":
        return preferred

    system = platform.system().lower()
    machine = platform.machine().lower()
    if system == "darwin" and machine in {"arm64", "aarch64"} and module_available("mlx_whisper"):
        return "mlx-whisper"
    if module_available("faster_whisper"):
        return "faster-whisper"
    if module_available("whisper"):
        return "whisper"
    if shutil.which("whisper-cli"):
        return "whisper-cpp"
    raise RuntimeError(
        "No supported transcription backend found. Install one of: openai-whisper, "
        "faster-whisper, mlx-whisper, or whisper.cpp."
    )


def normalize_segments(segments: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    normalized: List[Dict[str, Any]] = []
    for segment in segments:
        item: Dict[str, Any] = {
            "start": float(segment.get("start", 0.0)),
            "end": float(segment.get("end", 0.0)),
            "text": str(segment.get("text", "")).strip(),
        }
        words = segment.get("words")
        if words:
            item["words"] = words
        normalized.append(item)
    return normalized


def transcribe_with_whisper(args: argparse.Namespace) -> Dict[str, Any]:
    import whisper

    device = resolve_device(args.device)
    model = whisper.load_model(args.model, device=device)
    result = model.transcribe(
        args.input,
        language=args.language,
        task=args.task,
        verbose=False,
        word_timestamps=args.word_timestamps,
        beam_size=args.beam_size,
    )
    segments = []
    for seg in result.get("segments", []) or []:
        words = None
        if args.word_timestamps and seg.get("words"):
            words = [
                {
                    "start": float(word.get("start", 0.0)),
                    "end": float(word.get("end", 0.0)),
                    "word": str(word.get("word", "")).strip(),
                }
                for word in seg["words"]
            ]
        segments.append(
            {
                "start": float(seg.get("start", 0.0)),
                "end": float(seg.get("end", 0.0)),
                "text": str(seg.get("text", "")).strip(),
                "words": words,
            }
        )
    return {
        "engine": "whisper",
        "model": args.model,
        "language": result.get("language") or args.language,
        "text": str(result.get("text", "")).strip(),
        "segments": normalize_segments(segments),
    }


def transcribe_with_faster_whisper(args: argparse.Namespace) -> Dict[str, Any]:
    from faster_whisper import WhisperModel

    device = resolve_device(args.device)
    compute_type = args.compute_type if args.compute_type != "auto" else "default"
    model = WhisperModel(args.model, device=device, compute_type=compute_type)
    segments_iter, info = model.transcribe(
        args.input,
        language=args.language,
        task=args.task,
        beam_size=args.beam_size,
        word_timestamps=args.word_timestamps,
        vad_filter=True,
    )

    segments: List[Dict[str, Any]] = []
    text_parts: List[str] = []
    for seg in segments_iter:
        text = (seg.text or "").strip()
        if text:
            text_parts.append(text)
        words = None
        if args.word_timestamps and getattr(seg, "words", None):
            words = [
                {
                    "start": float(getattr(word, "start", 0.0) or 0.0),
                    "end": float(getattr(word, "end", 0.0) or 0.0),
                    "word": str(getattr(word, "word", "")).strip(),
                }
                for word in seg.words
            ]
        segments.append(
            {
                "start": float(seg.start),
                "end": float(seg.end),
                "text": text,
                "words": words,
            }
        )

    return {
        "engine": "faster-whisper",
        "model": args.model,
        "language": getattr(info, "language", None) or args.language,
        "text": " ".join(text_parts).strip(),
        "segments": normalize_segments(segments),
    }


def transcribe_with_mlx_whisper(args: argparse.Namespace) -> Dict[str, Any]:
    import mlx_whisper

    result = mlx_whisper.transcribe(
        args.input,
        path_or_hf_repo=args.model,
        language=args.language,
        task=args.task,
        word_timestamps=args.word_timestamps,
    )
    segments = []
    for seg in result.get("segments", []) or []:
        segments.append(
            {
                "start": float(seg.get("start", 0.0)),
                "end": float(seg.get("end", 0.0)),
                "text": str(seg.get("text", "")).strip(),
                "words": seg.get("words"),
            }
        )
    return {
        "engine": "mlx-whisper",
        "model": args.model,
        "language": result.get("language") or args.language,
        "text": str(result.get("text", "")).strip(),
        "segments": normalize_segments(segments),
    }


def whisper_cpp_candidates() -> List[str]:
    candidates = ["whisper-cli", "main"]
    found: List[str] = []
    for candidate in candidates:
        resolved = shutil.which(candidate)
        if resolved and resolved not in found:
            found.append(resolved)
    return found


def read_sidecar_json(json_path: Path) -> Tuple[str, Optional[str], List[Dict[str, Any]]]:
    data = json.loads(json_path.read_text("utf-8"))
    if isinstance(data, dict) and "transcription" in data:
        text = str(data.get("transcription", "")).strip()
        return text, None, []
    if isinstance(data, dict):
        text = str(data.get("text", "")).strip()
        language = data.get("language")
        segments = data.get("segments") or []
        if isinstance(segments, list):
            return text, language, segments
    raise RuntimeError(f"Unsupported whisper.cpp JSON output format: {json_path}")


def transcribe_with_whisper_cpp(args: argparse.Namespace) -> Dict[str, Any]:
    binary_candidates = whisper_cpp_candidates()
    if not binary_candidates:
        raise RuntimeError("whisper.cpp CLI not found (expected whisper-cli or main in PATH)")

    binary = binary_candidates[0]
    tmp_base = Path(args.input).with_suffix("")
    out_base = Path(f"{tmp_base}.gochat-transcript")
    json_path = out_base.with_suffix(".json")

    cmd = [
        binary,
        "-f",
        args.input,
        "-m",
        args.model,
        "-oj",
        "-of",
        str(out_base),
    ]
    if args.language:
        cmd.extend(["-l", args.language])
    if args.task == "translate":
        cmd.append("-tr")

    completed = subprocess.run(cmd, capture_output=True, text=True)
    if completed.returncode != 0:
        raise RuntimeError((completed.stderr or completed.stdout).strip() or "whisper.cpp failed")
    if not json_path.exists():
        raise RuntimeError(f"whisper.cpp did not produce JSON output: {json_path}")

    text, language, segments = read_sidecar_json(json_path)
    return {
        "engine": "whisper-cpp",
        "model": args.model,
        "language": language or args.language,
        "text": text,
        "segments": normalize_segments(segments),
    }


def run_transcription(args: argparse.Namespace) -> Dict[str, Any]:
    engine = choose_engine(args.engine)
    if engine == "whisper":
        return transcribe_with_whisper(args)
    if engine == "faster-whisper":
        return transcribe_with_faster_whisper(args)
    if engine == "mlx-whisper":
        return transcribe_with_mlx_whisper(args)
    if engine == "whisper-cpp":
        return transcribe_with_whisper_cpp(args)
    raise RuntimeError(f"Unsupported engine: {engine}")


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    input_path = Path(args.input).expanduser()
    if not input_path.exists():
        eprint(f"Input file not found: {input_path}")
        return 2

    args.input = str(input_path)

    try:
        result = run_transcription(args)
    except Exception as exc:
        eprint(f"Transcription failed: {exc}")
        return 1

    payload = {
        "ok": True,
        "input": str(input_path),
        "engine": result["engine"],
        "model": result["model"],
        "language": result.get("language"),
        "text": result.get("text", ""),
        "segments": result.get("segments", []),
    }

    if args.output_format == "text":
        print(payload["text"])
    else:
        print(json.dumps(payload, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
