#!/usr/bin/env bash
# Збирає програму qra у цій теці. Потрібен Go 1.22 або новіший.
set -euo pipefail
cd "$(dirname "$0")"

if ! command -v go >/dev/null 2>&1; then
  echo "Не знайдено go. Встановіть Go з https://go.dev/dl/ і повторіть." >&2
  exit 1
fi

echo "Go: $(go version)"
go vet ./...
if [[ "${1:-}" == "--test" ]]; then
  go test ./... -count=1
fi
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o qra .
echo "Зібрано: $(pwd)/qra ($(du -h qra | cut -f1))"
