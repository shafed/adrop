#!/usr/bin/env bash
# SessionStart: кладёт в контекст снимок состояния и next_action.
set -uo pipefail
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0
snap=$(bash scripts/snapshot.sh 2>&1)
next=$(jq -r '.next_action // empty' STATE.json 2>/dev/null)
ctx="Снимок состояния adrop (.claude/hooks/session-state.sh). Полное состояние — STATE.json.

$snap

next_action: ${next:-(не записан)}"
jq -n --arg ctx "$ctx" '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:$ctx}}'
