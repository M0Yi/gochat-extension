#!/usr/bin/env bash
set -euo pipefail

# ──────────────────────────────────────────────
# GoChat Extension Installer for OpenClaw
# Supports: macOS, Linux (amd64/arm64), WSL
# ──────────────────────────────────────────────

VERSION="2026.4.9-plugin.35"
EXTENSION_NAME="gochat"
OPENCLAW_MIN_VERSION="2026.3.28"
REPO_URL="https://github.com/M0Yi/gochat-extension.git"
REPO_TARBALL_URL="https://codeload.github.com/M0Yi/gochat-extension/tar.gz/refs/heads/main"
DEFAULT_RELAY_HTTP_URL="https://fund.moyi.vip"
DEFAULT_RELAY_WS_URL="wss://fund.moyi.vip/ws/plugin"
RELAY_HTTP_URL="${GOCHAT_RELAY_HTTP_URL:-${DEFAULT_RELAY_HTTP_URL}}"
RELAY_WS_URL="${GOCHAT_RELAY_WS_URL:-${DEFAULT_RELAY_WS_URL}}"

# ──────────────────────────────────────────────
# Globals (ALL declared upfront for set -u)
# ──────────────────────────────────────────────
PLATFORM=""
ARCH=""
OPENCLAW_BIN=""
TARBALL_PATH=""
PIPED_INSTALL=false
MODE_SWITCH_CHANGED=false

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

info()  { printf "${CYAN}${BOLD}[gochat]${NC} %s\n" "$*" >&2; }
ok()    { printf "${GREEN}${BOLD}[gochat]${NC} %s\n" "$*" >&2; }
warn()  { printf "${YELLOW}${BOLD}[gochat]${NC} %s\n" "$*" >&2; }
fail()  { printf "${RED}${BOLD}[gochat]${NC} %s\n" "$*" >&2; }

extract_version_triplet() {
  local raw="$1"
  printf '%s' "${raw}" | grep -oE '[0-9]{4}\.[0-9]{1,2}\.[0-9]{1,2}' | head -1 || true
}

version_triplet_key() {
  local triplet="$1"
  local year month day
  IFS='.' read -r year month day <<EOF
${triplet}
EOF
  [ -z "${year:-}" ] && return 1
  [ -z "${month:-}" ] && return 1
  [ -z "${day:-}" ] && return 1
  printf '%04d%02d%02d\n' "${year}" "${month}" "${day}"
}

warn_if_known_pairing_bug_host() {
  local raw_version="$1"
  local parsed
  local parsed_key
  parsed="$(extract_version_triplet "${raw_version}")"
  [ -z "${parsed}" ] && return 0
  parsed_key="$(version_triplet_key "${parsed}" || true)"
  [ -z "${parsed_key}" ] && return 0
  if [ "${parsed_key}" -lt 20260408 ]; then
    warn "OpenClaw ${parsed} is older than 2026.4.8 and is known to surface local subagent pairing-required failures."
    warn "GoChat ${VERSION} now surfaces subagent permission status and approval commands in chat, but upgrading OpenClaw is still recommended."
  fi
}

get_gochat_current_mode() {
  local config_file="$1"
  [ ! -f "${config_file}" ] && return 0
  node -e "
    try {
      const c = JSON.parse(require('fs').readFileSync(process.argv[1], 'utf8'));
      const mode = c.channels && c.channels.gochat && c.channels.gochat.mode;
      if (mode) process.stdout.write(String(mode));
    } catch {}
  " "${config_file}" 2>/dev/null || true
}

mode_switch_authorization_matches() {
  local config_file="$1"
  local target_mode="$2"
  [ ! -f "${config_file}" ] && return 1
  MODE_SWITCH_AUTH_EXPIRES_AT="$(node -e "
    try {
      const c = JSON.parse(require('fs').readFileSync(process.argv[1], 'utf8'));
      const auth = c.channels && c.channels.gochat && c.channels.gochat.modeSwitchAuthorization;
      const target = String(process.argv[2] || '');
      if (!auth || auth.targetMode !== target) process.exit(1);
      if (auth.expiresAt) {
        const expires = new Date(auth.expiresAt);
        if (Number.isNaN(expires.getTime()) || expires.getTime() <= Date.now()) process.exit(1);
        process.stdout.write(String(auth.expiresAt));
      }
    } catch {
      process.exit(1);
    }
  " "${config_file}" "${target_mode}" 2>/dev/null || true)"
  [ -n "${MODE_SWITCH_AUTH_EXPIRES_AT:-}" ]
}

require_mode_switch_authorization_if_needed() {
  local config_file="$1"
  local target_mode="$2"
  local current_mode=""
  current_mode="$(get_gochat_current_mode "${config_file}")"
  MODE_SWITCH_CHANGED=false

  if [ -z "${current_mode}" ] || [ "${current_mode}" = "${target_mode}" ]; then
    return 0
  fi

  if mode_switch_authorization_matches "${config_file}" "${target_mode}"; then
    MODE_SWITCH_CHANGED=true
    info "Using one-time mode switch authorization: ${current_mode} -> ${target_mode}"
    return 0
  fi

  fail "Switching GoChat mode from ${current_mode} to ${target_mode} requires explicit authorization."
  fail "Run: openclaw gochat authorize-mode-switch --mode ${target_mode}"
  exit 1
}

consume_mode_switch_authorization_if_needed() {
  local config_file="$1"
  if [ "${MODE_SWITCH_CHANGED}" != "true" ]; then
    return 0
  fi

  node -e "
    try {
      const fs = require('fs');
      const file = process.argv[1];
      const c = JSON.parse(fs.readFileSync(file, 'utf8'));
      if (c.channels && c.channels.gochat && c.channels.gochat.modeSwitchAuthorization) {
        delete c.channels.gochat.modeSwitchAuthorization;
        fs.writeFileSync(file, JSON.stringify(c, null, 2) + '\n');
      }
    } catch {}
  " "${config_file}" 2>/dev/null || true
}

# ──────────────────────────────────────────────
# JSON parsing helper (WSL-safe)
# Uses process.argv instead of /dev/stdin which is unreliable in WSL.
# Supports dot-notation keys: json_val '{"a":{"b":1}}' 'a.b' → 1
# ──────────────────────────────────────────────
json_val() {
  local json_data="$1"
  local key="$2"
  node -e "const a=process.argv.slice(1),k=a[0],d=JSON.parse(a[1]);let v=d;for(const s of k.split('.')){if(v==null||v[s]===undefined){v=null;break}v=v[s]}if(v!==null&&v!==undefined)process.stdout.write(String(v))" "$key" "$json_data" 2>/dev/null || true
}

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

get_skills_dir() {
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  echo "${openclaw_dir}/skills"
}

get_config_file() {
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  echo "${openclaw_dir}/openclaw.json"
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
# Git bootstrap
# ──────────────────────────────────────────────

run_with_privilege() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    return 1
  fi
}

try_auto_install_git() {
  if command -v git >/dev/null 2>&1; then
    return 0
  fi

  warn "git not found. Attempting automatic installation..."

  case "${PLATFORM}" in
    macos)
      if command -v brew >/dev/null 2>&1; then
        brew install git || return 1
      else
        return 1
      fi
      ;;
    linux|linux-wsl|bsd)
      if command -v apt-get >/dev/null 2>&1; then
        run_with_privilege apt-get update || return 1
        run_with_privilege apt-get install -y git || return 1
      elif command -v dnf >/dev/null 2>&1; then
        run_with_privilege dnf install -y git || return 1
      elif command -v yum >/dev/null 2>&1; then
        run_with_privilege yum install -y git || return 1
      elif command -v pacman >/dev/null 2>&1; then
        run_with_privilege pacman -Sy --noconfirm git || return 1
      elif command -v apk >/dev/null 2>&1; then
        run_with_privilege apk add --no-cache git || return 1
      elif command -v zypper >/dev/null 2>&1; then
        run_with_privilege zypper install -y git || return 1
      else
        return 1
      fi
      ;;
    *)
      return 1
      ;;
  esac

  if command -v git >/dev/null 2>&1; then
    ok "git installed successfully."
    return 0
  fi

  return 1
}

download_repo_tarball() {
  local output_path="$1"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${REPO_TARBALL_URL}" -o "${output_path}"
    return $?
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO "${output_path}" "${REPO_TARBALL_URL}"
    return $?
  fi

  return 1
}

# ──────────────────────────────────────────────
# Install functions
# ──────────────────────────────────────────────

install_from_tarball() {
  local tarball="$1"
  local extensions_dir
  local target
  local tmp_extract
  local extracted_dir
  extensions_dir="$(get_extensions_dir)"
  target="${extensions_dir}/${EXTENSION_NAME}"

  if [ -n "${OPENCLAW_BIN}" ] && [ -x "${OPENCLAW_BIN}" ]; then
    info "Installing via OpenClaw managed plugin installer..."
    if "${OPENCLAW_BIN}" plugins install "${tarball}" >/dev/null 2>&1; then
      "${OPENCLAW_BIN}" plugins enable "${EXTENSION_NAME}" >/dev/null 2>&1 || true
      ensure_plugin_trusted
      ok "Installed to ${target}"
      install_bundled_skills "${target}"
      return 0
    fi
    warn "Managed install failed, falling back to direct file install."
  fi

  ensure_dir_writable "${extensions_dir}"

  if [ -d "${target}" ]; then
    info "Removing previous installation..."
    rm -rf "${target}"
  fi

  info "Extracting to ${target}..."
  tmp_extract="$(mktemp -d "${TMPDIR:-/tmp}/gochat-extract.XXXXXX")"
  tar -xzf "${tarball}" -C "${tmp_extract}" || {
    rm -rf "${tmp_extract}"
    fail "Failed to extract tarball."
    exit 1
  }

  extracted_dir="$(find "${tmp_extract}" -mindepth 1 -maxdepth 1 -type d | head -1 || true)"
  if [ -n "${extracted_dir}" ]; then
    mv "${extracted_dir}" "${target}"
  else
    mv "${tmp_extract}" "${target}"
    tmp_extract=""
  fi
  [ -n "${tmp_extract}" ] && rm -rf "${tmp_extract}"

  if [ -f "${target}/package.json" ]; then
    info "Installing npm dependencies..."
    (cd "${target}" && npm install --omit=dev 2>&1) || {
      warn "npm install had warnings (non-fatal)"
    }
  fi

  ensure_plugin_trusted
  ok "Installed to ${target}"
  install_bundled_skills "${target}"
}

install_from_source() {
  local source_dir="$1"
  local extensions_dir
  extensions_dir="$(get_extensions_dir)"

  if [ -n "${OPENCLAW_BIN}" ] && [ -x "${OPENCLAW_BIN}" ]; then
    info "Installing via OpenClaw managed plugin installer..."
    if "${OPENCLAW_BIN}" plugins install "${source_dir}" >/dev/null 2>&1; then
      "${OPENCLAW_BIN}" plugins enable "${EXTENSION_NAME}" >/dev/null 2>&1 || true
      ensure_plugin_trusted
      ok "Installed to ${extensions_dir}/${EXTENSION_NAME}"
      install_bundled_skills "${extensions_dir}/${EXTENSION_NAME}"
      return 0
    fi
    warn "Managed install failed, falling back to direct file install."
  fi

  ensure_dir_writable "${extensions_dir}"

  if [ -d "${extensions_dir}/${EXTENSION_NAME}" ]; then
    info "Removing previous installation..."
    rm -rf "${extensions_dir}/${EXTENSION_NAME}"
  fi

  info "Copying to ${extensions_dir}/${EXTENSION_NAME}..."
  if command -v rsync >/dev/null 2>&1; then
    rsync -a --exclude='node_modules' --exclude='.git' "${source_dir}/" "${extensions_dir}/${EXTENSION_NAME}/"
  else
    cp -r "${source_dir}" "${extensions_dir}/${EXTENSION_NAME}"
    rm -rf "${extensions_dir}/${EXTENSION_NAME}/node_modules" 2>/dev/null || true
    rm -rf "${extensions_dir}/${EXTENSION_NAME}/.git" 2>/dev/null || true
  fi

  if [ -f "${extensions_dir}/${EXTENSION_NAME}/package.json" ]; then
    info "Installing npm dependencies..."
    (cd "${extensions_dir}/${EXTENSION_NAME}" && npm install --omit=dev 2>&1) || {
      warn "npm install had warnings (non-fatal)"
    }
  fi

  ensure_plugin_trusted
  ok "Installed to ${extensions_dir}/${EXTENSION_NAME}"
  install_bundled_skills "${extensions_dir}/${EXTENSION_NAME}"
}

install_bundled_skills() {
  local extension_dir="$1"
  local source_skills_dir="${extension_dir}/skills"
  local target_skills_dir
  target_skills_dir="$(get_skills_dir)"

  if [ ! -d "${source_skills_dir}" ]; then
    return 0
  fi

  ensure_dir_writable "${target_skills_dir}"
  info "Installing bundled skills to ${target_skills_dir}..."

  if command -v rsync >/dev/null 2>&1; then
    rsync -a "${source_skills_dir}/" "${target_skills_dir}/"
  else
    mkdir -p "${target_skills_dir}"
    cp -R "${source_skills_dir}/." "${target_skills_dir}/"
  fi

  ok "Bundled skills installed"
}

install_piped() {
  local tmp_dir
  local tarball
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/gochat-install.XXXXXX")"

  info "Pipe install detected..."
  if ! command -v git &>/dev/null; then
    try_auto_install_git || warn "Automatic git installation was unavailable. Falling back if possible..."
  fi

  if command -v git &>/dev/null; then
    info "Cloning from ${REPO_URL}..."
    git clone --depth 1 "${REPO_URL}" "${tmp_dir}/gochat-extension" 2>&1 || {
      fail "git clone failed. Check network or install git."
      rm -rf "${tmp_dir}"
      exit 1
    }
    install_from_source "${tmp_dir}/gochat-extension"
    rm -rf "${tmp_dir}"
  elif command -v curl &>/dev/null || command -v wget &>/dev/null; then
    info "git not found. Falling back to GitHub source tarball..."
    tarball="${tmp_dir}/gochat-extension.tar.gz"
    if download_repo_tarball "${tarball}"; then
      install_from_tarball "${tarball}"
      rm -rf "${tmp_dir}"
    else
      fail "GitHub source tarball download failed."
      fail "Refusing to fall back to npm because that may install an older plugin version."
      rm -rf "${tmp_dir}"
      exit 1
    fi
  else
    fail "Pipe install requires git or curl/wget, but none are installed."
    fail "Install one of them first:"
    case "${PLATFORM}" in
      macos)           fail "  brew install git  OR  brew install curl" ;;
      linux|linux-wsl) fail "  sudo apt install git  OR  sudo apt install curl" ;;
      bsd)             fail "  pkg install git  OR  pkg install curl" ;;
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
  local pair_code="${2:-}"
  local extensions_dir
  extensions_dir="$(get_extensions_dir)"
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  local config_file="${openclaw_dir}/openclaw.json"

  require_mode_switch_authorization_if_needed "${config_file}" "${mode}"

  echo ""
  info "──── GoChat Configuration ────"
  info "  platform:      ${PLATFORM} (${ARCH})"
  info "  mode:          ${mode}"
  info "  extension dir: ${extensions_dir}/${EXTENSION_NAME}"

  if [ "${mode}" = "relay" ]; then
    info "  relay:         ${RELAY_WS_URL}"
    info "  dmPolicy:      open (skip pairing)"
    if [ -n "${pair_code}" ]; then
      info "  connectCode:   ${pair_code}"
    fi
    info "──────────────────────────"
    echo ""
    register_relay "${pair_code}"
  else
    info "  server:        built-in HTTP API on port 9750"
    info "  dmPolicy:      open"
    info "──────────────────────────"
    ensure_gochat_install_defaults "${config_file}" "local"
  fi

  consume_mode_switch_authorization_if_needed "${config_file}"

  echo ""
  ok "GoChat is ready!"
  echo ""
  info "Usage:"
  info "  openclaw gateway run     # Start gateway"
  info "  openclaw channels list   # Check channel status"
  info "  openclaw gochat show-credentials  # View credentials"
  echo ""
}

ensure_config_file() {
  local config_file="$1"
  local config_dir
  config_dir="$(dirname "${config_file}")"
  mkdir -p "${config_dir}"
  if [ ! -f "${config_file}" ]; then
    printf "{\n}\n" > "${config_file}"
  fi
}

ensure_plugin_trusted() {
  local config_file
  config_file="$(get_config_file)"
  ensure_config_file "${config_file}"

  node -e "
    const fs = require('fs');
    const file = process.argv[1];
    const pluginId = process.argv[2];
    const c = JSON.parse(fs.readFileSync(file, 'utf8'));
    if (!c.plugins || typeof c.plugins !== 'object') c.plugins = {};
    const allow = Array.isArray(c.plugins.allow) ? c.plugins.allow.slice() : [];
    if (!allow.includes(pluginId)) allow.push(pluginId);
    c.plugins.allow = allow;
    fs.writeFileSync(file, JSON.stringify(c, null, 2) + '\n');
  " "${config_file}" "${EXTENSION_NAME}" 2>/dev/null || true
}

ensure_gochat_install_defaults() {
  local config_file="$1"
  local mode="${2:-relay}"

  ensure_config_file "${config_file}"

  node -e "
    const fs = require('fs');
    const c = JSON.parse(fs.readFileSync('${config_file}','utf8'));
    if (!c.channels) c.channels = {};
    c.channels.gochat = Object.assign(c.channels.gochat || {}, {
      enabled: true,
      mode: '${mode}',
      dmPolicy: 'open',
      blockStreaming: true
    });
    fs.writeFileSync('${config_file}', JSON.stringify(c, null, 2) + '\n');
  " 2>/dev/null || true
}

claim_relay_pair_code() {
  local config_file="$1"
  local pair_code="$2"
  local response_file
  local http_code

  ensure_config_file "${config_file}"

  info "Claiming connection code ${pair_code}..."
  local reg_response
  response_file="$(mktemp "${TMPDIR:-/tmp}/gochat-claim-response.XXXXXX")"
  http_code="$(curl -sS -o "${response_file}" -w "%{http_code}" -X POST "${RELAY_HTTP_URL}/api/plugin/pair/claim" \
    -H "Content-Type: application/json" \
    -d "{\"code\":\"${pair_code}\",\"name\":\"OpenClaw GoChat Plugin\"}" \
    --connect-timeout 10 \
    --max-time 20 2>/dev/null || true)"
  reg_response="$(cat "${response_file}" 2>/dev/null || true)"
  rm -f "${response_file}"

  if [ -z "${reg_response}" ] && [ -z "${http_code}" ]; then
    fail "Failed to claim connection code. Check the code and network, then try again."
    exit 1
  fi

  if [ "${http_code:-0}" -lt 200 ] || [ "${http_code:-0}" -ge 300 ]; then
    case "${reg_response}" in
      *"pair code expired"*)
        fail "Connection code expired. Generate a fresh 6-digit code and try again."
        ;;
      *"pair code already used"*)
        fail "Connection code was already used. Generate a fresh 6-digit code and try again."
        ;;
      *"pair code not found"*)
        fail "Connection code was not found. Double-check the 6-digit code or generate a new one."
        ;;
      *)
        fail "Failed to claim connection code (HTTP ${http_code}). Check the code and network, then try again."
        ;;
    esac
    exit 1
  fi

  local reg_channel_id=""
  local reg_secret=""
  local reg_name=""
  local reg_relay_url=""
  reg_channel_id="$(json_val "${reg_response}" 'channelId')"
  reg_secret="$(json_val "${reg_response}" 'secret')"
  reg_name="$(json_val "${reg_response}" 'name')"
  reg_relay_url="$(json_val "${reg_response}" 'relayPlatformUrl')"

  if [ -z "${reg_channel_id}" ] || [ -z "${reg_secret}" ]; then
    fail "Connection code response missing channelId or secret."
    exit 1
  fi

  if [ -z "${reg_relay_url}" ]; then
    reg_relay_url="${RELAY_WS_URL}"
  fi

  ok "Connection code accepted. channelId=${reg_channel_id}"

  info "Writing config..."
  node -e "
    const fs = require('fs');
    const c = JSON.parse(fs.readFileSync('${config_file}','utf8'));
    if (!c.channels) c.channels = {};
    c.channels.gochat = Object.assign(c.channels.gochat || {}, {
      enabled: true,
      mode: 'relay',
      name: '${reg_name}',
      channelId: '${reg_channel_id}',
      webhookSecret: '${reg_secret}',
      relayPlatformUrl: '${reg_relay_url}',
      dmPolicy: 'open',
      blockStreaming: true
    });
    fs.writeFileSync('${config_file}', JSON.stringify(c, null, 2) + '\n');
  " 2>/dev/null || {
    fail "Failed to write config."
    exit 1
  }
  ok "Config saved."

  print_credentials
}

register_relay() {
  local pair_code="${1:-}"
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  local config_file="${openclaw_dir}/openclaw.json"

  if [ -n "${pair_code}" ]; then
    claim_relay_pair_code "${config_file}" "${pair_code}"
    return 0
  fi

  # Create the base config up front so fresh installs can be registered
  # immediately instead of waiting for the first gateway startup.
  ensure_gochat_install_defaults "${config_file}" "relay"

  local existing_id
  existing_id="$(node -e "
    try {
      const c = JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'));
      const g = c.channels && c.channels.gochat;
      if (g && g.channelId) process.stdout.write(g.channelId);
    } catch {}
  " "${config_file}" 2>/dev/null || true)"

  if [ -n "${existing_id}" ]; then
    info "Existing channelId: ${existing_id} — skipping registration."
    ensure_dm_policy_open "${config_file}"
    ensure_gochat_install_defaults "${config_file}" "relay"
    print_credentials
    return 0
  fi

  info "Registering with relay platform..."
  local reg_response
  local device_name
  local device_name_json
  device_name="$(node -e "
    try {
      const c = JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'));
      const g = c.channels && c.channels.gochat;
      process.stdout.write(String(g && g.name ? g.name : 'OpenClaw GoChat Plugin'));
    } catch {
      process.stdout.write('OpenClaw GoChat Plugin');
    }
  " "${config_file}" 2>/dev/null || true)"
  [ -z "${device_name}" ] && device_name="OpenClaw GoChat Plugin"
  device_name_json="$(node -e "process.stdout.write(JSON.stringify(process.argv[1]))" "${device_name}" 2>/dev/null || true)"
  [ -z "${device_name_json}" ] && device_name_json='"OpenClaw GoChat Plugin"'

  local attempt=1
  local max_attempts=3
  while [ "${attempt}" -le "${max_attempts}" ]; do
    reg_response="$(curl -sf -X POST "${RELAY_HTTP_URL}/api/plugin/register" \
      -H "Content-Type: application/json" \
      -d "{\"name\":${device_name_json}}" \
      --connect-timeout 10 \
      --max-time 15 2>/dev/null || true)"
    if [ -n "${reg_response}" ]; then
      break
    fi
    if [ "${attempt}" -lt "${max_attempts}" ]; then
      warn "Relay registration attempt ${attempt}/${max_attempts} failed. Retrying..."
      sleep 2
    fi
    attempt=$((attempt + 1))
  done

  if [ -z "${reg_response}" ]; then
    warn "Registration failed (network error). Will auto-register on first gateway start."
    return 0
  fi

  local reg_channel_id=""
  local reg_secret=""
  reg_channel_id="$(json_val "${reg_response}" "channelId")"
  reg_secret="$(json_val "${reg_response}" "secret")"

  if [ -z "${reg_channel_id}" ] || [ -z "${reg_secret}" ]; then
    warn "Registration response missing channelId or secret."
    return 0
  fi

  ok "Registered! channelId=${reg_channel_id}"

  info "Writing config..."
  node -e "
    const fs = require('fs');
    const c = JSON.parse(fs.readFileSync('${config_file}','utf8'));
    if (!c.channels) c.channels = {};
    c.channels.gochat = Object.assign(c.channels.gochat || {}, {
      enabled: true,
      mode: 'relay',
      channelId: '${reg_channel_id}',
      webhookSecret: '${reg_secret}',
      relayPlatformUrl: '${RELAY_WS_URL}',
      dmPolicy: 'open',
      blockStreaming: true
    });
    fs.writeFileSync('${config_file}', JSON.stringify(c, null, 2) + '\n');
  " 2>/dev/null || {
    warn "Failed to write config."
    return 0
  }
  ok "Config saved."

  print_credentials
}

ensure_dm_policy_open() {
  local config_file="$1"
  node -e "
    const fs = require('fs');
    const c = JSON.parse(fs.readFileSync('${config_file}','utf8'));
    const g = c.channels && c.channels.gochat;
    if (g) {
      g.dmPolicy = 'open';
      g.blockStreaming = true;
      fs.writeFileSync('${config_file}', JSON.stringify(c, null, 2) + '\n');
    }
  " 2>/dev/null || true
}

print_credentials() {
  local openclaw_dir
  openclaw_dir="$(get_openclaw_dir)"
  local config_file="${openclaw_dir}/openclaw.json"

  if [ ! -f "${config_file}" ]; then
    return 0
  fi

  local channel_id=""
  local secret=""

  if command -v node &>/dev/null; then
    channel_id="$(node -e "
      try {
        const c = JSON.parse(require('fs').readFileSync('${config_file}','utf8'));
        const g = c.channels && c.channels.gochat;
        if (g) process.stdout.write(g.channelId || '');
      } catch {}
    " 2>/dev/null || true)"

    secret="$(node -e "
      try {
        const c = JSON.parse(require('fs').readFileSync('${config_file}','utf8'));
        const g = c.channels && c.channels.gochat;
        if (g) process.stdout.write(g.webhookSecret || '');
      } catch {}
    " 2>/dev/null || true)"
  fi

  if [ -z "${channel_id}" ] && [ -z "${secret}" ]; then
    return 0
  fi

  echo ""
  printf "${CYAN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
  printf "${CYAN}${BOLD}  GoChat Connection Credentials${NC}\n"
  printf "${CYAN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
  echo ""

  if [ -n "${channel_id}" ]; then
    printf "  Channel ID:    ${GREEN}${channel_id}${NC}\n"
  else
    printf "  Channel ID:    (pending — will be registered on first gateway start)\n"
  fi

  if [ -n "${secret}" ]; then
    printf "  Secret Key:    ${GREEN}${secret}${NC}\n"
  else
    printf "  Secret Key:    (pending — will be generated on first gateway start)\n"
  fi

  printf "  DM Policy:     open (no pairing approval needed)\n"

  echo ""
  printf "${CYAN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
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
    warn_if_known_pairing_bug_host "${oc_version}"
  else
    warn "OpenClaw CLI not found. Extension will install but won't work until OpenClaw is installed."
  fi

  local MODE="relay"
  local SOURCE=""
  local PAIR_CODE=""

  while [ $# -gt 0 ]; do
    case "$1" in
      --relay|-r)   MODE="relay"; shift ;;
      --local|-l)   MODE="local"; shift ;;
      --code)
        [ $# -lt 2 ] && { fail "--code requires an argument"; exit 1; }
        PAIR_CODE="$2"; MODE="relay"; shift 2
        ;;
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
        echo "  --relay, -r            Relay mode (default, auto-register)"
        echo "  --local, -l            Local mode"
        echo "  --code <code>          Claim a 6-digit relay connection code"
        echo "  --mode <mode>          Set mode: local or relay"
        echo "  --from-tarball <path>  Install from .tar.gz"
        echo "  --help, -h             Show this help"
        echo ""
        echo "Pipe install:"
        echo "  curl -sL <url>/install.sh | bash"
        echo "  curl -sL <url>/install.sh | bash -s -- 123456"
        exit 0
        ;;
      *)
        if [ -z "${PAIR_CODE}" ] && [ "${1#-}" = "$1" ]; then
          PAIR_CODE="$1"
          MODE="relay"
          shift
        else
          warn "Unknown option: $1"
          exit 1
        fi
        ;;
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

  configure_gochat "${MODE}" "${PAIR_CODE}"

  echo ""
  printf "${CYAN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
  printf "${CYAN}${BOLD}  Environment Summary${NC}\n"
  printf "${CYAN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
  printf "  Plugin:        GoChat v${VERSION}\n"
  printf "  Platform:      ${PLATFORM} (${ARCH})\n"
  printf "  Node.js:       ${node_version}\n"
  if [ -n "${OPENCLAW_BIN}" ]; then
    local oc_ver
    oc_ver="$("${OPENCLAW_BIN}" --version 2>/dev/null | head -1 || echo "unknown")"
    printf "  OpenClaw:      ${oc_ver}\n"
  else
    printf "  OpenClaw:      (not installed)\n"
  fi
  printf "${CYAN}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
  echo ""

  printf "${GREEN}${BOLD}GoChat extension installed successfully!${NC}\n"
}

# Detect pipe install: stdin is not a terminal
if [ ! -t 0 ]; then
  PIPED_INSTALL=true
fi

main "$@"
