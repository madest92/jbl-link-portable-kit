#!/bin/sh
# На компьютере: автозапуск своих скриптов при загрузке колонки (через ssh).
#   sh autostart/autostart.sh install   поставить в /data/jbl-boot и включить; уже стоит — ничего
#   sh autostart/autostart.sh status    установлен ли, включён ли, выполнились ли хуки
#   sh autostart/autostart.sh on | off  включить или выключить (строка в device.cfg; файлы остаются)
# Плагин — autostart/build (sh autostart/build.sh), иначе из релиза. Хук SSH install пишет сам.
set -eu
KIT=$(cd "$(dirname "$0")/.." && pwd)
. "$KIT/lib/kit.sh"
A=$KIT/autostart
PLUGIN=$A/build/flb-in_jbl_boot.so
STAGE=/tmp/jbl-boot-stage

[ "$(jssh id -u 2>/dev/null)" = 0 ] || fail "нет ssh $JBL_HOST"

case "${1:-status}" in
    install)
        [ -s "$PLUGIN" ] || { mkdir -p "$A/build"; fetch flb-in_jbl_boot.so "$PLUGIN"; }
        # ELF32 ARM, little-endian, наш символ
        { [ "$(head -c 6 "$PLUGIN" | od -An -tx1 | tr -d ' \n')" = 7f454c460101 ] &&
            grep -aq in_jbl_boot_plugin "$PLUGIN"; } || fail "$PLUGIN — не плагин автозапуска"
        jssh "rm -rf $STAGE && mkdir -m 700 $STAGE"
        jssh "cat > $STAGE/flb-in_jbl_boot.so" < "$PLUGIN"
        jssh "cat > $STAGE/device.sh" < "$A/device.sh"
        out=$(jssh sh -s install "$STAGE" < "$A/device.sh" 2>&1) || true
        jssh "rm -rf $STAGE"
        printf '%s\n' "$out" | grep -v '^JBL_' || true
        case "$out" in
            *JBL_BOOT_INSTALLED* | *JBL_BOOT_ALREADY* | *JBL_BOOT_ON*) ;;
            *) fail 'автозапуск не установлен' ;;
        esac
        ;;
    status | on | off) jssh sh -s "$1" < "$A/device.sh" ;;
    *) fail 'использование: autostart.sh install | status | on | off' ;;
esac
