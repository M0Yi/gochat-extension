#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-openai-whisper}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

if ! command -v "${PYTHON_BIN}" >/dev/null 2>&1; then
  echo "python3 not found" >&2
  exit 1
fi

case "${MODE}" in
  openai-whisper)
    exec "${PYTHON_BIN}" -m pip install --user -U openai-whisper
    ;;
  faster-whisper)
    exec "${PYTHON_BIN}" -m pip install --user -U faster-whisper
    ;;
  mlx-whisper)
    exec "${PYTHON_BIN}" -m pip install --user -U mlx-whisper
    ;;
  all)
    "${PYTHON_BIN}" -m pip install --user -U openai-whisper faster-whisper
    "${PYTHON_BIN}" -m pip install --user -U mlx-whisper || true
    ;;
  *)
    echo "unknown backend mode: ${MODE}" >&2
    echo "supported modes: openai-whisper, faster-whisper, mlx-whisper, all" >&2
    exit 1
    ;;
esac
