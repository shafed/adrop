#!/usr/bin/env bash
# Снимок состояния: HEAD, чистота дерева, этап и результат ворот из
# refs/notes/gate (для HEAD и последний по первой линии истории).
set -euo pipefail
root=$(git rev-parse --show-toplevel) || exit 2
cd "$root"
[ -f STATE.json ] && jq -e . STATE.json >/dev/null || { echo 'state_valid=false'; exit 2; }

gate() { git notes --ref=gate show "$1" 2>/dev/null | jq -r '.result // "unreadable"' 2>/dev/null || true; }

head=$(git rev-parse HEAD)
status=$(git status --short)
echo 'state_valid=true'
echo "head=$head"
echo "tree=$([ -n "$status" ] && echo dirty || echo clean)"
jq -r '"stage=\(.stage)\nstage_status=\(.stage_status)\nverification_command=\(.verification_command)"' STATE.json

last="" lastres="" n=0
for sha in $(git log --first-parent --format=%H -n 500); do
  r=$(gate "$sha")
  if [ -n "$r" ]; then last=$sha lastres=$r; break; fi
  n=$((n + 1))
done
gate_head=$(gate "$head")
if [ -z "$gate_head" ] && [ -n "$last" ]; then
  other=$(git diff --name-only "$last" HEAD | grep -v -x -e PLAN.md -e STATUS.md -e STATE.json || true)
  if [ -z "$other" ]; then gate_head=$lastres; echo "gate_inherited_from=${last:0:7}"; fi
fi
echo "gate_head=${gate_head:-none}"
if [ -n "$last" ]; then echo "last_gate=${last:0:7} $lastres"; echo "commits_since_gate=$n"; else echo 'last_gate=none'; fi
[ -z "$status" ] || printf 'status_begin\n%s\nstatus_end\n' "$status"
