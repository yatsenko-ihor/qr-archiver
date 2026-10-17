#!/usr/bin/env bash
# Інтерактивний прогін: питає шлях до будь-якого файлу, пакує його у .qra (QR-коди при цьому
# проходять лише в пам'яті), розпаковує і звіряє з оригіналом байт у байт.
# За бажанням коди записуються на диск компактно (.qrb/.qrt) або як PNG і скануються назад.
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

read -r -p "Записати коди на диск? [N]і / [b]its (.qrb) / [t]ext (.qrt) / [p]ng " fmt || true
case "${fmt:-}" in
  b|B) FORMAT="bits" ;;
  t|T) FORMAT="text" ;;
  p|P) FORMAT="png" ;;
  *)   FORMAT="" ;;
esac

BASE="$(basename "$SRC")"
OUT="${SRC}.qra-out"
mkdir -p "$OUT"
EXT="${BASE##*.}"
[[ "$EXT" == "$BASE" ]] && EXT="bin"

echo; echo "== 1. Пакуємо у .qra і проганяємо через QR-коди в пам'яті"
./qra pack $NOECC "$SRC" "$OUT/$BASE.qra"

QRA="$OUT/$BASE.qra"
if [[ -n "$FORMAT" ]]; then
  echo; echo "== 2. Записуємо коди ($FORMAT)"
  case "$FORMAT" in
    bits) CODES="$OUT/codes.qrb" ;;
    text) CODES="$OUT/codes.qrt" ;;
    png)  CODES="$OUT/codes"; rm -rf "$CODES" ;;
  esac
  ./qra render -format "$FORMAT" "$QRA" "$CODES"
  echo; echo "== 3. Скануємо коди назад у .qra"
  ./qra scan $NOECC "$OUT/scanned.qra" "$CODES"
  QRA="$OUT/scanned.qra"
fi

echo; echo "== Розпаковуємо"
./qra unpack "$QRA" "$OUT/restored.$EXT"

echo; echo "== Перевірка"
if cmp -s "$SRC" "$OUT/restored.$EXT"; then
  echo "✓ Відновлений файл збігається з оригіналом байт у байт."
else
  echo "✗ Відновлений файл ВІДРІЗНЯЄТЬСЯ від оригіналу!" >&2
  exit 1
fi
echo "Результати: $OUT"
ls -l "$OUT" | tail -n +2 | awk '{print "  " $5 "\t" $9}'
