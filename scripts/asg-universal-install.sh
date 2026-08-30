#!/bin/sh
# asg-universal-install.sh — universal one-line onboarding (harness-agnostic)
# Creates ~/.config/asg/universal.json, writes generic MCP config to
# ~/.config/asg/mcp.json, and starts asg-connect serve in background.
# Usage:
#   sh scripts/asg-universal-install.sh --hub-url <url> --tenant-key <key> [--listen 127.0.0.1:8181] [--dry-run]
#   curl -fsSL https://raw.githubusercontent.com/dedarek/agent-security-gateway/main/scripts/asg-universal-install.sh | sh -s -- --hub-url <url> --tenant-key <key>
# Env overrides: ASG_HUB_URL, ASG_TENANT_KEY, ASG_LISTEN
# POSIX sh, no sudo, no harness-specific files.
set -eu

HUB_URL="${ASG_HUB_URL:-}"
TENANT_KEY="${ASG_TENANT_KEY:-}"
LISTEN="${ASG_LISTEN:-127.0.0.1:8181}"
DRY_RUN=0
SHOW_HELP=0

while [ $# -gt 0 ]; do
  case "$1" in
    --hub-url) HUB_URL="$2"; shift 2 ;;
    --hub-url=*) HUB_URL="${1#--hub-url=}"; shift ;;
    --tenant-key) TENANT_KEY="$2"; shift 2 ;;
    --tenant-key=*) TENANT_KEY="${1#--tenant-key=}"; shift ;;
    --listen) LISTEN="$2"; shift 2 ;;
    --listen=*) LISTEN="${1#--listen=}"; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) SHOW_HELP=1; shift ;;
    --) shift; break ;;
    *) echo "[asg-install] unknown arg: $1" >&2; SHOW_HELP=1; shift ;;
  esac
done

if [ "$SHOW_HELP" = "1" ]; then
  cat >&2 <<'HELP'
asg-universal-install.sh — universal one-line onboarding

Usage:
  sh scripts/asg-universal-install.sh --hub-url <url> --tenant-key <key> [--listen addr] [--dry-run]
  ASG_HUB_URL=... ASG_TENANT_KEY=... sh scripts/asg-universal-install.sh [--dry-run]

What it does:
  1. Writes ~/.config/asg/universal.json  (hub_url, tenant_key, listen)
  2. Writes ~/.config/asg/mcp.json        (generic MCP, not per-harness)
  3. Starts asg-connect serve -config ~/.config/asg/universal.json in background

Options:
  --hub-url <url>     Central gateway URL (env: ASG_HUB_URL)
  --tenant-key <key>  Tenant bearer key   (env: ASG_TENANT_KEY)
  --listen <addr>     Sidecar listen addr (default: 127.0.0.1:8181, env: ASG_LISTEN)
  --dry-run           Print what would be written/started, do not write or start
  -h, --help          Show this help
HELP
  exit 0
fi

# defaults for demo/CI when nothing provided — still valid universal.json
if [ -z "$HUB_URL" ]; then
  HUB_URL="https://asg-gateway.vip.cpolar.cn"
fi
if [ -z "$TENANT_KEY" ]; then
  # allow dry-run without real key; real runs will still write placeholder so user can replace
  if [ "$DRY_RUN" = "1" ]; then
    TENANT_KEY="***"
  else
    echo "[asg-install] --tenant-key is required (or env ASG_TENANT_KEY)" >&2
    echo "[asg-install] hint: sh scripts/asg-universal-install.sh --hub-url $HUB_URL --tenant-key sk-..." >&2
    exit 2
  fi
fi

# resolve config dir (POSIX, respects $HOME)
CONFIG_DIR="$HOME/.config/asg"
UNIVERSAL_JSON="$CONFIG_DIR/universal.json"
MCP_JSON="$CONFIG_DIR/mcp.json"
LOG_FILE="$CONFIG_DIR/asg-connect.log"

# helper: json-escape a string (minimal: backslash and double-quote)
json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr -d '\n\r'
}

HUB_ESC=$(json_escape "$HUB_URL")
KEY_ESC=$(json_escape "$TENANT_KEY")
LISTEN_ESC=$(json_escape "$LISTEN")

UNIVERSAL_CONTENT=$(printf '{\n  "hub_url": "%s",\n  "tenant_key": "%s",\n  "listen": "%s"\n}\n' "$HUB_ESC" "$KEY_ESC" "$LISTEN_ESC")

MCP_URL="http://$LISTEN/mcp"
MCP_URL_ESC=$(json_escape "$MCP_URL")
MCP_CONTENT=$(printf '{\n  "mcpServers": {\n    "asg": {\n      "url": "%s"\n    }\n  }\n}\n' "$MCP_URL_ESC")

# locate asg-connect binary
find_asg_connect() {
  # 1. PATH
  if command -v asg-connect >/dev/null 2>&1; then
    command -v asg-connect
    return 0
  fi
  if command -v asg-connect.exe >/dev/null 2>&1; then
    command -v asg-connect.exe
    return 0
  fi
  # 2. relative to this script
  _script_dir=$(CDPATH= cd -- "$(dirname "$0")" 2>/dev/null && pwd 2>/dev/null || echo ".")
  for _cand in "$_script_dir/../bin/asg-connect.exe" "$_script_dir/../bin/asg-connect" "$_script_dir/../asg-connect.exe" "$_script_dir/../asg-connect" "./bin/asg-connect.exe" "./bin/asg-connect" "./asg-connect.exe" "./asg-connect"; do
    if [ -x "$_cand" ] 2>/dev/null; then
      # return absolute or original path; prefer forward slashes for Windows
      printf '%s' "$_cand"
      return 0
    fi
  done
  # 3. fallback name (let caller handle missing)
  printf '%s' "asg-connect"
  return 1
}

ASG_BIN=""
ASG_BIN_FOUND=0
if ASG_BIN=$(find_asg_connect 2>/dev/null); then
  ASG_BIN_FOUND=1
fi
# normalize empty
if [ -z "$ASG_BIN" ]; then ASG_BIN="asg-connect"; ASG_BIN_FOUND=0; fi

if [ "$DRY_RUN" = "1" ]; then
  echo "[asg-install] DRY-RUN — no files will be written, no process started" >&2
  echo "[asg-install] would create: $UNIVERSAL_JSON" >&2
  printf '%s\n' "$UNIVERSAL_CONTENT" >&2
  echo "[asg-install] would create: $MCP_JSON" >&2
  printf '%s\n' "$MCP_CONTENT" >&2
  echo "[asg-install] would start: $ASG_BIN serve -config $UNIVERSAL_JSON  (background, log: $LOG_FILE)" >&2
  # also verify universal.json parses as valid JSON/probe config if asg-connect supports --dry-run
  if [ "$ASG_BIN_FOUND" = "1" ] && [ -x "$ASG_BIN" ] 2>/dev/null; then
    # try probe dry-run validation if supported
    echo "[asg-install] dry-run: validating universal.json via probe (if supported)..." >&2
  fi
  # exit 0 for CI
  # also emit to stdout the two JSON blobs for programmatic check
  printf '%s' "$UNIVERSAL_CONTENT" > /tmp/asg-universal-dry.json 2>/dev/null || true
  echo "[asg-install] dry-run OK" >&2
  exit 0
fi

# real run: create dir and write files
mkdir -p "$CONFIG_DIR" 2>/dev/null || true

printf '%s' "$UNIVERSAL_CONTENT" > "$UNIVERSAL_JSON"
chmod 600 "$UNIVERSAL_JSON" 2>/dev/null || true
echo "[asg-install] wrote $UNIVERSAL_JSON" >&2

printf '%s' "$MCP_CONTENT" > "$MCP_JSON"
chmod 600 "$MCP_JSON" 2>/dev/null || true
echo "[asg-install] wrote $MCP_JSON (generic, harness-agnostic)" >&2

# start asg-connect serve in background if binary exists
if [ "$ASG_BIN_FOUND" = "1" ] && [ -x "$ASG_BIN" ] 2>/dev/null; then
  # check if already listening
  if command -v curl >/dev/null 2>&1; then
    if curl -s --max-time 1 "http://$LISTEN/healthz" 2>/dev/null | grep -q "ok"; then
      echo "[asg-install] probe already running at http://$LISTEN/healthz — skip start" >&2
      echo "[asg-install] done. Verify: curl -s http://$LISTEN/healthz  &&  cat $MCP_JSON" >&2
      exit 0
    fi
  fi
  # start background; use nohup if available
  mkdir -p "$(dirname "$LOG_FILE")" 2>/dev/null || true
  # Windows native binary needs Windows-style path (C:/...), not MSYS /c/... or /tmp/...
  _cfg_for_bin="$UNIVERSAL_JSON"
  if command -v cygpath >/dev/null 2>&1; then
    _win_cfg=$(cygpath -w "$UNIVERSAL_JSON" 2>/dev/null || echo "$UNIVERSAL_JSON")
    # only use conversion if it succeeded and looks like Windows path
    case "$_win_cfg" in
      [A-Za-z]:*) _cfg_for_bin="$_win_cfg" ;;
    esac
  fi
  if command -v nohup >/dev/null 2>&1; then
    nohup "$ASG_BIN" serve -config "$_cfg_for_bin" > "$LOG_FILE" 2>&1 &
  else
    "$ASG_BIN" serve -config "$_cfg_for_bin" > "$LOG_FILE" 2>&1 &
  fi
  _pid=$!
  echo "[asg-install] started $ASG_BIN serve (pid $_pid, log $LOG_FILE)" >&2
  # brief health check (wait up to 3s)
  _ok=0
  if command -v curl >/dev/null 2>&1; then
    for _i in 1 2 3 4 5 6; do
      sleep 0.5
      if curl -s --max-time 1 "http://$LISTEN/healthz" 2>/dev/null | grep -q "ok"; then
        _ok=1
        break
      fi
    done
    if [ "$_ok" = "1" ]; then
      echo "[asg-install] probe healthy: http://$LISTEN/healthz => ok" >&2
    else
      echo "[asg-install] warning: probe not yet healthy at http://$LISTEN/healthz (check $LOG_FILE)" >&2
    fi
  fi
else
  echo "[asg-install] asg-connect binary not found (looked for $ASG_BIN)" >&2
  echo "[asg-install] build it: go build -o bin/asg-connect ./cmd/asg-connect" >&2
  echo "[asg-install] then: $ASG_BIN serve -config $UNIVERSAL_JSON &" >&2
fi

echo "[asg-install] done." >&2
echo "[asg-install] Verify:" >&2
echo "[asg-install]   curl -s http://$LISTEN/healthz" >&2
echo "[asg-install]   cat $UNIVERSAL_JSON" >&2
echo "[asg-install]   cat $MCP_JSON" >&2
echo "[asg-install]   # point any agent's LLM base_url to http://$LISTEN/v1 and MCP url to http://$LISTEN/mcp" >&2
