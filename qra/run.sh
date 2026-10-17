#!/usr/bin/env bash
# Інтерактивний прогін: питає шлях до будь-якого файлу, пакує його у .qra, рендерить QR-коди,
# сканує їх назад, розпаковує і звіряє з оригіналом байт у байт.
set -euo pipefail
cd "$(dirname "$0")"

[[ -x ./qra ]] || ./build.sh

SRC="${1:-}"
if [[ -z "$SRC" ]]; then
  read -r -e -p "Шлях до файлу (будь-якого: зображення, архів, текст): " SRC
fi
SRC="${SRC/#\~/$HOME}"
if [[ ! -f "$SRC" ]]; then
  echo "Файл не знайдено: $SRC" >&2
  exit 1
fi

NOECC=""
read -r -p "Нестандартні коди без корекції (+25 % місткості, не читаються сканером)? [y/N] " ans || true
[[ "${ans:-}" =~ ^[yYтТ] ]] && NOECC="-no-ecc"

BASE="$(basename "$SRC")"
OUT="${SRC}.qra-out"
rm -rf "$OUT/codes"
mkdir -p "$OUT/codes"

echo; echo "== 1. Пакуємо у .qra"
./qra pack $NOECC "$SRC" "$OUT/$BASE.qra"

echo; echo "== 2. Нарізаємо на QR-коди"
./qra render "$OUT/$BASE.qra" "$OUT/codes"

echo; echo "== 3. Скануємо коди назад у .qra"
./qra scan $NOECC "$OUT/scanned.qra" "$OUT/codes"

echo; echo "== 4. Розпаковуємо"
EXT="${BASE##*.}"
[[ "$EXT" == "$BASE" ]] && EXT="bin"
./qra unpack "$OUT/scanned.qra" "$OUT/restored.$EXT"

echo; echo "== 5. Перевірка"
if cmp -s "$SRC" "$OUT/restored.$EXT"; then
  echo "✓ Відновлений файл збігається з оригіналом байт у байт."
else
  echo "✗ Відновлений файл ВІДРІЗНЯЄТЬСЯ від оригіналу!" >&2
  exit 1
fi
echo "Результати: $OUT"
echo "  $BASE.qra, scanned.qra, restored.$EXT, codes/ ($(find "$OUT/codes" -type f | wc -l) файлів)"
