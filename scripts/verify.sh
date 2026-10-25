#!/usr/bin/env bash
# Ворота adrop. Итог: GREEN, RED (дефект продукта) или ENVIRONMENT GAP
# (нет инструментов). Результат пишется заметкой refs/notes/gate на HEAD —
# только на чистом дереве и не в CI. --app добавляет flutter analyze приложения.
set -u
set -o pipefail

root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "RESULT ENVIRONMENT GAP"; exit 2; }
cd "$root"
grep -q '^module ' go.mod 2>/dev/null || { echo "RESULT ENVIRONMENT GAP"; exit 2; }

app=no
[ "${1:-}" = "--app" ] && app=yes

gaps=0 failures=0 results=()
note_head=$(git rev-parse HEAD)
note_clean=no
[ -z "$(git status --porcelain)" ] && note_clean=yes

finish() {
  local code=$1 result=$2
  if [ -n "${CI:-}${GITHUB_ACTIONS:-}" ]; then
    echo "gate note: not written (CI)"
  elif [ "$note_clean" != yes ] || [ -n "$(git status --porcelain)" ]; then
    echo "gate note: not written (working tree is not clean)"
  elif [ "$(git rev-parse HEAD)" != "$note_head" ]; then
    echo "gate note: not written (HEAD moved during the run)"
  else
    local body
    body=$(jq -nc --arg r "$result" --arg go "$(go env GOVERSION)" --arg when "$(date -Iseconds)" \
      --argjson app "$([ $app = yes ] && echo true || echo false)" \
      '{result:$r,go:$go,app:$app,when:$when}') &&
      git notes --ref=gate add -f -m "$body" "$note_head" >/dev/null 2>&1 &&
      echo "gate note: refs/notes/gate on $(git rev-parse --short "$note_head") ($result)" ||
      echo "gate note: NOT written (git notes or jq failed)"
  fi
  exit "$code"
}

run_check() {
  local name=$1; shift
  printf '\n== %s ==\n' "$name"
  if "$@"; then results+=("PASS $name"); else results+=("FAIL $name"); failures=$((failures + 1)); fi
}

check_gofmt() {
  local out
  out=$(gofmt -l . | grep -v '^\.' || true)
  [ -z "$out" ] || { echo "not formatted:"; echo "$out"; return 1; }
}

printf '\n== prerequisites ==\n'
for tool in go gofmt jq; do
  command -v "$tool" >/dev/null || { echo "нет $tool" >&2; results+=("GAP $tool"); gaps=$((gaps + 1)); }
done
if [ "$(go env CC 2>/dev/null)" ] && ! command -v "$(go env CC)" >/dev/null; then
  echo "для -race нужен компилятор C: $(go env CC)" >&2
  results+=("GAP cc"); gaps=$((gaps + 1))
fi
if [ $app = yes ] && ! command -v "${FLUTTER:-flutter}" >/dev/null; then
  echo "--app требует flutter (или FLUTTER=путь)" >&2
  results+=("GAP flutter"); gaps=$((gaps + 1))
fi
if [ "$gaps" -ne 0 ]; then
  printf '\n== SUMMARY ==\n%s\nRESULT ENVIRONMENT GAP\n' "$(printf '%s\n' "${results[@]}")"
  finish 2 "environment gap"
fi

run_check vet go vet ./...
run_check gofmt check_gofmt
run_check build-nocgo env CGO_ENABLED=0 go build ./...
run_check test go test -race ./...
if [ $app = yes ]; then
  run_check app bash -c 'cd app && "${FLUTTER:-flutter}" analyze'
fi

printf '\n== SUMMARY ==\n%s\n' "$(printf '%s\n' "${results[@]}")"
if [ "$failures" -ne 0 ]; then
  printf 'RESULT RED (%d checks failed)\n' "$failures"
  finish 1 red
fi
echo "RESULT GREEN"
finish 0 green
