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

# ──────────────────────────────────────────────
# Globals (declared upfront for set -u safety)
# ──────────────────────────────────────────────
OPENCLAW_BIN=""
TARBALL_PATH=""

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
      info "Platform: ${PLATFORM} (${ARCH}) — supported"
      return 0
      ;;
    bsd)
      warn "Platform: ${PLATFORM} (${ARCH}) — unofficial, best-effort support"
      return 0
      ;;
    windows)
      fail "Native Windows is not supported. Use WSL (Windows Subsystem for Linux)."
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
  # Check if openclaw CLI is available
  if command -v openclaw &>/dev/null; then
    OPENCLAW_BIN="$(command -v openclaw)"
    info "Found OpenClaw at: ${OPENCLAW_BIN}"
    return 0
  fi

  # Check common install locations per platform
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

  # Add npm global prefix if npm is available
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
  # Use OPENCLAW_STATE_DIR if set (safe under set -u)
  local state_dir="${OPENCLAW_STATE_DIR:-}"
  if [ -n "${state_dir}" ]; then
    echo "${state_dir}"
    return
  fi

  # XDG-compliant location on Linux
  if [ "${PLATFORM}" = "linux" ] || [ "${PLATFORM}" = "linux-wsl" ]; then
    local xdg_dir="${XDG_DATA_HOME:-${HOME}/.local/share}"
    if [ -d "${xdg_dir}/openclaw" ]; then
      echo "${xdg_dir}/openclaw"
      return
    fi
  fi

  # Default location
  echo "${HOME}/.openclaw"
}

get_extensions_dir() {
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  echo "${openclaw_dir}/extensions"
}

# ──────────────────────────────────────────────
# Install functions
# ──────────────────────────────────────────────

install_from_tarball() {
  local tarball="$1"
  local extensions_dir

  extensions_dir="$(get_extensions_dir)"

  # Create extensions directory if needed
  mkdir -p "${extensions_dir}"

  # Remove old installation if exists
  if [ -d "${extensions_dir}/${EXTENSION_NAME}" ]; then
    info "Removing previous installation..."
    rm -rf "${extensions_dir}/${EXTENSION_NAME}"
  fi

  # Extract
  info "Extracting to ${extensions_dir}/${EXTENSION_NAME}..."
  tar -xzf "${tarball}" -C "${extensions_dir}"

  # Install npm dependencies
  if [ -f "${extensions_dir}/${EXTENSION_NAME}/package.json" ]; then
    info "Installing npm dependencies..."
    (cd "${extensions_dir}/${EXTENSION_NAME}" && npm install --production 2>&1) || {
      warn "npm install had warnings (non-fatal)"
    }
  fi

  ok "Extension files installed to ${extensions_dir}/${EXTENSION_NAME}"
}

install_from_source() {
  local script_dir
  script_dir="$(cd "$(dirname "$0")" && pwd)"

  local extensions_dir
  extensions_dir="$(get_extensions_dir)"

  # Create extensions directory if needed
  mkdir -p "${extensions_dir}"

  # Remove old installation if exists
  if [ -d "${extensions_dir}/${EXTENSION_NAME}" ]; then
    info "Removing previous installation..."
    rm -rf "${extensions_dir}/${EXTENSION_NAME}"
  fi

  # Copy source files
  info "Copying extension files to ${extensions_dir}/${EXTENSION_NAME}..."
  cp -r "${script_dir}" "${extensions_dir}/${EXTENSION_NAME}"

  # Remove node_modules from copy if present
  rm -rf "${extensions_dir}/${EXTENSION_NAME}/node_modules"

  # Remove .git directory from copy if present
  rm -rf "${extensions_dir}/${EXTENSION_NAME}/.git"

  # Install npm dependencies
  if [ -f "${extensions_dir}/${EXTENSION_NAME}/package.json" ]; then
    info "Installing npm dependencies..."
    (cd "${extensions_dir}/${EXTENSION_NAME}" && npm install --production 2>&1) || {
      warn "npm install had warnings (non-fatal)"
    }
  fi

  ok "Extension files installed to ${extensions_dir}/${EXTENSION_NAME}"
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
    info "  server:        WebSocket relay to ws://localhost:9750/ws/plugin"
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

  # ── OS & Architecture Detection ──
  detect_os
  check_os_support

  # ── Check prerequisites ──
  if ! command -v node &>/dev/null; then
    fail "Node.js is required but not found."
    case "${PLATFORM}" in
      macos)
        info "Install with: brew install node"
        ;;
      linux|linux-wsl)
        info "Install with: sudo apt install nodejs  OR  sudo dnf install nodejs"
        info "Or use nvm:   curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash"
        ;;
    esac
    exit 1
  fi

  if ! command -v npm &>/dev/null; then
    fail "npm is required but not found."
    case "${PLATFORM}" in
      macos)
        info "Install with: brew install node"
        ;;
      linux|linux-wsl)
        info "Install with: sudo apt install npm  OR  sudo dnf install npm"
        ;;
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
    warn "OpenClaw CLI not found in PATH."
    warn "The extension will be installed but may not work until OpenClaw is installed."
    case "${PLATFORM}" in
      macos)
        warn "Install OpenClaw: brew install openclaw"
        ;;
      linux|linux-wsl)
        warn "Install OpenClaw: curl -sL https://get.openclaw.dev | bash"
        ;;
    esac
  fi

  # ── Parse arguments ──
  local MODE="local"
  local SOURCE=""

  while [ $# -gt 0 ]; do
    case "$1" in
      --relay|-r)
        MODE="relay"
        shift
        ;;
      --local|-l)
        MODE="local"
        shift
        ;;
      --mode)
        if [ $# -lt 2 ]; then
          fail "--mode requires an argument (local or relay)"
          exit 1
        fi
        MODE="$2"
        shift 2
        ;;
      --from-tarball)
        if [ $# -lt 2 ]; then
          fail "--from-tarball requires a file path argument"
          exit 1
        fi
        SOURCE="tarball"
        TARBALL_PATH="$2"
        shift 2
        ;;
      --help|-h)
        echo "Usage: $0 [OPTIONS]"
        echo ""
        echo "Options:"
        echo "  --local, -l            Local mode (default): built-in HTTP server"
        echo "  --relay, -r            Relay mode: WebSocket relay via GoChat platform"
        echo "  --mode <mode>          Set mode: local or relay"
        echo "  --from-tarball <path>  Install from a .tar.gz file"
        echo "  --help, -h             Show this help"
        echo ""
        echo "Examples:"
        echo "  $0                          # Install with local mode"
        echo "  $0 --relay                  # Install with relay mode"
        echo "  $0 --from-tarball ./pkg.tar.gz  # Install from tarball"
        echo "  curl -sL <url>/install.sh | bash          # One-command install (local)"
        echo "  curl -sL <url>/install.sh | bash -s -- --relay  # One-command install (relay)"
        echo ""
        exit 0
        ;;
      *)
        warn "Unknown option: $1"
        echo "Use --help for usage information."
        exit 1
        ;;
    esac
  done

  # ── Perform installation ──
  if [ "${SOURCE}" = "tarball" ]; then
    if [ ! -f "${TARBALL_PATH}" ]; then
      fail "Tarball not found: ${TARBALL_PATH}"
      exit 1
    fi
    install_from_tarball "${TARBALL_PATH}"
  else
    install_from_source
  fi

  # ── Configure ──
  configure_gochat "${MODE}"

  printf "${GREEN}${BOLD}✓ GoChat extension installed successfully!${NC}\n"
}

main "$@"
