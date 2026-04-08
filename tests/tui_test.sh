#!/usr/bin/env bash
#
# TUI integration tests for claude-esp using tui-use + claude -p.
# One claude -p call seeds all content, then tests run against it.
# Runtime: ~30-40s.
#
# Prerequisites: tui-use, claude-esp binary, claude CLI (authenticated)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="$(realpath "${CLAUDE_ESP_BIN:-${SCRIPT_DIR}/claude-esp}")"
MODEL="${CLAUDE_TEST_MODEL:-haiku}"
PASS=0 FAIL=0 ERRORS="" ESP_SESSION=""

# ── helpers ──────────────────────────────────────────────────────────────

snap()     { tui-use use "$ESP_SESSION" >/dev/null 2>&1; tui-use snapshot --format json 2>/dev/null | python3 -c "import json,sys;print(json.load(sys.stdin).get('screen',''))" 2>/dev/null; }
key()      { tui-use use "$ESP_SESSION" >/dev/null 2>&1; tui-use type "$1" >/dev/null 2>&1; sleep 0.4; }
has()      { echo "$3" | grep -qF "$2" || { ERRORS+="  FAIL: $1 — expected '$2'\n"; return 1; }; }
hasnt()    { echo "$3" | grep -qF "$2" && { ERRORS+="  FAIL: $1 — unexpected '$2'\n"; return 1; } || return 0; }
no_overflow() { local w; w=$(echo "$3" | awk '{if(length>m)m=length}END{print m+0}'); [ "$w" -le "$2" ] || { ERRORS+="  FAIL: $1 — width $w > $2\n"; return 1; }; }

run() {
  local name="$1"; shift; ERRORS=""
  if "$@"; then PASS=$((PASS+1)); echo "  PASS  $name"
  else FAIL=$((FAIL+1)); echo "  FAIL  $name"; printf "%b" "$ERRORS"; fi
}

esp_start() {
  ESP_SESSION=$(tui-use start --cols "${1:-80}" --rows "${2:-24}" --label esp \
    --cwd "$WORK" -- env CLAUDE_HOME="$FAKEHOME" "$BINARY" 2>/dev/null)
  tui-use use "$ESP_SESSION" >/dev/null 2>&1
  tui-use wait --text "Thinking" 10000 >/dev/null 2>&1 || sleep 5
}
esp_stop() { [ -n "$ESP_SESSION" ] && { tui-use use "$ESP_SESSION" >/dev/null 2>&1; tui-use kill >/dev/null 2>&1; ESP_SESSION=""; }; sleep 0.3; }

# ── setup ────────────────────────────────────────────────────────────────

WORK=$(mktemp -d /tmp/esp-test-XXXXXX)
FAKEHOME=$(mktemp -d /tmp/esp-home-XXXXXX)
mkdir -p "$FAKEHOME/projects"
trap 'esp_stop; proj=$(echo "$WORK"|tr "/" "-"); rm -rf "$HOME/.claude/projects/$proj" "$WORK" "$FAKEHOME" 2>/dev/null' EXIT

echo "=== claude-esp TUI tests ==="
echo "    model: $MODEL"
echo ""

# Single claude -p call: produces thinking + tool_use + tool_result + text
echo -n "  Seeding session... "
(cd "$WORK" && claude -p 'Run: echo ESP_TOOL_MARKER_42. Then say exactly: ESP_TEXT_MARKER_99' \
  --model "$MODEL" --allowedTools "Bash" >/dev/null 2>&1)

proj=$(echo "$WORK" | tr '/' '-')
src="$HOME/.claude/projects/$proj"
dst="$FAKEHOME/projects/$proj"
mkdir -p "$dst"
cp "$src"/*.jsonl "$dst/" 2>/dev/null || true
touch "$dst"/*.jsonl 2>/dev/null || true
echo "done"
echo ""

# ── layout tests ─────────────────────────────────────────────────────────

test_80x24() {
  esp_start 80 24; local s; s=$(snap); local ok=true
  has "header" "Thinking[t]" "$s" || ok=false
  has "help" "q: quit" "$s" || ok=false
  has "border" "╭" "$s" || ok=false
  no_overflow "80col" 80 "$s" || ok=false
  esp_stop; $ok
}

test_60x24() {
  esp_start 60 24; local s; s=$(snap); local ok=true
  has "header" "Thinking[t]" "$s" || ok=false
  has "border" "╭" "$s" || ok=false
  no_overflow "60col" 60 "$s" || ok=false
  esp_stop; $ok
}

test_40x20() {
  esp_start 40 20; local s; s=$(snap); local ok=true
  has "header" "Thinking[t]" "$s" || ok=false
  has "border" "╭" "$s" || ok=false
  no_overflow "40col" 40 "$s" || ok=false
  esp_stop; $ok
}

# ── functional tests (one esp instance) ──────────────────────────────────

test_toggles() {
  esp_start 100 40; local s ok=true
  # All header toggles
  for k in t i o x a; do
    key "$k"; s=$(snap); has "${k} off" "☐" "$s" || ok=false
    key "$k"; s=$(snap); has "${k} on" "☑" "$s" || ok=false
  done
  # Tree toggle — border count
  key h; s=$(snap)
  has "tree off" "☐ Tree" "$s" || ok=false
  [ "$(echo "$s" | grep -o "╭" | wc -l)" -eq 1 ] || { ERRORS+="  FAIL: tree off borders\n"; ok=false; }
  key h; sleep 0.3; s=$(snap)
  has "tree on" "☑ Tree" "$s" || ok=false
  [ "$(echo "$s" | grep -o "╭" | wc -l)" -ge 2 ] || { ERRORS+="  FAIL: tree on borders\n"; ok=false; }
  esp_stop; $ok
}

test_content() {
  esp_start 120 40; local s; s=$(snap); local ok=true
  has "text marker" "ESP_TEXT_MARKER_99" "$s" || ok=false
  has "tool marker" "ESP_TOOL_MARKER_42" "$s" || ok=false
  has "tool icon" "🔧" "$s" || ok=false
  has "response icon" "💬" "$s" || ok=false
  has "output icon" "📤" "$s" || ok=false
  has "separators" "────" "$s" || ok=false
  echo "$s" | grep -qE "📁|📂" || { ERRORS+="  FAIL: folder icon\n"; ok=false; }
  has "Main agent" "Main" "$s" || ok=false
  esp_stop; $ok
}

test_filters() {
  esp_start 120 40; local s ok=true
  # Thinking filter: turn off thinking, text should remain
  key t; s=$(snap)
  has "thinking off" "☐ Thinking" "$s" || ok=false
  has "text survives" "ESP_TEXT_MARKER_99" "$s" || ok=false
  key t  # restore
  # Output filter: turn off output, 📤 icon should vanish
  key o; s=$(snap)
  has "output off" "☐ Output" "$s" || ok=false
  hasnt "📤 gone" "📤" "$s" || ok=false
  key o  # restore
  esp_stop; $ok
}

test_tree_interact() {
  esp_start 120 40; local s ok=true
  key "$(printf '\t')"  # focus tree
  sleep 0.2
  tui-use use "$ESP_SESSION" >/dev/null 2>&1; tui-use type " " >/dev/null 2>&1  # uncheck
  sleep 0.4; s=$(snap)
  has "unchecked" "☐" "$s" || ok=false
  tui-use use "$ESP_SESSION" >/dev/null 2>&1; tui-use type " " >/dev/null 2>&1  # recheck
  sleep 0.4
  esp_stop; $ok
}

# ── run ──────────────────────────────────────────────────────────────────

run "layout 80x24"   test_80x24
run "layout 60x24"   test_60x24
run "layout 40x20"   test_40x20
run "toggles"        test_toggles
run "content"        test_content
run "filters"        test_filters
run "tree interact"  test_tree_interact

echo ""
echo "Results: $PASS passed, $FAIL failed (of 7 total)"
[ "$FAIL" -eq 0 ]
