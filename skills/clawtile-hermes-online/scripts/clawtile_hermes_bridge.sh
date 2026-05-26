#!/usr/bin/env bash
set -u -o pipefail

: "${CLAWTILE_BASE:=https://voinko.com}"
: "${CLAWTILE_TOKEN:?CLAWTILE_TOKEN env var is required (Bearer ct_a_xxx)}"
: "${HERMES_BIN:=hermes}"
: "${BRIDGE_LOG:=$HOME/.clawtile-hermes-bridge.log}"
: "${MAX_TRANSCRIPT_CHARS:=20000}"
: "${SOURCE_LABEL:=hermes:auto-bridge}"

CLAWTILE_BASE="${CLAWTILE_BASE%/}"
if [[ "$CLAWTILE_BASE" == */api/agent ]]; then
  AGENT_BASE="$CLAWTILE_BASE"
else
  AGENT_BASE="${CLAWTILE_BASE}/api/agent"
fi
SSE_URL="${AGENT_BASE}/events"

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >> "$BRIDGE_LOG"
}

abort() {
  log "FATAL: $*"
  exit 1
}

for cmd in curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || abort "missing command: $cmd"
done

command -v "$HERMES_BIN" >/dev/null 2>&1 || log "WARN: $HERMES_BIN not found yet; will retry on event"

DEDUP_FILE="${TMPDIR:-/tmp}/clawtile-hermes-bridge.dedup"
DEDUP_TTL=$((10 * 60))
: > "$DEDUP_FILE" 2>/dev/null || true

api_url() {
  local path="$1"
  printf '%s/%s' "$AGENT_BASE" "${path#/}"
}

json_field() {
  local file="$1"
  local expr="$2"
  python3 - "$file" "$expr" <<'PY'
import json
import sys

path, expr = sys.argv[1], sys.argv[2]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)

value = data
for part in expr.split("."):
    if isinstance(value, dict):
        value = value.get(part)
    else:
        value = None
        break

if value is None:
    value = ""
if isinstance(value, (dict, list)):
    print(json.dumps(value, ensure_ascii=False))
else:
    print(str(value))
PY
}

already_seen() {
  local rid="$1" now cutoff
  now=$(date +%s)
  cutoff=$((now - DEDUP_TTL))
  awk -v cutoff="$cutoff" '$1 >= cutoff' "$DEDUP_FILE" > "${DEDUP_FILE}.tmp" 2>/dev/null \
    && mv "${DEDUP_FILE}.tmp" "$DEDUP_FILE"
  if grep -qE "[[:space:]]${rid}\$" "$DEDUP_FILE" 2>/dev/null; then
    return 0
  fi
  printf '%s\t%s\n' "$now" "$rid" >> "$DEDUP_FILE"
  return 1
}

mark_state() {
  local rid="$1"
  local state="$2"
  curl --silent --show-error --fail \
    -X POST "$(api_url "/recordings/${rid}/state")" \
    -H "Authorization: Bearer ${CLAWTILE_TOKEN}" \
    -H "Content-Type: application/json" \
    --data "{\"state\":\"${state}\"}" >/dev/null 2>>"$BRIDGE_LOG" || true
}

build_prompt() {
  local rid="$1"
  local title="$2"
  local transcript_file="$3"
  python3 - "$rid" "$title" "$MAX_TRANSCRIPT_CHARS" "$transcript_file" <<'PY'
import sys

rid, title, max_chars, transcript_path = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
with open(transcript_path, "r", encoding="utf-8") as f:
    transcript = f.read().strip()
if len(transcript) > max_chars:
    transcript = transcript[:max_chars] + "\n\n[Transcript truncated by bridge limit]"

print(f"""请根据下面的 ClawTile 录音转写生成一份可直接写回平台的中文纪要。

录音 ID: {rid}
标题: {title or "未命名录音"}

要求:
- 严格只输出 JSON 对象，不要 Markdown 代码块。
- 字段必须包含 summary 和 action_items。
- summary 使用 3 到 8 个要点或短段落，覆盖核心结论、关键讨论、分歧/风险。
- action_items 是数组，每项包含 text、owner、due、priority；无法判断的字段留空，priority 使用 low/normal/high。
- 不要解释你的工作过程。

JSON 形状:
{{"summary":"...","action_items":[{{"text":"...","owner":"","due":"","priority":"normal"}}]}}

转写正文:
{transcript}
""")
PY
}

build_summary_payload() {
  local output_file="$1"
  local rid="$2"
  python3 - "$output_file" "$rid" "$SOURCE_LABEL" "${HERMES_MODEL:-}" <<'PY'
import json
import re
import sys

output_file, rid, source_label, model = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
raw = open(output_file, "r", encoding="utf-8", errors="replace").read().strip()

def clean(value: str) -> str:
    value = value.strip()
    value = re.sub(r"^```(?:json|JSON)?\s*", "", value)
    value = re.sub(r"\s*```$", "", value)
    return value.strip()

cleaned = clean(raw)
data = None
try:
    data = json.loads(cleaned)
except Exception:
    match = re.search(r"\{.*\}", cleaned, flags=re.S)
    if match:
        try:
            data = json.loads(match.group(0))
        except Exception:
            data = None

if isinstance(data, dict):
    summary = str(data.get("summary") or "").strip()
    action_items = data.get("action_items") or []
else:
    summary = cleaned
    action_items = []

if not summary:
    summary = raw or "(Hermes returned an empty summary.)"

clean_items = []
if isinstance(action_items, list):
    for item in action_items[:50]:
        if not isinstance(item, dict):
            continue
        text = str(item.get("text") or "").strip()
        if not text:
            continue
        priority = str(item.get("priority") or "normal").strip().lower()
        if priority not in {"low", "normal", "high"}:
            priority = "normal"
        clean_items.append({
            "text": text[:500],
            "owner": str(item.get("owner") or "").strip(),
            "due": str(item.get("due") or "").strip(),
            "priority": priority,
        })

payload = {
    "summary": summary[:8192],
    "action_items": clean_items,
    "source_label": source_label,
    "model": model or "hermes",
    "metadata": {
        "bridge": "clawtile-hermes-online",
        "runtime": "hermes-agent",
        "recording_id": rid,
    },
}
print(json.dumps(payload, ensure_ascii=False))
PY
}

process_recording() {
  local rid="$1"
  local event_title="${2:-}"
  local workdir transcript_json transcript_text hermes_output payload prompt title rc
  workdir="$(mktemp -d "${TMPDIR:-/tmp}/clawtile-hermes.XXXXXX")" || return 1
  transcript_json="${workdir}/transcript.json"
  transcript_text="${workdir}/transcript.txt"
  hermes_output="${workdir}/hermes.out"
  payload="${workdir}/summary.json"

  log "processing recording=${rid}"
  mark_state "$rid" "dispatched"

  if ! curl --silent --show-error --fail \
      -H "Authorization: Bearer ${CLAWTILE_TOKEN}" \
      "$(api_url "/recordings/${rid}/transcript")" > "$transcript_json" 2>>"$BRIDGE_LOG"; then
    log "ERROR: transcript fetch failed for ${rid}"
    mark_state "$rid" "failed"
    rm -rf "$workdir"
    return 1
  fi

  title="$(json_field "$transcript_json" "title")"
  if [ -z "$title" ]; then
    title="$event_title"
  fi
  json_field "$transcript_json" "text" > "$transcript_text"
  if [ ! -s "$transcript_text" ]; then
    log "ERROR: transcript text empty for ${rid}"
    mark_state "$rid" "failed"
    rm -rf "$workdir"
    return 1
  fi

  prompt="$(build_prompt "$rid" "$title" "$transcript_text")"
  if [ -n "${HERMES_MODEL:-}" ]; then
    "$HERMES_BIN" -z "$prompt" --model "$HERMES_MODEL" > "$hermes_output" 2>>"$BRIDGE_LOG"
  else
    "$HERMES_BIN" -z "$prompt" > "$hermes_output" 2>>"$BRIDGE_LOG"
  fi
  rc=$?
  if [ "$rc" -ne 0 ]; then
    log "ERROR: hermes failed for ${rid} rc=${rc}"
    mark_state "$rid" "failed"
    rm -rf "$workdir"
    return 1
  fi

  build_summary_payload "$hermes_output" "$rid" > "$payload"
  if ! curl --silent --show-error --fail \
      -X POST "$(api_url "/recordings/${rid}/summary")" \
      -H "Authorization: Bearer ${CLAWTILE_TOKEN}" \
      -H "Content-Type: application/json" \
      --data-binary "@${payload}" >/dev/null 2>>"$BRIDGE_LOG"; then
    log "ERROR: summary write failed for ${rid}"
    mark_state "$rid" "failed"
    rm -rf "$workdir"
    return 1
  fi

  log "summary written for ${rid}"
  rm -rf "$workdir"
}

run_once() {
  local rid="${1:-}"
  if [ -z "$rid" ]; then
    abort "usage: $0 once <recording_id>"
  fi
  process_recording "$rid" ""
}

# reconcile_pending 拉一次还没有总结的已转写录音，逐条处理。SSE 是内存事件，
# 后端不会重放；bridge 重启、网络断线、STT 完成时 bridge 不在线都会让事件
# 永久丢失。每次进入 / 重连 SSE 之前调一次这个就能补齐。
# 依赖 already_seen 去重，避免和 SSE 同时收到时重复处理。
reconcile_pending() {
  local resp rid title
  resp="$(curl --silent --show-error --fail --connect-timeout 15 \
    -H "Authorization: Bearer ${CLAWTILE_TOKEN}" \
    "$(api_url "/recordings?summary_state=none&status=completed&limit=20")" 2>>"$BRIDGE_LOG")" || {
    log "reconcile: list_recordings failed"
    return 1
  }
  if [ -z "$resp" ]; then
    return 0
  fi
  # 解析 recordings[] 里的 id + title，逐行输出 "id\ttitle"
  while IFS=$'\t' read -r rid title; do
    [ -z "$rid" ] && continue
    if already_seen "$rid"; then
      continue
    fi
    log "reconcile: catching up pending recording=${rid}"
    process_recording "$rid" "$title" &
  done < <(printf '%s' "$resp" | python3 - <<'PY' 2>>"$BRIDGE_LOG" || true
import json
import sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for rec in (data.get("recordings") or []):
    rid = (rec.get("id") or "").strip()
    if not rid:
        continue
    title = (rec.get("title") or "").strip()
    print(f"{rid}\t{title}")
PY
)
}

run_loop() {
  local backoff=3 max_backoff=30 ev="" evid="" data rid title
  log "bridge starting; SSE_URL=${SSE_URL} token=${CLAWTILE_TOKEN:0:13}... hermes=${HERMES_BIN}"
  # 启动时先补齐：bridge 不在线那段时间堆积的 summary_state=none 录音
  # 可能永远等不到 SSE 事件了（后端 PubSub 不持久化）。
  reconcile_pending || true
  while true; do
    ev=""
    evid=""
    if curl --silent --no-buffer --show-error \
        --connect-timeout 15 \
        -H "Authorization: Bearer ${CLAWTILE_TOKEN}" \
        -H "Accept: text/event-stream" \
        "$SSE_URL" 2>>"$BRIDGE_LOG" | \
      while IFS= read -r line; do
        if [[ -z "$line" ]]; then
          ev=""
          evid=""
          continue
        fi
        if [[ "${line:0:1}" == ":" ]]; then
          continue
        fi
        case "$line" in
          event:*)
            ev="${line#event:}"
            ev="${ev# }"
            ;;
          id:*)
            evid="${line#id:}"
            evid="${evid# }"
            ;;
          data:*)
            data="${line#data:}"
            data="${data# }"
            case "$ev" in
              recording.transcribed|recording.summary_requested)
                rid="$(printf '%s' "$data" | python3 -c 'import json,sys; print((json.load(sys.stdin).get("recording_id") or "").strip())' 2>/dev/null || true)"
                title="$(printf '%s' "$data" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("title") or "")' 2>/dev/null || true)"
                if [[ -z "$rid" ]]; then
                  log "WARN: ${ev} missing recording_id: ${data}"
                  continue
                fi
                if already_seen "$rid"; then
                  log "skip duplicate event for ${rid}"
                  continue
                fi
                process_recording "$rid" "$title" &
                ;;
              connection.ack)
                log "SSE connected"
                ;;
              *)
                log "event ${ev} id=${evid}"
                ;;
            esac
            ;;
        esac
      done
    then
      log "SSE stream ended cleanly"
    else
      log "SSE stream errored"
    fi
    log "reconnecting in ${backoff}s"
    sleep "$backoff"
    backoff=$((backoff + 3))
    if [[ "$backoff" -gt "$max_backoff" ]]; then
      backoff="$max_backoff"
    fi
    # 每次准备重连 SSE 之前补一遍：断线期间错过的事件就靠它兜底。
    reconcile_pending || true
  done
}

case "${1:-run}" in
  run)
    run_loop
    ;;
  once)
    shift
    run_once "$@"
    ;;
  *)
    echo "usage: $0 [run|once <recording_id>]" >&2
    exit 2
    ;;
esac
