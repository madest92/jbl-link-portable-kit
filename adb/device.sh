#!/bin/sh
# На колонке, от root: маркеры ADB.
#   enable       USB ADB после загрузки и сетевой ADB (порт 5555), маркеры защищены +i
#   status       маркеры и порт
#   network-on   открыть сетевой ADB сейчас (перезапуск adbd)
#   network-off  закрыть сетевой ADB (root без пароля для всей сети) сейчас
set -eu
LOCK=/tmp/lock_activated_adb  # не выключать USB ADB до перезагрузки
USB=/data/quasar_activate_adb # USB ADB после загрузки
NET=/data/network_adb         # adbd слушает 5555 в сети

fail() { echo "ОШИБКА: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'нужен root'

listening() {
    awk 'NR > 1 && $4 == "0A" { print $2 }' /proc/net/tcp /proc/net/tcp6 2>/dev/null | grep -qi ':15B3$'
}

# adbd читает маркер сетевого ADB при старте; ждать, пока порт станет $1 (open | closed)
restart_adbd() {
    systemctl restart android-tools-adbd.service
    i=0
    while :; do
        if listening; then [ "$1" = open ] && return 0; else [ "$1" = closed ] && return 0; fi
        i=$((i + 1))
        [ "$i" -lt 40 ] || return 1
        sleep 0.25
    done
}

case "${1:-status}" in
    enable)
        [ -f /system/vendor/quasar/activate_adb.sh ] || fail 'нет activate_adb.sh'
        grep -Fq "$LOCK" /system/vendor/quasar/activate_adb.sh || fail 'прошивка не знает маркер USB ADB'
        grep -aFq "$NET" /usr/bin/adbd || fail 'adbd не знает маркер сетевого ADB'
        command -v chattr >/dev/null 2>&1 || fail 'нет chattr'
        # сначала проверить все, потом менять
        for m in "$LOCK" "$USB" "$NET"; do
            [ ! -L "$m" ] || fail "символическая ссылка: $m"
            if [ -e "$m" ]; then
                [ -f "$m" ] || fail "не файл: $m"
                [ ! -s "$m" ] || fail "непустой маркер: $m"
            fi
        done
        for m in "$LOCK" "$USB" "$NET"; do
            [ -e "$m" ] || : > "$m"
        done
        chattr +i "$USB" "$NET"
        sync
        echo 'маркеры ADB на месте; сетевой ADB — после перезагрузки'
        echo JBL_ADB_ENABLED
        ;;
    status)
        for m in "$USB" "$NET"; do
            if [ -f "$m" ]; then echo "есть $m"; else echo "нет $m"; fi
        done
        if listening; then echo 'порт 5555 открыт'; else echo 'порт 5555 закрыт'; fi
        ;;
    network-on)
        grep -aFq "$NET" /usr/bin/adbd || fail 'adbd не знает маркер сетевого ADB'
        [ ! -L "$NET" ] || fail "символическая ссылка: $NET"
        if [ -e "$NET" ]; then { [ -f "$NET" ] && [ ! -s "$NET" ]; } || fail "не пустой файл: $NET"; fi
        [ -e "$NET" ] || : > "$NET"
        chattr +i "$NET"
        sync
        listening || restart_adbd open || fail 'порт 5555 не открылся'
        echo 'сетевой ADB включён: порт 5555 открыт'
        ;;
    network-off)
        if [ -e "$NET" ] || [ -L "$NET" ]; then
            { [ -f "$NET" ] && [ ! -L "$NET" ]; } || fail "не файл: $NET"
            { chattr -i "$NET" && rm -f "$NET" && sync; } || fail "не удалось убрать $NET"
        fi
        ! listening || restart_adbd closed || fail 'порт 5555 не закрылся'
        echo 'сетевой ADB выключен: порт 5555 закрыт'
        ;;
    *) fail 'использование: device.sh enable|status|network-on|network-off' ;;
esac
