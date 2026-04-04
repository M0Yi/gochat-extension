#!/usr/bin/env bash
set -euo pipefail

# ──────────────────────────────────────────────
# GoChat Extension Installer for OpenClaw
# ──────────────────────────────────────────────

VERSION="2026.3.28"
EXTENSION_NAME="gochat"
PACKAGE_NAME="@m0yi/gochat"
OPENCLAW_MIN_VERSION="2026.3.28"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${CYAN}${BOLD}[gochat]${NC} $*"; }
ok()   { echo -e "${GREEN}${BOLD}[gochat]${NC} $*"; }
warn() { echo -e "${YELLOW}${BOLD}[gochat]${NC} $*"; }
fail() { echo -e "${RED}${BOLD}[gochat]${NC} $*"; }

# ──────────────────────────────────────────────
# Detect OpenClaw installation
# ──────────────────────────────────────────────

detect_openclaw() {
  # Check if openclaw CLI is available
  if command -v openclaw &>/dev/null 2>&1; then
    OPENCLAW_BIN="$(command -v openclaw)"
    info "Found OpenClaw at: ${OPENCLAW_BIN}"
    return 0
  fi

  # Check common install locations
  local paths=(
    "$HOME/.local/bin/openclaw"
    "/usr/local/bin/openclaw"
    "$HOME/.npm-global/bin/openclaw"
    "$(npm config get prefix 2>/dev/null)/bin/openclaw"
  )

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
  # Use OPENCLAW_STATE_DIR if set
  if [ -n "${OPENCLAW_STATE_DIR}" ]; then
    echo "${OPENCLAW_STATE_DIR}"
    return
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
  echo -e "${BLUE}${BOLD}╔───────────────────────────────────${NC}"
  echo -e "${BLUE}${BOLD}  GoChat Extension Installer  v${VERSION}  ${NC}"
  echo -e "${BLUE}${BOLD}╔───────────────────────────────────${NC}"
  echo ""

  # Check prerequisites
  if ! command -v node &>/dev/null 2>&1; then
    fail "Node.js is required but not found. Install Node.js first."
    exit 1
  fi

  if ! command -v npm &>/dev/null 2>&1; then
    fail "npm is required but not found. Install npm first."
    exit 1
  fi

  if detect_openclaw; then
    local oc_version
    oc_version="$("${OPENCLAW_BIN}" --version 2>/dev/null | head -1)"
    info "OpenClaw version: ${oc_version}"
  else
    warn "OpenClaw CLI not found in PATH."
    warn "The extension will be installed but may not work until OpenClaw is installed."
    warn "Install OpenClaw first: https://github.com/openclaw/openclaw"
  fi

  # Determine install mode
  local MODE="local"
  local SOURCE=""

  # Parse arguments
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
        MODE="$2"
        shift 2
        ;;
      --from-tarball)
        SOURCE="tarball"
        TARBALL_PATH="$2"
        shift 2
        ;;
      --help|-h)
        echo "Usage: $0 [OPTIONS]"
        echo ""
        echo "Options:"
        echo "  --local, -l       Local mode (default): built-in HTTP server"
        echo "  --relay, -r       Relay mode: WebSocket relay via GoChat platform"
        echo "  --mode <mode>     Set mode: local or relay"
        echo "  --from-tarball <path>  Install from a .tar.gz file"
        echo "  --help, -h        Show this help"
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

  # Perform installation
  if [ "${SOURCE}" = "tarball" ]; then
    if [ -z "${TARBALL_PATH}" ] || [ ! -f "${TARBALL_PATH}" ]; then
      fail "Tarball not found: ${TARBALL_PATH}"
      exit 1
    fi
    install_from_tarball "${TARBALL_PATH}"
  else
    install_from_source
  fi

  # Configure
  configure_gochat "${MODE}"

  echo -e "${GREEN}${BOLD}✓ GoChat extension installed successfully!${NC}"
}

main "$@"
