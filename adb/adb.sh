#!/bin/sh
# На компьютере: root ADB на колонке.
#   sh adb/adb.sh enable [IP]   через окно USB при включении; с IP — дождаться сетевого ADB
#   sh adb/adb.sh status        маркеры и порт 5555 (через ssh или JBL_ADB)
#   sh adb/adb.sh network-on    открыть сетевой ADB сейчас (через ssh)
#   sh adb/adb.sh network-off   закрыть сетевой ADB сейчас, когда SSH уже работает
set -eu
KIT=$(cd "$(dirname "$0")/.." && pwd)
. "$KIT/lib/kit.sh"
DEV=$KIT/adb/device.sh

case "${1:-}" in
    enable)
        command -v adb >/dev/null 2>&1 || fail 'нужен adb'
        cat <<'TXT'
1. Соедините задний USB-C колонки с компьютером кабелем с передачей данных.
2. Выключите колонку кнопкой питания.
3. Когда ниже появится «жду USB», включите её обычным нажатием.
TXT
        echo 'жду USB…'
        out=$(adb -d wait-for-usb-device shell "set -- enable; $(cat "$DEV")" 2>&1 | tr -d '\r')
        printf '%s\n' "$out" | grep -v '^JBL_'
        case "$out" in *JBL_ADB_ENABLED*) ;; *) fail 'маркеры не созданы' ;; esac
        [ -n "${2:-}" ] || { echo 'Перезагрузите колонку кнопкой питания: сетевой ADB заработает после загрузки.'; exit 0; }
        echo "Перезагрузите колонку кнопкой питания. Жду сетевой ADB на $2:5555 (до 3 мин)…"
        i=0
        while [ "$i" -lt 36 ]; do
            adb connect "$2:5555" >/dev/null 2>&1 || true
            if [ "$(adb -s "$2:5555" shell id -u 2>/dev/null | tr -d '\r')" = 0 ]; then
                echo "сетевой ADB работает: adb -s $2:5555 shell"
                exit 0
            fi
            sleep 5
            i=$((i + 1))
        done
        fail "нет сетевого ADB на $2:5555"
        ;;
    status | network-on | network-off) on_device "$DEV" "$1" ;;
    *) fail 'использование: adb.sh enable [IP] | status | network-on | network-off' ;;
esac
