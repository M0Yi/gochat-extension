#!/usr/bin/env bash
set -euo pipefail

# ──────────────────────────────────────────────
# GoChat Extension Installer for OpenClaw
# Supports: macOS, Linux (amd64/arm64), WSL
# ──────────────────────────────────────────────

VERSION="2026.3.28"
EXTENSION_NAME="gochat"
PACKAGE_NAME="@m0yi/gochat"
OPENCLAW_MIN_VERSION="2026.3.28"
REPO_URL="https://github.com/M0Yi/gochat-extension.git"

# ──────────────────────────────────────────────
# Globals (ALL declared upfront for set -u)
# ──────────────────────────────────────────────
PLATFORM=""
ARCH=""
OPENCLAW_BIN=""
TARBALL_PATH=""
PIPED_INSTALL=false

# ──────────────────────────────────────────────
# Colors
# ──────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}${BOLD}[gochat]${NC} %s\n" "$*"; }
ok()    { printf "${GREEN}${BOLD}[gochat]${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}${BOLD}[gochat]${NC} %s\n" "$*"; }
fail()  { printf "${RED}${BOLD}[gochat]${NC} %s\n" "$*"; }

# ──────────────────────────────────────────────
# OS & Architecture Detection
# ──────────────────────────────────────────────

detect_os() {
  local uname_out
  uname_out="$(uname -s 2>/dev/null || echo "unknown")"

  case "${uname_out}" in
    Linux*)
      if grep -qi microsoft /proc/version 2>/dev/null; then
        PLATFORM="linux-wsl"
      else
        PLATFORM="linux"
      fi
      ;;
    Darwin*)
      PLATFORM="macos"
      ;;
    MINGW*|MSYS*|CYGWIN*)
      PLATFORM="windows"
      ;;
    FreeBSD|OpenBSD)
      PLATFORM="bsd"
      ;;
    *)
      PLATFORM="unknown"
      ;;
  esac

  local uname_m
  uname_m="$(uname -m 2>/dev/null || echo "unknown")"

  case "${uname_m}" in
    x86_64|amd64)   ARCH="amd64" ;;
    aarch64|arm64)  ARCH="arm64" ;;
    armv7l|armv7)   ARCH="armv7" ;;
    *)              ARCH="unknown" ;;
  esac
}

check_os_support() {
  case "${PLATFORM}" in
    linux|linux-wsl|macos)
      info "Platform: ${PLATFORM} (${ARCH})"
      return 0
      ;;
    bsd)
      warn "Platform: ${PLATFORM} (${ARCH}) — unofficial, best-effort"
      return 0
      ;;
    windows)
      fail "Native Windows is not supported. Use WSL."
      fail "  wsl --install"
      exit 1
      ;;
    *)
      fail "Unsupported platform: ${PLATFORM} (${ARCH})"
      exit 1
      ;;
  esac
}

# ──────────────────────────────────────────────
# Detect OpenClaw installation
# ──────────────────────────────────────────────

detect_openclaw() {
  if command -v openclaw &>/dev/null; then
    OPENCLAW_BIN="$(command -v openclaw)"
    info "Found OpenClaw at: ${OPENCLAW_BIN}"
    return 0
  fi

  local paths=()

  case "${PLATFORM}" in
    macos)
      paths=(
        "${HOME}/.local/bin/openclaw"
        "/usr/local/bin/openclaw"
        "/opt/homebrew/bin/openclaw"
        "${HOME}/.npm-global/bin/openclaw"
      )
      ;;
    linux|linux-wsl)
      paths=(
        "${HOME}/.local/bin/openclaw"
        "/usr/local/bin/openclaw"
        "/usr/bin/openclaw"
        "${HOME}/.npm-global/bin/openclaw"
        "${XDG_DATA_HOME:-${HOME}/.local/share}/openclaw/bin/openclaw"
      )
      ;;
    *)
      paths=(
        "${HOME}/.local/bin/openclaw"
        "/usr/local/bin/openclaw"
      )
      ;;
  esac

  if command -v npm &>/dev/null; then
    local npm_prefix
    npm_prefix="$(npm config get prefix 2>/dev/null || true)"
    if [ -n "${npm_prefix}" ]; then
      paths+=("${npm_prefix}/bin/openclaw")
    fi
  fi

  for p in "${paths[@]}"; do
    if [ -x "$p" ]; then
      OPENCLAW_BIN="$p"
      info "Found OpenClaw at: ${OPENCLAW_BIN}"
      return 0
    fi
  done

  return 1
}

get_openclaw_dir() {
  local state_dir="${OPENCLAW_STATE_DIR:-}"
  if [ -n "${state_dir}" ]; then
    echo "${state_dir}"
    return
  fi

  if [ "${PLATFORM}" = "linux" ] || [ "${PLATFORM}" = "linux-wsl" ]; then
    local xdg_dir="${XDG_DATA_HOME:-${HOME}/.local/share}"
    if [ -d "${xdg_dir}/openclaw" ]; then
      echo "${xdg_dir}/openclaw"
      return
    fi
  fi

  echo "${HOME}/.openclaw"
}

get_extensions_dir() {
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  echo "${openclaw_dir}/extensions"
}

# ──────────────────────────────────────────────
# Ensure directory is writable
# ──────────────────────────────────────────────

ensure_dir_writable() {
  local target_dir="$1"
  local parent_dir
  parent_dir="$(dirname "${target_dir}")"

  if [ -d "${target_dir}" ] && [ ! -w "${target_dir}" ]; then
    fail "Directory not writable: ${target_dir}"
    fail "Try: sudo mkdir -p ${target_dir} && sudo chown \$(whoami) ${target_dir}"
    exit 1
  fi

  if [ ! -d "${target_dir}" ]; then
    if [ ! -w "${parent_dir}" ]; then
      fail "Cannot create directory: ${target_dir}"
      fail "Parent not writable: ${parent_dir}"
      fail "Try: sudo mkdir -p ${target_dir} && sudo chown -R \$(whoami) ${parent_dir}"
      exit 1
    fi
  fi

  mkdir -p "${target_dir}"
}

# ──────────────────────────────────────────────
# Install functions
# ──────────────────────────────────────────────

install_from_tarball() {
  local tarball="$1"
  local extensions_dir
  extensions_dir="$(get_extensions_dir)"

  ensure_dir_writable "${extensions_dir}"

  if [ -d "${extensions_dir}/${EXTENSION_NAME}" ]; then
    info "Removing previous installation..."
    rm -rf "${extensions_dir}/${EXTENSION_NAME}"
  fi

  info "Extracting to ${extensions_dir}/${EXTENSION_NAME}..."
  tar -xzf "${tarball}" -C "${extensions_dir}"

  if [ -f "${extensions_dir}/${EXTENSION_NAME}/package.json" ]; then
    info "Installing npm dependencies..."
    (cd "${extensions_dir}/${EXTENSION_NAME}" && npm install --production 2>&1) || {
      warn "npm install had warnings (non-fatal)"
    }
  fi

  ok "Installed to ${extensions_dir}/${EXTENSION_NAME}"
}

install_from_source() {
  local source_dir="$1"
  local extensions_dir
  extensions_dir="$(get_extensions_dir)"

  ensure_dir_writable "${extensions_dir}"

  if [ -d "${extensions_dir}/${EXTENSION_NAME}" ]; then
    info "Removing previous installation..."
    rm -rf "${extensions_dir}/${EXTENSION_NAME}"
  fi

  info "Copying to ${extensions_dir}/${EXTENSION_NAME}..."
  cp -r "${source_dir}" "${extensions_dir}/${EXTENSION_NAME}"

  rm -rf "${extensions_dir}/${EXTENSION_NAME}/node_modules"
  rm -rf "${extensions_dir}/${EXTENSION_NAME}/.git"

  if [ -f "${extensions_dir}/${EXTENSION_NAME}/package.json" ]; then
    info "Installing npm dependencies..."
    (cd "${extensions_dir}/${EXTENSION_NAME}" && npm install --production 2>&1) || {
      warn "npm install had warnings (non-fatal)"
    }
  fi

  ok "Installed to ${extensions_dir}/${EXTENSION_NAME}"
}

install_piped() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/gochat-install.XXXXXX")"

  info "Pipe install detected — cloning from ${REPO_URL}..."
  if command -v git &>/dev/null; then
    git clone --depth 1 "${REPO_URL}" "${tmp_dir}/gochat-extension" 2>&1 || {
      fail "git clone failed. Check network or install git."
      rm -rf "${tmp_dir}"
      exit 1
    }
    install_from_source "${tmp_dir}/gochat-extension"
    rm -rf "${tmp_dir}"
  else
    fail "Pipe install requires git but git is not installed."
    fail "Install git first:"
    case "${PLATFORM}" in
      macos)     fail "  brew install git" ;;
      linux|linux-wsl) fail "  sudo apt install git  OR  sudo dnf install git" ;;
    esac
    rm -rf "${tmp_dir}"
    exit 1
  fi
}

# ──────────────────────────────────────────────
# Configure GoChat
# ──────────────────────────────────────────────

configure_gochat() {
  local mode="$1"
  local extensions_dir
  extensions_dir="$(get_extensions_dir)"

  echo ""
  info "──── GoChat Configuration ────"
  info "  platform:      ${PLATFORM} (${ARCH})"
  info "  mode:          ${mode}"
  info "  extension dir: ${extensions_dir}/${EXTENSION_NAME}"

  if [ "${mode}" = "local" ]; then
    info "  server:        built-in HTTP API on port 9750"
    info "  secret:        auto-generated"
  else
    info "  server:        WebSocket relay to wss://fund.moyi.vip/ws/plugin"
    info "  channelId:     auto-registered on first start"
  fi
  info "  dmPolicy:      open (anyone can chat)"
  info "──────────────────────────"

  echo ""
  ok "GoChat is ready! Start OpenClaw and the GoChat channel will activate automatically."
  echo ""
  info "Usage:"
  info "  openclaw start          # Start OpenClaw with GoChat enabled"
  info "  openclaw gochat setup   # Interactive setup wizard"
  echo ""
}

# ──────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────

main() {
  echo ""
  printf "${BLUE}${BOLD}─────────────────────────────────────\n"
  printf "  GoChat Extension Installer  v${VERSION}\n"
  printf "─────────────────────────────────────${NC}\n"
  echo ""

  detect_os
  check_os_support

  if ! command -v node &>/dev/null; then
    fail "Node.js is required but not found."
    case "${PLATFORM}" in
      macos)          info "Install: brew install node" ;;
      linux|linux-wsl) info "Install: sudo apt install nodejs  OR  use nvm" ;;
    esac
    exit 1
  fi

  if ! command -v npm &>/dev/null; then
    fail "npm is required but not found."
    case "${PLATFORM}" in
      macos)          info "Install: brew install node" ;;
      linux|linux-wsl) info "Install: sudo apt install npm" ;;
    esac
    exit 1
  fi

  local node_version
  node_version="$(node --version 2>/dev/null || echo "unknown")"
  info "Node.js: ${node_version}"

  if detect_openclaw; then
    local oc_version
    oc_version="$("${OPENCLAW_BIN}" --version 2>/dev/null | head -1 || echo "unknown")"
    info "OpenClaw version: ${oc_version}"
  else
    warn "OpenClaw CLI not found. Extension will install but won't work until OpenClaw is installed."
  fi

  local MODE="local"
  local SOURCE=""

  while [ $# -gt 0 ]; do
    case "$1" in
      --relay|-r)   MODE="relay"; shift ;;
      --local|-l)   MODE="local"; shift ;;
      --mode)
        [ $# -lt 2 ] && { fail "--mode requires an argument"; exit 1; }
        MODE="$2"; shift 2
        ;;
      --from-tarball)
        [ $# -lt 2 ] && { fail "--from-tarball requires a path"; exit 1; }
        SOURCE="tarball"; TARBALL_PATH="$2"; shift 2
        ;;
      --help|-h)
        echo "Usage: $0 [OPTIONS]"
        echo ""
        echo "Options:"
        echo "  --local, -l            Local mode (default)"
        echo "  --relay, -r            Relay mode"
        echo "  --mode <mode>          Set mode: local or relay"
        echo "  --from-tarball <path>  Install from .tar.gz"
        echo "  --help, -h             Show this help"
        echo ""
        echo "Pipe install:"
        echo "  curl -sL <url>/install.sh | bash"
        echo "  curl -sL <url>/install.sh | bash -s -- --relay"
        exit 0
        ;;
      *) warn "Unknown option: $1"; exit 1 ;;
    esac
  done

  if [ "${SOURCE}" = "tarball" ]; then
    [ ! -f "${TARBALL_PATH}" ] && { fail "Tarball not found: ${TARBALL_PATH}"; exit 1; }
    install_from_tarball "${TARBALL_PATH}"
  elif [ "${PIPED_INSTALL}" = "true" ]; then
    install_piped
  else
    local script_dir
    script_dir="$(cd "$(dirname "$0")" && pwd)"
    if [ ! -f "${script_dir}/package.json" ]; then
      warn "Not running from source directory. Falling back to git clone..."
      PIPED_INSTALL=true
      install_piped
    else
      install_from_source "${script_dir}"
    fi
  fi

  configure_gochat "${MODE}"

  printf "${GREEN}${BOLD}GoChat extension installed successfully!${NC}\n"
}

# Detect pipe install: stdin is not a terminal
if [ ! -t 0 ]; then
  PIPED_INSTALL=true
fi

main "$@"
